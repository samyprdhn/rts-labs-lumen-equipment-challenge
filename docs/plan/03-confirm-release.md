# 03 — Confirm and release a reservation

**Status:** draft, presented for approval. Nothing implemented.
**Challenge part(s):** 2
**Depends on:** [02-reservations.md](02-reservations.md) (status: `POST /api/reservations` done) and [ADR 0002](../decisions/0002-reservation-concurrency-control.md), which already designed and empirically verified the mechanism this plan builds — this is an implementation plan for work ADR 0002 scoped out, not a new design.

## Problem (in business terms)

A reservation created today is permanently stuck in whatever status it started in. A tentative hold can never become the firm booking point 4 of the approved tentative-hold policy promises ("confirming a hold converts it into a firm reservation"), and nothing — tentative or confirmed — can ever be released when plans change: a job gets cancelled, a crane turns out not to be needed, a foreman double-requested by mistake. Until this ships, equipment held or booked by accident stays locked up for its whole window with no way to free it, which recreates exactly the kind of avoidable idle-equipment cost Dana's original complaint was about, just from the other direction (equipment sitting reserved and unused instead of unreserved and double-booked).

## What I found (reading the existing design, not re-deriving it)

- ADR 0002 already designed, and empirically verified (5 sequential scenarios, documented in the ADR's own table), a `BEFORE UPDATE OF status` trigger (`no_overlap_on_confirm`) for the confirm transition, and reasoned that release needs **no** new trigger at all — cancelling a booking can only ever free a slot, never create a conflict with anything else, so SQLite's ordinary single-row `UPDATE` locking is already sufficient.
- That trigger's SQL already has the `datetime(...)`-wrapping fix applied in the ADR text (found and fixed during the pre-submission audit, after the trigger's sibling in `schema.sql` was found to have the same same-day-comparison bug) — it is **not yet applied to `db/schema.sql` itself**, since confirm/release were never built. Adding it is this plan's task 1, not a new design question.
- `docs/plan/02-reservations.md`'s B6 ("who may confirm or release a hold: the original requester, or anyone with B1-level booking authority at that site") was written with only tentative holds in mind. This plan generalizes `release` to cover confirmed bookings too (the scope change requested this session) — B6's reasoning carries over unchanged (see Assumptions).
- `internal/handlers/reservations.go`'s authorization pattern (role + `CanAccessSite`), error-JSON shape (`writeError`), and transaction pattern (`BEGIN IMMEDIATE` on a raw `*sql.Conn`, `isBusy`/`SQLITE_CONSTRAINT_TRIGGER` mapping) are directly reusable — this plan extends that file, it doesn't invent a second pattern.

## Assumptions

| # | Assumption | Assumed / Blocking | Source or reasoning |
|---|---|---|---|
| C1 | **B6 applies identically to confirmed and tentative bookings** — same authorization rule (original requester, or B1-level authority at the booking's site) for releasing either, and for confirming a tentative hold | Assumed, not blocking | No source material distinguishes the two cases; this is the natural, symmetric extension of the already-approved B6, and narrowing it later (e.g. "only a superintendent may release a confirmed job") is a one-line `if`, not a redesign |
| C2 | **Release of an already-cancelled booking is a client error (`409`), not a silent no-op `200`** | Assumed | A second release call most likely means the caller doesn't know it already happened; failing loudly is safer than pretending it succeeded twice |
| C3 | **No reason/comment field on release** | Assumed | Nothing in the source material asks for one; adding one later is additive, not a breaking change |
| C4 | **Confirm/release return `200` with the updated reservation**, not `201` (no new resource is created, an existing one is modified) | Assumed | Matches ordinary REST convention; `201` is reserved for `POST /api/reservations`'s actual creation |
| C5 | **A confirm or release racing a status change on the *same* booking is resolved by whichever `UPDATE` commits first, using the state it finds** — e.g. if a release and a confirm race on the same tentative hold, and confirm wins the lock first, a racing release still matches `status IN ('tentative','confirmed')` and will cancel the booking that was *just* confirmed a moment earlier | Assumed, flagged explicitly | This is a real, slightly surprising consequence of generalizing release to cover confirmed bookings (C's scope change) combined with no new trigger guarding release; see "Deterministic concurrency test" below — this is tested and documented, not silently accepted |

## Options

**Core decision: one generic status-transition endpoint, or two narrow ones.**

### A. Two narrow endpoints — `POST /api/reservations/{id}/confirm`, `POST /api/reservations/{id}/release`

Matches the shape already approved in `02-reservations.md`. Each endpoint has one legal precondition (confirm: must currently be `tentative`; release: must currently be `tentative` or `confirmed`) and one legal outcome. Validation is "is the booking in the one state this endpoint accepts," not "which of N possible transitions did the caller ask for."

- **Pros:** small, auditable surface per endpoint; a client can't request an illegal transition the server has to notice and reject, because there's no field where they'd express one; matches the explicit non-goal already on record ("not a general edit/`PATCH`" — `02-reservations.md`).
- **Cons:** two routes instead of one; Go 1.22's `net/http.ServeMux` needs its own pattern (`POST /api/reservations/{id}/confirm`) per route, including a new `OPTIONS` preflight handler per route (the existing `OPTIONS /api/reservations` preflight doesn't cover sub-paths).

### B. One generic transition endpoint — `PATCH /api/reservations/{id}` with `{"status": "confirmed" | "cancelled"}`

More conventionally RESTful; one route.

- **Cons:** the handler must now explicitly enumerate and reject every illegal transition (`cancelled→anything`, `confirmed→tentative`, `tentative→tentative` as a no-op or an error?, `confirmed→confirmed`?) instead of that falling out of which endpoint was called; re-opens exactly the "general edit endpoint" door `02-reservations.md` closed on purpose, since a future change could quietly accept a `start`/`end` field in the same body and nobody would notice until it shipped.

**Chosen: Option A**, for consistency with the already-approved design and because the narrower surface is structurally harder to misuse into a general edit later — the same reasoning `02-reservations.md` already used, not a new argument.

## Transaction strategy (reusing ADR 0002, not re-deciding it)

Both handlers follow `CreateReservation`'s existing shape: validate shape → **load the booking row first** (`SELECT booking_id, asset_tag, site_id, status, booked_by, expires_at_utc FROM bookings WHERE booking_id = ?`, outside any transaction — this is a read) → `404` if it doesn't exist → authorize (C1/B6, now that `booked_by`/`site_id` are known) → only then open `BEGIN IMMEDIATE` for the actual mutation:

- **Confirm:** `UPDATE bookings SET status='confirmed' WHERE booking_id=? AND status='tentative'`. The `no_overlap_on_confirm` trigger (added to `schema.sql` in task 1, with the `datetime(...)` fix already applied) is the backstop; its two `RAISE`s map to:
  - `'hold has expired'` → `409 {"error": "hold has expired"}`
  - `'overlapping active booking exists'` → `409` with conflict detail (kept as defense-in-depth per ADR 0002's own note that this branch is "currently unreachable in practice," not because it's expected to fire)
  - Zero rows affected and no trigger error (booking exists but wasn't `tentative`) → `409 {"error": "reservation is already <status>"}`, looked up from the pre-read row's `status`, not re-queried
- **Release:** `UPDATE bookings SET status='cancelled' WHERE booking_id=? AND status IN ('tentative','confirmed')`. No new trigger (ADR 0002). Zero rows affected (booking exists but is already `cancelled`) → `409 {"error": "reservation is already cancelled"}` (C2)
- Both: `SQLITE_BUSY` → `503` + `Retry-After`, any other DB error → `500`, logged server-side, generic to the client — same convention as `CreateReservation`, not a new one.

## Error contract (additions to `02-reservations.md`'s table)

| Status | Meaning |
|---|---|
| `200` | Confirmed or released; body is the updated reservation |
| `403` | Not the original requester and not B1/B6-authorized at this booking's site |
| `404` | `booking_id` doesn't exist |
| `409` | Hold expired (confirm only); real overlap (confirm only, defense-in-depth); already in the target-incompatible status (both) |
| `503` | Lock contention outlived `busy_timeout` |

## Task breakdown

- [ ] 1. Schema migration: add the `no_overlap_on_confirm` trigger to `db/schema.sql`, using the already-`datetime(...)`-corrected SQL from ADR 0002 (not the pre-correction version). Verify against a disposable DB: full historical reseed still loads cleanly (expected — all 743 rows are `confirmed`/inert to an `UPDATE`-only trigger), and a hand-run of the ADR's 5 documented scenarios against the *corrected* SQL specifically (the ADR's original verification predates the datetime fix).
- [ ] 2. `POST /api/reservations/{id}/confirm`: load → 404 → authorize (C1) → `BEGIN IMMEDIATE` → `UPDATE` → map trigger `RAISE`s and the zero-rows-but-wrong-status case → `COMMIT`/`ROLLBACK`.
- [ ] 3. `POST /api/reservations/{id}/release`: same shape, covering both `tentative` and `confirmed` source statuses (the scope generalization from this session).
- [ ] 4. Wire both routes (and their `OPTIONS` preflight handlers) into `server.go`.
- [ ] 5. Deterministic concurrency tests, real-process/real-HTTP per the Part 3A precedent already set for create (`backend-go/integration/`): (a) N goroutines confirm the *same already-expired* hold concurrently — all `409 "hold has expired"`, none succeed; (b) a release and a confirm racing on the *same still-valid tentative* hold — exactly one request's effect wins, and the test asserts and documents which outcome occurred (C5) rather than assuming one.
- [ ] 6. `security-review` pass specific to these two new endpoints (new authz paths, new SQL) — required per the standing instruction that each increment touching auth/SQL needs its own pass, not covered by the existing Part 2 review.
- [ ] 7. *(optional, time-boxed)* Minimal frontend: a "Release" button on an owned booking, and a "Confirm" button on an owned tentative hold — same minimal-UI treatment already given to create.

## Test plan (write-tests categories)

- **Happy path:** confirm a valid, unexpired, non-conflicting tentative hold; release a tentative hold; release a confirmed booking.
- **Boundaries:** confirm exactly at the expiry boundary (`expires_at_utc == now` — the trigger's `<=` treats this as already-expired, matching the inclusive-reject convention already chosen for `HasValidCert`'s *opposite* boundary in Part 2's cert check; worth a test precisely because the two boundaries point different directions and that's easy to get backwards).
- **Invalid input:** nonexistent `booking_id` → `404` for both endpoints.
- **Permissions:** owner vs. B1/B6-authorized-other-at-same-site vs. authorized-role-wrong-site vs. unrelated role, for both endpoints, against both a tentative and a confirmed starting booking where the combination is meaningful.
- **Idempotency/state conflicts:** confirm a booking that's already `confirmed` → `409`; confirm one that's `cancelled` → `409`; release one that's already `cancelled` → `409` (C2).
- **Concurrency:** both deterministic tests in task 5, including the C5 release-vs-confirm race, asserted and documented rather than assumed.
- **Data quality:** the schema migration (task 1) re-verified against the full real reseed, same discipline as every prior schema change this engagement.

## Risks / out of scope

**Out of scope, explicitly, unchanged from `02-reservations.md`:**
- Idempotency-key handling (B5) — still not built.
- The travel buffer — still not built.
- A general edit/`PATCH` of a reservation's time range or equipment — confirm/release remain the only two transitions, per the "Options" decision above.
- A background sweeper for expired holds — expiration stays lazy (read/write-time), unchanged.

**Risks:**
- C5 (the release-vs-confirm race outcome) is a real, slightly surprising behavior this plan is choosing to document and test rather than guard against with new complexity (e.g. a trigger that blocks releasing a booking confirmed within the last N seconds) — flagging it here so it's a decision, not a surprise found later.
- Task 1's schema change (a new trigger) is the same category of risk every prior schema change in this engagement has been: additive, but still worth the disposable-DB reseed verification before trusting it, not skipping that step because "it's just like the last one."

## Status log
- 2026-10-29: created, presented for approval. Nothing implemented.
