# raft-lab visualizer

React + TypeScript + Vite front end for [raft-lab](../README.md). It renders a Raft cluster from an
event stream, either a recorded NDJSON trace or a live simulation running in WebAssembly.

```bash
npm ci
npm run build:wasm   # needs Go; writes public/sim.wasm and public/wasm_exec.js
npm run dev          # http://localhost:5173/raft-lab/
npm test             # reducer, replay and store tests (the reducer is checked against the Go golden trace)
npm run lint
npm run build
```

## How it fits together

| File | Role |
|---|---|
| `src/reducer.ts` | Pure `reduce(state, event)`: events in, cluster view state out (nodes, in-flight messages, cut links, network settings). |
| `src/replay.ts` | Seeking = folding events from the start; cached checkpoints every 256 events keep scrubbing cheap. |
| `src/store.ts` | Zustand store: trace playback, live session, commands, session export and replay. Live events are batched into one update per burst. |
| `src/components/ClusterRing.tsx` | The SVG ring: roles, packets, dashed red cut links. |
| `src/components/LiveControls.tsx` | Write data, network loss and delay, partitions, speed, session download. |
| `public/sim.wasm` | The Go simulator (`../cmd/wasm`), exposing a `RaftSim` global. |

Because the reducer is pure and the simulator is deterministic, the same session always renders the
same states, and the UI can jump to any point in time without re-running anything.
