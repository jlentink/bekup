import { describe, expect, it } from "vitest";

// The guide is the reference text of the snippets (D-220): the UI fills in
// only the base URL and the job id.
import guide from "../../../../docs/guides/external-api.md?raw";

import { addressProblem, expiresAtFor, expiryInput, splitAddresses, tokenState } from "./model";
import {
  apiBaseUrl,
  ciScript,
  GUIDE_BASE_URL,
  GUIDE_JOB_ID,
  GUIDE_JOB_VAR,
  selfSnippet,
  startSnippet,
  statusSnippet,
} from "./snippets";

/** The first ```sh block after heading in the guide. */
function guideBlock(heading: string): string {
  const at = guide.indexOf(heading);
  expect(at, `heading ${heading} in the guide`).toBeGreaterThanOrEqual(0);
  const start = guide.indexOf("```sh\n", at);
  const end = guide.indexOf("\n```", start + 6);
  return guide.slice(start + 6, end);
}

const JOB = "0b6c3f7e-1234-4000-8000-00000000a001";

describe("CI snippets", () => {
  it("are the guide's text", () => {
    expect(startSnippet(GUIDE_BASE_URL, GUIDE_JOB_VAR)).toBe(guideBlock("### Start a run"));
    expect(statusSnippet(GUIDE_BASE_URL)).toBe(guideBlock("### Follow a request"));
    expect(selfSnippet(GUIDE_BASE_URL)).toBe(guideBlock("### Check a token"));
    expect(ciScript(GUIDE_BASE_URL, GUIDE_JOB_ID).trimEnd()).toBe(guideBlock("## CI script"));
  });

  it("carry this origin's /api/v1 and the job id, and the token only as $BEKUP_TOKEN", () => {
    const base = apiBaseUrl("https://backup.example.org:8443");
    expect(base).toBe("https://backup.example.org:8443/api/v1");
    for (const s of [startSnippet(base, JOB), ciScript(base, JOB)]) {
      expect(s).toContain(base);
      expect(s).toContain(JOB);
      expect(s).not.toMatch(/bkp_/);
    }
    expect(startSnippet(base, JOB)).toContain(`"${base}/jobs/${JOB}/runs"`);
    expect(startSnippet(base, JOB)).toContain('"Authorization: Bearer $BEKUP_TOKEN"');
    expect(statusSnippet(base)).toContain(`"${base}/requests/$REQUEST_ID"`);
    expect(ciScript(base, JOB)).toContain(`BEKUP_URL="\${BEKUP_URL:-${base}}"`);
    expect(ciScript(base, JOB)).toContain(`BEKUP_JOB_ID="\${BEKUP_JOB_ID:-${JOB}}"`);
    expect(ciScript(base, JOB)).toContain("${BEKUP_TOKEN}");
  });

  it("never put anything but a job id into the shell strings", () => {
    const base = apiBaseUrl("https://backup.example.org");
    const evil = '"; rm -rf / #';
    expect(startSnippet(base, evil)).not.toContain("rm -rf");
    expect(ciScript(base, evil)).not.toContain("rm -rf");
    expect(apiBaseUrl("https://backup.example.org/some/path?q=1")).toBe(
      "https://backup.example.org/api/v1",
    );
  });
});

describe("address checks", () => {
  it("mirror the server's rules", () => {
    expect(splitAddresses(" 192.0.2.0/24 ,\n\n2001:db8::/48\n")).toEqual([
      "192.0.2.0/24",
      "2001:db8::/48",
    ]);
    expect(addressProblem(["192.0.2.0/24", "203.0.113.7", "2001:db8::/48"])).toBeNull();
    expect(addressProblem([])).toMatch(/at least one network/);
    expect(addressProblem(Array.from({ length: 33 }, (_, i) => `192.0.2.${String(i)}`))).toMatch(
      /At most 32/,
    );
    expect(addressProblem(["example.com"])).toMatch(/not an IPv4 or IPv6/);
    expect(addressProblem(["192.0.2.256"])).toMatch(/not an IPv4 or IPv6/);
    expect(addressProblem(["192.0.2.0/33"])).toMatch(/prefix length/);
    expect(addressProblem(["0.0.0.0/0"])).toMatch(/Allow from any address/);
    expect(addressProblem(["::/0"])).toMatch(/Allow from any address/);
    expect(addressProblem(["0.0.0.0/1", "128.0.0.0/1"])).toMatch(
      /wider than \/8.*Allow from any address/,
    );
    expect(addressProblem(["2001:db8::/31"])).toMatch(/wider than \/32/);
    expect(addressProblem(["10.0.0.0/8", "2001:db8::/32"])).toBeNull();
    expect(addressProblem(["192.0.2.1/24"])).toMatch(/did you mean 192\.0\.2\.0\/24/);
    expect(addressProblem(["10.1.2.3/8"])).toMatch(/did you mean 10\.0\.0\.0\/8/);
    expect(addressProblem(["::ffff:192.0.2.1"])).toMatch(/IPv4-mapped/);
    expect(addressProblem(["192.0.2.7", "192.0.2.7/32"])).toMatch(/listed twice/);
    expect(addressProblem(["fe80::1%eth0"])).toMatch(/not an IPv4 or IPv6/);
  });
});

describe("expiry", () => {
  it("computes presets and states", () => {
    const now = Date.parse("2026-11-03T12:00:00Z");
    expect(expiresAtFor(90, now)).toBe("2027-02-01T12:00:00.000Z");
    expect(tokenState({ expiresAt: "2027-02-01T12:00:00Z" }, now)).toBe("active");
    expect(tokenState({ expiresAt: "2026-11-09T12:00:00Z" }, now)).toBe("expiring");
    expect(tokenState({ expiresAt: "2026-11-01T12:00:00Z" }, now)).toBe("expired");
    expect(
      tokenState({ expiresAt: "2027-02-01T12:00:00Z", revokedAt: "2026-11-02T00:00:00Z" }, now),
    ).toBe("revoked");
    // No expiry (D-229): active until revoked; the input carries exactly one choice.
    expect(tokenState({ expiresAt: null }, now)).toBe("active");
    expect(tokenState({ expiresAt: null, revokedAt: "2026-11-02T00:00:00Z" }, now)).toBe("revoked");
    expect(expiryInput("never", now)).toEqual({ expiresAt: null, neverExpires: true });
    expect(expiryInput(30, now)).toEqual({
      expiresAt: "2026-12-03T12:00:00.000Z",
      neverExpires: false,
    });
  });
});
