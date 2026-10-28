import "./env.js";
import { app } from "./app.js";

const port = Number(process.env.PORT || 8080);
app.listen(port, () => {
  console.log(`Lumen equipment API (TypeScript) listening on http://localhost:${port}`);
});
