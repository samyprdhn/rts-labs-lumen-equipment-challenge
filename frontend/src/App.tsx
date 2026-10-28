import { useEffect, useState } from "react";

const TYPES = [
  "mobile_crane",
  "tower_crane",
  "excavator",
  "skid_steer",
  "boom_lift",
  "scissor_lift",
  "telehandler",
  "dozer",
];

type RequestState = "idle" | "loading" | "success" | "error";

export default function App() {
  const [token, setToken] = useState(localStorage.getItem("token") || "");
  const [type, setType] = useState("mobile_crane");
  const [start, setStart] = useState("2026-10-01");
  const [end, setEnd] = useState("2026-10-31");
  const [items, setItems] = useState<any[]>([]);
  const [state, setState] = useState<RequestState>("idle");
  const [errorMessage, setErrorMessage] = useState("");
  const [retryNonce, setRetryNonce] = useState(0);

  useEffect(() => {
    if (!token) return;

    // Guards against a slower, superseded request clobbering a newer one
    // (e.g. the user changes the type filter again before the first
    // response arrives).
    let current = true;

    setState("loading");
    setErrorMessage("");

    fetch(`/api/equipment/availability?type=${type}&start=${start}&end=${end}`, {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then(async (r) => {
        if (!current) return;
        const json = await r.json().catch(() => ({}));
        if (!r.ok) {
          setErrorMessage(json?.error || `Request failed (${r.status})`);
          setState("error");
          return;
        }
        setItems(json.data || []);
        setState("success");
      })
      .catch(() => {
        if (!current) return;
        setErrorMessage("Could not reach the server. Check your connection and try again.");
        setState("error");
      });

    return () => {
      current = false;
    };
  }, [type, start, end, token, retryNonce]);

  if (!token) {
    return (
      <div className="login">
        <div className="title">Lumen Equipment</div>
        <input placeholder="Paste dev token" onChange={(e) => {
          localStorage.setItem("token", e.target.value);
          setToken(e.target.value);
        }} />
      </div>
    );
  }

  return (
    <div className="page">
      <div className="title">Equipment Availability</div>

      <div className="filters">
        <div className="field">
          <label htmlFor="type-filter">Equipment type</label>
          <select id="type-filter" value={type} onChange={(e) => setType(e.target.value)}>
            {TYPES.map((t) => (
              <option key={t}>{t}</option>
            ))}
          </select>
        </div>
        <div className="field">
          <label htmlFor="start-filter">Start date</label>
          <input id="start-filter" type="date" value={start} onChange={(e) => setStart(e.target.value)} />
        </div>
        <div className="field">
          <label htmlFor="end-filter">End date</label>
          <input id="end-filter" type="date" value={end} onChange={(e) => setEnd(e.target.value)} />
        </div>
        <button className="btn" onClick={() => { localStorage.removeItem("token"); setToken(""); }}>
          Log out
        </button>
      </div>

      {state === "loading" && <div className="status-message" role="status">Loading availability…</div>}

      {state === "error" && (
        <div className="status-message status-error" role="alert">
          <span>{errorMessage}</span>
          <button className="btn" onClick={() => setRetryNonce((n) => n + 1)}>
            Retry
          </button>
        </div>
      )}

      {state === "success" && items.length === 0 && (
        <div className="status-message">No equipment matches these filters.</div>
      )}

      {items.length > 0 && (
        <div className="grid">
          {items.map((item) => (
            <button
              type="button"
              className="card"
              key={item.asset_tag}
              onClick={() => alert(item.notes || "No notes")}
              aria-label={`${item.asset_tag}, ${item.available ? "available" : "unavailable"}. View notes.`}
            >
              <span className={item.available ? "dot green" : "dot red"} aria-hidden="true" />
              <div className="tag">{item.asset_tag}</div>
              <div className={`status-text ${item.available ? "available" : "unavailable"}`}>
                {item.available ? "Available" : "Unavailable"}
              </div>
              <div className="muted">{item.make_model} {item.capacity}</div>
              {item.daily_rate_usd && <div className="muted">${item.daily_rate_usd}/day · {item.home_yard}</div>}
              {!item.daily_rate_usd && <div className="muted">{item.home_yard}</div>}
              {item.conflict && item.conflict.exists && !item.conflict.site_id && (
                <div className="muted">Booked (details not available for your role)</div>
              )}
              {item.conflict && item.conflict.site_id && (
                <div className="muted">
                  Booked at {item.conflict.site_id} until {item.conflict.end_at}
                </div>
              )}
              {item.maintenance && item.maintenance.status !== "ok" && (
                <div className="muted">
                  {item.maintenance.status === "in_maintenance" ? "In maintenance (FleetCare)" : "Maintenance status unknown"}
                </div>
              )}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
