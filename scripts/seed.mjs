#!/usr/bin/env node
// Loads the spreadsheet exports in /data into a local SQLite database.
// Usage: npm run seed            (from repo root)
import { DatabaseSync } from "node:sqlite";
import { readFileSync, mkdirSync } from "node:fs";
import path from "node:path";
import { ROOT, loadEnv, parseCsv } from "./lib.mjs";

loadEnv();
const dbPath = path.resolve(ROOT, process.env.DB_PATH || "db/lumen.db");
mkdirSync(path.dirname(dbPath), { recursive: true });

const db = new DatabaseSync(dbPath);
db.exec(readFileSync(path.join(ROOT, "db/schema.sql"), "utf8"));

function load(table, rows, columns) {
  const stmt = db.prepare(
    `INSERT INTO ${table} (${columns.join(",")}) VALUES (${columns.map(() => "?").join(",")})`
  );
  for (const r of rows) stmt.run(...columns.map((c) => r[c] ?? null));
  console.log(`  ${table.padEnd(10)} ${rows.length} rows`);
}

const csv = (name) => parseCsv(readFileSync(path.join(ROOT, "data", name), "utf8"));

console.log(`Seeding ${dbPath}`);
db.exec("BEGIN");
load("sites", csv("sites.csv"), ["site_id", "name", "city", "state", "timezone", "region", "superintendent"]);
load("equipment", csv("equipment.csv"), ["asset_tag", "type", "make_model", "capacity", "home_yard", "daily_rate_usd", "required_cert", "status", "notes"]);
load("operators", csv("operators.csv"), ["operator_id", "first_name", "last_name", "phone", "email", "date_of_birth", "ssn_last4", "emergency_contact", "union_local", "certifications", "home_region"]);
load(
  "bookings",
  csv("bookings.csv").map((b) => ({ ...b, start_at: b.start, end_at: b.end })),
  ["booking_id", "asset_tag", "site_id", "start_at", "end_at", "operator_id", "booked_by", "status", "notes"]
);
const users = JSON.parse(readFileSync(path.join(ROOT, "data/users.json"), "utf8")).users;
load("users", users.map((u) => ({ ...u, site_ids: u.site_ids.join(",") })), ["user_id", "name", "email", "role", "site_ids"]);
db.exec("COMMIT");
db.close();
console.log("Done.");
