# Submission

> Fill this in before you submit. Keep it concise. Bullet points are fine.
> This document plus a 45-minute conversation is how we'll understand your work.

## About me (optional)
- Name: Samir Pradhan
- Backend chosen: ~~TypeScript~~ / Go
- Database: SQLite / ~~Postgres~~
- Approx. time spent: 4 Hours
- AI tools used: Claude Code

## How to run it
```bash
# from a clean checkout — Go backend (backend-ts was removed, see f5a80d2)
cp .env.example .env        # then fill in real values (JWT_SECRET, FLEETCARE_API_KEY, ...)

npm --prefix frontend install
npm run seed                 # builds db/lumen.db from data/
cd backend-go && go mod tidy && cd ..

# in separate terminals:
npm run mock                 # FleetCare mock on :4010 (start before dev:go)
npm run dev:go                # API on :8080
npm run dev:web               # frontend on :5173, http://localhost:5173

# optional: get a demo JWT for testing the API
npm run token -- --list
npm run token -- u-for-01

# create a reservation (ops_director token can book any site)
curl -X POST http://localhost:8080/api/reservations \
  -H "Authorization: Bearer $(npm run token -- u-ops-01 | tail -1)" \
  -H "Content-Type: application/json" \
  -d '{"asset_tag":"SS-001","site_id":"S01","start":"2027-01-01T08:00:00Z","end":"2027-01-01T16:00:00Z","status":"confirmed"}'
```

> Note: `npm run setup` is stale — it still tries to install `backend-ts`, which no longer exists. Use the steps above instead.

### Running the tests

```bash
cd backend-go && go test ./...                           # unit/HTTP-level tests, ~90 cases, all packages
cd backend-go && go test -tags integration ./integration/... -v -count=1   # real-process concurrency proof (Part 3A) - see "Testing" below
npm --prefix frontend test -- --run
```

The integration tests never touch `db/lumen.db` — each one builds its own disposable SQLite database (seeds + backfills into a temp file) and runs the real `cmd/api` binary as its own OS process against it. `-count=1` matters: the test shells out to rebuild the server binary on every run, which Go's own test cache doesn't see as a changed input, so a cached "pass" can silently predate a real code change.

## What I built

**Part 0 — Backend choice.** Kept `backend-go`, deleted `backend-ts` (both started out functionally identical and shared the same bugs; Go won on Azure-hosting fit, no-Kubernetes constraint, and `database/sql`'s cleaner transaction story for Part 2). [ADR 0001](docs/decisions/0001-choose-go-backend.md).

**Part 1 — Trustworthy `GET /api/equipment/availability`.** Fixed the containment-vs-overlap bug, the 4-format date-string comparison, the asset-tag join, and the missing status filter (cancelled bookings no longer block). Added real JWT verification (the old code accepted any 2-dot garbage string), role/site-scoped authorization on conflict detail, and removed operator PII and `daily_rate_usd` from the response by role. Integrated FleetCare with a TTL cache, bounded timeouts, and a combined `available = bookingAvailable && maintenance.status == "ok"` rule so "maintenance data unknown" can't silently render as available. Frontend: loading/empty/error/retry states, and accessibility fixes (pinch-zoom restored, visible focus outline, real `<button>`s, labeled inputs, text label alongside the color status dot, 44px touch targets). [Plan](docs/plan/01-trustworthy-availability.md).

**Part 2 — `POST /api/reservations` (create only).** The smallest complete, correct version: role/site authorization, operator-certification validity for the whole booking window, and a concurrency guarantee that's actually proven, not just argued — two overlapping reservations for the same asset can never both commit, even at the exact same instant. Confirm, release, and the idempotency-key header are approved in the design but explicitly **not** built in this pass. [Plan](docs/plan/02-reservations.md) · [ADR 0002](docs/decisions/0002-reservation-concurrency-control.md).

**Part 3A — Prove the concurrency guarantee.** Done, via the same work Part 2's own completion bar required: a real-process, real-HTTP test (`backend-go/integration/reservation_concurrency_test.go`) fires 20 genuinely simultaneous reservation requests at the actual `cmd/api` binary, 5 times, and shows exactly one wins every time. The mechanism, and how it changes on Postgres or with multiple API instances, is written up in ADR 0002.

**Not attempted:** Part 3B (observability/audit trail), 3C (schema redesign + 10x-scale design), 3D (AI stretch goal — foreman text-message parsing). All deliberately out of scope for the time available; see "What I'd do next."

## What I found in the inherited code
Confirmed live against real seeded data (not just by reading the code) — full evidence table in [docs/plan/01-trustworthy-availability.md](docs/plan/01-trustworthy-availability.md).

| Issue | Severity | Fixed? | Notes |
|---|---|---|---|
| Containment check instead of overlap (`start>=q.start && end<=q.end`) missed any booking not fully inside the query window | High | Yes | `MC-007`/`BK-1005` showed available while actually booked, live |
| 4 incompatible raw date/time formats compared as strings | High | Yes | 743-row dataset, mixed formats; one shared parser now, unit-tested against the real data |
| Asset-tag join was exact-string against a dirty column (case/whitespace/hyphen mismatches) | High | Yes | 75/743 bookings (10.1%) invisible to the conflict check before the fix |
| No status filter — cancelled bookings still blocked | Medium | Yes | `BK-1368` |
| JWT signature never verified in either backend | Critical | Yes | A 2-dot garbage string with no signature was accepted and served the full equipment list |
| No role/site authorization at all | Critical | Yes | All 4 roles got an identical, unredacted response |
| Full operator PII (SSN fragment, DOB, phone, email, emergency contact...) in every conflict | Critical | Yes | `SELECT *` into the response; the query was deleted, not gated |
| `daily_rate_usd` (internal cost data) visible to every role, including subcontractors | Medium | Yes | Redacted by role |
| FleetCare maintenance windows never checked | High | Yes | Both backends had a literal `// TODO: check FleetCare` comment |
| CORS wildcard + credentials; full headers incl. `Authorization` logged; 500s leaked stack traces | Medium | Yes | |
| SQL injection via string-concatenated `type` query param | High | Yes | `type=mobile_crane' OR '1'='1'` returned every row, live, before the fix |
| Zero DB constraints of any kind (`db/schema.sql`) | Medium | Partially | Added two `BEFORE INSERT`/overlap-enforcing structures for Part 2's new table only; the rest of the schema is untouched (out of scope — see Part 3C) |
| 18 pre-existing confirmed-vs-confirmed double-bookings already in the historical spreadsheet data | High | N/A — data fact, not a code bug | `data/` is read-only per instructions; carried into Part 2's concurrency design instead. Re-checked under correct site-timezone handling during Part 2 and now reads 15, not 18 — see "Known issues" |
| No request body size limit or HTTP server timeouts | Medium | Yes | Found during Part 2's own security review, fixed since Part 2 was the first write endpoint where it mattered |
| Frontend: no focus outline, pinch-zoom disabled, non-semantic clickable `div`s, color-only status, unlabeled inputs, no loading/empty/error state | Medium | Yes | |
| Data inconsistency: `sites.csv` lists a superintendent for S06/S07 with no matching user in `users.json` | Low | No — can't fix, `data/` is read-only | Reported, not silently absorbed |
| Data inconsistency: `sites.csv` and `users.json` disagree about one person's role (superintendent vs. foreman) for the same sites | Low | No — can't fix, `data/` is read-only | Reported, not silently absorbed |

## Key decisions & trade-offs

- [0001 — Retain backend-go, delete backend-ts](docs/decisions/0001-choose-go-backend.md): both backends shared the same bugs; Go won on Azure-hosting fit, the no-Kubernetes constraint, and a cleaner transaction story for Part 2's write path.
- [0002 — Reservation concurrency control](docs/decisions/0002-reservation-concurrency-control.md): `BEGIN IMMEDIATE` (app-level transaction, holding an overlap pre-check and the `INSERT` together) **plus** a schema-level `BEFORE INSERT` trigger as backstop — proven empirically (not just argued) to hold even for a bare, naive `INSERT` with zero application-level discipline, and again under 5 trials of 20 genuinely concurrent real HTTP requests. Also covers why neither mechanism alone is enough, and how this changes on Postgres (native `EXCLUDE` constraint, not a ported trigger) or across multiple API instances (SQLite's write lock is a local file lock and doesn't coordinate across machines; Postgres's MVCC does).
- Tentative-hold lifecycle (visible, protects the slot, required expiration, atomic confirm) was a stakeholder-supplied policy I incorporated mid-plan — it roughly doubled Part 2's endpoint surface (confirm/release, a second trigger), which I flagged explicitly rather than absorbing quietly, then scoped this pass down to just `POST /api/reservations` (create) per instruction.
- Idempotency (a client-retry-safety mechanism) and the travel buffer were both deferred the same way: present in the plan, not built, called out rather than silently dropped.

## Data flow

```
Browser (React)
  → fetch() with Authorization: Bearer <JWT>
  → Go API (net/http, cors → logRequests → recoverErrors → limitBody middleware chain)
      → auth.RequireAuth: verify JWT signature/exp/nbf/iss/aud, attach *auth.User to the request context
      → GET /api/equipment/availability:
          - load equipment + bookings from SQLite
          - availability.BuildEquipmentIndex / FindConflict (shared overlap logic)
          - fleetcare.Cache.Status() per asset (TTL-cached; cold start/stale reads "unknown", never "ok")
          - available = bookingAvailable && maintenance.status == "ok"
          - redact conflict detail / daily_rate_usd / operator PII by role before responding
      → POST /api/reservations:
          - validate request shape; authorize (role + site); resolve equipment/site/operator; check cert validity
          - BEGIN IMMEDIATE on a raw *sql.Conn → app-level overlap pre-check → INSERT (trigger as backstop) → COMMIT/ROLLBACK
          - map SQLITE_CONSTRAINT_TRIGGER → 409, SQLITE_BUSY → 503 + Retry-After
  ← JSON response
Browser renders card grid / handles 4xx/5xx with a Retry button
```

FleetCare (`services/maintenance-api`) is called only by the cache's background refresh, never synchronously inline with a user's request — a slow or down FleetCare degrades to "unknown" (shown as unavailable), never to a hang or a crash.

## Testing

**Backend (`cd backend-go && go test ./...`):** 111 top-level tests (170 including subtests) across `auth`, `availability`, `certs`, `fleetcare`, `handlers`, `server` — all green. Covers happy path, boundaries (adjacent/touching overlap geometry), invalid input, permissions (every role × site combination), external-dependency failure (FleetCare, via deterministic `CHAOS_SEED`s, never live chaos), and data quality (the full 743-row historical reseed, 0 parse failures).

**Concurrency (`go test -tags integration ./integration/... -v -count=1`):** real `cmd/api` binary as its own OS process, real HTTP requests over a real loopback socket, disposable database — never `httptest`, never the developer's working `db/lumen.db`. 5 trials of 20 simultaneous requests for the same overlapping window → exactly one `201` and nineteen `409`s every time, cross-checked against committed DB rows directly. Disjoint-same-asset and overlapping-different-asset cases: 10/10 succeed in both.

**Regression-detection check:** I deliberately disabled the app-level overlap pre-check mid-implementation and reran the suite. It was caught by tests that assert on response *detail* (conflict body), but **not** by tests that only checked the status code — because the schema trigger backstop alone still returns 409. That's real evidence the trigger works independently of the app-level check (not just argued in the ADR), and it was also a real gap in my first draft of those tests, which I closed before restoring the check.

**A real bug the tests actually caught, not a flaky one:** re-running the suite hours later, on a later real-world date, `expired_tentative_does_not_block` failed where it had previously passed. The cause was genuine: both the trigger and the handler's pre-check compared `expires_at_utc` directly against SQLite's `datetime('now')` — our format is `...T...Z`, SQLite's is space-separated, and `'T'` always lexicographically outranks a space, so a hold that expired *earlier today* was wrongly treated as still protecting the slot. Earlier runs only passed because "1 hour ago" happened to cross a UTC-midnight boundary by chance. Fixed by wrapping the stored column in `datetime(...)` before comparing (both the trigger and the handler), re-verified across 3 consecutive full test runs and the real-process integration suite. Full detail in `docs/plan/PROGRESS.md`'s 2026-10-29 entry.

**Frontend (`npm --prefix frontend test -- --run`):** 15 tests, 3 files — loading/empty/error/retry states, accessibility (focus, labels, keyboard operability).

**Not covered:** confirm/release and idempotency have no tests because they have no implementation. The 12-hour inter-site travel buffer (Part 1's A7, Part 2's stretch item) was never attempted, so there's nothing to test there either.

## Questions I would have asked Lumen

- **Do tentative bookings block availability, or not?** Marcus wants a hold to protect him from losing a race; Dana's own words suggest tentatives shouldn't count. Assumed: they block (matches today's accidental behavior, now on purpose), tagged explicitly in the response so this is a one-line change if the answer is "no."
- **Exact role/site visibility rules**, especially for `safety_manager` (cert-oversight job, but `site_ids:["*"]` in the data) and `subcontractor` (outside company, should see fleet-wide free/busy but never booking detail). Assumed the narrower reading for both, reasoned from the README's own role descriptions and `foreman_messages.jsonl:7`.
- **Naive timestamp interpretation**: are unlabeled timestamps in site-local time, or UTC? Assumed site-local (humans typed these from a spreadsheet, thinking locally), which turned out to matter: re-checking the historical double-booking count under this assumption found 15 real overlapping pairs, not the 18 an earlier, less careful pass reported.
- **The exact travel-buffer rule** ("half a day, minimum" — calendar hours? business hours? cranes only, or everything?). Never implemented; would need an answer before attempting it for real.
- **Who else may confirm or release someone else's tentative hold** (a superintendent, on a foreman's hold at their own site?) — a new question the tentative-hold policy introduced. Assumed yes, matching the existing booking-authority model, but not implemented either way since confirm/release are deferred.
- **Is an `Idempotency-Key` header actually expected from the real mobile/field client**, or was that solvable with a simpler retry-safe design? Assumed required, per Marcus's "checking this from a truck... with gloves on" — not implemented in this pass.

## Known issues / limitations

- **Confirm, release, idempotency, and the travel buffer are not implemented.** All are approved in the design (`docs/plan/02-reservations.md`, ADR 0002) but were explicitly scoped out of this pass, not abandoned silently.
- **The 15-vs-18 double-booking discrepancy isn't fully reconciled.** Re-checking the historical data under correct site-timezone-aware UTC conversion finds 15 confirmed-vs-confirmed overlapping pairs, not the 18 an earlier recon pass reported. I verified the mechanism (timezone handling) on one pair by hand, but didn't do a full pair-by-pair reconciliation against the original list — see `docs/plan/PROGRESS.md`.
- **No request-level rate limiting** on either endpoint (pre-existing from Part 1, still true).
- **SQLite's concurrency guarantee is single-machine.** It serializes every writer against one file on one machine; it does not extend across multiple API instances or a networked filesystem. ADR 0002 covers the Postgres migration path for when that matters.
- **The schema has no constraints beyond what Part 2 added.** `db/schema.sql`'s original tables (sites, equipment, operators, users) still have no primary/foreign keys, uniqueness, or check constraints — only the new `bookings` columns and the overlap trigger were added. A real schema redesign is Part 3C, not attempted.
- A background sweeper for expired tentative holds doesn't exist (and isn't needed for the invariant — expiry is checked lazily, at read/write time — but it means an expired hold stays `tentative` in storage forever rather than flipping to a distinct `expired` state).
- One process from an earlier debugging pass (before I switched the real-process integration test to a pre-built binary rather than `go run`, which was itself leaving orphaned child processes) may still be running on the machine this was built on; flagged to the person running the session rather than killed, since that's not something this assistant does on its own initiative.

## What I'd do next

If this were going to production for Lumen in 3 months:

1. **Postgres migration** before anything else that needs multiple API instances — SQLite's file lock is this design's one real single-machine constraint, and ADR 0002 already specifies the replacement (`EXCLUDE` constraint over `tstzrange`+`btree_gist`, not a ported trigger).
2. **Confirm/release endpoints + idempotency**, finishing the reservation lifecycle that's already designed and partially built.
3. **Observability** (Part 3B): structured logs with request correlation IDs, FleetCare latency/error metrics, health/readiness checks beyond the current bare `/healthz`, and an audit trail of who booked/changed what — right now that's only implicit in the `booked_by`/`created_at` columns, not a queryable log.
4. **Real SSO** (Entra ID, per Lumen's IT constraints) instead of the current dev-token/shared-secret JWT scheme, which is fine for this exercise but not for a real deployment.
5. **Schema redesign for scale** (Part 3C): proper constraints, indexes on `asset_tag`/`site_id`/the UTC date columns, and a design for 120 sites / 600 assets / possibly multiple client companies sharing one deployment — including revisiting the FleetCare batch/rate-limit strategy at that volume.
6. **Resolve the open stakeholder questions above** before any of them become load-bearing for a real rollout, especially the tentative-blocking and role-visibility defaults, which are reversible now but get more expensive to change the longer real data accumulates under the wrong assumption.
7. **Reconcile the 15-vs-18 discrepancy properly** — it's a signal that the original recon methodology had a real, specific bug (naive-UTC vs. site-local interpretation), and that's worth nailing down precisely before trusting any other number from that earlier pass.

## How I used AI

I used Claude Code throughout, directing it with explicit, staged instructions rather than one broad "build Part 2" prompt: read-and-report before proposing, propose a plan before implementing, implement one increment at a time with tests-first, then security-review the diff — and I reviewed and triaged its plans and findings at each stage rather than accepting them wholesale (e.g. the Part 1 plan review's findings were explicitly split into "fold in now" vs. "not worth it yet" by me before any fix was applied).

Where it helped most: the mechanical, evidence-gathering work (running both backends against the same 19-scenario matrix, counting real data-quality issues in `data/*.csv`, tracing the exact file:line of each inherited bug) and the test-writing discipline (enumerate cases before code, always). The real-process concurrency test and the deliberate-break regression-detection proof are the kind of thorough, slightly tedious verification work that's easy to skip under time pressure and that I specifically asked for rather than accepted "it should work" as an answer.

Where I had to correct it: it initially recommended TypeScript over Go on a weaker evidence base than I wanted — I overrode that and told it to use Go, and it recorded the override honestly in the ADR rather than retrofitting a Go-favoring argument. It also needed to be told explicitly, more than once, not to let Part 2 feel more interesting than finishing Part 1's six increments first, and not to broaden scope (e.g. fixing a security finding) without flagging it first — both instructions it then followed. During Part 2 I caught it making a methodology error on the historical double-booking recount (worth a second look, noted above) and had it investigate and report that honestly instead of smoothing it over. I also directly blocked several of its attempted actions — writing a real token to a file, running a seed/reset against the working database, killing an arbitrary process — via the sandbox's own safeguards, and in each case it stopped rather than finding a workaround, and told me what it needed instead.

The clearest example of AI-written code being wrong in a way neither of us caught by inspection: the `expires_at_utc` comparison it wrote (`expires_at_utc > datetime('now')`) looked correct and passed every test for hours, until a routine pre-submission rerun — on a later real-world date — failed a test that had previously passed. Rather than treat that as flakiness and retry, I had it find the actual mechanism (a lexicographic-comparison bug that only misbehaves for same-day expiries), prove it with a standalone `sqlite3` probe before touching any code, fix both occurrences (the trigger and the handler), and rerun everything — including the real-process integration suite — three times to confirm it was genuinely fixed and not just time-lucky again. That sequence (a test catching something real, demanding a mechanism before a fix, re-verifying rather than assuming) is the main thing I was directing it to do throughout, not just on this one bug.
