# AI usage log

**Using Claude Code?** You don't need to do anything. Hooks in `.claude/settings.json` record your prompts, tool calls, plan-mode plans and session transcripts into `.ai-log/` and `docs/plan/auto/`. Obvious secrets are redacted automatically, but **don't paste real credentials or personal data into prompts**. Run `npm run ai-log` to see a summary.

**Using something else** (Cursor, Copilot Chat, ChatGPT, Windsurf, Claude.ai, etc.)? That's fine. Please export or copy your conversations into this folder as markdown or text, one file per session: `docs/ai-log/<tool>-<n>.md`. Rough is fine. We're interested in how you directed the tool, not polish.

**Not using AI at all?** That's fine too. Say so in `SUBMISSION.md`.
