import { create } from "zustand";
import type {
  ClusterState,
  Command,
  Event,
  LiveOptions,
  NetConfig,
  NodeID,
  RaftSimAPI,
  SessionFile,
  TraceHeader,
} from "./types";
import { createInitialState, reduce } from "./reducer";
import { nodeIdsFor, stateAt } from "./replay";

/** What the simulator starts with (sim.Config.defaults) when nothing else is known. */
export const DEFAULT_NET: NetConfig = { drop: 0, dup: 0, minDelay: 1, maxDelay: 20 };
export const MAX_LIVE_SPEED = 5; // virtual seconds per wall second
export const DEFAULT_LIVE_SPEED = 0.5;

export interface TraceStore {
  // Core cluster state (required by spec)
  cluster: ClusterState;
  applyEvent: (ev: Event) => void;
  resetCluster: (nodeIds?: NodeID[]) => void;

  // Trace playback state
  header: TraceHeader | null;
  events: Event[];
  cursor: number;
  isPlaying: boolean;
  isLive: boolean;
  playbackSpeed: number; // ms per tick in auto-play
  selectedNodeId: NodeID | null;

  // Live session state
  liveSpeed: number; // virtual seconds per wall second (0 = paused)
  liveNet: NetConfig; // network settings currently in force in the live simulator
  commandError: string | null; // last error returned by the simulator, if any
  checkpoints: ClusterState[]; // seek cache, see replay.ts

  // Actions
  startLiveSession: (seed: number, options?: LiveOptions) => boolean;
  /** Send a command to the live simulator. Returns "" on success, else the error. */
  sendCommand: (cmd: Command) => string;
  issueCommand: (kind: string, node?: number) => string;
  setLiveSpeed: (speed: number) => void;
  /** The recording of the live run so far (JSON), or null if there is none. */
  exportSession: () => string | null;
  /** Re-run a recording from t=0 and carry on live. Returns "" on success, else the error. */
  replaySession: (sessionJSON: string) => string;
  loadTrace: (ndjsonText: string) => void;
  stepForward: () => void;
  stepBackward: () => void;
  seekTo: (index: number) => void;
  /** Jump to the newest event of a live session and follow it again. */
  goLive: () => void;
  play: () => void;
  pause: () => void;
  setPlaybackSpeed: (speed: number) => void;
  setSelectedNodeId: (id: NodeID | null) => void;
  clearCommandError: () => void;
}

export function getSim(): RaftSimAPI | undefined {
  const g = typeof window !== "undefined" ? window : (globalThis as { RaftSim?: RaftSimAPI });
  return g.RaftSim;
}

/** True if text is a session recording (one JSON document) rather than an NDJSON trace. */
export function isSessionText(text: string): boolean {
  try {
    const v = JSON.parse(text);
    return v !== null && typeof v === "object" && Array.isArray(v.commands) && typeof v.config === "object";
  } catch {
    return false; // multi-line NDJSON is not a single JSON document
  }
}

function parseSession(text: string): SessionFile {
  const f = JSON.parse(text) as SessionFile;
  if (f.v !== 1) throw new Error(`unsupported session version ${f.v}`);
  if (!f.config || typeof f.config.n !== "number" || f.config.n < 1) throw new Error("session has no valid config.n");
  return f;
}

/** The network settings a simulator was configured with, read from its session export. */
function readBaseNet(sim: RaftSimAPI): NetConfig {
  try {
    const raw = typeof sim.exportSession === "function" ? sim.exportSession() : "";
    if (!raw) return DEFAULT_NET;
    const c = (JSON.parse(raw) as SessionFile).config;
    return { drop: c.dropProb, dup: c.dupProb, minDelay: c.minDelayMs, maxDelay: c.maxDelayMs };
  } catch {
    return DEFAULT_NET;
  }
}

// ---- live event intake ----------------------------------------------------
// The simulator calls back once per event, often hundreds in a burst. Events are
// buffered and applied in one store update per burst (a microtask later), which
// keeps the cost linear instead of copying the whole event list per event.
// `generation` invalidates callbacks from a session that has been replaced.
let generation = 0;
let pending: Event[] = [];
let flushQueued = false;

export const useClusterStore = create<TraceStore>((set, get) => {
  const flush = () => {
    flushQueued = false;
    if (pending.length === 0) return;
    const batch = pending;
    pending = [];
    set((state) => {
      const events = state.events.concat(batch);
      // Follow the live edge only if the viewer is sitting on it. A viewer who
      // scrubbed back keeps their position; new events just accumulate.
      const following = state.cursor === state.events.length;
      let cluster = state.cluster;
      if (following) for (const ev of batch) cluster = reduce(cluster, ev);
      let liveNet = state.liveNet;
      for (const ev of batch) if (ev.ev === "net" && ev.net) liveNet = ev.net;
      return { events, cluster, cursor: following ? events.length : state.cursor, liveNet };
    });
  };

  const makeOnEvent = (gen: number) => (evJSON: string) => {
    if (gen !== generation) return;
    try {
      pending.push(JSON.parse(evJSON) as Event);
    } catch (err) {
      console.error("Failed to parse live event:", err);
      return;
    }
    if (!flushQueued) {
      flushQueued = true;
      queueMicrotask(flush);
    }
  };

  /** Reset everything that belongs to the current trace and enter live mode. */
  const enterLive = (header: TraceHeader) => {
    generation++;
    pending = [];
    flushQueued = false;
    set({
      isLive: true,
      isPlaying: false,
      events: [],
      cursor: 0,
      header,
      cluster: createInitialState(header.nodes),
      checkpoints: [],
      selectedNodeId: null,
      commandError: null,
    });
    return generation;
  };

  const leaveLive = () => {
    if (!get().isLive) return;
    generation++; // ignore any late callbacks
    pending = [];
    getSim()?.setSpeed?.(0); // stop the simulator burning CPU in the background
  };

  return {
    cluster: createInitialState([1, 2, 3, 4, 5]),
    header: null,
    events: [],
    cursor: 0,
    isPlaying: false,
    isLive: false,
    playbackSpeed: 50,
    selectedNodeId: null,
    liveSpeed: DEFAULT_LIVE_SPEED,
    liveNet: DEFAULT_NET,
    commandError: null,
    checkpoints: [],

    applyEvent: (ev: Event) => {
      set((state) => ({ cluster: reduce(state.cluster, ev) }));
    },

    resetCluster: (nodeIds = [1, 2, 3, 4, 5]) => {
      set({ cluster: createInitialState(nodeIds), cursor: 0, isPlaying: false, checkpoints: [] });
    },

    startLiveSession: (seed: number, options?: LiveOptions) => {
      const sim = getSim();
      if (!sim) {
        set({ commandError: "The simulator (sim.wasm) has not loaded yet." });
        return false;
      }
      const nodes = nodeIdsFor(options?.n ?? 5);
      const gen = enterLive({ v: 1, seed, nodes });
      // A brand-new session always runs; if the last one was left paused, un-pause.
      // (replaySession, by contrast, honours a paused speed so you can inspect the end state.)
      if (get().liveSpeed === 0) set({ liveSpeed: DEFAULT_LIVE_SPEED });
      sim.setSpeed?.(get().liveSpeed);
      const onEvent = makeOnEvent(gen);
      if (options) sim.start(seed, onEvent, JSON.stringify(options));
      else sim.start(seed, onEvent);
      set({ liveNet: readBaseNet(sim) });
      flush();
      return true;
    },

    sendCommand: (cmd: Command) => {
      const sim = getSim();
      let err: string;
      if (!get().isLive) err = "Start a live session first.";
      else if (!sim) err = "The simulator (sim.wasm) has not loaded yet.";
      else err = sim.sendCommand(JSON.stringify(cmd)) ?? "";
      set({ commandError: err || null });
      return err;
    },

    issueCommand: (kind: string, node?: number) =>
      get().sendCommand({ kind, node } as unknown as Command),

    setLiveSpeed: (speed: number) => {
      const v = Math.max(0, Math.min(MAX_LIVE_SPEED, speed));
      getSim()?.setSpeed?.(v);
      set({ liveSpeed: v });
    },

    exportSession: () => {
      const sim = getSim();
      if (!get().isLive || !sim) return null;
      return sim.exportSession() || null;
    },

    replaySession: (sessionJSON: string) => {
      const sim = getSim();
      if (!sim) {
        const err = "The simulator (sim.wasm) has not loaded yet.";
        set({ commandError: err });
        return err;
      }
      let f: SessionFile;
      try {
        f = parseSession(sessionJSON);
      } catch (e) {
        const err = `Not a valid session file: ${e instanceof Error ? e.message : String(e)}`;
        set({ commandError: err });
        return err;
      }
      leaveLive(); // retire the current session first
      const gen = enterLive({ v: 1, seed: f.seed, nodes: nodeIdsFor(f.config.n) });
      sim.setSpeed?.(get().liveSpeed);
      // replay() emits the whole run from t=0 synchronously, then carries on live.
      const err = sim.replay(sessionJSON, makeOnEvent(gen));
      if (err) {
        generation++;
        pending = [];
        set({ isLive: false, commandError: err });
        return err;
      }
      set({ liveNet: readBaseNet(sim) }); // any later net commands arrive as events
      flush();
      return "";
    },

    loadTrace: (ndjsonText: string) => {
      leaveLive();
      const lines = ndjsonText.trim().split("\n");
      if (lines.length === 0) return;

      let header: TraceHeader | null = null;
      const events: Event[] = [];

      for (let i = 0; i < lines.length; i++) {
        const line = lines[i].trim();
        if (!line) continue;
        try {
          const parsed = JSON.parse(line);
          if (i === 0 && parsed.v !== undefined && parsed.nodes) {
            header = parsed as TraceHeader;
          } else if (parsed.ev) {
            events.push(parsed as Event);
          }
        } catch {
          // Skip malformed lines
        }
      }

      const nodeIds = header?.nodes ?? [1, 2, 3, 4, 5];
      set({
        header,
        events,
        cursor: 0,
        isPlaying: false,
        isLive: false,
        cluster: createInitialState(nodeIds),
        checkpoints: [],
        selectedNodeId: null,
        commandError: null,
      });
    },

    stepForward: () => {
      const { cursor, events, cluster } = get();
      if (cursor < events.length) {
        set({ cluster: reduce(cluster, events[cursor]), cursor: cursor + 1 });
      } else {
        set({ isPlaying: false });
      }
    },

    stepBackward: () => {
      const { cursor } = get();
      if (cursor <= 0) return;
      get().seekTo(cursor - 1);
    },

    seekTo: (target: number) => {
      const { events, header, checkpoints } = get();
      const clamped = Math.max(0, Math.min(events.length, target));
      const nodeIds = header?.nodes ?? [1, 2, 3, 4, 5];
      set({
        cluster: stateAt(events, nodeIds, clamped, checkpoints),
        cursor: clamped,
        isPlaying: false,
      });
    },

    goLive: () => get().seekTo(get().events.length),

    play: () => set({ isPlaying: true }),
    pause: () => set({ isPlaying: false }),
    setPlaybackSpeed: (speed: number) => set({ playbackSpeed: speed }),
    setSelectedNodeId: (id: NodeID | null) => set({ selectedNodeId: id }),
    clearCommandError: () => set({ commandError: null }),
  };
});
