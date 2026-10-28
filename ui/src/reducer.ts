import type { ClusterState, Event, NodeID, NodeState } from "./types";
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
  };
}

export function reduce(state: ClusterState, event: Event): ClusterState {
  const nextNodes: Record<NodeID, NodeState> = {};
  for (const id in state.nodes) {
    const n = state.nodes[id];
    nextNodes[id] = {
      ...n,
      log: [...n.log],
    };
  }
  const nextInflight = { ...state.inflight };

  const nextState: ClusterState = {
    time: event.t ?? state.time,
    nodes: nextNodes,
    inflight: nextInflight,
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
        const node = nextNodes[event.from] ?? {
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
        const node = nextNodes[event.from];
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
          nextNodes[event.from].up = false;
        } else if (event.why === "restart") {
          nextNodes[event.from].up = true;
        }
      }
      break;
    }
  }

  return nextState;
}
