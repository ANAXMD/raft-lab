package sim_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
	"github.com/ANAXMD/raft-lab/sim"
)

var updateGolden = flag.Bool("update", false, "update golden files")

func generateTrace(seed int64) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)

	if err := enc.Encode(sim.TraceHeader{
		V:     1,
		Seed:  seed,
		Nodes: []raft.NodeID{1, 2, 3, 4, 5},
	}); err != nil {
		return nil, err
	}

	cfg := sim.Config{
		Seed: seed,
		N:    5,
		OnEvent: func(e sim.Event) {
			enc.Encode(e)
		},
	}

	s := sim.New(cfg)
	s.RunUntil(5000, func() bool { _, ok := s.Leader(); return ok })
	if l, ok := s.Leader(); ok {
		s.Crash(l)
	}
	s.Run(10000)

	return buf.Bytes(), nil
}

func TestTraceDeterministic_Seed182(t *testing.T) {
	got, err := generateTrace(182)
	if err != nil {
		t.Fatalf("generateTrace(182): %v", err)
	}

	// Verify in-memory repeatability
	got2, err := generateTrace(182)
	if err != nil {
		t.Fatalf("generateTrace(182) run 2: %v", err)
	}
	if !bytes.Equal(got, got2) {
		t.Fatalf("two runs of seed 182 produced different output (non-deterministic)")
	}

	goldenPath := filepath.Join("testdata", "trace_seed182.golden")

	if *updateGolden {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(goldenPath, got, 0644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}

	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden: %v (run with -update to generate)", err)
	}

	if !bytes.Equal(got, want) {
		t.Fatalf("trace output does not match golden file %s; non-determinism detected", goldenPath)
	}
}
