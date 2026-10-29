import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";

import { ThemeProvider } from "@/lib/theme";
import { THEME_STORAGE_KEY } from "@/lib/theme-model";
import { mockMatchMedia } from "@/test/match-media";

import { ThemeToggle } from "./theme-toggle";

function renderToggle() {
  return render(
    <ThemeProvider>
      <ThemeToggle />
    </ThemeProvider>,
  );
}

const root = () => document.documentElement;

describe("ThemeToggle", () => {
  it("follows prefers-color-scheme when nothing is stored", () => {
    mockMatchMedia(true);
    renderToggle();
    expect(screen.getByRole("radio", { name: "System" })).toBeChecked();
    expect(root()).toHaveClass("dark");
  });

  it("applies and persists an explicit choice", async () => {
    mockMatchMedia(false);
    const user = userEvent.setup();
    renderToggle();
    expect(root()).not.toHaveClass("dark");

    await user.click(screen.getByRole("radio", { name: "Dark" }));

    expect(screen.getByRole("radio", { name: "Dark" })).toBeChecked();
    expect(root()).toHaveClass("dark");
    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBe("dark");
  });

  it("restores the stored choice and overrides the system preference", () => {
    mockMatchMedia(true);
    window.localStorage.setItem(THEME_STORAGE_KEY, "light");
    renderToggle();
    expect(screen.getByRole("radio", { name: "Light" })).toBeChecked();
    expect(root()).not.toHaveClass("dark");
  });

  it("clears storage when switching back to system and ignores garbage values", async () => {
    mockMatchMedia(false);
    window.localStorage.setItem(THEME_STORAGE_KEY, "<script>");
    const user = userEvent.setup();
    renderToggle();
    expect(screen.getByRole("radio", { name: "System" })).toBeChecked();

    await user.click(screen.getByRole("radio", { name: "Dark" }));
    await user.click(screen.getByRole("radio", { name: "System" }));

    expect(window.localStorage.getItem(THEME_STORAGE_KEY)).toBeNull();
    expect(root()).not.toHaveClass("dark");
  });
});
