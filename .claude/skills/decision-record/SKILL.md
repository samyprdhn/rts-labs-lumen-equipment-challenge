---
name: decision-record
description: Use when making a significant or hard-to-reverse technical decision (database choice, concurrency strategy, auth approach, API shape, handling of a vendor outage, adopting an LLM). Writes a short Architecture Decision Record to docs/decisions/.
---

# Architecture Decision Record

1. Find the next number in `docs/decisions/` (they look like `0001-...md`).
2. Copy `docs/decisions/0000-template.md` to `docs/decisions/NNNN-<kebab-title>.md`.
3. Fill in the sections:
   - **Context**: the forces at play, including business constraints, not just technical ones.
   - **Options considered**: at least two, with honest pros and cons.
   - **Decision**: one paragraph.
   - **Consequences**: what gets easier, what gets harder, and what we would need to revisit (and at what scale or trigger).
4. Keep it under a page. Link it from the relevant plan in `docs/plan/`.
5. If a later decision reverses this one, don't edit history. Set this record to `Superseded by NNNN` and write a new one.
