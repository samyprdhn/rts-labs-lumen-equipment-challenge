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

export default function App() {
  const [token, setToken] = useState(localStorage.getItem("token") || "");
  const [type, setType] = useState("mobile_crane");
  const [start, setStart] = useState("2026-10-01");
  const [end, setEnd] = useState("2026-10-31");
  const [items, setItems] = useState<any[]>([]);

  useEffect(() => {
    if (!token) return;
    fetch(`/api/equipment/availability?type=${type}&start=${start}&end=${end}`, {
      headers: { Authorization: `Bearer ${token}` },
    })
      .then((r) => r.json())
      .then((json) => setItems(json.data));
  }, [type, start, end, token]);

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
        <select value={type} onChange={(e) => setType(e.target.value)}>
          {TYPES.map((t) => (
            <option key={t}>{t}</option>
          ))}
        </select>
        <input type="date" value={start} onChange={(e) => setStart(e.target.value)} />
        <input type="date" value={end} onChange={(e) => setEnd(e.target.value)} />
        <div className="btn" onClick={() => { localStorage.removeItem("token"); setToken(""); }}>
          Log out
        </div>
      </div>

      <div className="grid">
        {items.map((item) => (
          <div className="card" key={item.asset_tag} onClick={() => alert(item.notes || "No notes")}>
            <span className={item.available ? "dot green" : "dot red"} />
            <div className="tag">{item.asset_tag}</div>
            <div className="muted">{item.make_model} {item.capacity}</div>
            <div className="muted">${item.daily_rate_usd}/day · {item.home_yard}</div>
            {item.conflict && (
              <div className="muted">
                Booked at {item.conflict.site_id} until {item.conflict.end_at}
                {item.conflict.operator && (
                  <> · Operator {item.conflict.operator.first_name} {item.conflict.operator.last_name} ({item.conflict.operator.phone})</>
                )}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}
