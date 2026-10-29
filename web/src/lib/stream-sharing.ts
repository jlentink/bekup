/**
 * Cross-tab sharing of event streams (D-197). Every subscription is one
 * SSE connection and a session may hold only graphql.stream_max_per_session
 * (default 4) of them; over plain HTTP/1.1 a browser also allows only six
 * connections per origin. Tabs of one browser share the session cookie, so
 * one tab (the leader) holds a shared stream and relays its events to the
 * other tabs (followers) over a BroadcastChannel.
 *
 * Election per stream key, without timers in the leader: a tab that wants
 * a key asks ("hello"); the leader answers at once ("lead"). Without an
 * answer within the election window the tab claims the key itself. Two
 * claimants resolve by the lower tab id. Followers ping now and then and
 * take over when the leader does not answer; a closing leader resigns
 * (pagehide or unsubscribe), so its followers elect a new one at once.
 * Events between a handoff are not replayed (the streams carry hints the
 * pages reconcile with queries).
 *
 * The channel is same-origin only. Keys must include the account (the
 * caller passes e.g. `account:<user id>`), so a tab never accepts events
 * of a stream opened for another account.
 */

/** The part of BroadcastChannel the hub uses (tests pass a fake). */
export interface ChannelLike {
  postMessage(message: unknown): void;
  addEventListener(type: "message", listener: (ev: MessageEvent) => void): void;
  removeEventListener(type: "message", listener: (ev: MessageEvent) => void): void;
  close(): void;
}

/** How a shared stream ended at the leader. */
export type SharedEnd =
  { kind: "complete" } | { kind: "error"; message: string; authLost: boolean };

/**
 * The receiving side of a shared stream in one tab. `local` is true when
 * this tab opened the stream (it leads), false for relayed values.
 */
export interface SharedListener<T> {
  next(value: T, local: boolean): void;
  end(end: SharedEnd, local: boolean): void;
}

/** The leader's sink for the stream it opened. */
export interface LeaderSink<T> {
  next(value: T): void;
  end(end: SharedEnd): void;
}

/** Opens the real stream (leader only); returns its disposer. */
export type StreamOpener<T> = (sink: LeaderSink<T>) => () => void;

type Message =
  | { t: "hello"; key: string; from: string }
  | { t: "ping"; key: string; from: string }
  | { t: "lead"; key: string; from: string; term: string }
  | { t: "resign"; key: string; from: string; term: string }
  | { t: "event"; key: string; from: string; term: string; value: unknown }
  | { t: "end"; key: string; from: string; term: string; end: SharedEnd };

const MESSAGE_TYPES: ReadonlySet<string> = new Set([
  "hello",
  "ping",
  "lead",
  "resign",
  "event",
  "end",
]);

function isMessage(v: unknown): v is Message {
  if (typeof v !== "object" || v === null) return false;
  const m = v as Record<string, unknown>;
  return (
    typeof m["t"] === "string" &&
    MESSAGE_TYPES.has(m["t"]) &&
    typeof m["key"] === "string" &&
    typeof m["from"] === "string"
  );
}

type Role = "candidate" | "leader" | "follower" | "ended";

interface Entry {
  key: string;
  role: Role;
  listeners: Set<SharedListener<unknown>>;
  open: StreamOpener<unknown>;
  /** Leader: own term; follower: the followed leader's tab id and term. */
  term: string;
  leader: string;
  leaderTerm: string;
  dispose: (() => void) | undefined;
  election: ReturnType<typeof setTimeout> | undefined;
  pingTimer: ReturnType<typeof setTimeout> | undefined;
  pingDeadline: ReturnType<typeof setTimeout> | undefined;
  /** A lead message arrived since the last ping. */
  answered: boolean;
}

export interface SharedStreamsOptions {
  channel: ChannelLike;
  /** Unique per tab (default: random). */
  tabId?: string;
  /** Election window before claiming a key, plus up to `electionJitterMs`. */
  electionMs?: number;
  electionJitterMs?: number;
  /** Followers ping the leader this often... */
  pingMs?: number;
  /** ...and take over when it does not answer within this time. */
  pingTimeoutMs?: number;
  random?: () => number;
  /** Where to listen for pagehide (default: window when present). */
  lifecycle?: Pick<Window, "addEventListener" | "removeEventListener"> | null;
}

/** A random id; getRandomValues also works in insecure contexts (plain HTTP). */
function randomId(): string {
  const b = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

/**
 * The per-tab hub. One instance per Apollo client; subscribe once per
 * local subscriber.
 */
export class SharedStreams {
  readonly tabId: string;
  private readonly channel: ChannelLike;
  private readonly entries = new Map<string, Entry>();
  private readonly electionMs: number;
  private readonly electionJitterMs: number;
  private readonly pingMs: number;
  private readonly pingTimeoutMs: number;
  private readonly random: () => number;
  private readonly lifecycle: SharedStreamsOptions["lifecycle"];
  private closed = false;

  constructor(opts: SharedStreamsOptions) {
    this.channel = opts.channel;
    this.tabId = opts.tabId ?? randomId();
    this.electionMs = opts.electionMs ?? 150;
    this.electionJitterMs = opts.electionJitterMs ?? 100;
    this.pingMs = opts.pingMs ?? 15_000;
    this.pingTimeoutMs = opts.pingTimeoutMs ?? 3_000;
    this.random = opts.random ?? Math.random;
    this.lifecycle =
      opts.lifecycle === undefined
        ? typeof window === "undefined"
          ? null
          : window
        : opts.lifecycle;
    this.channel.addEventListener("message", this.onMessage);
    this.lifecycle?.addEventListener("pagehide", this.onPageHide);
  }

  /** The role of this tab for key (tests and diagnostics). */
  roleOf(key: string): Role | undefined {
    return this.entries.get(key)?.role;
  }

  /**
   * Joins the shared stream key: listener receives its events, whichever
   * tab leads; open starts the real stream when this tab leads. Returns
   * the unsubscribe function.
   */
  subscribe<T>(key: string, open: StreamOpener<T>, listener: SharedListener<T>): () => void {
    const l = listener as SharedListener<unknown>;
    let e = this.entries.get(key);
    if (!e || e.role === "ended") {
      if (e) this.drop(e);
      e = {
        key,
        role: "candidate",
        listeners: new Set(),
        open,
        term: "",
        leader: "",
        leaderTerm: "",
        dispose: undefined,
        election: undefined,
        pingTimer: undefined,
        pingDeadline: undefined,
        answered: false,
      };
      this.entries.set(key, e);
      e.listeners.add(l);
      this.elect(e);
    } else {
      e.open = open;
      e.listeners.add(l);
    }
    const entry = e;
    return () => {
      entry.listeners.delete(l);
      if (entry.listeners.size === 0 && this.entries.get(key) === entry) this.drop(entry);
    };
  }

  /** Resigns every led stream and stops listening (client disposal). */
  close(): void {
    if (this.closed) return;
    this.closed = true;
    for (const e of [...this.entries.values()]) this.drop(e);
    this.channel.removeEventListener("message", this.onMessage);
    this.lifecycle?.removeEventListener("pagehide", this.onPageHide);
    this.channel.close();
  }

  private post(m: Message): void {
    if (this.closed) return;
    try {
      this.channel.postMessage(m);
    } catch {
      // A value that cannot be cloned is not relayed; followers resync on
      // the next event.
    }
  }

  private clearTimers(e: Entry): void {
    if (e.election !== undefined) clearTimeout(e.election);
    if (e.pingTimer !== undefined) clearTimeout(e.pingTimer);
    if (e.pingDeadline !== undefined) clearTimeout(e.pingDeadline);
    e.election = e.pingTimer = e.pingDeadline = undefined;
  }

  /** Leaves key: a leader closes its stream and resigns. */
  private drop(e: Entry): void {
    this.clearTimers(e);
    if (e.role === "leader") {
      this.post({ t: "resign", key: e.key, from: this.tabId, term: e.term });
      const d = e.dispose;
      e.dispose = undefined;
      d?.();
    }
    e.role = "ended";
    this.entries.delete(e.key);
  }

  private elect(e: Entry): void {
    this.clearTimers(e);
    e.role = "candidate";
    e.leader = e.leaderTerm = "";
    this.post({ t: "hello", key: e.key, from: this.tabId });
    const wait = this.electionMs + Math.floor(this.random() * this.electionJitterMs);
    e.election = setTimeout(() => {
      e.election = undefined;
      if (e.role === "candidate") this.lead(e);
    }, wait);
  }

  private lead(e: Entry): void {
    this.clearTimers(e);
    e.role = "leader";
    e.term = randomId();
    e.leader = this.tabId;
    e.leaderTerm = e.term;
    const term = e.term;
    this.post({ t: "lead", key: e.key, from: this.tabId, term });
    const current = () => e.role === "leader" && e.term === term;
    e.dispose = e.open({
      next: (value) => {
        if (!current()) return;
        this.post({ t: "event", key: e.key, from: this.tabId, term, value });
        for (const l of [...e.listeners]) l.next(value, true);
      },
      end: (end) => {
        if (!current()) return;
        this.post({ t: "end", key: e.key, from: this.tabId, term, end });
        e.dispose = undefined;
        this.clearTimers(e);
        e.role = "ended";
        this.entries.delete(e.key);
        for (const l of [...e.listeners]) l.end(end, true);
      },
    });
  }

  private follow(e: Entry, leader: string, term: string): void {
    this.clearTimers(e);
    e.role = "follower";
    e.leader = leader;
    e.leaderTerm = term;
    e.answered = true;
    this.schedulePing(e);
  }

  private schedulePing(e: Entry): void {
    e.pingTimer = setTimeout(() => {
      e.pingTimer = undefined;
      if (e.role !== "follower") return;
      e.answered = false;
      this.post({ t: "ping", key: e.key, from: this.tabId });
      e.pingDeadline = setTimeout(() => {
        e.pingDeadline = undefined;
        if (e.role !== "follower") return;
        if (e.answered) this.schedulePing(e);
        else this.elect(e);
      }, this.pingTimeoutMs);
    }, this.pingMs);
  }

  /** The leader steps down for another tab: closes its stream, follows. */
  private stepDown(e: Entry, leader: string, term: string): void {
    const d = e.dispose;
    e.dispose = undefined;
    d?.();
    this.follow(e, leader, term);
  }

  private readonly onPageHide = (): void => {
    for (const e of this.entries.values()) {
      if (e.role === "leader")
        this.post({ t: "resign", key: e.key, from: this.tabId, term: e.term });
    }
  };

  private readonly onMessage = (ev: MessageEvent): void => {
    const m: unknown = ev.data;
    if (!isMessage(m) || m.from === this.tabId) return;
    const e = this.entries.get(m.key);
    if (!e || e.role === "ended") return;
    switch (m.t) {
      case "hello":
      case "ping":
        if (e.role === "leader")
          this.post({ t: "lead", key: e.key, from: this.tabId, term: e.term });
        return;
      case "lead":
        if (e.role === "candidate") {
          this.follow(e, m.from, m.term);
        } else if (e.role === "leader") {
          // Two leaders: the lower tab id wins.
          if (m.from < this.tabId) this.stepDown(e, m.from, m.term);
          else this.post({ t: "lead", key: e.key, from: this.tabId, term: e.term });
        } else if (m.from === e.leader) {
          e.leaderTerm = m.term;
          e.answered = true;
        } else if (m.from < e.leader) {
          this.follow(e, m.from, m.term);
        }
        return;
      case "resign":
        if (e.role === "follower" && m.from === e.leader) this.elect(e);
        return;
      case "event":
        if (e.role === "follower" && m.from === e.leader) {
          e.answered = true;
          for (const l of [...e.listeners]) l.next(m.value, false);
        }
        return;
      case "end":
        if (e.role === "follower" && m.from === e.leader && m.term === e.leaderTerm) {
          this.clearTimers(e);
          e.role = "ended";
          this.entries.delete(e.key);
          for (const l of [...e.listeners]) l.end(m.end, false);
        }
        return;
    }
  };
}

/** The channel name (versioned with the message format). */
export const SHARED_STREAMS_CHANNEL = "bekup:streams:v1";

/**
 * The hub of this tab, or null when BroadcastChannel is unavailable (each
 * tab then opens its own streams).
 */
export function createSharedStreams(): SharedStreams | null {
  if (typeof BroadcastChannel === "undefined") return null;
  try {
    return new SharedStreams({ channel: new BroadcastChannel(SHARED_STREAMS_CHANNEL) });
  } catch {
    return null;
  }
}
