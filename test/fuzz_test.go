package test

import (
	"math/rand"
	"os"
	"strconv"
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
	"github.com/ANAXMD/raft-lab/sim"
)

func fuzzSeeds() int {
	if v, _ := strconv.Atoi(os.Getenv("FUZZN")); v > 0 {
		return v
	}
	return 400
}

// TestFuzz_SafetyAndLiveness throws random crashes, restarts, partitions,
// isolations, 15% message loss, duplication, delays up to 60 ms and client
// proposals at the cluster (PreVote on for even seeds). The safety checker runs
// after every event. After healing, every node must converge.
//
//	FUZZN=2000 go test ./test -run Fuzz -v      # bigger sweep
//	go test ./test -run Fuzz -seed=182 -v       # replay one seed
func TestFuzz_SafetyAndLiveness(t *testing.T) {
	for _, seed := range seeds(fuzzSeeds()) {
		s := sim.New(sim.Config{Seed: seed, N: 5, DropProb: 0.15, DupProb: 0.1,
			MinDelayMs: 1, MaxDelayMs: 60, PreVote: seed%2 == 0})
		r := rand.New(rand.NewSource(seed * 104729))
		n := 0
		for step := 0; step < 120; step++ {
			s.Run(int64(10 + r.Intn(150)))
			id := raft.NodeID(1 + r.Intn(5))
			switch r.Intn(9) {
			case 0:
				s.Crash(id)
			case 1:
				s.Restart(id)
			case 2:
				s.Partition([]raft.NodeID{1, 2}, []raft.NodeID{3, 4, 5})
			case 3:
				s.Isolate(id)
			case 4:
				s.Heal()
			default:
				if l, ok := s.Leader(); ok && s.Propose(l, []byte{byte(n)}) {
					n++
				}
			}
		}
		s.MustBeSafe(t)

		s.Heal()
		for _, id := range s.IDs() {
			s.Restart(id)
		}
		converged := func() bool {
			if _, ok := s.Leader(); !ok {
				return false
			}
			want := len(s.Applied(1))
			for _, id := range s.IDs() {
				if len(s.Applied(id)) != want {
					return false
				}
			}
			return true
		}
		if !s.RunUntil(90_000, converged) {
			t.Fatalf("seed %d: cluster did not converge within 90 virtual seconds after healing", seed)
		}
		s.MustBeSafe(t)
	}
}
