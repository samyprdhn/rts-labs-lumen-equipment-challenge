# RTS Labs Engineering Challenge: Lumen Builders Equipment Scheduling

Welcome, and thanks for spending time on this.

You're joining an RTS Labs engagement as the consultant engineer. Our client, **Lumen Builders**, is a fictional mid-size general contractor. They have a scheduling problem that's costing them real money. A previous contractor started building a replacement for their spreadsheet and then left. You're picking it up.

**This challenge is used for every level, from early-career to principal.** Nobody is expected to finish everything. Pick the depth that shows your best thinking. A small, correct, well-reasoned submission beats a large, shaky one.

**AI tools are expected and encouraged.** This repo is set up for [Claude Code](https://docs.claude.com/en/docs/claude-code) with project skills and automatic logging of your session. We care much more about **how you think, decide and direct the tools** than about how many lines get written.

---

## 1. The client

Lumen runs **12 active jobsites** across Virginia, North Carolina and Tennessee. They share a fleet of about **60 pieces of heavy equipment**: mobile and tower cranes, excavators, dozers, lifts and telehandlers. Some of it requires a certified operator.

Today everything is scheduled in a shared Excel workbook. Here is what we heard in discovery.

> **Dana Whitaker, Director of Operations**
> "Last month a 90-ton crane went to Short Pump while the steel crew at Riverside stood around for two days. That's about $38k in idle labor, and it happens every month or two. I want one place where anyone can see what's actually free."
> "The number one complaint is equipment that looks available in the sheet but is actually in the shop. The shop uses FleetCare for maintenance, and nobody checks it."
> "Honestly, tentative bookings shouldn't block anything. People put tentatives on everything 'just in case'."

> **Marcus Bell, Superintendent (Riverside, Scott's Addition, James River Bridge)**
> "I'm checking this from a truck or a lift, on my phone, with gloves on and sun on the screen. If it's fiddly, nobody will use it."
> "You can't move a crane across town in zero minutes. Figure half a day between sites, minimum."
> "If two of my foremen grab the same excavator, fine, first one wins. But tell the second guy *right away*, not when the machine doesn't show up."
> "If I put a tentative on the crane, it's because I need it. Don't let somebody take it out from under me."

> **Priya Nair, Safety Manager**
> "Nobody operates a crane, dozer or lift without a current certification. If someone's cert expires in the middle of a booking, that's an OSHA problem, and it has happened."
> "Operator records come from HR. They have personal information in them. Only people who need it should see it."

> **Leo Park, IT Manager**
> "We're a Microsoft 365 shop. Eventually everyone should sign in with their work account (Entra ID). Subcontractors get guest accounts."
> "FleetCare is a vendor system. Their API is slow and goes down. We can't change it, and they rate-limit us."
> "We'll probably host in Azure. Please don't make me run Kubernetes."

### Users and roles

Demo users live in `data/users.json`. Lumen hasn't fully defined permissions yet. This is what we know:

| Role | What we know |
|---|---|
| `ops_director` | Sees and manages everything. |
| `superintendent` | Runs one or more sites and manages equipment bookings for them. |
| `foreman` | Works at specific sites and requests or books equipment for them. |
| `safety_manager` | Oversees operator certifications company-wide. |
| `subcontractor` | An outside company working on a site. Needs to see the equipment schedule for their site. |

Where the rules are unclear, **make a reasonable call and write it down**, or list it as a question you'd take back to the client.

---

## 2. What's in the repo

```
frontend/                  React + TypeScript (Vite)
backend-ts/                TypeScript API (Express 5, node:sqlite)   ← pick ONE backend
backend-go/                Go API (net/http, modernc sqlite)         ←
db/schema.sql              Current schema (from the previous contractor)
data/                      Raw exports: sites, equipment, operators, bookings, users,
                           foreman_messages.jsonl (for Part 3D)
services/maintenance-api/  Mock of FleetCare, the vendor maintenance API (don't modify)
scripts/                   seed.mjs, dev-token.mjs
docs/plan/                 Your plans + PROGRESS.md
docs/decisions/            Your architecture decision records (ADRs)
docs/ai-log/               Exports from non-Claude AI tools, if used
.claude/                   Claude Code skills + logging hooks
CLAUDE.md                  Context for AI agents
SUBMISSION.md              Fill this in at the end
```

Both backends implement the same single endpoint, `GET /api/equipment/availability`. **Choose one** (TypeScript or Go) and feel free to delete the other. You may switch from SQLite to Postgres (`docker compose up postgres`), but you don't have to.

The existing code *runs*. Whether it's *right* is another matter. **Treat it like any codebase you inherit on a client project.**

## 3. Getting started

Prerequisites: **Node 22.13+** (always needed), and **Go 1.22+** only if you choose the Go backend. Docker is optional.

```bash
git init && git add -A && git commit -m "chore: initial import"   # commit history is part of your submission
[ -f .env ] || cp .env.example .env
npm run setup               # installs frontend + backend-ts deps and seeds SQLite

# in separate terminals
npm run mock                # FleetCare mock on :4010  (CHAOS=off npm run mock for a calm version)
npm run dev:ts              # or: cd backend-go && go mod tidy && go run ./cmd/api
npm run dev:web             # http://localhost:5173

npm run token -- --list     # demo users
npm run token -- u-sup-01   # mint a dev JWT; paste it into the web app
```

Try the API directly:

```bash
TOKEN=$(npm run -s token -- u-for-01)
curl -s -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8080/api/equipment/availability?type=mobile_crane&start=2026-10-01&end=2026-10-31" | jq
```

**If you're using Claude Code**, start it from the repo root. The skills in `.claude/skills/` (`plan-first`, `decision-record`, `write-tests`, `security-review`, `checkpoint`, `submission`) are there to help you work the way we work on client projects. Using them is optional. Directing your agent well is what we're looking for.

---

## 4. The challenge

### Part 0: Understand and plan (required)

- Read the code, schema and data. Run the app.
- Write a plan in `docs/plan/01-<slug>.md`. The template and the `plan-first` skill will help.
- Make a list of **problems you find in the inherited code and data**, ranked by what matters most to Lumen. You don't have to fix them all. Show us you see them and can prioritise.

### Part 1: Trustworthy availability (required)

Make **"what equipment is free for these dates?"** something a superintendent can trust.

- The `GET /api/equipment/availability` results should be **correct**, taking into account existing bookings and FleetCare maintenance windows.
- The endpoint should behave sensibly **when FleetCare is slow, down or rate-limiting**. Decide what "sensibly" means for Lumen and explain it.
- Users should see only what their role and sites allow.
- The UI should work for Marcus: on a phone, and **accessible**. It needs clear loading, empty and error states.
- Include tests that give you, and us, confidence.

### Part 2: Reserve equipment (expected for mid-level and above)

Add **`POST /api/reservations`** (and whatever else you need) so a user can book a piece of equipment for a site and a time range.

- Two people booking the same equipment for overlapping times **must not both succeed**, even at the same instant.
- If the equipment requires a certified operator, the assigned operator's certification must be valid for the whole booking.
- Enforce who may book what.
- Return errors a client can act on.
- The UI for this can be minimal. The API and its guarantees matter more.

### Part 3: Deep dive (pick at least one if you're senior or above; optional for everyone else)

- **A. Prove the concurrency guarantee.** Write a test that fires many simultaneous reservation requests and shows exactly one wins. Explain the mechanism you rely on, and how it would change on Postgres or with multiple API instances.
- **B. Production readiness.** Add observability that would let you run this for Lumen: structured logs, request correlation, metrics on FleetCare latency and errors, health and readiness checks, and an audit trail of who booked or changed what. Tell us what you'd alert on and why.
- **C. Data and scale.** Redesign the schema properly and migrate the messy spreadsheet data into it (dates, time zones, asset tags, certifications). Then write a short design for about 10× scale: 120 sites, 600 assets, and possibly multiple client companies on one deployment. Cover indexes, caching and the FleetCare rate limit.
- **D. AI stretch goal: foreman text requests.** Foremen text requests like *"need a 90 ton crane at riverside tues thru thurs next week"*. Build a feature that turns a message into a **draft** reservation for a human to confirm. `data/foreman_messages.jsonl` has 24 labelled examples. Use it to build an **evaluation harness** and report your scores. Consider prompt injection, permissions, personal data sent to the model, cost and latency, and how you'd monitor quality over time. (A stubbed or mocked model is acceptable if you don't have an API key. We care most about the design and the eval.)

---

## 5. What to submit

A git repository (or zip of one) that includes:

1. **Your code**, with a commit history that shows how you worked.
2. **`SUBMISSION.md`**, filled in. This is important.
3. **`docs/plan/`** and **`docs/decisions/`**: your plans, progress log and ADRs.
4. **`.ai-log/`**: this is generated automatically if you use Claude Code. If you use other AI tools, put exports in `docs/ai-log/`. See `docs/ai-log/README.md`.

Please **don't commit secrets** or local database files.

## 6. Ground rules

- Use any libraries, frameworks or AI tools you like.
- Don't modify `data/` or `services/maintenance-api/`. Treat them as the client's data and a vendor's system.
- Questions? Email us, or make an assumption, write it down and keep going. Both are fine, and how you handle ambiguity is part of what we look at.
- **Stop at 4 hours.** Unfinished work plus a clear "what I'd do next" is a normal, good outcome.
- If you need an accommodation (more time, a different format, anything else), just tell us. It won't affect your evaluation.

## 7. How we'll evaluate

We're not counting features. Across your code, docs, commits and AI log, we look at:

- **Understanding the problem**: did you solve Lumen's problem, not just the ticket?
- **Technical judgement**: what you prioritised, what you deliberately didn't do, and why.
- **Architecture**: separation of concerns, abstractions, data modelling, and data flow from browser to database.
- **Correctness and resilience**: edge cases, failure handling, concurrency.
- **Security**: authentication, authorization, and handling of sensitive data.
- **Quality**: tests, readability, accessibility.
- **Delivery**: plan, commits, documentation, SDLC habits.
- **Working with AI**: how you directed, verified and corrected your tools.

Afterwards there's a **45-minute conversation**. You'll walk us through your work, and we'll explore a few "what if" extensions together. No trick questions.

Good luck, and have fun with it.
