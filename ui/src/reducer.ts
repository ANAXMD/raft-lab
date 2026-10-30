import type { ClusterState, Cut, Event, NodeID, NodeState, ProposeOutcome } from "./types";
import { Role } from "./types";

export function createInitialState(nodeIds: NodeID[] = [1, 2, 3, 4, 5]): ClusterState {
  const nodes: Record<NodeID, NodeState> = {};
  for (const id of nodeIds) {
    nodes[id] = {
      id,
      role: Role.Follower,
      term: 0,
      commit: 0,
      last: 0,
      applied: 0,
      up: false,
      log: [],
    };
  }
  return {
    time: 0,
    nodes,
    inflight: {},
    cuts: [],
    net: null,
    lastPropose: null,
  };
}

/** Faults that rewrite the whole set of cut links (crash/restart leave it alone). */
const NETWORK_FAULTS = new Set(["isolate", "partition", "cut", "heal"]);
const PROPOSE_OUTCOMES = new Set<string>(["accepted", "rejected", "no-leader"]);

/** True if the link between a and b is cut (links are undirected in the UI). */
export function isCut(cuts: readonly Cut[], a: NodeID, b: NodeID): boolean {
  return cuts.some(([x, y]) => (x === a && y === b) || (x === b && y === a));
}

export function reduce(state: ClusterState, event: Event): ClusterState {
  // Structural sharing: node objects are shared with the previous state until this
  // event changes them (own() clones one on first write). Log arrays are replaced,
  // never mutated in place, so previous states (checkpoints) stay valid.
  const nextNodes: Record<NodeID, NodeState> = { ...state.nodes };
  const owned = new Set<NodeID>();
  const own = (id: NodeID): NodeState | undefined => {
    const n = nextNodes[id];
    if (!n) return undefined;
    if (!owned.has(id)) {
      nextNodes[id] = { ...n };
      owned.add(id);
    }
    return nextNodes[id];
  };
  const nextInflight = { ...state.inflight };

  const nextState: ClusterState = {
    time: event.t ?? state.time,
    nodes: nextNodes,
    inflight: nextInflight,
    // These are replaced, never mutated, so sharing them between states is safe.
    cuts: state.cuts,
    net: state.net,
    lastPropose: state.lastPropose,
  };

  switch (event.ev) {
    case "send": {
      if (event.id != null && event.from != null && event.to != null) {
        nextInflight[event.id] = {
          id: event.id,
          from: event.from,
          to: event.to,
          type: event.type ?? "",
          term: event.term ?? 0,
          n: event.n,
          sendTime: event.t,
        };
      }
      break;
    }

    case "deliver":
    case "drop": {
      if (event.id != null) {
        delete nextInflight[event.id];
      }
      break;
    }

    case "node": {
      if (event.from != null) {
        const node = own(event.from) ?? {
          id: event.from,
          role: Role.Follower,
          term: 0,
          commit: 0,
          last: 0,
          applied: 0,
          up: false,
          log: [],
        };
        // Node events emit full snapshots with Go omitempty;
        // omitted values represent zero/default values.
        node.role = event.role ?? Role.Follower;
        node.term = event.term ?? 0;
        node.commit = event.commit ?? 0;
        node.last = event.last ?? 0;
        node.up = event.up ?? false;
        if (node.commit > node.applied) {
          node.applied = node.commit;
        }
        nextNodes[event.from] = node;
      }
      break;
    }

    case "log": {
      if (event.from != null && event.entries) {
        const node = own(event.from);
        if (node) {
          const startIdx = (event.idx ?? 1) - 1;
          const currentLog = node.log.slice(0, Math.max(0, startIdx));
          node.log = [...currentLog, ...event.entries];
          node.last = node.log.length;
        }
      }
      break;
    }

    case "fault": {
      if (event.from != null && nextNodes[event.from]) {
        if (event.why === "crash") {
          own(event.from)!.up = false;
        } else if (event.why === "restart") {
          own(event.from)!.up = true;
        }
      }
      if (event.why && NETWORK_FAULTS.has(event.why)) {
        // The event carries the COMPLETE set of cut links; absent means none.
        nextState.cuts = event.cuts ?? [];
      }
      break;
    }

    case "net": {
      if (event.net) nextState.net = event.net;
      break;
    }

    case "propose": {
      if (event.why && PROPOSE_OUTCOMES.has(event.why)) {
        nextState.lastPropose = {
          t: event.t,
          node: event.from ?? 0,
          outcome: event.why as ProposeOutcome,
          data: event.msg ?? "",
        };
      }
      break;
    }
  }

  return nextState;
}
