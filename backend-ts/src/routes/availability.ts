import { Router } from "express";
import { db } from "../db.js";

export const availabilityRouter = Router();

// GET /api/equipment/availability?type=mobile_crane&start=2026-10-13&end=2026-10-15
//
// Returns every piece of equipment and whether it is free for the requested dates.
availabilityRouter.get("/equipment/availability", (req, res) => {
  const type = req.query.type as string | undefined;
  const start = req.query.start as string;
  const end = req.query.end as string;

  let sql = "SELECT * FROM equipment WHERE status = 'active'";
  if (type) {
    sql += ` AND type = '${type}'`;
  }
  const equipment = db.prepare(sql).all() as any[];

  const results = [];
  for (const item of equipment) {
    const bookings = db
      .prepare("SELECT * FROM bookings WHERE asset_tag = ?")
      .all(item.asset_tag) as any[];

    let available = true;
    let conflict: any = null;
    for (const b of bookings) {
      if (b.start_at >= start && b.end_at <= end) {
        available = false;
        conflict = b;
        if (b.operator_id) {
          conflict.operator = db
            .prepare("SELECT * FROM operators WHERE operator_id = ?")
            .get(b.operator_id);
        }
      }
    }

    // TODO: check FleetCare for maintenance windows (Dana says this is the #1 complaint)

    results.push({ ...item, available, conflict });
  }

  res.json({ data: results });
});
