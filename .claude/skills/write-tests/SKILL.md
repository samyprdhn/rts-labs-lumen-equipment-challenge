---
name: write-tests
description: Use when adding or changing behaviour in backend-ts, backend-go or frontend. Enumerates cases first, then writes focused automated tests using this repo's tooling.
---

# Write tests

## 1. Enumerate before you write

Before writing any test code, produce a table of cases:

| # | Scenario | Input / setup | Expected | Why it matters |
|---|----------|---------------|----------|----------------|

Cover these categories, or state why one doesn't apply:
- Happy path
- Boundaries (empty, exactly-equal, first/last, limits)
- Invalid input
- Permissions (who must **not** be able to do or see this)
- External dependency failure
- Concurrency (two actors at once)
- Data quality (the seed data is a spreadsheet export)

Show the table to the human if the behaviour is ambiguous. Ambiguous cases are product questions.

## 2. Tooling

| Project | Runner | Command |
|---|---|---|
| backend-ts | vitest + supertest | `npm --prefix backend-ts test` |
| backend-go | `go test` | `cd backend-go && go test ./...` |
| frontend | vitest + Testing Library (jsdom) | `npm --prefix frontend test` |

## 3. Principles

- Tests must be deterministic and runnable offline. Don't depend on the FleetCare mock's random chaos. Inject a fake client or use `CHAOS=off` / `CHAOS_SEED`.
- Prefer testing through a public seam (HTTP handler, service function) over private internals.
- Each test sets up the data it needs. Tests that depend on whatever happens to be in the dev database are brittle, so flag them if you find them.
- A test that has never failed proves nothing. Where practical, see it fail first.
- Run the full suite before declaring done, and paste the summary line into your checkpoint.
