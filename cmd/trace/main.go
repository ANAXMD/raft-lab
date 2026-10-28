package main

import (
	"encoding/json"
	"flag"
	"os"

	"github.com/ANAXMD/raft-lab/raft"
	"github.com/ANAXMD/raft-lab/sim"
)

func main() {
	seed := flag.Int64("seed", 1, "random seed for deterministic simulation")
	flag.Parse()

	enc := json.NewEncoder(os.Stdout)

	// Emit the header
	enc.Encode(sim.TraceHeader{
		V:     1,
		Seed:  *seed,
		Nodes: []raft.NodeID{1, 2, 3, 4, 5},
	})

	// Configure the simulator to dump NDJSON on every event
	cfg := sim.Config{
		Seed: *seed,
		N:    5,
		OnEvent: func(e sim.Event) {
			enc.Encode(e)
		},
	}

	s := sim.New(cfg)
	// Example preset: leader crash
	s.RunUntil(5000, func() bool { _, ok := s.Leader(); return ok })
	if l, ok := s.Leader(); ok {
		s.Crash(l)
	}
	s.Run(10000)
}
