---
name: checkpoint
description: Use at natural stopping points (after finishing a task step, before a break, before switching approach, roughly every 30–45 minutes). Appends a short progress entry to docs/plan/PROGRESS.md and suggests a commit.
---

# Checkpoint

1. Run the relevant test suite(s) and capture the summary line.
2. Append an entry to `docs/plan/PROGRESS.md`:

```md
## <YYYY-MM-DD HH:MM> — <one-line headline>
- **Done:** …
- **Tests:** <summary line, or "not run: why">
- **Decisions / changes of direction:** … (link ADRs)
- **Next:** …
- **Open questions / risks:** …
```

3. Suggest a conventional commit message for the work since the last checkpoint (`feat:`, `fix:`, `test:`, `docs:`, `refactor:`, `chore:`). Keep commits small and focused. Before committing, check that no secrets or local databases are staged.
