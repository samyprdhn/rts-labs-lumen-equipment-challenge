# CLAUDE.md

Context for AI coding agents working in this repo. Humans should read `README.md` first.

## What this is

This is an equipment scheduling system for **Lumen Builders**, a general contractor with 12 jobsites and a shared heavy-equipment fleet. It's replacing a shared spreadsheet. The code was started by a previous contractor and should be treated as **inherited code of unknown quality**.

This repo is also a hiring exercise. The human you are working with is being evaluated on their **judgement, architecture and process**, not on how much code gets produced. Help them think. Don't just do the work silently.

## Repo map

| Path | What |
|---|---|
| `frontend/` | React + TypeScript (Vite). Proxies `/api` → `localhost:8080`. |
| `backend-ts/` | TypeScript API (Express 5, `node:sqlite`). |
| `backend-go/` | Go API (`net/http`, `modernc.org/sqlite`). Same contract as backend-ts. |
| `db/schema.sql` | Current SQLite schema (loaded by the seed script). |
| `data/` | Raw exports from Lumen's spreadsheet and HR system. **Read-only source data.** |
| `services/maintenance-api/` | Mock of FleetCare, a third-party vendor API. **Do not modify**; treat it as external. |
| `scripts/` | `seed.mjs` (build SQLite from `data/`), `dev-token.mjs` (issue dev JWTs). |
| `docs/plan/` | Plans (one per piece of work) + `PROGRESS.md`. |
| `docs/decisions/` | Architecture Decision Records. |
| `.ai-log/` | Automatic log of prompts, tool calls and transcripts (via hooks in `.claude/settings.json`). |

The candidate will pick **one** backend (TS or Go) and may delete the other.

## Commands

```bash
npm run seed                 # (re)build db/lumen.db from data/
npm run token -- --list      # list demo users; npm run token -- u-for-01 to mint a JWT
npm run mock                 # FleetCare mock on :4010 (CHAOS=off to disable failures)
npm run dev:ts | dev:go      # API on :8080
npm run dev:web              # frontend on :5173
npm run ai-log               # summarise the AI log
npm --prefix backend-ts test
cd backend-go && go mod tidy && go test ./...
npm --prefix frontend test
```

## Working agreement

1. **Plan before code.** For anything beyond a one-line fix, use the `plan-first` skill and wait for the human to approve.
2. **Record significant decisions** with the `decision-record` skill.
3. **Enumerate test cases first** with the `write-tests` skill. Run tests before claiming something works.
4. **Checkpoint** with the `checkpoint` skill at natural stopping points. Keep commits small.
5. **Security review** with the `security-review` skill before a feature is called done.
6. **Surface problems, don't bury them.** If you notice an issue outside the current task, tell the human and add it to the plan's risks. Don't silently fix or ignore it.
7. **Ask when requirements are ambiguous.** The stakeholder notes in `README.md` are deliberately incomplete and sometimes contradictory.
8. Never read or print secrets from `.env`. Never add secrets, `.env` or `*.db` files to git.
9. Don't modify `data/` or `services/maintenance-api/`. If the data is messy, handle it in code or migrations.
10. Leave the `.ai-log/` hooks enabled. They are part of the submission.
