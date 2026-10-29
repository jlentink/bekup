import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { Tag } from "./tag";

describe("Tag", () => {
  it("renders words with a decorative dot and its tone", () => {
    render(
      <Tag tone="danger" title="why">
        Not ready
      </Tag>,
    );
    const tag = screen.getByText("Not ready");
    expect(tag).toHaveAttribute("data-tone", "danger");
    expect(tag).toHaveAttribute("title", "why");
    expect(tag.querySelector("[aria-hidden='true']")).not.toBeNull();
    expect(tag.className).toContain("h-6");
  });

  it("lets a compact tag be overridden by className", () => {
    render(
      <Tag compact tone="primary" className="h-auto">
        Original
      </Tag>,
    );
    const tag = screen.getByText("Original");
    expect(tag.className).toContain("h-auto");
    expect(tag.className).not.toContain("h-5");
  });
});
