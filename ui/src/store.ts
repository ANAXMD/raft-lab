import { create } from "zustand";
import type { ClusterState, Event, NodeID, TraceHeader } from "./types";
import { createInitialState, reduce } from "./reducer";

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

  // Actions
  startLiveSession: (seed: number) => void;
  issueCommand: (kind: string, node?: number) => void;
  loadTrace: (ndjsonText: string) => void;
  stepForward: () => void;
  stepBackward: () => void;
  seekTo: (index: number) => void;
  play: () => void;
  pause: () => void;
  setPlaybackSpeed: (speed: number) => void;
  setSelectedNodeId: (id: NodeID | null) => void;
}

export const useClusterStore = create<TraceStore>((set, get) => ({
  cluster: createInitialState([1, 2, 3, 4, 5]),
  header: null,
  events: [],
  cursor: 0,
  isPlaying: false,
  isLive: false,
  playbackSpeed: 50,
  selectedNodeId: null,

  applyEvent: (ev: Event) => {
    set((state) => ({
      cluster: reduce(state.cluster, ev),
    }));
  },

  resetCluster: (nodeIds = [1, 2, 3, 4, 5]) => {
    set({
      cluster: createInitialState(nodeIds),
      cursor: 0,
      isPlaying: false,
    });
  },

  startLiveSession: (seed: number) => {
    const { applyEvent, resetCluster } = get();
    resetCluster([1, 2, 3, 4, 5]);
    set({
      isLive: true,
      isPlaying: false,
      events: [],
      cursor: 0,
      header: { v: 1, seed, nodes: [1, 2, 3, 4, 5] },
    });

    const onEvent = (evJSON: string) => {
      try {
        const ev = JSON.parse(evJSON) as Event;
        applyEvent(ev);
        set((state) => ({
          events: [...state.events, ev],
          cursor: state.events.length + 1,
        }));
      } catch (err) {
        console.error("Failed to parse live event:", err);
      }
    };

    const sim = typeof window !== "undefined" ? window.RaftSim : (globalThis as any).RaftSim;
    if (sim) {
      sim.start(seed, onEvent);
    } else {
      console.warn("RaftSim is not available");
    }
  },

  issueCommand: (kind: string, node?: number) => {
    const sim = typeof window !== "undefined" ? window.RaftSim : (globalThis as any).RaftSim;
    if (sim) {
      sim.sendCommand(JSON.stringify({ kind, node }));
    } else {
      console.warn("RaftSim is not available");
    }
  },

  loadTrace: (ndjsonText: string) => {
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
    const initial = createInitialState(nodeIds);

    set({
      header,
      events,
      cursor: 0,
      isPlaying: false,
      isLive: false,
      cluster: initial,
      selectedNodeId: null,
    });
  },

  stepForward: () => {
    const { cursor, events, cluster } = get();
    if (cursor < events.length) {
      const ev = events[cursor];
      set({
        cluster: reduce(cluster, ev),
        cursor: cursor + 1,
      });
    } else {
      set({ isPlaying: false });
    }
  },

  stepBackward: () => {
    const { cursor, events, header } = get();
    if (cursor <= 0) return;

    const target = cursor - 1;
    const nodeIds = header?.nodes ?? [1, 2, 3, 4, 5];
    let newCluster = createInitialState(nodeIds);

    for (let i = 0; i < target; i++) {
      newCluster = reduce(newCluster, events[i]);
    }

    set({
      cluster: newCluster,
      cursor: target,
      isPlaying: false,
    });
  },

  seekTo: (target: number) => {
    const { events, header } = get();
    const clamped = Math.max(0, Math.min(events.length, target));
    const nodeIds = header?.nodes ?? [1, 2, 3, 4, 5];
    let newCluster = createInitialState(nodeIds);

    for (let i = 0; i < clamped; i++) {
      newCluster = reduce(newCluster, events[i]);
    }

    set({
      cluster: newCluster,
      cursor: clamped,
      isPlaying: false,
    });
  },

  play: () => set({ isPlaying: true }),
  pause: () => set({ isPlaying: false }),
  setPlaybackSpeed: (speed: number) => set({ playbackSpeed: speed }),
  setSelectedNodeId: (id: NodeID | null) => set({ selectedNodeId: id }),
}));
