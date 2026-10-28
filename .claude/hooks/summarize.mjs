#!/usr/bin/env node
// Prints a readable timeline of .ai-log/events.jsonl and writes .ai-log/SUMMARY.md.
// Usage: npm run ai-log
import { readFileSync, writeFileSync, existsSync, readdirSync } from "node:fs";
import path from "node:path";

const root = process.env.CLAUDE_PROJECT_DIR || process.cwd();
const file = path.join(root, ".ai-log/events.jsonl");
if (!existsSync(file)) {
  console.log("No AI log yet (.ai-log/events.jsonl). If you used a tool other than Claude Code, see docs/ai-log/README.md.");
  process.exit(0);
}

const events = readFileSync(file, "utf8").trim().split("\n").filter(Boolean).map((l) => {
  try { return JSON.parse(l); } catch { return null; }
}).filter(Boolean);

const prompts = events.filter((e) => e.event === "prompt");
const tools = events.filter((e) => e.event === "tool");
const sessions = new Set(events.map((e) => e.session_id).filter(Boolean));
const toolCounts = tools.reduce((m, e) => ((m[e.tool] = (m[e.tool] || 0) + 1), m), {});
const skills = tools.filter((e) => e.tool === "Skill").map((e) => e.input?.skill);
const filesTouched = [...new Set(tools.filter((e) => ["Edit", "Write", "MultiEdit"].includes(e.tool)).map((e) => e.input?.file_path))];
const testRuns = tools.filter((e) => e.tool === "Bash" && /\b(test|vitest|go test|jest)\b/.test(e.input?.command || "")).length;
const plansDir = path.join(root, "docs/plan/auto");
const autoPlans = existsSync(plansDir) ? readdirSync(plansDir) : [];

const first = events[0]?.ts, last = events.at(-1)?.ts;
const minutes = first && last ? Math.round((new Date(last) - new Date(first)) / 60000) : 0;

const lines = [
  `# AI session summary`,
  ``,
  `- Sessions: ${sessions.size}`,
  `- Span: ${first ?? "?"} → ${last ?? "?"} (~${minutes} min wall clock)`,
  `- Prompts: ${prompts.length}`,
  `- Tool calls: ${tools.length} (${Object.entries(toolCounts).sort((a, b) => b[1] - a[1]).map(([k, v]) => `${k} ${v}`).join(", ")})`,
  `- Test runs observed: ${testRuns}`,
  `- Skills used: ${skills.length ? [...new Set(skills)].join(", ") : "none"}`,
  `- Plan-mode plans captured: ${autoPlans.length}`,
  `- Files written: ${filesTouched.length}`,
  ``,
  `## Prompt timeline`,
  ``,
  ...prompts.map((p, i) => `${i + 1}. \`${p.ts.slice(11, 16)}\` ${String(p.prompt).replace(/\s+/g, " ").slice(0, 300)}`),
  ``,
];
const out = lines.join("\n");
writeFileSync(path.join(root, ".ai-log/SUMMARY.md"), out);
console.log(out);
