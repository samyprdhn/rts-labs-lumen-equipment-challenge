# 02 — Reservations (Part 2)

**Status:** draft — presenting for approval, nothing implemented yet. Revised after the tentative-hold lifecycle was resolved (was open question B2) — this added real scope (two new endpoints, an expiration mechanism) beyond the original draft; see the Status log.
**Challenge part(s):** 2
**Depends on:** Part 1 ([01-trustworthy-availability.md](01-trustworthy-availability.md), status: done) for the overlap/date-parsing/authorization primitives this plan reuses. Concurrency mechanism: [ADR 0002](../decisions/0002-reservation-concurrency-control.md).

## Problem (in business terms)

Right now a superintendent or foreman can *see* what's free (Part 1) but has no way to actually claim it through the system — booking still happens by phone call or spreadsheet edit, which is exactly the process that produced 18 real double-bookings in the historical data. Part 2 adds `POST /api/reservations` so a claim is atomic, auditable, and respects who's allowed to book what, with operators correctly certified for the whole booking — and, non-negotiably, two people racing for the same machine at the same instant must never both win.

## What I found (reading the code, schema, data, and Part 1's own evidence)

- `db/schema.sql`'s `bookings` table has zero constraints of any kind (confirmed in Part 1 recon) — the overlap invariant currently exists nowhere, not even informally, beyond "the previous contractor's code happened to check it sometimes."
- Part 1 already built and tested the exact primitives Part 2 needs to reuse: `availability.Overlaps` (shared overlap test), `availability.ParseBookingTime` (4-format, site-timezone-aware date parsing, proven against all 743 real rows), `availability.NormalizeTag`, and the role/site redaction rules in `handlers/availability.go`.
- Three empirical experiments (ADR 0002) ruled out "trust `busy_timeout` alone" and confirmed a `BEFORE INSERT` trigger holds under real concurrent load using nothing but a bare `INSERT` — this is the evidence base for the chosen mechanism, not a guess.
- Certification data (`data/operators.csv`, `certifications` column): 115 entries across 40 operators, two date formats, 65% of operators mixing both *within the same field* (Part 1 recon). No code anywhere currently reads this column.
- `foreman_messages.jsonl:7` is still the only concrete evidence that subcontractors shouldn't be able to book (only view) — carried over from Part 1's A5.

## Assumptions and open questions

| # | Topic | Type | Position | Evidence | Consequence if wrong |
|---|---|---|---|---|---|
| B1 | **Who may create a reservation**: `ops_director` anywhere; `superintendent`/`foreman` only at a site in their own `site_ids`; `subcontractor` and `safety_manager` cannot create reservations at all (view-only, consistent with Part 1's A5 posture and the one piece of direct evidence available) | Assumed, low-risk | `foreman_messages.jsonl:7` ("routed to superintendent, not booked"); `safety_manager`'s stated job is cert oversight, not scheduling (same reasoning as Part 1's A5 carve-out) | A single role-check branch to widen if wrong — not an architecture change |
| B2 | **Resolved** (was Blocking). Tentative holds are visible and *do* protect the slot (a competing tentative-or-confirmed attempt gets an immediate conflict, not a silent override); every tentative hold has a **required** expiration, past which it's treated as released without a human action; confirming converts it to a firm reservation with the same atomic guarantee; the owner may release a hold early | **Resolved by explicit stakeholder instruction** (pasted verbatim into this session) | — | Materially expands scope versus the original draft: requires an `expires_at_utc` column, lazy (not background-swept) expiration, and two new narrow endpoints (confirm, release) acting on an existing `booking_id` — not just `POST /api/reservations`. See "Reservation lifecycle" below and [ADR 0002](../decisions/0002-reservation-concurrency-control.md)'s "Tentative-hold lifecycle" section for the full mechanism and its empirical verification. |
| B3 | **Certification expiry is evaluated against the booking's `end_at`** (must be valid for the *entire* window, not just the start), interpreted in the booking site's local timezone (same convention as Part 1's A3) | Assumed, same reasoning as A3 | Priya: *"if someone's cert expires in the middle of a booking, that's an OSHA problem"* — directly specifies end-of-window, not start | Same bounded, ≤1-hour blast radius as A3 if the timezone convention is wrong |
| B4 | **A reservation on cert-required equipment requires a valid `operator_id`**; non-cert equipment does not require one | Assumed, uncontested | `equipment.required_cert` already exists and is used this way nowhere yet; Priya's statement is unconditional for cert-required gear | Low — this is the plain reading of the data model |
| B5 | **Idempotency key is a required header** (`Idempotency-Key`, client-generated), not optional | Assumed | Marcus: *"checking this from a truck... with gloves on"* — a spotty-connection retry resubmitting the same `POST` is a realistic, not hypothetical, failure mode for this exact user | If declined, drop the idempotency table and the header requirement — isolated, not load-bearing elsewhere |
| B6 | **Who may confirm or release a hold**: the original requester (`booked_by` matches the caller), or anyone who holds B1-level booking authority at that site (e.g. a superintendent confirming/releasing a foreman's hold at their own site) | Assumed, new question introduced by B2's resolution | Not directly stated anywhere in the source material; this is the most natural reading of "the owner can confirm it... or explicitly release it," extended to match B1's existing authority model rather than inventing a second one | If confirm/release should be owner-only with no delegation, this narrows one `if`, not a redesign |

## Core invariant and transaction strategy

**Full evaluation, evidence, and the chosen mechanism are in [ADR 0002](../decisions/0002-reservation-concurrency-control.md)** — not duplicated here. Summary: `BEGIN IMMEDIATE` (app-level transaction boundary, bounded `busy_timeout`) **plus two triggers** — `BEFORE INSERT` and `BEFORE UPDATE OF status` (the second needed because confirming a hold is an `UPDATE`, which the insert trigger never sees) — as the schema-level backstop, empirically proven (5 sequential scenarios plus the original 10/10 concurrent-insert trials) to hold even with zero app-level discipline. Over three new additive columns: `start_at_utc`/`end_at_utc` (backfilled from Part 1's proven date parser) and `expires_at_utc` (new for the tentative-hold lifecycle, below). `PRAGMA journal_mode=WAL` so availability reads are never blocked by an in-flight reservation.

**What the triggers enforce vs. what application code enforces**, stated plainly since the review instructions asked for this explicitly:

| Enforced by the database | Enforced by application code |
|---|---|
| No two active bookings (confirmed, or tentative-and-not-yet-expired) for the same `asset_tag` ever have overlapping `start_at_utc`/`end_at_utc` ranges — on create *and* on confirm — holding even for a careless future caller | Authorization (who may book, confirm, or release — B1, B6) |
| Confirming an already-expired hold is rejected outright, before the overlap check even runs | Operator presence + certification validity for cert-required equipment (B3, B4) |
| | Input validation, idempotency-key handling, audit field population |
| | Expiration is lazy (evaluated at read/write time via `expires_at_utc` comparisons), not a background sweep — no scheduler infrastructure exists yet; noted as a reasonable later addition, not required for the guarantee |

## Equipment and site authorization

Per B1: reject with `403` (not the "redact and still 200" pattern Part 1 uses for reads — there's no partial reservation to show on a write) if the authenticated role/site doesn't permit booking at the requested `site_id`. Reuses Part 1's `auth.User`/site-matching logic; `safety_manager`'s `["*"]` site list does **not** grant booking rights here either, same reasoning as Part 1's redaction carve-out.

## Operator assignment and certification

1. If `equipment.required_cert` is non-empty, `operator_id` is required in the request (`400` if absent, naming the requirement).
2. Load the operator's `certifications` field, split on `;`, parse each entry's name and `(exp DATE)` suffix with a **new** `ParseCertExpiry` function mirroring `ParseBookingTime`'s two-format handling (ISO and `MM/DD/YYYY` — cert entries are date-only, no time component, so this is simpler than booking-time parsing, not more).
3. Find an entry whose name matches `equipment.required_cert` exactly (Part 1 recon already confirmed every `required_cert` value is held by at least one operator, by exact string match) and whose expiry (end-of-day, booking site's timezone, per B3) is **after** the reservation's `end_at`.
4. No matching, unexpired entry → `422` naming the missing/expired certification, not a generic validation error.

## Conflict semantics (confirmed / tentative / cancelled)

Per B2 (resolved): a booking blocks a new overlapping attempt if it's `confirmed`, or `tentative` and not yet past its `expires_at_utc`. `cancelled`, and `tentative`-but-expired, never block. This applies identically whether the new attempt is itself tentative or confirmed — the policy doesn't distinguish ("a competing reservation for overlapping dates should not silently take the equipment away," independent of what kind of reservation is competing). See both triggers in ADR 0002.

## Reservation lifecycle: create, confirm, release

`POST /api/reservations` creates a booking in either state, chosen by the caller:

- **`status: "tentative"`** requires an `expires_at` (or a `hold_minutes` duration the server converts to one) — point 3 of the resolved policy is explicit that an expiration is required, not optional. Visible to other users with requester, site, dates, and expiration (point 1) — this is a read-side change too: Part 1's availability response needs a `hold` object alongside `conflict` when the blocking row is a tentative hold, so "who has it and until when" is visible, not just "busy." Noted here since it touches the Part 1 endpoint; sized into task 3 below rather than a separate plan.
- **`status: "confirmed"`** directly — no expiration, immediately firm. For a superintendent who doesn't need the hold-and-confirm dance.

Two new, narrow endpoints — **status transitions only, not a general edit** (time range and equipment stay fixed; changing those is still out of scope, see below):

- **`POST /api/reservations/{id}/confirm`** — tentative → confirmed. `403` if the caller isn't the owner or B6-authorized; `409` with `"hold has expired"` if past `expires_at_utc` (caught via the confirm trigger's first `RAISE`); `409` with conflict detail if the slot was taken by someone else in the meantime (the confirm trigger's second `RAISE` — kept as defense-in-depth even though, under the current design, it's not reachable independently of expiry; see ADR 0002's note on this).
- **`POST /api/reservations/{id}/release`** — tentative → cancelled, before expiry. `403` under the same B6 rule. No conflict possible (releasing can only free a slot, never contest one), so no new trigger needed for this direction.

## Travel buffer between sites

Carried over from Part 1 (A7), still unresolved in the same way: Marcus's "half a day, minimum" has no agreed exact definition (calendar vs. business hours; all equipment or cranes only). **Scoped as optional/stretch for Part 2**, same treatment as Part 1 gave it — the core concurrency guarantee is the required deliverable per the challenge's own framing ("the central requirement is..."), and travel-buffer enforcement is an orthogonal, separately-approvable rule layered on top of (not a prerequisite for) the overlap invariant. If attempted: flat 12-hour gap required between the end of one booking and the start of the next for the *same asset* when `site_id` changes, enforced as an additional application-level check (not pushed into the trigger, since the exact rule is still unconfirmed).

## Input validation, idempotency, errors, and audit fields

- **Validation**: `asset_tag` must resolve to real, active equipment (reusing Part 1's `NormalizeTag`); `site_id` must be a real site; `start`/`end` parseable and `end > start` (reusing Part 1's validation); `operator_id` must reference a real operator when required (B4).
- **Idempotency** (B5): client sends `Idempotency-Key: <uuid>`. A new, small `idempotency_keys` table (`key TEXT PRIMARY KEY, booking_id TEXT, response_status INT, created_at TEXT`) records the outcome of the *first* request for a given key; a repeat with the same key returns the **original** response verbatim rather than re-running the conflict check (which could legitimately give a different answer the second time if state changed) — this is what makes a retried submission safe rather than merely survivable.
- **Error contract**:

  | Status | Meaning | Example |
  |---|---|---|
  | `201` | Created | the reservation, including `booking_id` |
  | `400` | Malformed/missing input | `{"error": "operator_id is required for equipment requiring NCCCO Mobile Crane"}` |
  | `403` | Not authorized for this site/role (B1) | `{"error": "not authorized to book at site S09"}` |
  | `409` | Real overlap, caught via `SQLITE_CONSTRAINT_TRIGGER`; or confirming a hold that's since expired | `{"error": "conflict", "conflict": {...or redacted, per Part 1's role rules}}` or `{"error": "hold has expired"}` |
  | `422` | Certification missing/expired for the window (B3/B4) | `{"error": "operator OP038's NCCCO Mobile Crane certification expires before the booking ends"}` |
  | `503` | Lock contention outlived `busy_timeout`, via `SQLITE_BUSY` | `{"error": "try again"}`, `Retry-After` header |

  `confirm`/`release` reuse this same contract (`403` for B6, `409` for the two confirm-specific rejections above, `404` if `booking_id` doesn't exist).

- **Audit fields**: `booked_by` is populated from the verified JWT's `sub` (not free text, unlike the 8-distinct-values-across-743-rows mess Part 1 found in the historical data), plus a server-generated `created_at` timestamp and the idempotency key used, if any.

## Deterministic concurrency test (designed now, written during implementation)

N goroutines (proposed: 30) each call the real HTTP handler concurrently (via `httptest`, in-process, no network) attempting to reserve the **same** asset for an **overlapping** window. Assert: exactly one `201`, all others `409` (never `500`, never a second `201`, never a hang past a bounded test timeout). Final DB state check: exactly one non-cancelled row for that asset in that window. Deterministic because it's real goroutines against a real (temp-file or in-memory) SQLite database with a fixed TTL/timeout — no dependency on the FleetCare mock's chaos or any wall-clock race. This directly answers Part 3A's ask ("fire many simultaneous reservation requests and show exactly one wins") using the same mechanism Part 2 ships with, not a separate proof.

A second, equally deterministic test for the confirm path: one tentative hold with `expires_at_utc` set to a fixed instant in the past (no need to actually wait for real time to pass — the trigger compares against `datetime('now')`, and a past `expires_at_utc` is trivial to construct), N goroutines all call `confirm` on it concurrently. Assert: all `409` with `"hold has expired"`, none succeed — proves the expiry check, not just the overlap check, holds under concurrency too.

## Task breakdown (small, ordered, each independently testable)

| # | Task | Size | Depends on |
|---|---|---|---|
| 1 | Schema migration: add `start_at_utc`/`end_at_utc`/`expires_at_utc` columns + both triggers (`no_overlapping_active_bookings`, `no_overlap_on_confirm`); backfill script using `availability.ParseBookingTime`, verified against the full real reseed (0 failures expected, per Part 1's own proof — report if wrong, don't weaken the rule) | M | B2 (resolved) |
| 2 | `ParseCertExpiry` (two-format, date-only) + unit tests against representative real operator rows | S | — |
| 3 | `POST /api/reservations` (tentative-or-confirmed, per the lifecycle section) + the availability response's new `hold` field: validation → authz (B1) → cert check (B3/B4, confirmed only or always? — recommend always, so a tentative hold on cert-required equipment can't later fail its own confirm; flagging as a sub-decision, not re-litigating B3/B4) → `BEGIN IMMEDIATE` → insert → map `SQLITE_CONSTRAINT_TRIGGER`/`SQLITE_BUSY` to 409/503 | M | 1, 2 |
| 4 | `POST /api/reservations/{id}/confirm` and `/release`: B6 authz → `BEGIN IMMEDIATE` → `UPDATE` → map both confirm-trigger `RAISE`s distinctly (expired vs. conflict) | M | 1, 3 |
| 5 | Idempotency-key table + middleware/check (create only — confirm/release are naturally idempotent via their own state checks, not re-using the same table) | S | 3 |
| 6 | Deterministic concurrency tests (both: concurrent create, and concurrent confirm-of-an-expired-hold, above) | S | 3, 4 |
| 7 | `security-review` pass over the new endpoints specifically (new SQL, new auth paths, new external-facing writes) — **required per the standing instruction that each new increment touching auth/SQL/external calls needs its own pass**, not covered by Part 1's reviews | S | 3, 4, 5 |
| 8 | *(optional, time-boxed)* Travel-buffer check | S | 3 |
| 9 | *(optional, time-boxed)* Minimal reservation form in the frontend, including the create/confirm/release flow — README: *"the UI for this can be minimal. The API and its guarantees matter more"* | M | 3, 4 |

## Test plan (write-tests categories)

- **Happy path**: valid reservation on open equipment, with/without a required operator, succeeds.
- **Boundaries**: the exact same overlap geometries Part 1 already proved for reads (contained, containing, partial-left/right, adjacent-non-overlapping) — reused, not reinvented, since the trigger and the read path should agree.
- **Invalid input**: missing fields, bad dates, nonexistent equipment/site/operator, end ≤ start.
- **Permissions**: each role × own-site vs. other-site vs. no-site-at-all (B1) for create; owner vs. non-owner vs. B6-authorized-other for confirm/release.
- **Concurrency**: both deterministic tests above (concurrent create; concurrent confirm-of-expired). Also sequential (non-concurrent, state-machine) coverage for every row of the "Empirically verified" table in ADR 0002, as real Go tests, not just the throwaway experiments that produced them.
- **Lifecycle**: confirm a valid hold succeeds; confirm an expired hold fails with the specific error; release before expiry succeeds; confirm/release on an already-resolved booking (`404` or a clear "already confirmed/released" error, not a confusing generic one).
- **Certification**: valid, expired mid-window, expired before window, missing entirely, missing cert for non-required equipment (should pass).
- **Idempotency**: same key twice returns the same response; different keys for the same request body create two attempts (second correctly conflicts if the first won).
- **External dependency**: N/A — this endpoint doesn't call FleetCare.
- **Data quality**: the backfill migration run against the full 743-row real dataset, asserting zero unparseable rows (reusing Part 1's exact verification pattern).

## Risks / out of scope

**Out of scope for this plan, explicitly:**
- A general edit (`PATCH`) of an existing reservation's time range or equipment — only the two narrow status transitions (confirm, release) are in scope, per B2's resolution. Changing *what* or *when* a booking is for is still not supported by anything in this plan.
- Active (background-job) expiration sweeping — expiration is lazy (evaluated at read/write time); a sweeper that proactively flips stale tentatives to a distinct `expired` status (rather than just treating them as non-blocking) is a reasonable later addition, not required for the guarantee B2 asks for.
- Postgres migration and multi-instance deployment — ADR 0002 states what would need to change; not built now.
- Operator-side double-booking (same operator, overlapping times, different equipment) — the challenge's Part 2 bullet is equipment-focused; this is a parallel, unaddressed race, noted for Part 3 consideration.
- Full audit trail / observability (structured logs, correlation IDs, metrics) — Part 3B.
- Travel buffer and the frontend form — optional/stretch, per their own rows above.
- Cert-expiry data quality fixes to `data/` itself — never touched, per standing instruction; handled defensively in `ParseCertExpiry` only.

**Risks:**
- The backfill (task 1) is new-data-touching in spirit even though it's additive and schema-only — recommend running it against a copy of the seeded DB first and reviewing the diff before it's considered final.
- B6 (who may confirm/release) is a new assumption, not directly evidenced — worth a quick confirm before task 4, since it's an authorization rule, not purely a reversible implementation detail.
- The lifecycle resolution roughly doubled this plan's endpoint count (1 → 3) and added a second trigger. If time is tight, `confirm`/`release` (tasks 4, 6's confirm-specific test) are the right things to cut before cutting anything in the core create-path concurrency guarantee, which is the one the challenge calls non-negotiable.

## Status log
- 2026-10-28: created, presented for approval. Nothing implemented.
- 2026-10-28: revised — B2 resolved by explicit stakeholder instruction (tentative holds protect, expire, and convert via an atomically-enforced confirm). Added: `expires_at_utc` column, a second (`BEFORE UPDATE`) trigger, two new endpoints (confirm, release), B6 (new assumption), and corresponding tasks/tests. Empirically verified the extended trigger design (5 sequential scenarios) the same way the original was verified. Still nothing implemented; re-presenting for approval with the larger scope made explicit rather than absorbed quietly.
