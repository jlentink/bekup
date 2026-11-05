import { screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { baseHandlers, meFixture, renderApp } from "@/test/mock-api";

const UI_VERSION = `v${__BEKUP_VERSION__}`;

describe("about dialog", () => {
  it("opens from the mobile navigation drawer and shows the UI version", async () => {
    const { user } = renderApp("/profile", baseHandlers());
    await user.click(await screen.findByRole("button", { name: "Open navigation" }));
    const drawer = await screen.findByRole("dialog");
    await user.click(within(drawer).getByRole("button", { name: "About bekup" }));
    const dialog = await screen.findByRole("dialog", { name: "About bekup" });
    expect(within(dialog).getByTestId("about-product")).toHaveTextContent(`bekup ${UI_VERSION}`);
    expect(within(dialog).getByTestId("about-ui-version")).toHaveTextContent(UI_VERSION);
    expect(within(dialog).getByText("MIT")).toBeInTheDocument();
    const docsLink = within(dialog).getByRole("link", { name: /Documentation/ });
    expect(docsLink).toHaveAttribute("rel", "noreferrer");
    expect(docsLink).toHaveAttribute("target", "_blank");
    expect(within(dialog).getByRole("link", { name: /Release notes/ })).toHaveAttribute(
      "href",
      "https://github.com/jlentink/bekup/blob/main/CHANGELOG.md",
    );
  });

  it("shows the server version to admins from the account menu", async () => {
    const { user } = renderApp("/profile", baseHandlers());
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    await user.click(await screen.findByRole("menuitem", { name: "About" }));
    const dialog = await screen.findByRole("dialog", { name: "About bekup" });
    expect(await within(dialog).findByTestId("about-server-version")).toHaveTextContent("v1.0.0");
  });

  it("does not show the server version to other roles", async () => {
    const { user } = renderApp("/profile", baseHandlers(meFixture({}, "USER")));
    await user.click(await screen.findByRole("button", { name: "Account menu" }));
    await user.click(await screen.findByRole("menuitem", { name: "About" }));
    const dialog = await screen.findByRole("dialog", { name: "About bekup" });
    expect(within(dialog).getByTestId("about-ui-version")).toHaveTextContent(UI_VERSION);
    expect(within(dialog).queryByTestId("about-server-version")).not.toBeInTheDocument();
    expect(within(dialog).queryByText("Server")).not.toBeInTheDocument();
  });
});
