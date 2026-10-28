# 0001 — Retain backend-go, delete backend-ts

- **Status:** accepted
- **Date:** 2026-10-28

## Context

The repo ships two parallel, functionally identical implementations of `GET /api/equipment/availability` (TypeScript/Express/`node:sqlite` and Go/`net/http`/`modernc.org/sqlite`). The challenge requires picking one. Lumen's IT manager (Leo Park) has stated constraints: Azure hosting, no Kubernetes, and FleetCare as a slow/unreliable vendor dependency the backend must integrate with defensively. The remaining work (auth fix, authorization, overlap-correctness fix, `POST /api/reservations` with a concurrency guarantee) is the same size either way, since both backends currently share identical bugs (confirmed by running an identical 19-scenario request matrix against both — see runtime evidence from this session): the containment-vs-overlap bug, the 4-format date-string comparison bug, the asset-tag join bug, no site/role filtering, full operator PII leak, and an unverified JWT signature.

## Options considered

1. **Keep backend-ts (Express, `node:sqlite`).**
   - Pros: `npm run setup` fully provisions it end-to-end in one documented command (Go needed an extra, undocumented `go mod tidy`); the one auth defect found in this language is a crash (fails *closed*) rather than silently serving data; `BEGIN IMMEDIATE` is one direct `.exec()` call away for tightening the reservation race window.
   - Cons: ships a Node runtime + `node_modules` to production; no structural deployment advantage for an Azure/no-K8s target.
2. **Keep backend-go (`net/http`, `modernc.org/sqlite`).**
   - Pros: compiles to a single static, dependency-free binary — smaller container image, faster cold start, nothing to install at runtime; a clean fit for Azure Container Apps/App Service for Containers without Kubernetes.
   - Cons: the one auth defect found in this language fails *open* — a malformed-but-JWT-shaped token is silently accepted (full data served, no error) because `json.Unmarshal`'s error return is discarded in the existing code (`internal/auth/auth.go`); `database/sql`'s `Begin()` takes a deferred (lazy) lock by default, so getting the same tight `BEGIN IMMEDIATE` race-window closure as TS requires writing raw SQL rather than using the `sql.Tx` API directly (confirmed by a two-process concurrent-write test against both drivers — SQLite itself serializes correctly either way, this only affects *where* the collision is caught); needs one extra manual setup step from a clean checkout.

A concurrency experiment (two processes racing a check-then-insert against the same SQLite file) showed both drivers correctly serialize writers with no corruption — this is a SQLite-level guarantee, not something either language implementation earns or loses.

## Decision

Retain **backend-go**, delete backend-ts. The near-term risks identified against Go (fail-open on a malformed token, deferred-lock default, one extra setup step) are real but are either one-line fixes (check the `Unmarshal` error; use raw `BEGIN IMMEDIATE` SQL; document `go mod tidy`) or already being addressed as part of the Part 1/2 work regardless of backend. Against that, Go's single static binary is a genuine, structural fit for Lumen's stated Azure-without-Kubernetes target, and that operational property doesn't disappear once the bugs are fixed — it's a lasting characteristic of the choice, whereas the near-term risks are one-time costs paid once during this engagement.

## Consequences

- **Easier:** production deployment story (single binary, small image, no Node runtime in the container); matches a team that's said "don't make me run Kubernetes."
- **Harder:** every error return in the Go code must be checked explicitly going forward — the fail-open auth bug is exactly what happens when that discipline lapses, and there's no implicit propagation safety net like JS exceptions give by default. The reservation endpoint's write path must use raw `BEGIN IMMEDIATE` SQL rather than `sql.Tx` defaults to get the same race-window tightness verified for TS.
- **Revisit when:** moving off SQLite to Postgres (Part 3C) — Go's `database/sql` story gets *better* here (a real `pgx` driver with native advisory locks/exclusion constraints), so this decision is not a trade-off that likely reverses later.
