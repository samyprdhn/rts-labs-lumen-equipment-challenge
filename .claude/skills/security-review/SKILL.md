---
name: security-review
description: Use before committing a feature or when asked to review code for security. Reviews the current diff (or named files) against a checklist and writes findings to docs/reviews/.
---

# Security review

## Scope

By default, review `git diff main...HEAD` plus any uncommitted changes. If the human names files, review those instead.

## Checklist

For each item, answer **OK / Issue / N/A**, with file:line evidence.

1. **Authentication**: are credentials and tokens actually verified (signature, expiry, issuer, audience)? Are there any code paths that skip it?
2. **Authorization**: is access checked per resource, not just per route? Can user A read or change user B's or site B's data?
3. **Input handling**: is every external input validated (type, range, size)? Are queries parameterized?
4. **Sensitive data**: what personal data (PII) or secrets are read? Are only the needed fields returned, per role? Is anything sensitive written to logs, errors, analytics or LLM prompts?
5. **Secrets management**: are there secrets in source, committed config, or client bundles? Does `.gitignore` cover them?
6. **Error handling**: do errors leak internals (stack traces, SQL, file paths) to clients?
7. **Transport and browser**: is the CORS policy intentional? Where are tokens stored on the client?
8. **Abuse and limits**: can someone request unbounded data, trigger expensive work, or overwhelm a downstream vendor?
9. **Dependencies**: are there new packages, and are they justified?
10. **AI-specific** (if an LLM is involved): prompt injection, output validation, data sent to third parties.

## Output

Write `docs/reviews/<date>-<slug>.md` with a table: `Severity (High/Med/Low) | Finding | Evidence | Recommendation | Status`. Then summarise the top three for the human. **Do not silently fix issues outside the current task's scope.** List them and let the human prioritise.
