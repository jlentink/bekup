import { describe, expect, it } from "vitest";

import { describeUserAgent, groupKey, recoveryCodesText } from "./model";

describe("account helpers", () => {
  it("groups the TOTP key in blocks of four", () => {
    expect(groupKey("JBSWY3DPEHPK3PXP")).toBe("JBSW Y3DP EHPK 3PXP");
    expect(groupKey("ABCDE")).toBe("ABCD E");
  });

  it("names browsers and systems", () => {
    expect(
      describeUserAgent(
        "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36",
      ),
    ).toBe("Chrome on macOS");
    expect(
      describeUserAgent("Mozilla/5.0 (Windows NT 10.0) Chrome/130.0 Safari/537.36 Edg/130.0"),
    ).toBe("Edge on Windows");
    expect(describeUserAgent(null)).toBe("Unknown browser");
  });

  it("writes the recovery code file", () => {
    expect(
      recoveryCodesText(["aaaa-bbbb", "cccc-dddd"], "ada@example.com", "bekup.example.com"),
    ).toBe(
      "bekup recovery codes for ada@example.com at bekup.example.com\nEach code works once. Keep them somewhere safe.\n\naaaa-bbbb\ncccc-dddd\n",
    );
  });
});
