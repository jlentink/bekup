import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  type ChannelLike,
  createSharedStreams,
  type LeaderSink,
  type SharedEnd,
  SharedStreams,
} from "./stream-sharing";

/** An in-memory BroadcastChannel: async, cloned delivery to the other members. */
class FakeBus {
  readonly members = new Set<FakeChannel>();
  channel(): FakeChannel {
    const c = new FakeChannel(this);
    this.members.add(c);
    return c;
  }
}

class FakeChannel implements ChannelLike {
  private readonly listeners = new Set<(ev: MessageEvent) => void>();
  muted = false;
  private readonly bus: FakeBus;
  constructor(bus: FakeBus) {
    this.bus = bus;
  }
  postMessage(message: unknown): void {
    if (this.muted) return;
    const data: unknown = structuredClone(message);
    for (const m of this.bus.members) {
      if (m === this || m.muted) continue;
      queueMicrotask(() => {
        for (const l of [...m.listeners]) l(new MessageEvent("message", { data }));
      });
    }
  }
  addEventListener(_type: "message", l: (ev: MessageEvent) => void): void {
    this.listeners.add(l);
  }
  removeEventListener(_type: "message", l: (ev: MessageEvent) => void): void {
    this.listeners.delete(l);
  }
  close(): void {
    this.bus.members.delete(this);
  }
}

interface Tab {
  hub: SharedStreams;
  channel: FakeChannel;
  opened: number;
  disposed: number;
  sink: LeaderSink<string> | undefined;
  got: string[];
  ends: { end: SharedEnd; local: boolean }[];
  local: boolean[];
  leave: (() => void) | undefined;
}

function tab(bus: FakeBus, id: string): Tab {
  const channel = bus.channel();
  const t: Tab = {
    hub: new SharedStreams({
      channel,
      tabId: id,
      electionMs: 100,
      electionJitterMs: 0,
      pingMs: 1000,
      pingTimeoutMs: 200,
      lifecycle: null,
    }),
    channel,
    opened: 0,
    disposed: 0,
    sink: undefined,
    got: [],
    ends: [],
    local: [],
    leave: undefined,
  };
  return t;
}

function join(t: Tab, key = "account:u1") {
  t.leave = t.hub.subscribe<string>(
    key,
    (sink) => {
      t.opened++;
      t.sink = sink;
      return () => {
        t.disposed++;
        t.sink = undefined;
      };
    },
    {
      next(v, local) {
        t.got.push(v);
        t.local.push(local);
      },
      end(end, local) {
        t.ends.push({ end, local });
      },
    },
  );
}

/** Advances fake time and flushes the channel's microtasks. */
async function tick(ms: number) {
  await vi.advanceTimersByTimeAsync(ms);
}

beforeEach(() => {
  vi.useFakeTimers();
});

afterEach(() => {
  vi.useRealTimers();
});

describe("shared streams", () => {
  it("lets a lone tab lead after the election window", async () => {
    const a = tab(new FakeBus(), "a");
    join(a);
    expect(a.hub.roleOf("account:u1")).toBe("candidate");
    expect(a.opened).toBe(0);
    await tick(100);
    expect(a.hub.roleOf("account:u1")).toBe("leader");
    expect(a.opened).toBe(1);
    a.sink?.next("e1");
    expect(a.got).toEqual(["e1"]);
    expect(a.local).toEqual([true]);
  });

  it("relays the leader's events to followers, which open no stream", async () => {
    const bus = new FakeBus();
    const a = tab(bus, "a");
    const b = tab(bus, "b");
    join(a);
    await tick(100);
    join(b);
    await tick(150);
    expect(b.hub.roleOf("account:u1")).toBe("follower");
    expect(b.opened).toBe(0);
    a.sink?.next("e1");
    a.sink?.next("e2");
    await tick(0);
    expect(b.got).toEqual(["e1", "e2"]);
    expect(b.local).toEqual([false, false]);
  });

  it("keeps different keys apart (another account never receives them)", async () => {
    const bus = new FakeBus();
    const a = tab(bus, "a");
    const b = tab(bus, "b");
    join(a, "account:u1");
    join(b, "account:u2");
    await tick(150);
    expect(a.opened).toBe(1);
    expect(b.opened).toBe(1);
    a.sink?.next("for u1");
    await tick(0);
    expect(b.got).toEqual([]);
  });

  it("hands over at once when the leader leaves", async () => {
    const bus = new FakeBus();
    const a = tab(bus, "a");
    const b = tab(bus, "b");
    join(a);
    await tick(100);
    join(b);
    await tick(150);
    a.leave?.();
    expect(a.disposed).toBe(1);
    await tick(150);
    expect(b.hub.roleOf("account:u1")).toBe("leader");
    expect(b.opened).toBe(1);
  });

  it("takes over when the leader stops answering pings", async () => {
    const bus = new FakeBus();
    const a = tab(bus, "a");
    const b = tab(bus, "b");
    join(a);
    await tick(100);
    join(b);
    await tick(150);
    a.channel.muted = true; // the leader's tab crashed
    await tick(1000 + 200 + 100);
    expect(b.hub.roleOf("account:u1")).toBe("leader");
    expect(b.opened).toBe(1);
  });

  it("keeps the follower while the leader answers pings", async () => {
    const bus = new FakeBus();
    const a = tab(bus, "a");
    const b = tab(bus, "b");
    join(a);
    await tick(100);
    join(b);
    await tick(5000);
    expect(b.hub.roleOf("account:u1")).toBe("follower");
    expect(b.opened).toBe(0);
  });

  it("lets the first claim win over candidates in the window", async () => {
    const bus = new FakeBus();
    const b = tab(bus, "b");
    const a = tab(bus, "a");
    join(b);
    join(a);
    await tick(150);
    expect(b.hub.roleOf("account:u1")).toBe("leader");
    expect(a.hub.roleOf("account:u1")).toBe("follower");
    expect(a.opened).toBe(0);
  });

  it("resolves two leaders to the lower tab id", async () => {
    const bus = new FakeBus();
    const b = tab(bus, "b");
    const a = tab(bus, "a");
    // Both claim without hearing each other (e.g. a delayed channel).
    a.channel.muted = b.channel.muted = true;
    join(b);
    join(a);
    await tick(150);
    expect(a.opened).toBe(1);
    expect(b.opened).toBe(1);
    a.channel.muted = b.channel.muted = false;
    // A third tab asks; both answer and learn about each other.
    const c = tab(bus, "c");
    join(c);
    await tick(150);
    expect(a.hub.roleOf("account:u1")).toBe("leader");
    expect(b.hub.roleOf("account:u1")).toBe("follower");
    expect(c.hub.roleOf("account:u1")).toBe("follower");
    expect(b.disposed).toBe(1);
    expect(c.opened).toBe(0);
    a.sink?.next("only once");
    await tick(0);
    expect(b.got).toEqual(["only once"]);
    expect(c.got).toEqual(["only once"]);
  });

  it("relays the end of the stream with its kind", async () => {
    const bus = new FakeBus();
    const a = tab(bus, "a");
    const b = tab(bus, "b");
    join(a);
    await tick(100);
    join(b);
    await tick(150);
    a.sink?.end({ kind: "error", message: "refused", authLost: true });
    await tick(0);
    expect(a.ends).toEqual([
      { end: { kind: "error", message: "refused", authLost: true }, local: true },
    ]);
    expect(b.ends).toEqual([
      { end: { kind: "error", message: "refused", authLost: true }, local: false },
    ]);
    expect(b.hub.roleOf("account:u1")).toBeUndefined();
  });

  it("shares one stream between subscribers of one tab", async () => {
    const a = tab(new FakeBus(), "a");
    join(a);
    const second: string[] = [];
    let secondOpened = 0;
    const leave = a.hub.subscribe<string>(
      "account:u1",
      (sink) => {
        secondOpened++;
        a.sink = sink;
        return () => {
          a.disposed++;
        };
      },
      { next: (v) => second.push(v), end: () => undefined },
    );
    await tick(100);
    expect(a.opened + secondOpened).toBe(1);
    a.sink?.next("x");
    expect(second).toEqual(["x"]);
    leave();
    expect(a.disposed).toBe(0);
    a.leave?.();
    expect(a.disposed).toBe(1);
  });

  it("ignores foreign and malformed messages", async () => {
    const bus = new FakeBus();
    const a = tab(bus, "a");
    join(a);
    await tick(100);
    const rogue = bus.channel();
    rogue.postMessage("junk");
    rogue.postMessage({ t: "event", key: "account:u1", from: "z", term: "t", value: "forged" });
    await tick(0);
    expect(a.got).toEqual([]);
    expect(a.hub.roleOf("account:u1")).toBe("leader");
  });
});

describe("createSharedStreams", () => {
  it("returns null without BroadcastChannel (each tab opens its own streams)", () => {
    vi.stubGlobal("BroadcastChannel", undefined);
    try {
      expect(createSharedStreams()).toBeNull();
    } finally {
      vi.unstubAllGlobals();
    }
  });
});
