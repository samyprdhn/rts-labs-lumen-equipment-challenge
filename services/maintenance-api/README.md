# FleetCare (mock)

A stand-in for the third-party maintenance system Lumen's equipment shop uses. **Treat it as a vendor API you can't change.**

```bash
npm run mock              # from repo root, chaos ON (realistic)
CHAOS=off npm run mock    # chaos off, for local UI work
```

| Endpoint | Notes |
|---|---|
| `GET /v1/health` | No auth |
| `GET /v1/assets/:assetTag/maintenance` | One asset per call |
| `POST /v1/maintenance/batch` | `{ "asset_tags": [...] }`, max 25 per call |

Auth: `X-Api-Key` header (see `.env.example`).

Here's what the vendor's docs say, verbatim: *"Service is provided on a best-effort basis. Clients should expect intermittent errors and must respect rate limits (20 req / 10 s)."*

Maintenance windows are returned as UTC timestamps. An asset is **not usable** during a window.
