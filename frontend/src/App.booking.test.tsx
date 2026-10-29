import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import App from "./App";

const availableAsset = {
  asset_tag: "EX-001",
  type: "excavator",
  available: true,
  required_cert: "",
  conflict: null,
  maintenance: { status: "ok" },
};

function response(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    headers: new Headers(),
  } as Response;
}

function availabilityResponse(asset = availableAsset) {
  return response(200, { data: [asset] });
}

function fillBookingForm() {
  fireEvent.change(screen.getByLabelText("Site ID"), { target: { value: "S01" } });
  fireEvent.change(screen.getByLabelText("Start date and time"), { target: { value: "2026-11-02T09:00" } });
  fireEvent.change(screen.getByLabelText("End date and time"), { target: { value: "2026-11-02T11:00" } });
}

async function openBookingForm() {
  await screen.findByText("EX-001");
  fireEvent.click(screen.getByRole("button", { name: "Book EX-001" }));
}

describe("reservation form", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("token", "fake-token-for-tests");
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("submits a confirmed booking with RFC3339 timestamps and refreshes availability", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(availabilityResponse())
      .mockResolvedValueOnce(response(201, { data: { booking_id: "RES-001" } }))
      .mockResolvedValueOnce(availabilityResponse({ ...availableAsset, available: false }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    await openBookingForm();
    expect((screen.getByLabelText("Status") as HTMLSelectElement).value).toBe("confirmed");
    expect(screen.queryByLabelText(/operator id/i)).toBeNull();
    fillBookingForm();
    fireEvent.click(screen.getByRole("button", { name: "Submit booking" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    const [url, options] = fetchMock.mock.calls[1];
    expect(url).toBe("/api/reservations");
    expect(options.headers).toEqual({ Authorization: "Bearer fake-token-for-tests", "Content-Type": "application/json" });
    expect(JSON.parse(options.body)).toEqual({
      asset_tag: "EX-001",
      site_id: "S01",
      start: new Date("2026-11-02T09:00").toISOString(),
      end: new Date("2026-11-02T11:00").toISOString(),
      status: "confirmed",
    });
    expect(screen.queryByRole("form", { name: "Booking EX-001" })).toBeNull();
    expect(await screen.findByText("Unavailable")).toBeTruthy();
  });

  it("shows tentative expiry and operator fields only when applicable", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(200, { data: [
      { ...availableAsset, asset_tag: "MC-001", required_cert: "NCCCO Mobile Crane" },
      { ...availableAsset, asset_tag: "EX-002", available: false },
    ] })));
    render(<App />);

    await screen.findByText("MC-001");
    expect(screen.queryByRole("button", { name: "Book EX-002" })).toBeNull();
    expect(screen.getByRole("button", { name: "Book MC-001" })).toBeTruthy();
    fireEvent.click(screen.getByRole("button", { name: "Book MC-001" }));

    expect(screen.getByLabelText(/Operator ID/)).toBeTruthy();
    expect(screen.queryByLabelText("Expiration date and time")).toBeNull();
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "tentative" } });
    expect(screen.getByLabelText("Expiration date and time")).toBeTruthy();
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "confirmed" } });
    expect(screen.queryByLabelText("Expiration date and time")).toBeNull();
  });

  it("submits a tentative booking with its required RFC3339 expiry", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(availabilityResponse({ ...availableAsset, required_cert: "NCCCO Mobile Crane" }))
      .mockResolvedValueOnce(response(201, { data: { booking_id: "RES-002" } }))
      .mockResolvedValueOnce(availabilityResponse({ ...availableAsset, available: false }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    await openBookingForm();
    fillBookingForm();
    fireEvent.change(screen.getByLabelText("Status"), { target: { value: "tentative" } });
    fireEvent.change(screen.getByLabelText("Expiration date and time"), { target: { value: "2026-11-01T17:00" } });
    fireEvent.change(screen.getByLabelText(/Operator ID/), { target: { value: "OP-007" } });
    fireEvent.click(screen.getByRole("button", { name: "Submit booking" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    expect(JSON.parse(fetchMock.mock.calls[1][1].body)).toMatchObject({
      status: "tentative",
      expires_at: new Date("2026-11-01T17:00").toISOString(),
      operator_id: "OP-007",
    });
  });

  it.each([
    [400, "start must be after the current time"],
    [422, "operator is not certified"],
    [403, "not authorized to book at site S01"],
  ])("shows the backend message for HTTP %i", async (status, message) => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(availabilityResponse())
      .mockResolvedValueOnce(response(status, { error: message }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    await openBookingForm();
    fillBookingForm();
    fireEvent.click(screen.getByRole("button", { name: "Submit booking" }));

    expect((await screen.findByRole("alert")).textContent).toContain(message);
    expect(screen.getByRole("form", { name: "Booking EX-001" })).toBeTruthy();
  });

  it("shows available conflict detail or a generic message when details are redacted", async () => {
    for (const [body, expected] of [
      [{ error: "conflict", conflict: { site_id: "S02", start_at: "2026-11-02T09:00:00Z", end_at: "2026-11-02T11:00:00Z" } }, /S02.*2026-11-02T09:00/],
      [{ error: "conflict", conflict: { exists: true } }, /already booked/i],
    ] as const) {
      cleanup();
      const fetchMock = vi.fn()
        .mockResolvedValueOnce(availabilityResponse())
        .mockResolvedValueOnce(response(409, body));
      vi.stubGlobal("fetch", fetchMock);
      render(<App />);
      await openBookingForm();
      fillBookingForm();
      fireEvent.click(screen.getByRole("button", { name: "Submit booking" }));
      expect((await screen.findByRole("alert")).textContent).toMatch(expected);
    }
  });

  it("shows retry guidance for database contention and keeps the form open", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(availabilityResponse())
      .mockResolvedValueOnce(response(503, { error: "try again" }));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    await openBookingForm();
    fillBookingForm();
    fireEvent.click(screen.getByRole("button", { name: "Submit booking" }));

    expect((await screen.findByRole("alert")).textContent).toMatch(/temporarily busy|try again/i);
    expect(screen.getByRole("form", { name: "Booking EX-001" })).toBeTruthy();
  });

  it("shows a visible error if the booking request cannot reach the server", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(availabilityResponse())
      .mockRejectedValueOnce(new TypeError("Failed to fetch"));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    await openBookingForm();
    fillBookingForm();
    fireEvent.click(screen.getByRole("button", { name: "Submit booking" }));

    expect((await screen.findByRole("alert")).textContent).toMatch(/could not reach the server/i);
  });

  it("keeps all booking controls labeled and keyboard-focusable in form order", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(availabilityResponse()));
    render(<App />);
    await openBookingForm();

    const controls = [
      screen.getByLabelText("Site ID"),
      screen.getByLabelText("Start date and time"),
      screen.getByLabelText("End date and time"),
      screen.getByLabelText("Status"),
      screen.getByRole("button", { name: "Submit booking" }),
      screen.getByRole("button", { name: "Cancel booking" }),
    ];
    for (const control of controls) {
      expect(control.tabIndex).toBeGreaterThanOrEqual(0);
      (control as HTMLElement).focus();
      expect(document.activeElement).toBe(control);
    }
  });
});
