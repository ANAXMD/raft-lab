import { describe, it, expect, vi, beforeEach } from "vitest";
import { isSessionText, useClusterStore } from "./store";
import type { RaftSimAPI } from "./types";

function mockSim(over: Partial<RaftSimAPI> = {}): RaftSimAPI {
  return {
    start: vi.fn(),
    sendCommand: vi.fn(() => ""),
    exportSession: vi.fn(() => ""),
    replay: vi.fn(() => ""),
    setSpeed: vi.fn(),
    ...over,
  };
}

const nodeEv = (t: number, from: number, extra: object = {}) =>
  JSON.stringify({ t, ev: "node", from, role: 3, term: 1, up: true, ...extra });

// Events are applied one microtask after they arrive (bursts are batched).
const tick = () => Promise.resolve();

beforeEach(() => {
  (globalThis as any).window = globalThis;
  delete (globalThis as any).RaftSim;
});

describe("live session", () => {
  it("starts the simulator and applies events it emits", async () => {
    let emit: (evJSON: string) => void = () => {};
    const sim = mockSim({ start: vi.fn((_seed, onEvent) => void (emit = onEvent)) });
    window.RaftSim = sim;

    expect(useClusterStore.getState().startLiveSession(42)).toBe(true);
    expect(sim.start).toHaveBeenCalledWith(42, expect.any(Function));
    expect(useClusterStore.getState().isLive).toBe(true);

    emit(nodeEv(10, 1));
    await tick();

    const s = useClusterStore.getState();
    expect(s.cluster.nodes[1].role).toBe(3);
    expect(s.events.length).toBe(1);
    expect(s.cursor).toBe(1);
  });

  it("passes options through and sizes the cluster from them", () => {
    const sim = mockSim();
    window.RaftSim = sim;
    useClusterStore.getState().startLiveSession(7, { n: 3, preVote: true });
    expect(sim.start).toHaveBeenCalledWith(7, expect.any(Function), JSON.stringify({ n: 3, preVote: true }));
    expect(useClusterStore.getState().header?.nodes).toEqual([1, 2, 3]);
    expect(Object.keys(useClusterStore.getState().cluster.nodes)).toHaveLength(3);
  });

  it("a new session runs even if the previous one was left paused", () => {
    const sim = mockSim();
    window.RaftSim = sim;
    useClusterStore.getState().setLiveSpeed(0);
    useClusterStore.getState().startLiveSession(1);
    expect(sim.setSpeed).toHaveBeenLastCalledWith(0.5);
    expect(useClusterStore.getState().liveSpeed).toBe(0.5);
  });

  it("fails cleanly when sim.wasm has not loaded", () => {
    expect(useClusterStore.getState().startLiveSession(1)).toBe(false);
    expect(useClusterStore.getState().commandError).toMatch(/not loaded/);
  });

  it("applies a burst of events in one store update", async () => {
    let emit: (evJSON: string) => void = () => {};
    window.RaftSim = mockSim({ start: vi.fn((_s, cb) => void (emit = cb)) });
    useClusterStore.getState().startLiveSession(1);

    let updates = 0;
    const unsub = useClusterStore.subscribe(() => updates++);
    for (let i = 1; i <= 50; i++) emit(JSON.stringify({ t: i, ev: "send", id: i, from: 1, to: 2, type: "App" }));
    await tick();
    unsub();

    expect(updates).toBe(1);
    expect(useClusterStore.getState().events).toHaveLength(50);
    expect(Object.keys(useClusterStore.getState().cluster.inflight)).toHaveLength(50);
  });

  it("ignores callbacks from a session that was replaced", async () => {
    const emits: Array<(j: string) => void> = [];
    window.RaftSim = mockSim({ start: vi.fn((_s, cb) => void emits.push(cb)) });
    useClusterStore.getState().startLiveSession(1);
    useClusterStore.getState().startLiveSession(2);

    emits[0](nodeEv(5, 1)); // stale
    emits[1](nodeEv(6, 2));
    await tick();

    const s = useClusterStore.getState();
    expect(s.events).toHaveLength(1);
    expect(s.events[0].t).toBe(6);
  });

  it("keeps the viewer's position when they scrub back, and follows again on goLive", async () => {
    let emit: (evJSON: string) => void = () => {};
    window.RaftSim = mockSim({ start: vi.fn((_s, cb) => void (emit = cb)) });
    useClusterStore.getState().startLiveSession(1);
    for (let t = 1; t <= 3; t++) emit(nodeEv(t, 1, { term: t }));
    await tick();
    expect(useClusterStore.getState().cursor).toBe(3);

    useClusterStore.getState().seekTo(1);
    expect(useClusterStore.getState().cluster.nodes[1].term).toBe(1);

    emit(nodeEv(4, 1, { term: 4 }));
    await tick();
    let s = useClusterStore.getState();
    expect(s.events).toHaveLength(4); // recorded
    expect(s.cursor).toBe(1); // but the view did not move
    expect(s.cluster.nodes[1].term).toBe(1);

    useClusterStore.getState().goLive();
    s = useClusterStore.getState();
    expect(s.cursor).toBe(4);
    expect(s.cluster.nodes[1].term).toBe(4);

    emit(nodeEv(5, 1, { term: 5 })); // following again
    await tick();
    expect(useClusterStore.getState().cursor).toBe(5);
    expect(useClusterStore.getState().cluster.nodes[1].term).toBe(5);
  });

  it("tracks network settings from the simulator and from net events", async () => {
    let emit: (evJSON: string) => void = () => {};
    const cfg = { n: 5, dropProb: 0.1, dupProb: 0, minDelayMs: 2, maxDelayMs: 9 };
    window.RaftSim = mockSim({
      start: vi.fn((_s, cb) => void (emit = cb)),
      exportSession: vi.fn(() => JSON.stringify({ v: 1, seed: 1, config: cfg, commands: [], until: 0 })),
    });
    useClusterStore.getState().startLiveSession(1);
    expect(useClusterStore.getState().liveNet).toEqual({ drop: 0.1, dup: 0, minDelay: 2, maxDelay: 9 });

    emit(JSON.stringify({ t: 50, ev: "net", net: { drop: 0.3, dup: 0.2, minDelay: 5, maxDelay: 40 } }));
    await tick();
    expect(useClusterStore.getState().liveNet).toEqual({ drop: 0.3, dup: 0.2, minDelay: 5, maxDelay: 40 });
    expect(useClusterStore.getState().cluster.net?.maxDelay).toBe(40);
  });
});

describe("commands", () => {
  it("sends commands as JSON and reports success", () => {
    const sim = mockSim();
    window.RaftSim = sim;
    useClusterStore.getState().startLiveSession(1);

    expect(useClusterStore.getState().sendCommand({ kind: "crash", node: 2 })).toBe("");
    expect(sim.sendCommand).toHaveBeenCalledWith(JSON.stringify({ kind: "crash", node: 2 }));

    useClusterStore.getState().sendCommand({ kind: "propose", key: "a", value: "1" });
    expect(sim.sendCommand).toHaveBeenCalledWith(JSON.stringify({ kind: "propose", key: "a", value: "1" }));

    useClusterStore.getState().issueCommand("heal");
    expect(sim.sendCommand).toHaveBeenCalledWith(JSON.stringify({ kind: "heal" }));
    expect(useClusterStore.getState().commandError).toBeNull();
  });

  it("surfaces the simulator's error string", () => {
    window.RaftSim = mockSim({ sendCommand: vi.fn(() => "unknown node 9") });
    useClusterStore.getState().startLiveSession(1);

    expect(useClusterStore.getState().sendCommand({ kind: "crash", node: 9 })).toBe("unknown node 9");
    expect(useClusterStore.getState().commandError).toBe("unknown node 9");
    useClusterStore.getState().clearCommandError();
    expect(useClusterStore.getState().commandError).toBeNull();
  });

  it("refuses commands when no live session is running", () => {
    const sim = mockSim();
    window.RaftSim = sim;
    useClusterStore.getState().loadTrace(JSON.stringify({ v: 1, seed: 1, nodes: [1, 2, 3] }));
    expect(useClusterStore.getState().sendCommand({ kind: "heal" })).toMatch(/live session/);
    expect(sim.sendCommand).not.toHaveBeenCalled();
  });

  it("clamps and forwards the live speed", () => {
    const sim = mockSim();
    window.RaftSim = sim;
    useClusterStore.getState().setLiveSpeed(99);
    expect(sim.setSpeed).toHaveBeenLastCalledWith(5);
    useClusterStore.getState().setLiveSpeed(-3);
    expect(sim.setSpeed).toHaveBeenLastCalledWith(0);
  });
});

describe("sessions", () => {
  const session = {
    v: 1,
    seed: 9,
    config: { n: 3, dropProb: 0, dupProb: 0, minDelayMs: 1, maxDelayMs: 20 },
    commands: [{ t: 100, kind: "heal" }],
    until: 500,
  };

  it("recognises session files and NDJSON traces", () => {
    expect(isSessionText(JSON.stringify(session))).toBe(true);
    expect(isSessionText(JSON.stringify(session, null, 2))).toBe(true);
    expect(isSessionText('{"v":1,"seed":1,"nodes":[1,2,3]}\n{"t":1,"ev":"node","from":1}')).toBe(false);
    expect(isSessionText('{"v":1,"seed":1,"nodes":[1,2,3]}')).toBe(false);
    expect(isSessionText("not json")).toBe(false);
  });

  it("replays a session through the simulator and ends up live", async () => {
    let emit: (evJSON: string) => void = () => {};
    const sim = mockSim({
      replay: vi.fn((_json, cb) => {
        emit = cb;
        // the real bridge emits the whole recorded run synchronously
        cb(nodeEv(1, 1));
        cb(nodeEv(2, 2));
        return "";
      }),
    });
    window.RaftSim = sim;

    expect(useClusterStore.getState().replaySession(JSON.stringify(session))).toBe("");
    const s = useClusterStore.getState();
    expect(sim.replay).toHaveBeenCalledWith(JSON.stringify(session), expect.any(Function));
    expect(s.isLive).toBe(true);
    expect(s.header).toEqual({ v: 1, seed: 9, nodes: [1, 2, 3] });
    expect(s.events).toHaveLength(2); // applied before replaySession returned
    expect(s.cursor).toBe(2);

    emit(nodeEv(3, 3)); // and it carries on live afterwards
    await tick();
    expect(useClusterStore.getState().events).toHaveLength(3);
  });

  it("reports a replay error and leaves live mode", () => {
    window.RaftSim = mockSim({ replay: vi.fn(() => "replay: command at t=5 is in the past") });
    const err = useClusterStore.getState().replaySession(JSON.stringify(session));
    expect(err).toMatch(/in the past/);
    expect(useClusterStore.getState().isLive).toBe(false);
    expect(useClusterStore.getState().commandError).toBe(err);
  });

  it("rejects malformed session text without touching the simulator", () => {
    const sim = mockSim();
    window.RaftSim = sim;
    expect(useClusterStore.getState().replaySession("{nope")).toMatch(/Not a valid session/);
    expect(useClusterStore.getState().replaySession(JSON.stringify({ ...session, v: 2 }))).toMatch(/version/);
    expect(sim.replay).not.toHaveBeenCalled();
  });

  it("exports only while live", () => {
    const sim = mockSim({ exportSession: vi.fn(() => '{"v":1}') });
    window.RaftSim = sim;
    expect(useClusterStore.getState().exportSession()).toBeNull();
    useClusterStore.getState().startLiveSession(1);
    expect(useClusterStore.getState().exportSession()).toBe('{"v":1}');
  });

  it("loading a trace retires the live simulator", async () => {
    let emit: (evJSON: string) => void = () => {};
    const sim = mockSim({ start: vi.fn((_s, cb) => void (emit = cb)) });
    window.RaftSim = sim;
    useClusterStore.getState().startLiveSession(1);
    useClusterStore.getState().loadTrace(JSON.stringify({ v: 1, seed: 5, nodes: [1, 2, 3] }));

    expect(useClusterStore.getState().isLive).toBe(false);
    expect(sim.setSpeed).toHaveBeenLastCalledWith(0);
    emit(nodeEv(1, 1)); // late event from the old simulator
    await tick();
    expect(useClusterStore.getState().events).toHaveLength(0);
  });
});
