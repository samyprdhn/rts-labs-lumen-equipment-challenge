# 0002 — Reservation concurrency control (no two overlapping bookings ever both commit)

- **Status:** accepted
- **Date:** 2026-10-28 (revised same day — tentative-hold lifecycle resolved, see "Tentative-hold lifecycle" below; accepted for implementation same day, scoped to `POST /api/reservations` only — the confirm/release endpoints and their `BEFORE UPDATE` trigger are approved in design but explicitly deferred to a separate implementation pass, not built in this one)
- **Also the write-up for challenge Part 3A** ("prove the concurrency guarantee"): the "Options considered" and "Postgres / multi-instance" sections below are that explanation. The test itself is `backend-go/integration/reservation_concurrency_test.go` (`go test -tags integration ./integration/...`) — a real `cmd/api` process, real concurrent HTTP requests, 5 trials of 20 simultaneous requests for the same overlapping window, exactly one winner every time, verified against committed DB rows directly.

## Context

`POST /api/reservations` (Part 2) must guarantee that two overlapping reservations for the same asset never both commit, even when requests arrive at the same instant — "if two of my foremen grab the same excavator, fine, first one wins. But tell the second guy right away" (Marcus). A naive read-then-write (`SELECT` for conflicts, then `INSERT` if none found) is not that guarantee: between the `SELECT` and the `INSERT`, a second request can run the identical `SELECT`, see the same "no conflict," and also `INSERT` — both succeed, producing exactly the double-booking this system exists to prevent. This is not hypothetical: Part 1's recon found 18 pairs of confirmed-vs-confirmed double-bookings already sitting in the historical spreadsheet export, produced by the human equivalent of this exact race. I was explicitly told not to propose this sequence without an atomic guarantee underneath it, and I'm not.

Backend is Go, per [ADR 0001](0001-choose-go-backend.md); database is SQLite (`modernc.org/sqlite`, pure Go, no cgo), unchanged from Part 1.

Three throwaway experiments (outside the repo, not committed) informed this decision, each run as genuinely concurrent OS processes, not goroutines racing in one process:

1. **Plain `BEGIN IMMEDIATE`, no `busy_timeout`**: the losing writer fails in under 1ms with `SQLITE_BUSY` — confirms SQLite's single-writer lock is real and immediate.
2. **`BEGIN IMMEDIATE` with `PRAGMA busy_timeout=5000`**: the losing writer *waits* (observed ~338ms, matching the winner's hold time) instead of failing, then proceeds — **but both transactions still committed their own row**, because neither one's application code actually re-checked for a conflict inside the lock. This is the key finding: **`busy_timeout` changes the failure mode from "immediate error" to "wait and retry access," it does not by itself enforce the invariant.** The check-then-insert logic inside the lock is what has to be correct.
3. **A `BEFORE INSERT` trigger** (full SQL in Options, below) that rejects an overlapping, non-cancelled row: run 10 times as truly concurrent processes, each attempting a naive **bare `INSERT`** — no app-level check, no explicit transaction at all. **Exactly one row committed every single time**, the loser rejected with a specific, `errors.As`-catchable Go error (`*sqlite.Error`, `.Code() == 1811`, `SQLITE_CONSTRAINT_TRIGGER`) distinguishable from ordinary lock contention (`SQLITE_BUSY`, code 5). Also confirmed: in `PRAGMA journal_mode=WAL`, a `SELECT` (an availability read) completed in ~46ms while a writer held its lock mid-transaction for 300ms — readers are not blocked by an in-flight write.

## Options considered

### A. Application-level `BEGIN IMMEDIATE` transaction, check-then-insert inside it

The handler opens `BEGIN IMMEDIATE` (acquires SQLite's write lock up front, not lazily on first write — this is the detail that matters: a plain `BEGIN` defers lock acquisition, and an earlier experiment this engagement showed two plain-`BEGIN` transactions can both pass their own `SELECT` check before either writes). Inside that transaction: run the same `availability.Overlaps`/`FindConflict` logic already built in Part 1 against currently-committed rows; if clear, `INSERT`; either way, `COMMIT`/`ROLLBACK`.

- **Two concurrent requests:** the second's `BEGIN IMMEDIATE` blocks (with `busy_timeout` set) until the first commits or rolls back, then its own check correctly sees the first's result. Proven safe in an earlier experiment this engagement (two separate OS processes, `BEGIN IMMEDIATE`, one winner, one clean `SQLITE_BUSY`).
- **Transaction boundary:** the entire check-then-insert sequence, start to finish — this *is* the atomic unit, same pattern Part 1's `security-review` skill would expect to see on a write path.
- **Independent connections/instances:** SQLite's write lock is file-level, not connection-level or process-level — it serializes correctly across any number of connections or OS processes writing the *same file on the same machine*. It does **not** serialize across machines or networked filesystems; see "Postgres/multi-instance" below.
- **Lock contention / busy handling:** without `busy_timeout`, immediate `SQLITE_BUSY` on contention (bad UX for ordinary, non-malicious races — two foremen a few hundred ms apart). With a bounded `busy_timeout` (proposed: 3s, matching the FleetCare client's own bound from Part 1), ordinary contention resolves silently; pathological contention still bounds out to a catchable error rather than hanging.
- **Postgres / multi-instance:** the equivalent is a `SERIALIZABLE` transaction (or `SELECT ... FOR UPDATE` on a per-asset row) — Postgres's MVCC means the database itself, not a local file lock, is the coordination point, so this *does* extend correctly to multiple API instances/machines without further change, unlike SQLite's file-lock story.
- **Invariant enforced by DB vs. app:** the DB enforces *mutual exclusion* (nobody else writes while I hold the lock); the *overlap logic itself* is 100% application code. Nothing in the schema stops a different code path — a future admin tool, a bulk-import script, a bug in a second handler — from inserting a conflicting row without going through this exact function. This is Option A's real weakness: the invariant's correctness depends on every future writer remembering to use it.

### B. Database-level `BEFORE INSERT` trigger (recommended, combined with A)

```sql
CREATE TRIGGER no_overlapping_active_bookings
BEFORE INSERT ON bookings
WHEN NEW.status IN ('confirmed', 'tentative')
BEGIN
  SELECT RAISE(ABORT, 'overlapping active booking exists')
  WHERE EXISTS (
    SELECT 1 FROM bookings
    WHERE asset_tag = NEW.asset_tag
      AND (
        status = 'confirmed'
        OR (status = 'tentative' AND (expires_at_utc IS NULL OR datetime(expires_at_utc) > datetime('now')))
      )
      AND NEW.start_at_utc < end_at_utc
      AND start_at_utc < NEW.end_at_utc
  );
END;
```

(`start_at_utc`/`end_at_utc`/`expires_at_utc` — see "A necessary companion change" and "Tentative-hold lifecycle," below — not the existing `start_at`/`end_at` columns. The `WHEN` clause and the `expires_at_utc` condition were added after the policy resolution below; the originally-proposed version only excluded `status='cancelled'`.)

- **Two concurrent requests:** proven empirically, not just argued — 10/10 trials with genuinely concurrent processes doing nothing but a bare `INSERT`, exactly one winner every time. The trigger runs *inside* whatever transaction wraps the `INSERT` (explicit or SQLite's own implicit one), and because SQLite fully serializes writers, the second trigger invocation always sees the first's committed row before deciding.
- **Transaction boundary:** the `INSERT` statement itself — even the most careless possible caller (a bare, unwrapped `INSERT`, no app-level check at all) is still safe. This is strictly stronger than Option A alone.
- **Independent connections/instances:** same SQLite file-lock mechanics as Option A for *how* writers serialize — but the invariant itself is enforced by the schema, not by whichever handler happens to call it correctly. A second connection, a second process, a future endpoint, an admin script — all go through the same trigger, because it's attached to the table, not to one code path.
- **Lock contention / busy handling:** identical to Option A (same `busy_timeout`); the difference is what the loser's error *means*. `SQLITE_BUSY` (code 5) = "try again, transient contention." `SQLITE_CONSTRAINT_TRIGGER` (code 1811) = "there is a real, durable conflict, don't retry." Reliably distinguishable via `errors.As(err, &sqliteErr)` + `sqliteErr.Code()` (verified directly, not assumed) — this maps cleanly to 503-retry vs. 409-conflict without the application having to re-derive that distinction itself.
- **Postgres / multi-instance:** a PL/pgSQL trigger with the same logic would work, but Postgres's idiomatic mechanism for "no two rows with equal `asset_tag` whose ranges overlap" is a native `EXCLUDE` constraint (`tstzrange` + `btree_gist`) — declarative, self-documenting, and enforced via a GiST index rather than a per-insert linear scan. At a Postgres migration (Part 3C), I'd recommend replacing the trigger with an exclusion constraint, not porting the trigger verbatim.
- **Invariant enforced by DB vs. app:** the core invariant — *no two active bookings for one asset ever overlap* — lives entirely in the schema. This survives future code mistakes in a way Option A alone cannot. Application code is still responsible for everything the trigger can't see: authorization, operator/certification validity (a cross-table, business-policy check that doesn't belong in a row-level trigger), and the tentative-vs-tentative nuance below.

### Why not either alone

Option A alone is exactly the "implementation convenience" the review instruction warned against relying on solely — it's correct *only if every future writer remembers the pattern*, and this codebase's own history (the inherited availability bug, the inherited double-bookings) is evidence that assumption doesn't hold for this team under time pressure. Option B alone (a bare `INSERT` with no app-level `BEGIN IMMEDIATE`) is safe for the overlap invariant specifically, but gives up the natural place to *also* atomically write audit fields, check certification validity, and return a clean, well-shaped error — all of which want an explicit transaction boundary anyway.

## Decision

**Combine both.** The reservation handler opens `BEGIN IMMEDIATE` (bounded by a `busy_timeout`), performs authorization and certification checks, then attempts the `INSERT`; the trigger is the backstop that makes the core overlap invariant hold *even if the application-level check has a bug or a future code path skips it*. The trigger's `SQLITE_CONSTRAINT_TRIGGER` error is caught and mapped to `409 Conflict`; `SQLITE_BUSY` (if contention outlasts `busy_timeout`) maps to `503` with `Retry-After`. `PRAGMA journal_mode=WAL` is enabled so availability reads (Part 1's endpoint, and anyone polling it) are never blocked by an in-flight reservation write — confirmed empirically, not assumed.

### A necessary companion change: canonical UTC columns

The trigger's overlap check is a **raw SQL string comparison** — it cannot call Go's multi-format date parser. Part 1 found 4 incompatible raw date formats across the 743 historical bookings; comparing those directly in SQL would silently reproduce the exact string-comparison bug Part 1 fixed in application code, except now unfixable from Go. The fix: add two new, additive columns, `start_at_utc`/`end_at_utc` (strict ISO-8601 UTC text, which *does* sort correctly as a string), populated by application code — at write time for every new reservation, and via a **one-time backfill** for the 743 existing rows using the exact, already-tested `availability.ParseBookingTime` function from Part 1 (which already has zero parse failures proven against this exact dataset, including after a full reseed — see `docs/reviews` from Part 1). The original `start_at`/`end_at` columns are left untouched — this is additive, not a rewrite, and carries over Part 1's own evidence that the parser handles 100% of the real data rather than assuming it will. The trigger reads only the new canonical columns, making its string comparison provably safe.

## Tentative-hold lifecycle (resolves B2 from the Part 2 plan)

After this ADR was first written, the tentative-vs-confirmed question was resolved with a specific policy, not left to my own default:

1. A tentative hold is visible to other users (who, what site, requested dates, expiration) — not a silent block.
2. A tentative hold **does** protect the equipment: a competing request for overlapping dates gets an immediate conflict response, not a silent override.
3. Every tentative hold has a **required expiration deadline** and is treated as released once it passes, without a human action. The owner may confirm early or release early.
4. Confirming a hold converts it to a firm reservation, **atomically enforced the same way** — two simultaneous confirm attempts, or a confirm racing a new competing request, must not both succeed.

This is a real extension of scope, not just an answered question, flagged explicitly rather than absorbed quietly: it requires (a) an `expires_at_utc` column, (b) **lazy** expiration — a hold past its deadline is treated as released at read/write time, not swept by a background job (no scheduler infrastructure exists in this codebase yet; an active sweeper is a reasonable later addition, not required for the guarantee to hold), and (c) **two new, narrow endpoints** — confirm and release — acting on an existing `booking_id`, each a single status transition, not a general edit/`PATCH`. The `BEFORE INSERT` trigger above was revised accordingly (point 2 is now the`WHEN`/`expires_at_utc` clause). Point 4 needs a second trigger, because confirming a hold is an `UPDATE`, which a `BEFORE INSERT` trigger never sees:

```sql
CREATE TRIGGER no_overlap_on_confirm
BEFORE UPDATE OF status ON bookings
WHEN NEW.status = 'confirmed' AND OLD.status = 'tentative'
BEGIN
  SELECT RAISE(ABORT, 'hold has expired')
  WHERE OLD.expires_at_utc IS NOT NULL AND datetime(OLD.expires_at_utc) <= datetime('now');

  SELECT RAISE(ABORT, 'overlapping active booking exists')
  WHERE EXISTS (
    SELECT 1 FROM bookings
    WHERE asset_tag = NEW.asset_tag
      AND booking_id != NEW.booking_id
      AND (
        status = 'confirmed'
        OR (status = 'tentative' AND (expires_at_utc IS NULL OR datetime(expires_at_utc) > datetime('now')))
      )
      AND NEW.start_at_utc < end_at_utc
      AND start_at_utc < NEW.end_at_utc
  );
END;
```

**Empirically verified**, same discipline as the rest of this ADR (5 scenarios, sequential — the state-machine logic doesn't need true concurrency to prove, only the underlying overlap/lock mechanics already proven above do):

| Scenario | Result |
|---|---|
| A tentative hold past its `expires_at_utc` no longer blocks a new competing `INSERT` | ✅ new booking succeeds |
| A tentative hold still within its deadline blocks a new competing `INSERT` | ✅ rejected, `overlapping active booking exists` |
| Confirming an already-expired hold | ✅ rejected outright, `hold has expired` — never reaches the overlap check |
| Confirming a still-valid, non-conflicting hold | ✅ succeeds, `status` becomes `confirmed` |
| Confirming a hold whose slot was taken by someone else after it expired | ✅ rejected (via the expiry check — see note) |

**Note on the overlap-check branch of the confirm trigger:** under the current design, it's currently unreachable in practice — the `INSERT` trigger already prevents any competing booking from ever coexisting with a still-valid tentative hold, so by the time a confirm attempt could collide with something else, the hold being confirmed has necessarily already expired (caught by the first `RAISE`, as in the table above). It's kept anyway, deliberately, as defense-in-depth: if a future change relaxes the `INSERT` trigger's blocking rule, this backstop still protects the confirm path independently, without anyone having to remember the connection between the two triggers. The exact boundary race (a confirm attempt and a competing insert arriving within the same instant as a hold's expiry) is deterministic-testable and is specified as its own test case in the Part 2 plan's task breakdown, to be written during implementation.

**Release** (explicit early release of a still-tentative hold) needs no new trigger — `UPDATE bookings SET status='cancelled' WHERE booking_id=? AND status='tentative'` can only ever resolve a conflict, never create one. It needs an authorization check (proposed: the original requester, or anyone who could have confirmed it) but no concurrency protection beyond what SQLite's ordinary locking already gives a single-row `UPDATE`.

**Correction found during Part 2 implementation (`expires_at_utc` comparisons):** both `expires_at_utc > datetime('now')` above (and the confirm trigger's `OLD.expires_at_utc <= datetime('now')`) were originally written as a raw comparison against the TEXT column. That's wrong whenever the expiry and "now" share the same calendar day: this codebase's canonical UTC format is `...T...Z`, but SQLite's `datetime('now')` returns a space-separated string, and the `'T'` character (0x54) always lexicographically outranks a space (0x20) at that position — so an expiry from *earlier the same day* compared as "still in the future" (not yet expired) regardless of the actual time values that followed. Confirmed empirically (`sqlite3`, both the broken and fixed forms side by side) and caught by a real test failure that only manifested hours into the session, once "1 hour ago" stopped crossing a UTC-midnight boundary by chance. Fixed by wrapping the stored column in `datetime(...)` before comparing, in both the `INSERT` trigger above and the handler's app-level pre-check (`internal/handlers/reservations.go`'s `findActiveConflict`) — not yet re-verified in the confirm trigger above, since it isn't built; whoever builds it must carry the corrected form shown here, not the version that was in this ADR before this correction.

## Consequences

- **Easier:** the overlap invariant can never be silently reintroduced by a future handler, admin script, or bug — it's enforced by the schema. Error handling is simple and precise (`SQLITE_CONSTRAINT_TRIGGER` vs. `SQLITE_BUSY` are different, catchable conditions). Availability reads are never blocked by a reservation write (WAL). The tentative-hold lifecycle (expire, confirm, release) reuses the exact same mechanism — no second concurrency story to design or trust separately.
- **Harder:** SQLite triggers are less visible/discoverable than application code to someone unfamiliar with the schema (mitigated by this ADR and a schema comment). The backfill is a new, one-time migration step that needs its own verification pass against the real data before Part 2 ships (same discipline Part 1 used: run against the full reseed, report any row that doesn't parse, don't weaken the rule to force it to pass). Two triggers (insert, confirm-update) now need to be kept in sync by hand if the blocking rule ever changes — both are in one place in this ADR specifically so that's one documented edit, not two independent discoveries.
- **Scope:** the tentative-hold lifecycle resolution added two endpoints (confirm, release) and a lazy-expiration mechanism that weren't in the original Part 2 draft — called out explicitly in the Part 2 plan rather than absorbed silently, since it's materially more surface area than "just" `POST /api/reservations`.
- **Revisit when:** moving to Postgres (replace the trigger with a native `EXCLUDE` constraint — don't port the trigger) or moving to multiple API instances/machines sharing one database (the SQLite file-lock story doesn't extend across machines; Postgres's does, which is itself a reason to prioritize that migration before any multi-instance deployment, not after).
