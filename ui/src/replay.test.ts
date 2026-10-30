import { describe, it, expect } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import type { ClusterState, Event } from "./types";
import { createInitialState, reduce } from "./reducer";
import { CHECKPOINT_EVERY, nodeIdsFor, stateAt } from "./replay";

const dir = path.dirname(fileURLToPath(import.meta.url));
const lines = fs
  .readFileSync(path.resolve(dir, "../../sim/testdata/trace_seed182.golden"), "utf-8")
  .trim()
  .split("\n");
const header = JSON.parse(lines[0]);
const events: Event[] = lines.slice(1).map((l) => JSON.parse(l));

function naive(n: number): ClusterState {
  let s = createInitialState(header.nodes);
  for (let i = 0; i < n; i++) s = reduce(s, events[i]);
  return s;
}

describe("stateAt", () => {
  it("matches a plain fold at every kind of index", () => {
    expect(events.length).toBeGreaterThan(3 * CHECKPOINT_EVERY);
    const cps: ClusterState[] = [];
    const picks = [0, 1, CHECKPOINT_EVERY - 1, CHECKPOINT_EVERY, CHECKPOINT_EVERY + 1, 2 * CHECKPOINT_EVERY + 7, events.length];
    for (const i of picks) expect(stateAt(events, header.nodes, i, cps)).toEqual(naive(i));
    // out of range is clamped
    expect(stateAt(events, header.nodes, events.length + 99, cps)).toEqual(naive(events.length));
    expect(stateAt(events, header.nodes, -5, cps)).toEqual(naive(0));
  });

  it("caches checkpoints and keeps them valid as the trace grows", () => {
    const cps: ClusterState[] = [];
    const half = events.slice(0, 2 * CHECKPOINT_EVERY + 10);
    stateAt(half, header.nodes, half.length, cps);
    expect(cps.length).toBe(3);
    const saved = cps[2];

    // the same cache is used with a longer (appended) event list
    expect(stateAt(events, header.nodes, events.length, cps)).toEqual(naive(events.length));
    expect(cps[2]).toBe(saved);
    expect(cps.length).toBe(Math.floor(events.length / CHECKPOINT_EVERY) + 1);
  });

  it("does not let later states alter earlier ones", () => {
    const cps: ClusterState[] = [];
    const before = stateAt(events, header.nodes, 300, cps);
    const snapshot = structuredClone(before);
    stateAt(events, header.nodes, events.length, cps);
    expect(before).toEqual(snapshot);
  });
});

describe("nodeIdsFor", () => {
  it("numbers nodes from 1", () => {
    expect(nodeIdsFor(3)).toEqual([1, 2, 3]);
    expect(nodeIdsFor(0)).toEqual([1]);
  });
});
