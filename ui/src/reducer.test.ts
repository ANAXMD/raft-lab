import { describe, it, expect } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import type { Event, TraceHeader } from "./types";
import { Role } from "./types";
import { createInitialState, isCut, reduce } from "./reducer";

const __filename = fileURLToPath(import.meta.url);
const __dirname = path.dirname(__filename);

describe("reducer", () => {
  it("replays trace_seed182.golden and matches final node snapshots", () => {
    const goldenPath = path.resolve(__dirname, "../../sim/testdata/trace_seed182.golden");
    expect(fs.existsSync(goldenPath)).toBe(true);

    const content = fs.readFileSync(goldenPath, "utf-8");
    const lines = content.trim().split("\n");
    expect(lines.length).toBeGreaterThan(100);

    // Parse header
    const header: TraceHeader = JSON.parse(lines[0]);
    expect(header.v).toBe(1);
    expect(header.seed).toBe(182);
    expect(header.nodes).toEqual([1, 2, 3, 4, 5]);

    // Parse events and collect expected final node snapshots
    const events: Event[] = [];
    const expectedFinalSnapshots: Record<number, Event> = {};

    for (let i = 1; i < lines.length; i++) {
      const line = lines[i].trim();
      if (!line) continue;
      const ev: Event = JSON.parse(line);
      events.push(ev);
      if (ev.ev === "node" && ev.from != null) {
        expectedFinalSnapshots[ev.from] = ev;
      }
    }

    // Run entire stream through the reducer
    let state = createInitialState(header.nodes);
    for (const ev of events) {
      state = reduce(state, ev);
    }

    // Verify all 5 nodes have a final snapshot recorded
    for (const id of header.nodes) {
      const expected = expectedFinalSnapshots[id];
      expect(expected).toBeDefined();

      const node = state.nodes[id];
      expect(node).toBeDefined();

      // Check fields matching the final snapshot
      const expectedRole = expected.role ?? Role.Follower;
      const expectedTerm = expected.term ?? 0;
      const expectedCommit = expected.commit ?? 0;
      const expectedLast = expected.last ?? 0;
      const expectedUp = expected.up ?? false;

      expect(node.role).toBe(expectedRole);
      expect(node.term).toBe(expectedTerm);
      expect(node.commit).toBe(expectedCommit);
      expect(node.last).toBe(expectedLast);
      expect(node.up).toBe(expectedUp);
      expect(node.log.length).toBe(expectedLast);
      expect(node.applied).toBe(expectedCommit);
    }

    // Verify virtual time matches final event timestamp
    const lastEvent = events[events.length - 1];
    expect(state.time).toBe(lastEvent.t);
  });
});

describe("network faults", () => {
  const start = () => createInitialState([1, 2, 3, 4, 5]);

  it("replaces the cut set on every network fault; absent cuts means none", () => {
    let st = reduce(start(), {
      t: 10, ev: "fault", why: "partition",
      cuts: [[1, 3], [1, 4], [2, 3], [2, 4]],
    });
    expect(st.cuts).toHaveLength(4);
    expect(isCut(st.cuts, 1, 3)).toBe(true);
    expect(isCut(st.cuts, 3, 1)).toBe(true); // undirected
    expect(isCut(st.cuts, 1, 2)).toBe(false);

    st = reduce(st, { t: 20, ev: "fault", why: "cut", cuts: [[1, 3], [1, 4], [2, 3], [2, 4], [4, 5]] });
    expect(st.cuts).toHaveLength(5);

    st = reduce(st, { t: 30, ev: "fault", why: "heal" }); // Go omits an empty list
    expect(st.cuts).toEqual([]);
  });

  it("isolate cuts one node from the rest", () => {
    const st = reduce(start(), {
      t: 5, ev: "fault", why: "isolate", from: 2,
      cuts: [[1, 2], [2, 3], [2, 4], [2, 5]],
    });
    for (const o of [1, 3, 4, 5]) expect(isCut(st.cuts, 2, o)).toBe(true);
    expect(isCut(st.cuts, 1, 3)).toBe(false);
  });

  it("crash and restart leave the cuts alone", () => {
    let st = reduce(start(), { t: 1, ev: "fault", why: "partition", cuts: [[1, 2]] });
    st = reduce(st, { t: 2, ev: "node", from: 3, up: true });
    st = reduce(st, { t: 3, ev: "fault", why: "crash", from: 3 });
    expect(st.nodes[3].up).toBe(false);
    expect(st.cuts).toEqual([[1, 2]]);
    st = reduce(st, { t: 4, ev: "fault", why: "restart", from: 3 });
    expect(st.nodes[3].up).toBe(true);
    expect(st.cuts).toEqual([[1, 2]]);
  });

  it("does not mutate the previous state", () => {
    const a = reduce(start(), { t: 1, ev: "fault", why: "cut", cuts: [[1, 2]] });
    const b = reduce(a, { t: 2, ev: "fault", why: "heal" });
    expect(a.cuts).toEqual([[1, 2]]);
    expect(b.cuts).toEqual([]);
  });
});

describe("propose and net events", () => {
  it("records the latest proposal with its outcome", () => {
    let st = createInitialState([1, 2, 3]);
    st = reduce(st, { t: 100, ev: "propose", from: 2, why: "accepted", msg: "SET a=1" });
    expect(st.lastPropose).toEqual({ t: 100, node: 2, outcome: "accepted", data: "SET a=1" });
    st = reduce(st, { t: 200, ev: "propose", why: "no-leader", msg: "SET b=2" });
    expect(st.lastPropose).toEqual({ t: 200, node: 0, outcome: "no-leader", data: "SET b=2" });
  });

  it("ignores a propose event with an unknown outcome", () => {
    const st = reduce(createInitialState([1]), { t: 1, ev: "propose", why: "???", msg: "x" });
    expect(st.lastPropose).toBeNull();
  });

  it("tracks the network settings", () => {
    const st = reduce(createInitialState([1]), {
      t: 7, ev: "net", net: { drop: 0.2, dup: 0, minDelay: 3, maxDelay: 30 },
    });
    expect(st.net).toEqual({ drop: 0.2, dup: 0, minDelay: 3, maxDelay: 30 });
  });

  it("ignores event kinds it does not know", () => {
    const st = createInitialState([1, 2]);
    expect(reduce(st, { t: 9, ev: "from-the-future" as never }).time).toBe(9);
  });
});

describe("structural sharing", () => {
  it("shares untouched nodes and copies only the node an event changes", () => {
    const a = reduce(createInitialState([1, 2, 3]), { t: 1, ev: "node", from: 1, role: 3, term: 1, up: true });
    const b = reduce(a, { t: 2, ev: "send", id: 1, from: 1, to: 2, type: "App" });
    expect(b.nodes[1]).toBe(a.nodes[1]);
    expect(b.nodes[2]).toBe(a.nodes[2]);

    const c = reduce(b, { t: 3, ev: "log", from: 2, idx: 1, entries: [{ Term: 1, Index: 1, Data: "SET a=1" }] });
    expect(c.nodes[1]).toBe(b.nodes[1]);
    expect(c.nodes[2]).not.toBe(b.nodes[2]);
    expect(b.nodes[2].log).toHaveLength(0); // the old state is untouched
    expect(c.nodes[2].log[0].Data).toBe("SET a=1");
  });

  it("truncates and replaces log suffixes without altering earlier states", () => {
    const e = (i: number, d: string) => ({ Term: 1, Index: i, Data: d });
    let st = createInitialState([1]);
    st = reduce(st, { t: 1, ev: "log", from: 1, idx: 1, entries: [e(1, "a"), e(2, "b"), e(3, "c")] });
    const before = st;
    st = reduce(st, { t: 2, ev: "log", from: 1, idx: 2, entries: [e(2, "B")] });
    expect(st.nodes[1].log.map((x) => x.Data)).toEqual(["a", "B"]);
    expect(before.nodes[1].log.map((x) => x.Data)).toEqual(["a", "b", "c"]);
  });
});

