#!/usr/bin/env node
// Claude Code hook: records how the candidate drove the agent.
//
// Writes to <repo>/.ai-log/ :
//   events.jsonl            one line per prompt / tool call / plan / stop
//   transcripts/<id>.jsonl  copy of the full session transcript (refreshed each turn)
// and to <repo>/docs/plan/auto/ :
//   <timestamp>-plan.md     every plan approved in Claude Code plan mode
//
// Obvious secrets (API keys, JWTs, bearer tokens) are redacted before writing.
// This hook must never block or fail the session: all errors are swallowed.
//
// Usage (from .claude/settings.json): node .claude/hooks/ai-log.mjs <prompt|tool|stop|session-end>
import { appendFileSync, mkdirSync, readFileSync, writeFileSync, existsSync } from "node:fs";
import path from "node:path";

const kind = process.argv[2] || "unknown";
const root = process.env.CLAUDE_PROJECT_DIR || process.cwd();
const logDir = path.join(root, ".ai-log");

const REDACTIONS = [
  [/sk-ant-[A-Za-z0-9_\-]{10,}/g, "[REDACTED_ANTHROPIC_KEY]"],
  [/\b(?:sk|pk|rk|bi|fc)_(?:live|test)_[A-Za-z0-9]{8,}/g, "[REDACTED_API_KEY]"],
  [/eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}/g, "[REDACTED_JWT]"],
  [/(Bearer\s+)[A-Za-z0-9._\-]{16,}/gi, "$1[REDACTED]"],
  [/((?:password|secret|api[_-]?key)\s*[=:]\s*)["']?[^\s"',]{6,}/gi, "$1[REDACTED]"],
];
const redact = (s) => REDACTIONS.reduce((acc, [re, rep]) => acc.replace(re, rep), String(s));
const clip = (s, n = 800) => (s && s.length > n ? s.slice(0, n) + `… [${s.length - n} more chars]` : s);

function readStdin() {
  try { return JSON.parse(readFileSync(0, "utf8") || "{}"); } catch { return {}; }
}

function append(event) {
  mkdirSync(logDir, { recursive: true });
  appendFileSync(path.join(logDir, "events.jsonl"), redact(JSON.stringify({ ts: new Date().toISOString(), ...event })) + "\n");
}

function summarizeToolInput(name, input = {}) {
  switch (name) {
    case "Bash": return { command: clip(input.command, 500), description: input.description };
    case "Edit": case "MultiEdit": case "Write": case "Read": case "NotebookEdit":
      return { file_path: input.file_path && path.relative(root, input.file_path) };
    case "TodoWrite": return { todos: input.todos?.map((t) => `${t.status}: ${t.content}`) };
    case "TaskCreate": return { subject: input.subject, description: clip(input.description, 300) };
    case "TaskUpdate": return { taskId: input.taskId, status: input.status, subject: input.subject };
    case "Skill": return { skill: input.skill ?? input.command, args: clip(input.args, 200) };
    case "Task": case "Agent": return { description: input.description, prompt: clip(input.prompt, 400) };
    default: return { keys: Object.keys(input) };
  }
}

function copyTranscript(transcriptPath, sessionId) {
  if (!transcriptPath || !existsSync(transcriptPath)) return;
  const dir = path.join(logDir, "transcripts");
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, `${sessionId || "session"}.jsonl`), redact(readFileSync(transcriptPath, "utf8")));
}

try {
  const input = readStdin();
  const base = { session_id: input.session_id, event: kind };

  if (kind === "prompt") {
    append({ ...base, prompt: input.prompt });
  } else if (kind === "tool") {
    const name = input.tool_name;
    if (!name) process.exit(0);
    append({ ...base, tool: name, input: summarizeToolInput(name, input.tool_input) });
    if (name === "ExitPlanMode" && input.tool_input?.plan) {
      const dir = path.join(root, "docs/plan/auto");
      mkdirSync(dir, { recursive: true });
      const stamp = new Date().toISOString().replace(/[:.]/g, "-");
      writeFileSync(path.join(dir, `${stamp}-plan.md`), redact(`<!-- captured automatically from Claude Code plan mode -->\n\n${input.tool_input.plan}\n`));
    }
  } else if (kind === "stop" || kind === "session-end") {
    append({ ...base, reason: input.reason });
    copyTranscript(input.transcript_path, input.session_id);
  } else {
    append({ ...base });
  }
} catch {
  // never block the session
}
process.exit(0);
