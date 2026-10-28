import { describe, it, expect, beforeEach } from "vitest";
import { render, screen } from "@testing-library/react";
import App from "./App";

describe("App", () => {
  beforeEach(() => localStorage.clear());

  it("asks for a token when logged out", () => {
    render(<App />);
    expect(screen.getByPlaceholderText("Paste dev token")).toBeTruthy();
  });
});
