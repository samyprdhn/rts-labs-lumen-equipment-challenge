// Enumerated before writing any test code (write-tests skill, step 1).
//
// | # | Scenario | Expected |
// |---|---|---|
// | 1 | Conflict with booking_id + status=confirmed | Release button shown |
// | 2 | Conflict with booking_id + status=tentative | Release button shown |
// | 3 | Redacted conflict (no booking_id, {exists:true}) | no Release button |
// | 4 | Click release | POSTs to /api/reservations/<id>/release with the REAL
// |   | | Bearer token - not a placeholder (regression test for the earlier
// |   | | "******" bug found in the booking-create form) |
// | 5 | 200 response | notice shown, availability re-fetched |
// | 6 | 403 response | permission-denied message shown |
// | 7 | 404 response | not-found message shown |
// | 8 | 409 response | server's own message shown |

import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import App from "./App";

function response(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
    headers: new Headers(),
  } as Response;
}

function bookedAsset(conflict: unknown) {
  return {
    asset_tag: "EX-001",
    type: "excavator",
    available: false,
    required_cert: "",
    conflict,
    maintenance: { status: "ok" },
  };
}

function availabilityResponse(asset: unknown) {
  return response(200, { data: [asset] });
}

describe("release a reservation", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("token", "fake-token-for-tests");
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("shows a Release button for a confirmed booking with full conflict detail", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(
      availabilityResponse(bookedAsset({ booking_id: "RES-1", site_id: "S01", end_at: "2026-11-02T11:00:00Z", status: "confirmed" })),
    );
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    expect(await screen.findByRole("button", { name: "Release RES-1" })).toBeTruthy();
  });

  it("shows a Release button for a tentative hold too", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(
      availabilityResponse(bookedAsset({ booking_id: "RES-2", site_id: "S01", end_at: "2026-11-02T11:00:00Z", status: "tentative" })),
    );
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    expect(await screen.findByRole("button", { name: "Release RES-2" })).toBeTruthy();
  });

  it("shows no Release button when the conflict is redacted (no booking_id)", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(availabilityResponse(bookedAsset({ exists: true })));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    await screen.findByText("EX-001");
    expect(screen.queryByRole("button", { name: /^Release/ })).toBeNull();
  });

  it("sends the real bearer token, not a placeholder, and refreshes availability on success", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        availabilityResponse(bookedAsset({ booking_id: "RES-3", site_id: "S01", end_at: "2026-11-02T11:00:00Z", status: "confirmed" })),
      )
      .mockResolvedValueOnce(response(200, { data: { booking_id: "RES-3", status: "cancelled" } }))
      .mockResolvedValueOnce(availabilityResponse(bookedAsset(null)));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    fireEvent.click(await screen.findByRole("button", { name: "Release RES-3" }));

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(3));
    const [url, options] = fetchMock.mock.calls[1];
    expect(url).toBe("/api/reservations/RES-3/release");
    expect(options.method).toBe("POST");
    expect(options.headers).toEqual({ Authorization: "Bearer fake-token-for-tests" });
    expect(options.body).toBeUndefined();

    expect(await screen.findByText(/Reservation RES-3 released/)).toBeTruthy();
  });

  it.each([
    [403, { error: "not authorized" }, /permission/i],
    [404, { error: "unknown booking_id" }, /not found/i],
    [409, { error: "reservation is already cancelled" }, /already cancelled/],
  ])("shows an error message for a %i response", async (status, body, expectedText) => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        availabilityResponse(bookedAsset({ booking_id: "RES-4", site_id: "S01", end_at: "2026-11-02T11:00:00Z", status: "confirmed" })),
      )
      .mockResolvedValueOnce(response(status, body));
    vi.stubGlobal("fetch", fetchMock);
    render(<App />);

    fireEvent.click(await screen.findByRole("button", { name: "Release RES-4" }));

    expect(await screen.findByText(expectedText)).toBeTruthy();
  });
});
