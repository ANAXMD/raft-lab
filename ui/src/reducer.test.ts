import { describe, it, expect } from "vitest";
import * as fs from "node:fs";
import * as path from "node:path";
import { fileURLToPath } from "node:url";
import type { Event, TraceHeader } from "./types";
import { Role } from "./types";
import { createInitialState, reduce } from "./reducer";

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
