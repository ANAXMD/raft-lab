// Command trace writes an NDJSON event trace for the viewer.
//
//	go run ./cmd/trace -seed 182 > trace.ndjson          built-in "leader crash" scenario
//	go run ./cmd/trace -session my-session.json > t.ndjson   replay a downloaded session
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/ANAXMD/raft-lab/raft"
	"github.com/ANAXMD/raft-lab/sim"
)

func main() {
	seed := flag.Int64("seed", 1, "random seed for deterministic simulation")
	sessionPath := flag.String("session", "", "replay a session file (as exported by the live viewer) instead of the built-in scenario")
	flag.Parse()

	enc := json.NewEncoder(os.Stdout)
	var encErr error
	emit := func(v any) {
		if err := enc.Encode(v); err != nil && encErr == nil {
			encErr = err
		}
	}

	if *sessionPath != "" {
		raw, err := os.ReadFile(*sessionPath)
		if err != nil {
			fatal(err)
		}
		f, err := sim.ParseSession(raw)
		if err != nil {
			fatal(err)
		}
		nodes := make([]raft.NodeID, f.Config.N)
		for i := range nodes {
			nodes[i] = raft.NodeID(i + 1)
		}
		emit(sim.TraceHeader{V: 1, Seed: f.Seed, Nodes: nodes})
		if _, err := sim.Replay(f, func(e sim.Event) { emit(e) }); err != nil {
			fatal(err)
		}
		if encErr != nil {
			fatal(encErr)
		}
		return
	}

	emit(sim.TraceHeader{V: 1, Seed: *seed, Nodes: []raft.NodeID{1, 2, 3, 4, 5}})

	// Built-in preset: wait for a leader, crash it, watch the failover.
	s := sim.New(sim.Config{Seed: *seed, N: 5, OnEvent: func(e sim.Event) { emit(e) }})
	s.RunUntil(5000, func() bool { _, ok := s.Leader(); return ok })
	if l, ok := s.Leader(); ok {
		s.Crash(l)
	}
	s.Run(10000)
	if encErr != nil {
		fatal(encErr)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "trace:", err)
	os.Exit(1)
}
