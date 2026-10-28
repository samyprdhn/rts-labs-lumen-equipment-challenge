import { describe, it, expect } from "vitest";
import request from "supertest";
import { createHmac } from "node:crypto";
import { app } from "../src/app.js";

// NOTE: requires `npm run seed` from the repo root first.

function token(claims: object) {
  const b64 = (o: object) => Buffer.from(JSON.stringify(o)).toString("base64url");
  const unsigned = `${b64({ alg: "HS256", typ: "JWT" })}.${b64(claims)}`;
  const sig = createHmac("sha256", process.env.JWT_SECRET || "").update(unsigned).digest("base64url");
  return `${unsigned}.${sig}`;
}

describe("GET /api/equipment/availability", () => {
  it("requires a token", async () => {
    const res = await request(app).get("/api/equipment/availability");
    expect(res.status).toBe(401);
  });

  it("returns cranes", async () => {
    const res = await request(app)
      .get("/api/equipment/availability?type=mobile_crane&start=2026-10-13&end=2026-10-15")
      .set("Authorization", `Bearer ${token({ sub: "u-ops-01", role: "ops_director", site_ids: ["*"] })}`);
    expect(res.status).toBe(200);
    expect(res.body.data.length).toBeGreaterThan(0);
    expect(res.body.data.every((e: any) => e.type === "mobile_crane")).toBe(true);
  });
});
