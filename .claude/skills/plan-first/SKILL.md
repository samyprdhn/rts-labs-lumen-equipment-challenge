---
name: plan-first
description: Use before starting any non-trivial change in this repo (a new endpoint, a schema change, a refactor, any challenge Part). Produces a written plan in docs/plan/ before code is written, and keeps it updated as work proceeds.
---

# Plan first

Write down what you're going to do before any code gets written. The plan is a deliverable in its own right. Reviewers read it to see how the problem was understood.

## Steps

1. **Restate the problem** in 2–4 sentences, in the business's language (superintendents, foremen, cranes), not the code's.
2. **Read before proposing.** Look at the relevant code, schema and data files. Note anything surprising.
3. **List assumptions and open questions.** Mark each one as either:
   - *Assumed*: we'll proceed on this basis, and it's written down so it can be challenged.
   - *Blocking*: we need an answer first. Ask the human.
4. **Options.** Give at least two approaches for the core design decision, each with its trade-offs (complexity, correctness, performance, time).
5. **Chosen approach** and why. If the decision is significant or hard to reverse, also create an ADR with the `decision-record` skill.
6. **Task breakdown.** Small, ordered steps, each independently testable and committable.
7. **Test plan.** What proves this works? List the cases, including failure and edge cases.
8. **Risks / out of scope.** What you are deliberately *not* doing and why.

## Output

Save the plan to `docs/plan/NN-<short-slug>.md`, where NN is the next number. Use `docs/plan/TEMPLATE.md`.

Present the plan to the human and **wait for approval** before implementing. If the human changes direction, update the plan: note what changed and why rather than silently rewriting it.

As work proceeds, update the plan's `## Status log` section with dated one-line entries. When a plan is abandoned or replaced, set its status to `superseded` and link to the new plan. Never delete a plan.
