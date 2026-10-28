# 02 — Reservations (Part 2)

**Status:** draft — presenting for approval, nothing implemented yet
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
| B2 | **Tentative-vs-tentative overlap is allowed** (two tentative holds on the same slot can coexist); **any confirmed booking blocks everything** (tentative or confirmed); **cancelled never blocks** | **Blocking** — this is Dana vs. Marcus's unresolved contradiction from Part 1, now with real write consequences, not just a display question | Dana: *"tentative bookings shouldn't block anything... people put tentatives on everything 'just in case'"* (most literally satisfied by tentative-vs-tentative not blocking); Marcus: *"first one wins"* (satisfied by confirmed always blocking) | If tentative-vs-tentative should also be first-come-first-served, this is a trigger `WHERE` clause change (see ADR), not a rewrite. The harder sub-question — what happens to a *losing* tentative when a competing one gets confirmed — has **no resolution mechanism in this plan at all** (no cancel/update endpoint is in scope; see Out of scope). Flagging, not solving. |
| B3 | **Certification expiry is evaluated against the booking's `end_at`** (must be valid for the *entire* window, not just the start), interpreted in the booking site's local timezone (same convention as Part 1's A3) | Assumed, same reasoning as A3 | Priya: *"if someone's cert expires in the middle of a booking, that's an OSHA problem"* — directly specifies end-of-window, not start | Same bounded, ≤1-hour blast radius as A3 if the timezone convention is wrong |
| B4 | **A reservation on cert-required equipment requires a valid `operator_id`**; non-cert equipment does not require one | Assumed, uncontested | `equipment.required_cert` already exists and is used this way nowhere yet; Priya's statement is unconditional for cert-required gear | Low — this is the plain reading of the data model |
| B5 | **Idempotency key is a required header** (`Idempotency-Key`, client-generated), not optional | Assumed | Marcus: *"checking this from a truck... with gloves on"* — a spotty-connection retry resubmitting the same `POST` is a realistic, not hypothetical, failure mode for this exact user | If declined, drop the idempotency table and the header requirement — isolated, not load-bearing elsewhere |

## Core invariant and transaction strategy

**Full evaluation, evidence, and the chosen mechanism are in [ADR 0002](../decisions/0002-reservation-concurrency-control.md)** — not duplicated here. Summary: `BEGIN IMMEDIATE` (app-level transaction boundary, bounded `busy_timeout`) **plus** a `BEFORE INSERT` trigger (schema-level backstop, empirically proven to hold under concurrent bare `INSERT`s with zero app-level discipline required), over two new additive `start_at_utc`/`end_at_utc` columns backfilled from Part 1's already-proven date parser. `PRAGMA journal_mode=WAL` so availability reads are never blocked by an in-flight reservation.

**What the trigger enforces vs. what application code enforces**, stated plainly since the review instructions asked for this explicitly:

| Enforced by the database | Enforced by application code |
|---|---|
| No two non-cancelled bookings for the same `asset_tag` ever have overlapping `start_at_utc`/`end_at_utc` ranges — this holds even for a careless future caller | Authorization (who may book what, B1) |
| | Operator presence + certification validity for cert-required equipment (B3, B4) |
| | Tentative-vs-tentative policy nuance (B2) — deliberately *not* pushed into the trigger, since it's a business-policy question that may change, not a hard invariant |
| | Input validation, idempotency-key handling, audit field population |

## Equipment and site authorization

Per B1: reject with `403` (not the "redact and still 200" pattern Part 1 uses for reads — there's no partial reservation to show on a write) if the authenticated role/site doesn't permit booking at the requested `site_id`. Reuses Part 1's `auth.User`/site-matching logic; `safety_manager`'s `["*"]` site list does **not** grant booking rights here either, same reasoning as Part 1's redaction carve-out.

## Operator assignment and certification

1. If `equipment.required_cert` is non-empty, `operator_id` is required in the request (`400` if absent, naming the requirement).
2. Load the operator's `certifications` field, split on `;`, parse each entry's name and `(exp DATE)` suffix with a **new** `ParseCertExpiry` function mirroring `ParseBookingTime`'s two-format handling (ISO and `MM/DD/YYYY` — cert entries are date-only, no time component, so this is simpler than booking-time parsing, not more).
3. Find an entry whose name matches `equipment.required_cert` exactly (Part 1 recon already confirmed every `required_cert` value is held by at least one operator, by exact string match) and whose expiry (end-of-day, booking site's timezone, per B3) is **after** the reservation's `end_at`.
4. No matching, unexpired entry → `422` naming the missing/expired certification, not a generic validation error.

## Conflict semantics (confirmed / tentative / cancelled)

Per B2: the trigger's `WHERE NEW.status != 'cancelled'` / `AND status != 'cancelled'` clauses currently match Part 1's conservative default (tentative blocks everything, same as Part 1's availability reads) — **this needs to change to "confirmed blocks tentative-and-confirmed; tentative blocks only confirmed, not other tentatives"** once B2 is approved, which is a one-clause change to the trigger (documented in the ADR), not a rewrite. Implementation should not start on this specific behavior until B2 is confirmed, since it changes the schema object itself.

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
  | `409` | Real overlap, caught via `SQLITE_CONSTRAINT_TRIGGER` | `{"error": "conflict", "conflict": {...or redacted, per Part 1's role rules}}` |
  | `422` | Certification missing/expired for the window (B3/B4) | `{"error": "operator OP038's NCCCO Mobile Crane certification expires before the booking ends"}` |
  | `503` | Lock contention outlived `busy_timeout`, via `SQLITE_BUSY` | `{"error": "try again"}`, `Retry-After` header |

- **Audit fields**: `booked_by` is populated from the verified JWT's `sub` (not free text, unlike the 8-distinct-values-across-743-rows mess Part 1 found in the historical data), plus a server-generated `created_at` timestamp and the idempotency key used, if any.

## Deterministic concurrency test (designed now, written during implementation)

N goroutines (proposed: 30) each call the real HTTP handler concurrently (via `httptest`, in-process, no network) attempting to reserve the **same** asset for an **overlapping** window. Assert: exactly one `201`, all others `409` (never `500`, never a second `201`, never a hang past a bounded test timeout). Final DB state check: exactly one non-cancelled row for that asset in that window. Deterministic because it's real goroutines against a real (temp-file or in-memory) SQLite database with a fixed TTL/timeout — no dependency on the FleetCare mock's chaos or any wall-clock race. This directly answers Part 3A's ask ("fire many simultaneous reservation requests and show exactly one wins") using the same mechanism Part 2 ships with, not a separate proof.

## Task breakdown (small, ordered, each independently testable)

| # | Task | Size | Depends on |
|---|---|---|---|
| 1 | Schema migration: add `start_at_utc`/`end_at_utc` columns + the `no_overlapping_active_bookings` trigger; backfill script using `availability.ParseBookingTime`, verified against the full real reseed (0 failures expected, per Part 1's own proof — report if wrong, don't weaken the rule) | M | B2 (trigger's exact `WHERE` clause) |
| 2 | `ParseCertExpiry` (two-format, date-only) + unit tests against representative real operator rows | S | — |
| 3 | `POST /api/reservations` handler: validation → authz (B1) → cert check (B3/B4) → `BEGIN IMMEDIATE` → insert → map `SQLITE_CONSTRAINT_TRIGGER`/`SQLITE_BUSY` to 409/503 | M | 1, 2 |
| 4 | Idempotency-key table + middleware/check | S | 3 |
| 5 | Deterministic concurrency test (above) | S | 3 |
| 6 | `security-review` pass over the new endpoint specifically (new SQL, new auth path, new external-facing write) — **required per the standing instruction that each new increment touching auth/SQL/external calls needs its own pass**, not covered by Part 1's reviews | S | 3, 4 |
| 7 | *(optional, time-boxed)* Travel-buffer check | S | 3 |
| 8 | *(optional, time-boxed)* Minimal reservation form in the frontend — README: *"the UI for this can be minimal. The API and its guarantees matter more"* | M | 3 |

## Test plan (write-tests categories)

- **Happy path**: valid reservation on open equipment, with/without a required operator, succeeds.
- **Boundaries**: the exact same overlap geometries Part 1 already proved for reads (contained, containing, partial-left/right, adjacent-non-overlapping) — reused, not reinvented, since the trigger and the read path should agree.
- **Invalid input**: missing fields, bad dates, nonexistent equipment/site/operator, end ≤ start.
- **Permissions**: each role × own-site vs. other-site vs. no-site-at-all (B1).
- **Concurrency**: the deterministic 30-goroutine test above; also a mixed test (one tentative + one confirmed racing, per whatever B2 resolves to).
- **Certification**: valid, expired mid-window, expired before window, missing entirely, missing cert for non-required equipment (should pass).
- **Idempotency**: same key twice returns the same response; different keys for the same request body create two attempts (second correctly conflicts if the first won).
- **External dependency**: N/A — this endpoint doesn't call FleetCare.
- **Data quality**: the backfill migration run against the full 743-row real dataset, asserting zero unparseable rows (reusing Part 1's exact verification pattern).

## Risks / out of scope

**Out of scope for this plan, explicitly:**
- Updating or cancelling an existing reservation (`PATCH`/`DELETE`) — not asked for by the challenge's Part 2 bullet, and B2's "losing tentative" problem has no resolution without it; flagged, not solved.
- Postgres migration and multi-instance deployment — ADR 0002 states what would need to change; not built now.
- Operator-side double-booking (same operator, overlapping times, different equipment) — the challenge's Part 2 bullet is equipment-focused; this is a parallel, unaddressed race, noted for Part 3 consideration.
- Full audit trail / observability (structured logs, correlation IDs, metrics) — Part 3B.
- Travel buffer and the frontend form — optional/stretch, per their own rows above.
- Cert-expiry data quality fixes to `data/` itself — never touched, per standing instruction; handled defensively in `ParseCertExpiry` only.

**Risks:**
- B2 is a real, unresolved product question with write consequences — starting task 1 (which bakes the trigger's exact semantics into the schema) before it's answered risks rework. Recommend confirming B2 before task 1 begins.
- The backfill (task 1) is new-data-touching in spirit even though it's additive and schema-only — recommend running it against a copy of the seeded DB first and reviewing the diff before it's considered final.

## Status log
- 2026-10-28: created, presented for approval. Nothing implemented.
