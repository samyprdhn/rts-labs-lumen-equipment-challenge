# Progress log

Append entries with the `checkpoint` skill (or by hand). Newest at the bottom.

## 2026-10-28 16:10 — Increment 1: booking-overlap and date-window correctness
- **Done:** New `backend-go/internal/availability` package (shared `Overlaps`, `ParseBookingTime`, `NormalizeTag`, `FindConflict`/`BuildEquipmentIndex`), wired into `handlers/availability.go`. Fixes: containment-vs-overlap bug, 4-format date parsing with site-timezone-aware DST handling, cancelled bookings no longer block, tentative bookings block-and-tagged (A1 default), asset-tag case/whitespace/hyphen normalization. Incidental: `type` query param parameterized (was string-concatenated).
- **Tests:** `go test ./...` (backend-go) — all packages `ok`, 17 new tests added (9 unit, 8 HTTP-regression including a full-real-743-booking-dataset data-quality check), 0 regressions in the 2 pre-existing tests.
- **Decisions / changes of direction:** none beyond what [ADR 0001](../decisions/0001-choose-go-backend.md) and the approved plan already covered. Go's `time.ParseInLocation` DST/layout behavior was empirically verified in a throwaway script before being relied on, per instruction.
- **Next:** Increment 2 — FleetCare integration and the approved degraded-mode policy.
- **Open questions / risks:** A1 (tentative-blocks default) and A3 (site-local-time assumption) are implemented per the plan's recommended defaults but still await your sign-off; reversing either is a small, isolated change, not a rewrite. Query-param time parsing is still interim pending increment 4.
