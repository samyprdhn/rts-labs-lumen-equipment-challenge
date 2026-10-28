import "./env.js";
import { DatabaseSync } from "node:sqlite";
import path from "node:path";
import { REPO_ROOT } from "./env.js";

const dbPath = path.resolve(REPO_ROOT, process.env.DB_PATH || "db/lumen.db");

export const db = new DatabaseSync(dbPath);
