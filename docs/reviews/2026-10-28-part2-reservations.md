# Security review — Part 2, `POST /api/reservations`

**Date:** 2026-10-28
**Scope:** all uncommitted changes since commit `f1451bd` on `main` (there is no divergent branch, so `git diff main...HEAD` is empty — the real range is working tree vs. `HEAD`, tracked diffs plus new untracked files):

- `backend-go/internal/handlers/reservations.go` (new — the endpoint itself, primary focus)
- `backend-go/internal/handlers/reservations_test.go`, `availability_test.go` (test infra)
- `db/schema.sql` (new columns + `no_overlapping_active_bookings` trigger)
- `backend-go/internal/db/db.go` (DSN/pragma delivery change)
- `backend-go/internal/server/server.go` (new route + CORS wiring)
- `backend-go/internal/certs/` (new — cert-date parsing)
- `backend-go/cmd/backfill-booking-utc/` (new — one-time migration tool, not externally reachable)
- `backend-go/integration/` (new — real-process concurrency test, not shipped/not externally reachable)

Part 1's `auth.go`, `availability.go`, CORS/logging middleware, etc. were reviewed in `2026-10-28-part1-auth-review.md` and `2026-10-28-part1-six-increments.md` and are unchanged here except where noted.

## Checklist

| # | Item | Verdict | Evidence |
|---|---|---|---|
| 1 | Authentication | OK | `POST /api/reservations` is wrapped in the same `auth.RequireAuth` as Part 1 (`server.go:19`) — signature, `exp`/`nbf`/`iss`/`aud` all verified before any claim is trusted (unchanged from the already-reviewed `auth.go`). No path bypasses it. |
| 2 | Authorization | OK | B1 enforced per-request, not just per-route: `reservations.go:115-125` — `ops_director` anywhere, `superintendent`/`foreman` only via `CanAccessSite(req.SiteID)`, every other role (including `safety_manager` despite `site_ids:["*"]`) hits the `default` branch and is denied. Verified behaviorally, not just by inspection — `TestCreateReservation_Permissions` exercises all seven role/site combinations including the `safety_manager` wildcard carve-out. Authorization also runs *before* any equipment/site/operator lookup, so an unauthorized caller never learns whether a given `asset_tag`/`operator_id` exists. |
| 3 | Input handling | OK | Every external field is validated: presence, RFC3339 parseability, `end > start`, `status` enum, `expires_at` required/forbidden per status, equipment existence + active status (via `NormalizeTag`, not raw match), site existence, operator existence + certification validity. All SQL uses parameterized queries (`?` placeholders) throughout — `loadEquipment`/`loadSite`/`loadOperator`/`findActiveConflict`/the `INSERT` — no string concatenation anywhere. Verified with a live attempt: `TestCreateReservation_SQLMetacharacterInputIsPlainlyRejected` posts `asset_tag: "x'); DROP TABLE bookings;--"` and confirms a plain 400 with the `bookings` table intact. |
| 4 | Sensitive data | OK | Response never includes operator PII (name, phone, email, SSN fragment, DOB, emergency contact) — only `operator_id`, verified by `TestCreateReservation_ResponseHasNoOperatorPII`. `booked_by` comes from the verified JWT `sub`, not the request body, even when the body supplies a spoofed value — verified by `TestCreateReservation_AuditFieldsNotSpoofable`. No cert content, PII, or secrets appear in any `log.Printf` call in this file. |
| 5 | Secrets management | OK (N/A — no change) | No new secrets introduced. `JWT_SECRET` continues to come from `config`/env exactly as in Part 1. |
| 6 | Error handling | OK | Every DB-layer error (`BEGIN`, pre-check, `INSERT`, `COMMIT`) is logged server-side with `log.Printf` and answered to the client with a generic `"internal server error"` (500) or a specific, safe message — never raw SQL, driver internals, or a stack trace. `SQLITE_CONSTRAINT_TRIGGER` and `SQLITE_BUSY` are the only two driver error codes inspected and mapped to 409/503 respectively; anything else falls through to the generic 500 path, so an unhandled DB error can never be mistaken for success (direct answer to the standing instruction on this point). |
| 7 | Transport and browser | OK (N/A — no new issue) | `server.go`'s CORS allow-list is unchanged in kind, just extended: `Access-Control-Allow-Methods` now includes `POST` (`server.go:66`), and a matching unauthenticated `OPTIONS /api/reservations` preflight route was added, consistent with the existing GET route's handling. No wildcard origin introduced. |
| 8 | Abuse and limits | **Issue (Medium)** | See finding below. |
| 9 | Dependencies | OK | No new third-party packages. `modernc.org/sqlite`'s `/lib` subpackage (for `SQLITE_BUSY`/`SQLITE_CONSTRAINT_TRIGGER` constants) and its `*sqlite.Error` type were already part of the existing `modernc.org/sqlite` dependency; `go.mod`/`go.sum` are unchanged (confirmed via `git diff`, empty). |
| 10 | AI-specific | N/A | No LLM involved in this endpoint. |

## Finding

| Severity | Finding | Evidence | Recommendation | Status |
|---|---|---|---|---|
| Medium | No request body size limit and no HTTP server timeouts on the API process, so `POST /api/reservations` will `json.Decode` an arbitrarily large body before any validation runs, and a slow/stalled client can hold a goroutine open indefinitely. | `backend-go/cmd/api/main.go` calls `http.ListenAndServe(":"+port, server.New())` with no custom `http.Server{ReadTimeout, WriteTimeout, IdleTimeout, ReadHeaderTimeout}` and no `http.MaxBytesReader`/body-limiting middleware anywhere in the stack. This is pre-existing from Part 1 (unchanged by this diff — `main.go` has no diff against `HEAD`), but Part 1 only had a `GET` endpoint; Part 2 adds the first endpoint that reads and acts on a client-supplied body, which is where this gap becomes consequential (memory pressure from a huge body, or a connection held open by a slow/incomplete one). | Add a small `http.Server` with sane timeouts in `cmd/api/main.go`, and wrap request bodies with `http.MaxBytesReader` (e.g. in `Wrap`'s middleware chain, applied to all routes, not just this one) — bounded by something comfortably larger than any real reservation payload (a few KB). This is infrastructure shared with Part 1's existing route, not scoped to this endpoint alone, so it's reported rather than fixed silently in this pass, per the standing instruction not to broaden scope without approval. | **Not fixed** — reported for prioritization. |

## Items checked and found correct (no action needed)

- Authorization runs before any resource-existence check (no enumeration leak to unauthorized callers).
- Transaction rollback-on-error path uses `context.Background()` for the `ROLLBACK`, not the request's own (possibly-cancelled) context — correct, deliberate (a cancelled/disconnected request must still release the write lock).
- `conn.Close()` and the rollback-on-not-committed `defer` both fire on every exit path, including panics recovered upstream by `recoverErrors`.
- `generateBookingID` uses `crypto/rand` (12 bytes, 96 bits) — not guessable/enumerable, not derived from any predictable input.
- The conflict-redaction rule on the write path reuses Part 1's exact `fullDetailRole`/`siteMatchedRole`/`hasSite` logic rather than re-implementing it, so the already-reviewed redaction guarantee carries over verbatim to the new 409 response. Verified behaviorally for the asset-moved-to-another-site case specifically (`TestCreateReservation_ConflictRedaction`), not just by code inspection.

## Summary (top 3 for the human)

1. **One real finding, Medium severity, not fixed in this pass**: no request body size cap or server timeouts on the API process (`cmd/api/main.go`). Pre-existing from Part 1, but Part 2's write endpoint is the first place it's consequential. Recommend a small `http.Server` config + a body-size-limiting middleware; this touches shared infrastructure, not just this endpoint, so it's flagged rather than changed silently.
2. Authorization, input validation (including a live SQL-injection-style probe), PII exposure, and error-handling all checked out clean, each backed by a passing automated test rather than inspection alone.
3. No new third-party dependencies, no new secrets, no CORS regressions.
