import { createInitialState, reduce } from "./reducer";
import type { ClusterState, Event, NodeID } from "./types";

/**
 * Seeking is a fold over the event list: state(i) = reduce(...reduce(initial, e0)..., e[i-1]).
 * To keep scrubbing cheap on long traces we cache the state at every
 * CHECKPOINT_EVERY-th event. Reducer states are immutable, so a checkpoint is
 * just a reference, and because traces only ever grow at the end (live mode),
 * a checkpoint never goes stale. The cache must be discarded when the event
 * list is replaced.
 */
export const CHECKPOINT_EVERY = 256;

/** State after the first `index` events (0 = initial state). */
export function stateAt(
  events: readonly Event[],
  nodeIds: NodeID[],
  index: number,
  checkpoints: ClusterState[],
): ClusterState {
  const target = Math.max(0, Math.min(events.length, index));
  if (checkpoints.length === 0) checkpoints.push(createInitialState(nodeIds));

  const k = Math.floor(target / CHECKPOINT_EVERY);
  for (let j = checkpoints.length; j <= k; j++) {
    let st = checkpoints[j - 1];
    for (let i = (j - 1) * CHECKPOINT_EVERY; i < j * CHECKPOINT_EVERY; i++) {
      st = reduce(st, events[i]);
    }
    checkpoints[j] = st;
  }

  let state = checkpoints[k];
  for (let i = k * CHECKPOINT_EVERY; i < target; i++) {
    state = reduce(state, events[i]);
  }
  return state;
}

/** Node ids 1..n, as the simulator numbers them. */
export function nodeIdsFor(n: number): NodeID[] {
  return Array.from({ length: Math.max(1, n) }, (_, i) => i + 1);
}
