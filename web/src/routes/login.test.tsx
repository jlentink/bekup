import { MockedProvider } from "@apollo/client/testing/react";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { toast } from "sonner";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { ThemeProvider } from "@/lib/theme";
import { mockMatchMedia } from "@/test/match-media";

import { LoginPage } from "./login";

vi.mock("sonner", () => ({ toast: { info: vi.fn() } }));

function renderLogin() {
  mockMatchMedia(false);
  return render(
    <MockedProvider mocks={[]}>
      <ThemeProvider>
        <LoginPage />
      </ThemeProvider>
    </MockedProvider>,
  );
}

describe("LoginPage", () => {
  beforeEach(() => {
    vi.mocked(toast.info).mockClear();
  });

  it("asks for an e-mail address, not a username", () => {
    renderLogin();
    const email = screen.getByLabelText("E-mail address");
    expect(email).toHaveAttribute("type", "email");
    expect(email).toHaveAttribute("autocomplete", "username webauthn");
    expect(screen.queryByLabelText("Username")).not.toBeInTheDocument();
  });

  it("requires both fields", async () => {
    const user = userEvent.setup();
    renderLogin();
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Enter your e-mail address.")).toBeInTheDocument();
    expect(screen.getByText("Enter your password.")).toBeInTheDocument();
  });

  it("rejects a malformed e-mail address", async () => {
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByLabelText("E-mail address"), "ada");
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(await screen.findByText("Enter a valid e-mail address.")).toBeInTheDocument();
    expect(toast.info).not.toHaveBeenCalled();
  });

  it("accepts a well-formed e-mail address", async () => {
    const user = userEvent.setup();
    renderLogin();
    await user.type(screen.getByLabelText("E-mail address"), "  ada@example.com ");
    await user.type(screen.getByLabelText("Password"), "correct horse battery staple");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    await waitFor(() => {
      expect(toast.info).toHaveBeenCalledOnce();
    });
    expect(screen.queryByText("Enter a valid e-mail address.")).not.toBeInTheDocument();
  });
});
