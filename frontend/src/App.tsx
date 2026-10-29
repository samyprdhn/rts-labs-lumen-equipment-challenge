import { useEffect, useState, type FormEvent } from "react";

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

type ReservationStatus = "tentative" | "confirmed";

export default function App() {
  const [token, setToken] = useState(localStorage.getItem("token") || "");
  const [type, setType] = useState("mobile_crane");
  const [start, setStart] = useState("2026-10-01");
  const [end, setEnd] = useState("2026-10-31");
  const [items, setItems] = useState<any[]>([]);
  const [state, setState] = useState<RequestState>("idle");
  const [errorMessage, setErrorMessage] = useState("");
  const [retryNonce, setRetryNonce] = useState(0);
  const [bookingAsset, setBookingAsset] = useState<any | null>(null);
  const [bookingSiteID, setBookingSiteID] = useState("");
  const [bookingStart, setBookingStart] = useState("");
  const [bookingEnd, setBookingEnd] = useState("");
  const [bookingStatus, setBookingStatus] = useState<ReservationStatus>("confirmed");
  const [bookingOperatorID, setBookingOperatorID] = useState("");
  const [bookingExpiresAt, setBookingExpiresAt] = useState("");
  const [bookingError, setBookingError] = useState("");
  const [bookingPending, setBookingPending] = useState(false);
  const [bookingNotice, setBookingNotice] = useState("");
  const [releasingID, setReleasingID] = useState<string | null>(null);
  const [releaseError, setReleaseError] = useState<{ id: string; message: string } | null>(null);
  const [releaseNotice, setReleaseNotice] = useState("");

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

  function openBooking(item: any) {
    setBookingAsset(item);
    setBookingSiteID("");
    setBookingStart("");
    setBookingEnd("");
    setBookingStatus("confirmed");
    setBookingOperatorID("");
    setBookingExpiresAt("");
    setBookingError("");
    setBookingNotice("");
  }

  function closeBooking() {
    setBookingAsset(null);
    setBookingError("");
  }

  function conflictMessage(conflict: any) {
    if (!conflict || typeof conflict !== "object" || conflict.exists === true && !conflict.site_id && !conflict.booking_id) {
      return "This equipment is already booked for that time.";
    }

    const details = [
      conflict.booking_id && `Booking ${conflict.booking_id}`,
      conflict.site_id && `site ${conflict.site_id}`,
      conflict.start_at && `from ${conflict.start_at}`,
      conflict.end_at && `to ${conflict.end_at}`,
    ].filter(Boolean);
    return details.length ? `This equipment is already booked: ${details.join(", ")}.` : "This equipment is already booked for that time.";
  }

  async function submitBooking(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!bookingAsset) return;

    setBookingError("");
    setBookingNotice("");
    setBookingPending(true);
    const payload: Record<string, string> = {
      asset_tag: bookingAsset.asset_tag,
      site_id: bookingSiteID.trim(),
      start: new Date(bookingStart).toISOString(),
      end: new Date(bookingEnd).toISOString(),
      status: bookingStatus,
    };
    if (bookingAsset.required_cert) payload.operator_id = bookingOperatorID.trim();
    if (bookingStatus === "tentative") payload.expires_at = new Date(bookingExpiresAt).toISOString();

    try {
      const response = await fetch("/api/reservations", {
        method: "POST",
        headers: {
          Authorization: `Bearer ${token}`,
          "Content-Type": "application/json",
        },
        body: JSON.stringify(payload),
      });
      const body = await response.json().catch(() => ({}));

      if (response.status === 201) {
        closeBooking();
        setBookingNotice(`Reservation created for ${bookingAsset.asset_tag}. Availability is refreshing.`);
        setRetryNonce((nonce) => nonce + 1);
        return;
      }

      if (response.status === 400 || response.status === 422 || response.status === 403) {
        setBookingError(body?.error || `Booking request rejected (${response.status}).`);
      } else if (response.status === 409) {
        setBookingError(conflictMessage(body?.conflict));
      } else if (response.status === 503) {
        setBookingError("The booking service is temporarily busy. Please try again.");
      } else {
        setBookingError(body?.error || `Booking failed (${response.status}).`);
      }
    } catch {
      setBookingError("Could not reach the server. Check your connection and try again.");
    } finally {
      setBookingPending(false);
    }
  }

  // A reservation's owner, or anyone with B1-level booking authority at its
  // site, may release it (B6 - enforced server-side; this UI just offers
  // the action to everyone who can see the booking_id in the first place,
  // same as the "Book" button doesn't pre-filter by role either).
  async function releaseReservation(bookingID: string) {
    setReleasingID(bookingID);
    setReleaseError(null);
    setReleaseNotice("");
    try {
      const response = await fetch(`/api/reservations/${bookingID}/release`, {
        method: "POST",
        headers: { Authorization: `Bearer ${token}` },
      });
      const body = await response.json().catch(() => ({}));

      if (response.status === 200) {
        setReleaseNotice(`Reservation ${bookingID} released. Availability is refreshing.`);
        setRetryNonce((nonce) => nonce + 1);
        return;
      }
      if (response.status === 403) {
        setReleaseError({ id: bookingID, message: "You don't have permission to release this reservation." });
      } else if (response.status === 404) {
        setReleaseError({ id: bookingID, message: "Reservation not found - it may already be resolved." });
      } else if (response.status === 409) {
        setReleaseError({ id: bookingID, message: body?.error || "This reservation can't be released right now." });
      } else if (response.status === 503) {
        setReleaseError({ id: bookingID, message: "The release service is temporarily busy. Please try again." });
      } else {
        setReleaseError({ id: bookingID, message: body?.error || `Release failed (${response.status}).` });
      }
    } catch {
      setReleaseError({ id: bookingID, message: "Could not reach the server. Check your connection and try again." });
    } finally {
      setReleasingID(null);
    }
  }

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

      {bookingNotice && <div className="status-message" role="status">{bookingNotice}</div>}
      {releaseNotice && <div className="status-message" role="status">{releaseNotice}</div>}

      {state === "success" && items.length === 0 && (
        <div className="status-message">No equipment matches these filters.</div>
      )}

      {items.length > 0 && (
        <div className="grid">
          {items.map((item) => (
            <div className="equipment-card" key={item.asset_tag}>
              <button
                type="button"
                className="card"
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

              {item.available && (
                <div className="card-actions">
                  <button type="button" className="btn" onClick={() => openBooking(item)}>
                    Book {item.asset_tag}
                  </button>
                </div>
              )}

              {item.conflict && item.conflict.booking_id && (item.conflict.status === "tentative" || item.conflict.status === "confirmed") && (
                <div className="card-actions">
                  <button
                    type="button"
                    className="btn"
                    disabled={releasingID === item.conflict.booking_id}
                    onClick={() => releaseReservation(item.conflict.booking_id)}
                  >
                    {releasingID === item.conflict.booking_id ? "Releasing…" : `Release ${item.conflict.booking_id}`}
                  </button>
                  {releaseError && releaseError.id === item.conflict.booking_id && (
                    <div className="status-message status-error" role="alert">{releaseError.message}</div>
                  )}
                </div>
              )}

              {bookingAsset?.asset_tag === item.asset_tag && (
                <form className="booking-form status-message" aria-label={`Booking ${item.asset_tag}`} onSubmit={submitBooking}>
                  <div className="muted">Times use this device&apos;s local timezone and are sent to the API as UTC.</div>
                  <div className="field">
                    <label htmlFor="booking-site-id">Site ID</label>
                    <input id="booking-site-id" value={bookingSiteID} onChange={(event) => setBookingSiteID(event.target.value)} placeholder="S01" required />
                  </div>
                  <div className="field">
                    <label htmlFor="booking-start">Start date and time</label>
                    <input id="booking-start" type="datetime-local" value={bookingStart} onChange={(event) => setBookingStart(event.target.value)} required />
                  </div>
                  <div className="field">
                    <label htmlFor="booking-end">End date and time</label>
                    <input id="booking-end" type="datetime-local" value={bookingEnd} onChange={(event) => setBookingEnd(event.target.value)} required />
                  </div>
                  <div className="field">
                    <label htmlFor="booking-status">Status</label>
                    <select id="booking-status" value={bookingStatus} onChange={(event) => setBookingStatus(event.target.value === "tentative" ? "tentative" : "confirmed")}>
                      <option value="confirmed">Confirmed</option>
                      <option value="tentative">Tentative</option>
                    </select>
                  </div>
                  {item.required_cert && (
                    <div className="field">
                      <label htmlFor="booking-operator">Operator ID ({item.required_cert})</label>
                      <input id="booking-operator" value={bookingOperatorID} onChange={(event) => setBookingOperatorID(event.target.value)} required />
                    </div>
                  )}
                  {bookingStatus === "tentative" && (
                    <div className="field">
                      <label htmlFor="booking-expires">Expiration date and time</label>
                      <input id="booking-expires" type="datetime-local" value={bookingExpiresAt} onChange={(event) => setBookingExpiresAt(event.target.value)} required />
                    </div>
                  )}
                  {bookingError && <div className="status-message status-error" role="alert">{bookingError}</div>}
                  <div className="card-actions">
                    <button className="btn" type="submit" disabled={bookingPending}>
                      {bookingPending ? "Submitting…" : "Submit booking"}
                    </button>
                    <button className="btn" type="button" onClick={closeBooking} disabled={bookingPending}>Cancel booking</button>
                  </div>
                </form>
              )}
            </div>
          ))}
        </div>
      )}
    </div>
  );
}
