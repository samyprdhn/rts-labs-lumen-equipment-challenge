#!/usr/bin/env node
// Issues a development JWT for one of the demo users in data/users.json.
// In production Lumen plans to use Microsoft Entra ID (Azure AD) SSO; this
// stands in for it locally.
//
// Usage: npm run token -- u-for-01
//        npm run token -- --list
import { createHmac } from "node:crypto";
import { readFileSync } from "node:fs";
import path from "node:path";
import { ROOT, loadEnv } from "./lib.mjs";

loadEnv();
const users = JSON.parse(readFileSync(path.join(ROOT, "data/users.json"), "utf8")).users;
const arg = process.argv[2];

if (!arg || arg === "--list") {
  console.log("Demo users:\n");
  for (const u of users) console.log(`  ${u.user_id.padEnd(10)} ${u.role.padEnd(15)} ${u.site_ids.join(",").padEnd(12)} ${u.name}`);
  console.log("\nUsage: npm run token -- <user_id>");
  process.exit(arg ? 0 : 1);
}

const user = users.find((u) => u.user_id === arg);
if (!user) {
  console.error(`Unknown user '${arg}'. Run: npm run token -- --list`);
  process.exit(1);
}

const secret = process.env.JWT_SECRET;
if (!secret) {
  console.error("JWT_SECRET is not set (copy .env.example to .env)");
  process.exit(1);
}

const b64url = (obj) => Buffer.from(JSON.stringify(obj)).toString("base64url");
const now = Math.floor(Date.now() / 1000);
const header = { alg: "HS256", typ: "JWT" };
const payload = {
  iss: "lumen-dev",
  aud: "lumen-equipment-api",
  sub: user.user_id,
  name: user.name,
  role: user.role,
  site_ids: user.site_ids,
  iat: now,
  exp: now + 8 * 3600,
};
const unsigned = `${b64url(header)}.${b64url(payload)}`;
const sig = createHmac("sha256", secret).update(unsigned).digest("base64url");
console.log(`${unsigned}.${sig}`);
