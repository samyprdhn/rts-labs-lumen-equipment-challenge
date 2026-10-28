---
name: submission
description: Use when the candidate says they are wrapping up or ready to submit. Verifies the repo is in a submittable state and helps finalise SUBMISSION.md.
---

# Submission

Work through this list and report the results to the human:

1. **Tests:** run every suite that applies. Report pass/fail counts. Don't hide failures. Document them.
2. **It runs:** confirm the setup steps in SUBMISSION.md work from a clean checkout (seed, backend, frontend).
3. **Secrets:** `git ls-files` and `git log -p` must contain no real secrets, `.env`, or `*.db` files. If they do, tell the human. Rewriting history is their call.
4. **AI log:** run `npm run ai-log` to regenerate `.ai-log/SUMMARY.md`. If other AI tools were used, check that their exports are in `docs/ai-log/`.
5. **Docs:** plans in `docs/plan/`, ADRs in `docs/decisions/`, and `docs/plan/PROGRESS.md` is current.
6. **SUBMISSION.md:** help the human complete every section. The *"What I'd do next"* and *"Known issues"* sections matter as much as what was built. Be candid. Write it in the human's voice, but don't invent claims they haven't made.

Finally, remind the human to commit everything (including `.ai-log/`) and share the repo or zip.
