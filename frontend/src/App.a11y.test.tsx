import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { render, screen, cleanup } from "@testing-library/react";
import { readFileSync } from "node:fs";
import path from "node:path";
import App from "./App";

const readSrc = (name: string) => readFileSync(path.resolve(process.cwd(), "src", name), "utf8");
const readRoot = (name: string) => readFileSync(path.resolve(process.cwd(), name), "utf8");

function mockFetchOnce(jsonBody: any) {
  return vi.fn().mockResolvedValueOnce({ ok: true, status: 200, json: async () => jsonBody } as Response);
}

describe("accessibility and mobile usability", () => {
  beforeEach(() => {
    localStorage.clear();
    localStorage.setItem("token", "fake-token-for-tests");
  });
  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  // Scenario 1: pinch-zoom must not be disabled.
  it("does not disable pinch-zoom in the viewport meta", () => {
    const html = readRoot("index.html");
    expect(html).not.toMatch(/user-scalable\s*=\s*no/i);
    expect(html).not.toMatch(/maximum-scale\s*=\s*1(\.0)?\b/i);
  });

  // Scenario 2: "Log out" and each card are real buttons - native keyboard
  // and focus support comes for free, rather than a div with onClick.
  it("renders Log out and equipment cards as real buttons", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetchOnce({ data: [{ asset_tag: "MC-001", type: "mobile_crane", available: true, conflict: null, maintenance: { status: "ok" } }] })
    );
    render(<App />);
    await screen.findByText("MC-001");

    expect(screen.getByRole("button", { name: /log out/i })).toBeTruthy();
    expect(screen.getByRole("button", { name: /MC-001, available/ })).toBeTruthy();
  });

  // Scenario 3: filter controls must be labeled, not bare inputs.
  it("labels the type, start, and end filter controls", async () => {
    vi.stubGlobal("fetch", mockFetchOnce({ data: [] }));
    render(<App />);
    await screen.findByText(/no equipment/i);

    expect(screen.getByLabelText(/equipment type/i)).toBeTruthy();
    expect(screen.getByLabelText(/^start date$/i)).toBeTruthy();
    expect(screen.getByLabelText(/^end date$/i)).toBeTruthy();
  });

  // Scenario 4: availability must be conveyed by text, not color alone.
  it("shows an Available/Unavailable text label alongside the status dot", async () => {
    vi.stubGlobal(
      "fetch",
      mockFetchOnce({
        data: [
          { asset_tag: "MC-001", type: "mobile_crane", available: true, conflict: null, maintenance: { status: "ok" } },
          { asset_tag: "MC-002", type: "mobile_crane", available: false, conflict: { exists: true }, maintenance: { status: "ok" } },
        ],
      })
    );
    render(<App />);
    await screen.findByText("MC-001");
    expect(screen.getAllByText(/^available$/i).length).toBeGreaterThan(0);
    expect(screen.getAllByText(/^unavailable$/i).length).toBeGreaterThan(0);
  });

  // Scenario 5: loading/error messages carry explicit ARIA status roles.
  it("marks the loading message with role=status and the error message with role=alert", async () => {
    let resolveFetch: (v: any) => void;
    const pending = new Promise((resolve) => {
      resolveFetch = resolve;
    });
    vi.stubGlobal("fetch", vi.fn().mockReturnValue(pending));
    render(<App />);
    expect(await screen.findByRole("status")).toBeTruthy();
    resolveFetch!({ ok: false, status: 500, json: async () => ({ error: "internal server error" }) });

    cleanup();
    vi.unstubAllGlobals();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValueOnce({ ok: false, status: 500, json: async () => ({ error: "internal server error" }) } as Response));
    render(<App />);
    expect(await screen.findByRole("alert")).toBeTruthy();
  });

  // Scenario 6: no blanket outline:none; a :focus-visible rule exists.
  it("does not blanket-disable the focus outline and defines :focus-visible", () => {
    const css = readSrc("styles.css");
    expect(css).not.toMatch(/\*\s*:\s*focus\s*\{[^}]*outline\s*:\s*none/);
    expect(css).toMatch(/:focus-visible\s*\{/);
  });

  // Scenario 8: interactive controls define a minimum touch-target size.
  it("gives interactive controls a minimum touch-target size", () => {
    const css = readSrc("styles.css");
    expect(css).toMatch(/min-height\s*:\s*44px/);
  });
});
