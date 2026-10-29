import { describe, it, expect, beforeEach, vi, afterEach } from "vitest";
import { render, screen, cleanup, fireEvent } from "@testing-library/react";
import App from "./App";

function mockFetchOnce(response: Partial<Response> & { jsonBody?: any }) {
  return vi.fn().mockResolvedValueOnce({
    ok: response.ok ?? true,
    status: response.status ?? 200,
    json: async () => response.jsonBody,
  } as Response);
}

describe("App loading/empty/error/retry states", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("token", "fake-token-for-tests");
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // Scenario 2: a loading indicator is shown while the request is in flight.
  it("shows a loading indicator while the request is pending", async () => {
    let resolveFetch: (v: any) => void;
    const pending = new Promise((resolve) => {
      resolveFetch = resolve;
    });
    vi.stubGlobal("fetch", vi.fn().mockReturnValue(pending));

    render(<App />);
    expect(await screen.findByText(/loading/i)).toBeTruthy();

    resolveFetch!({ ok: true, status: 200, json: async () => ({ data: [] }) });
  });

  // Scenario 3: a successful fetch with results renders the grid, no error, no loading.
  it("renders results and clears loading on success", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetchOnce({
        jsonBody: {
          data: [{ asset_tag: "MC-001", type: "mobile_crane", available: true, conflict: null, maintenance: { status: "ok" } }],
        },
      })
    );
    render(<App />);
    expect(await screen.findByText("MC-001")).toBeTruthy();
    expect(screen.queryByText(/loading/i)).toBeNull();
    expect(screen.queryByText(/error/i)).toBeNull();
  });

  // Scenario 4: zero results get a distinct empty-state message, not a blank grid and not an error.
  it("shows a distinct empty state for zero results", async () => {
    vi.stubGlobal("fetch", mockFetchOnce({ jsonBody: { data: [] } }));
    render(<App />);
    expect(await screen.findByText(/no equipment/i)).toBeTruthy();
    expect(screen.queryByText(/error/i)).toBeNull();
  });

  // Scenario 5: a non-2xx response shows an error state with the server's message.
  it("shows an error state with the server's message on a non-2xx response", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetchOnce({ ok: false, status: 400, jsonBody: { error: "end must be after start" } })
    );
    render(<App />);
    expect(await screen.findByText(/end must be after start/i)).toBeTruthy();
  });

  // Scenario 6: a thrown/rejected fetch (network failure) shows a generic error state.
  it("shows a generic error state when the request itself fails", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValueOnce(new TypeError("Failed to fetch")));
    render(<App />);
    expect(await screen.findByText(/could not reach the server|network/i)).toBeTruthy();
  });

  // Scenario 7: the error state offers a retry that can recover to success.
  it("retries and recovers to a successful state", async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new TypeError("Failed to fetch"))
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ data: [{ asset_tag: "MC-002", type: "mobile_crane", available: true, conflict: null, maintenance: { status: "ok" } }] }),
      } as Response);
    vi.stubGlobal("fetch", fetchMock);

    render(<App />);
    const retryButton = await screen.findByRole("button", { name: /retry/i });
    fireEvent.click(retryButton);

    expect(await screen.findByText("MC-002")).toBeTruthy();
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  // Scenario 8: a stale in-flight response must not clobber a newer one.
  it("ignores a stale response superseded by a newer request", async () => {
    let resolveFirst: (v: any) => void;
    const firstPending = new Promise((resolve) => {
      resolveFirst = resolve;
    });
    const fetchMock = vi
      .fn()
      .mockReturnValueOnce(firstPending)
      .mockResolvedValueOnce({
        ok: true,
        status: 200,
        json: async () => ({ data: [{ asset_tag: "NEWER", type: "mobile_crane", available: true, conflict: null, maintenance: { status: "ok" } }] }),
      } as Response);
    vi.stubGlobal("fetch", fetchMock);

    const { rerender } = render(<App />);
    // trigger a second request before the first resolves, by changing a filter
    const select = await screen.findByRole("combobox");
    fireEvent.change(select, { target: { value: "excavator" } });
    rerender(<App />);

    await screen.findByText("NEWER");

    // now let the FIRST (stale) request resolve - it must not overwrite "NEWER"
    resolveFirst!({
      ok: true,
      status: 200,
      json: async () => ({ data: [{ asset_tag: "STALE", type: "mobile_crane", available: true, conflict: null, maintenance: { status: "ok" } }] }),
    });
    await new Promise((r) => setTimeout(r, 10));

    expect(screen.queryByText("STALE")).toBeNull();
    expect(screen.getByText("NEWER")).toBeTruthy();
  });
});
