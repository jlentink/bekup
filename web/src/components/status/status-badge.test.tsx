import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { SecurityBadge } from "./security-badge";
import { RUN_STATUSES, SECURITY_FLAGS, STATUS_META } from "./status";
import { StatusBadge, StatusDot } from "./status-badge";

describe("StatusBadge", () => {
  it.each(RUN_STATUSES)("renders %s with a text label and a shape glyph", (status) => {
    const { container } = render(<StatusBadge status={status} />);
    const badge = screen.getByText(STATUS_META[status].label);
    expect(badge).toHaveAttribute("data-status", status);
    // Color is never the only signal: an icon and the text are both present.
    expect(container.querySelector("svg[aria-hidden='true']")).not.toBeNull();
  });

  it("uses a distinct icon per status", () => {
    const icons = new Set(RUN_STATUSES.map((s) => STATUS_META[s].icon));
    expect(icons.size).toBe(RUN_STATUSES.length);
  });

  it("accepts a custom label", () => {
    render(<StatusBadge status="partial" label="Partial · 1 of 2" />);
    expect(screen.getByText("Partial · 1 of 2")).toBeInTheDocument();
  });
});

describe("StatusDot", () => {
  it("has an accessible name", () => {
    render(<StatusDot status="missed" />);
    expect(screen.getByRole("img", { name: "Missed" })).toBeInTheDocument();
  });
});

describe("SecurityBadge", () => {
  it.each(SECURITY_FLAGS)("explains the %s flag to screen readers", (flag) => {
    const { container } = render(<SecurityBadge flag={flag} />);
    const badge = container.querySelector(`[data-flag="${flag}"]`);
    expect(badge).toHaveClass("hatch-insecure");
    expect(badge?.textContent).toMatch(/administrator/);
  });

  it("labels both weakened settings", () => {
    render(
      <>
        <SecurityBadge flag="unencrypted" />
        <SecurityBadge flag="insecure-transport" />
      </>,
    );
    expect(screen.getByText("Unencrypted")).toBeInTheDocument();
    expect(screen.getByText("Insecure transport")).toBeInTheDocument();
  });
});
