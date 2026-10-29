# Security review — confirm/release reservation endpoints

**Date:** 2026-10-29
**Scope:** commits `5bfcc1c` and `f1e47f5` (`9a2e417..f1e47f5`) — new code only:

- `backend-go/internal/handlers/reservations.go` (`ConfirmReservation`, `ReleaseReservation`, `loadReservation`, `canManageBooking`, `beginReservationTx`, `currentStatus`, `writeReservationJSON`)
- `backend-go/internal/server/server.go` (two new routes + their `OPTIONS` preflight handlers)
- `db/schema.sql` (the `no_overlap_on_confirm` trigger)
- Test files are not security-reviewed themselves, but their coverage is cited as evidence below.

`CreateReservation` and everything it reuses (`writeError`, `isBusy`, `nullableString`, the auth middleware) were reviewed in `2026-10-28-part2-reservations.md` and are unchanged here.

## Checklist

| # | Item | Verdict | Evidence |
|---|---|---|---|
| 1 | Authentication | OK | Both new routes wrapped in the same `auth.RequireAuth` as every other endpoint (`server.go`). No path bypasses it; `TestConfirmRelease_NoToken` exercises both. |
| 2 | Authorization | OK | `canManageBooking` is checked per-resource, immediately after loading the specific booking and before any mutation, for both endpoints — not a route-level or role-only check. Verified behaviorally across all 6 B6 combinations (unrelated role/site, wrong-site authority, original requester even with an otherwise-disqualifying role, `ops_director`, same-site `superintendent`/`foreman`) via `TestConfirmRelease_Authorization`, for both confirm and release. |
| 3 | Input handling | OK | `bookingID` (`r.PathValue("id")`) is used only as a parameterized `?` argument in every query (`loadReservation`, the two `UPDATE`s, `currentStatus`) — never concatenated into SQL text. Same pattern already proven safe for `CreateReservation`'s inputs. |
| 4 | Sensitive data | OK | `writeReservationJSON` returns the same field set as `CreateReservation`'s response (`operator_id`, never operator PII). No new field leaks anything `CreateReservation` didn't already expose. |
| 5 | Secrets management | N/A | No new secrets. |
| 6 | Error handling | OK | Every DB-layer error is logged server-side and answered with a generic `"internal server error"` or a specific, safe message — same convention as `CreateReservation`. The two `SQLITE_CONSTRAINT_TRIGGER` branches distinguish "hold has expired" from a real conflict only by matching the trigger's own `RAISE` text, which is safe (both are static strings this codebase wrote into `schema.sql` itself, not attacker-influenced). |
| 7 | Transport and browser | OK | New `OPTIONS` preflight routes added for both new paths (Go 1.22's `ServeMux` matches each pattern independently — confirmed necessary, not just defensive, since `/api/reservations`'s own preflight route does not cover a sub-path). No CORS policy change. |
| 8 | Abuse and limits | **Issue (Low)** | See finding below. |
| 9 | Dependencies | OK | No new packages. |
| 10 | AI-specific | N/A | No LLM involved. |

## Finding

| Severity | Finding | Evidence | Recommendation | Status |
|---|---|---|---|---|
| Low | A `404` ("unknown booking_id") is returned *before* the authorization check (`403`), so any authenticated user who knows or guesses a `booking_id` learns whether it exists, even if they have no right to act on it. | `ConfirmReservation`/`ReleaseReservation`: `loadReservation` → `404` check → *then* `canManageBooking` → `403`. | In isolation this is a textbook "return 404 for both cases" finding — but `booking_id` is `crypto/rand`-generated (96 bits of entropy, `generateBookingID`), not sequential or guessable, so there is no practical enumeration path: an attacker has no way to *acquire* a valid-but-unauthorized `booking_id` to test in the first place short of already having legitimate access to it (e.g. seeing it in another response they were authorized to see). Noting it for completeness rather than fixing it, since collapsing the two into a single ambiguous `404` would also make the legitimate "you mistyped the ID" case indistinguishable from "that's not your booking," which is worse UX for close to zero real security benefit here. | **Not fixed** — reported for prioritization, judged not worth the UX cost given the ID's entropy. |

## Items checked and found correct (no action needed)

- `canManageBooking`'s owner-match branch (`user.Sub == b.BookedBy`) is checked *before* the role switch, so the original requester can always manage their own booking regardless of their current role — including a role the B1 switch alone would reject (verified directly: `TestConfirmRelease_Authorization/confirm_original_requester_allowed_even_as_subcontractor`, inserting a booking with `booked_by` set to a `subcontractor`'s `sub` and confirming that role can still act on it).
- The `currentStatus()` re-read fix (this review's commit `f1e47f5`) is not just a reasoning exercise — it's proven under genuine concurrent load by `TestReservationConcurrency_ReleaseVsConfirmRace`, which explicitly asserts the correct re-read message ("already cancelled") on every trial where the race resolved in the order that would have exposed the old stale-read bug ("already tentative," which the test treats as a hard failure).
- `beginReservationTx`/the rollback-on-not-committed `defer` pattern is identical to `CreateReservation`'s already-reviewed transaction handling, reused rather than reimplemented — same correctness guarantees carry over.
- The `no_overlap_on_confirm` trigger's two `RAISE`s were verified by hand against the live SQL (not just read) before any Go code was written — see `docs/plan/03-confirm-release.md`'s commit history — including the defense-in-depth overlap branch, which required deliberately constructing a scenario the `INSERT` trigger would normally prevent from ever arising, to prove the backstop works independently.

## Summary (top 3 for the human)

1. **One Low-severity, not-fixed finding**: existence of a `booking_id` leaks via `404`-before-`403` ordering, judged low-risk given 96 bits of entropy and no practical enumeration path, and not fixed because the alternative (collapsing to a single ambiguous `404`) has a real UX cost for close to no security benefit.
2. Authorization, input handling (parameterized throughout), and error handling all checked out clean, each backed by a passing automated test (including a genuinely concurrent one) rather than inspection alone.
3. No new dependencies, no new secrets, no CORS regressions; the one real bug found in this increment (the stale-pre-read race message) was caught and fixed *before* this review, during test design — not by the review itself.
