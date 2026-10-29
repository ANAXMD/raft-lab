import { describe, it, expect, vi, beforeEach } from "vitest";
import { useClusterStore } from "./store";

beforeEach(() => {
  (globalThis as any).window = globalThis;
});

describe("store live session actions", () => {
  it("calls window.RaftSim.start on startLiveSession and updates state", () => {
    let callback: (evJSON: string) => void = () => {};
    window.RaftSim = {
      start: vi.fn((_seed, onEvent) => {
        callback = onEvent;
      }),
      sendCommand: vi.fn(),
    };

    const store = useClusterStore.getState();
    store.startLiveSession(42);

    expect(window.RaftSim.start).toHaveBeenCalledWith(42, expect.any(Function));
    expect(useClusterStore.getState().isLive).toBe(true);

    // Simulate an event arriving from WASM
    callback(
      JSON.stringify({
        t: 10,
        ev: "node",
        from: 1,
        role: 3,
        term: 1,
        commit: 0,
        last: 0,
        up: true,
      })
    );

    const updated = useClusterStore.getState();
    expect(updated.cluster.nodes[1].role).toBe(3);
    expect(updated.events.length).toBe(1);
    expect(updated.cursor).toBe(1);
  });

  it("calls window.RaftSim.sendCommand on issueCommand", () => {
    const sendCommandMock = vi.fn();
    window.RaftSim = {
      start: vi.fn(),
      sendCommand: sendCommandMock,
    };

    const store = useClusterStore.getState();
    store.issueCommand("kill", 2);

    expect(sendCommandMock).toHaveBeenCalledWith(
      JSON.stringify({ kind: "kill", node: 2 })
    );

    store.issueCommand("heal");
    expect(sendCommandMock).toHaveBeenCalledWith(
      JSON.stringify({ kind: "heal" })
    );
  });
});
