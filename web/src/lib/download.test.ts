import { describe, expect, it, vi } from "vitest";

import { downloadText } from "./download";

describe("downloadText", () => {
  it("clicks a revoked blob: link with the file name and leaves nothing behind", async () => {
    const createObjectURL = vi.fn<(blob: Blob) => string>(() => "blob:fake");
    const revokeObjectURL = vi.fn();
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL, revokeObjectURL }));
    const clicked: HTMLAnchorElement[] = [];
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(function (
      this: HTMLAnchorElement,
    ) {
      clicked.push(this);
    });

    downloadText("codes.txt", "secret-ish");

    expect(clicked).toHaveLength(1);
    expect(clicked[0]?.download).toBe("codes.txt");
    expect(clicked[0]?.getAttribute("href")).toBe("blob:fake");
    expect(document.querySelector("a[download]")).toBeNull();
    const blob = createObjectURL.mock.calls[0]?.[0];
    expect(blob?.type).toBe("text/plain;charset=utf-8");
    await expect(blob?.text()).resolves.toBe("secret-ish");
    await vi.waitFor(() => {
      expect(revokeObjectURL).toHaveBeenCalledWith("blob:fake");
    });
    vi.unstubAllGlobals();
  });
});
