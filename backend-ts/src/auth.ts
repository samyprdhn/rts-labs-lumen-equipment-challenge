import type { Request, Response, NextFunction } from "express";

export interface User {
  sub: string;
  name: string;
  role: string;
  site_ids: string[];
}

declare global {
  namespace Express {
    interface Request {
      user?: User;
    }
  }
}

// Reads the user from the bearer token.
export function requireAuth(req: Request, res: Response, next: NextFunction) {
  const header = req.headers.authorization;
  if (!header) {
    return res.status(401).json({ error: "missing token" });
  }
  const token = header.replace("Bearer ", "");
  const payload = token.split(".")[1];
  req.user = JSON.parse(Buffer.from(payload, "base64").toString());
  next();
}
