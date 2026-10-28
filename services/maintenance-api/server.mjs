#!/usr/bin/env node
// Mock of "FleetCare", the third-party maintenance system Lumen's shop uses.
// You do NOT need to modify this service. Treat it as an external vendor API
// you can't change: it is slow, occasionally down, and rate limited.
//
// Endpoints
//   GET  /v1/health
//   GET  /v1/assets/:assetTag/maintenance      -> { asset_tag, windows: [...] }
//   POST /v1/maintenance/batch                 body { "asset_tags": [...] } (max 25)
//                                              -> { results: { [tag]: [...] } }
//   Auth: header  X-Api-Key: <FLEETCARE_API_KEY>
//
// Env
//   PORT=4010
//   FLEETCARE_API_KEY=fc_test_key
//   CHAOS=on|off   (default on) - injects failures below
//   CHAOS_SEED=<n> make the chaos deterministic for tests
import http from "node:http";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import path from "node:path";

const here = path.dirname(fileURLToPath(import.meta.url));
const windows = JSON.parse(readFileSync(path.join(here, "windows.json"), "utf8"));
const PORT = Number(process.env.PORT || 4010);
const API_KEY = process.env.FLEETCARE_API_KEY || "fc_test_key";
const CHAOS = (process.env.CHAOS || "on") !== "off";

// Tiny seeded PRNG so CHAOS_SEED gives reproducible behaviour.
let seed = Number(process.env.CHAOS_SEED || Date.now()) >>> 0;
const rand = () => ((seed = (seed * 1664525 + 1013904223) >>> 0) / 2 ** 32);
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

// Rate limit: 20 requests / 10s window, global.
let windowStart = Date.now(), count = 0;

function send(res, status, body, headers = {}) {
  const payload = typeof body === "string" ? body : JSON.stringify(body);
  res.writeHead(status, { "Content-Type": "application/json", ...headers });
  res.end(payload);
}

function byTag(tag) {
  const norm = String(tag).trim().toUpperCase();
  return windows.filter((w) => w.asset_tag === norm).map(({ asset_tag, ...rest }) => rest);
}

async function chaos(res) {
  if (!CHAOS) return false;
  const r = rand();
  if (r < 0.12) { send(res, 503, { error: "service_unavailable" }); return true; }
  if (r < 0.2) { await sleep(2500 + rand() * 2500); return false; } // slow but OK
  if (r < 0.23) { res.writeHead(200, { "Content-Type": "application/json" }); res.end('{"asset_tag":"'); return true; } // truncated body
  if (r < 0.25) { await sleep(30000); send(res, 504, { error: "gateway_timeout" }); return true; } // hangs
  return false;
}

const server = http.createServer(async (req, res) => {
  const url = new URL(req.url, `http://${req.headers.host}`);
  if (url.pathname === "/v1/health") return send(res, 200, { ok: true });

  if (req.headers["x-api-key"] !== API_KEY) return send(res, 401, { error: "invalid_api_key" });

  if (Date.now() - windowStart > 10_000) { windowStart = Date.now(); count = 0; }
  if (++count > 20) return send(res, 429, { error: "rate_limited" }, { "Retry-After": "3" });

  if (await chaos(res)) return;

  const m = url.pathname.match(/^\/v1\/assets\/([^/]+)\/maintenance$/);
  if (req.method === "GET" && m) {
    const tag = decodeURIComponent(m[1]);
    return send(res, 200, { asset_tag: tag.toUpperCase(), windows: byTag(tag) });
  }

  if (req.method === "POST" && url.pathname === "/v1/maintenance/batch") {
    let raw = "";
    for await (const chunk of req) raw += chunk;
    let body;
    try { body = JSON.parse(raw); } catch { return send(res, 400, { error: "invalid_json" }); }
    const tags = Array.isArray(body?.asset_tags) ? body.asset_tags : null;
    if (!tags) return send(res, 400, { error: "asset_tags must be an array" });
    if (tags.length > 25) return send(res, 413, { error: "max 25 asset_tags per request" });
    await sleep(150 + tags.length * 20);
    return send(res, 200, { results: Object.fromEntries(tags.map((t) => [String(t).toUpperCase(), byTag(t)])) });
  }

  send(res, 404, { error: "not_found" });
});

server.listen(PORT, () => console.log(`FleetCare mock listening on :${PORT} (chaos ${CHAOS ? "ON" : "off"})`));
