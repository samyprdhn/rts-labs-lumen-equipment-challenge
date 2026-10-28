import express from "express";
import { requireAuth } from "./auth.js";
import { availabilityRouter } from "./routes/availability.js";

export const app = express();

app.use((req, res, next) => {
  res.setHeader("Access-Control-Allow-Origin", "*");
  res.setHeader("Access-Control-Allow-Credentials", "true");
  res.setHeader("Access-Control-Allow-Headers", "*");
  next();
});

app.use((req, _res, next) => {
  console.log(new Date().toISOString(), req.method, req.url, JSON.stringify(req.headers));
  next();
});

app.use(express.json());

app.get("/healthz", (_req, res) => res.json({ ok: true }));

app.use("/api", requireAuth, availabilityRouter);

app.use((err: any, _req: express.Request, res: express.Response, _next: express.NextFunction) => {
  console.error(err);
  res.status(500).json({ error: err.message, stack: err.stack });
});
