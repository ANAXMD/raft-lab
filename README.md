# raft-lab

Raft in Go, built on a deterministic simulator. Pure state machine + seeded virtual clock:
every run is reproducible from a seed.

    go test ./sim -race            # simulator + checker self-tests (green)
    go test ./test -v              # milestone tests (RED until you implement them)
    go test ./test -seed=48213 -run M2 -v   # replay one exact run

## Layout
- `raft/types.go`  Message, Entry, HardState, Ready, and the `Node` interface
- `raft/node.go`   skeleton node: plumbing done, Raft logic marked `TODO(Mx)`
- `sim/sim.go`     event queue, virtual clock, lossy/duplicating/partitionable network, crash/restart
- `sim/disk.go`    fake disk: writes are lost on Crash() unless Sync()ed
- `sim/check.go`   safety checker run after EVERY event (election safety, log matching,
                   leader completeness, state machine safety)
- `test/`          milestone tests, swept over many seeds

## Rules that keep it deterministic
No goroutines, no `time.Now`, no global rand, no behavior-affecting map iteration
(iterate `s.ids`). A node's only randomness is `cfg.Rand`.

## Driver contract (Ready)
persist+fsync HardState/Entries -> send Messages -> apply Committed -> Advance().
Sending before fsync breaks safety; `Config.SkipSync` exists to prove the checker catches it.

## Milestones
M1 harness (done) | M2 election | M3 replication + Figure 8 | M4 persistence + crash fuzz |
M5 PreVote, CheckQuorum, client dedupe | M6 KV + Porcupine | M7 visualizer (consumes `Sim.TraceLog`)
