package sim

import (
	"bytes"
	"fmt"

	"github.com/ANAXMD/raft-lab/raft"
)

// The safety checker. It runs after EVERY simulated event and records
// violations of Raft's safety properties (paper, Figure 3). It never stops the
// run; tests call Violations()/MustBeSafe at the end so you see the first
// violation together with the seed that reproduces it.
//
// NOTE: this is O(N^2 * log length) per event. Fine for a skeleton and small
// fuzz runs; optimize (incremental / sampled checks) if long runs get slow.

func (s *Sim) violate(format string, args ...any) {
	msg := fmt.Sprintf("[seed=%d t=%dms] ", s.cfg.Seed, s.now) + fmt.Sprintf(format, args...)
	s.violations = append(s.violations, msg)
	s.logf("VIOLATION %s", msg)
}

// Violations returns every safety violation observed so far.
func (s *Sim) Violations() []string { return append([]string(nil), s.violations...) }

// T is the subset of *testing.T the checker needs (keeps sim free of a testing import).
type T interface {
	Helper()
	Fatalf(format string, args ...any)
}

// MustBeSafe fails the test with the first violation (and a repro hint).
func (s *Sim) MustBeSafe(t T) {
	t.Helper()
	if len(s.violations) > 0 {
		t.Fatalf("%d safety violation(s); first: %s\nreproduce with seed %d", len(s.violations), s.violations[0], s.cfg.Seed)
	}
}

func sameEntry(a, b raft.Entry) bool {
	return a.Term == b.Term && a.Index == b.Index && bytes.Equal(a.Data, b.Data)
}

func (s *Sim) check() {
	if len(s.violations) > 20 { // don't drown the output after a bug
		return
	}
	logs := map[raft.NodeID][]raft.Entry{}
	for _, id := range s.ids {
		h := s.hosts[id]
		if !h.up {
			continue
		}
		st := h.node.Status()
		logs[id] = h.node.Log()

		// 1. Election Safety: at most one leader per term.
		if st.Role == raft.Leader {
			if prev, ok := s.leaderOfTerm[st.Term]; ok && prev != id {
				s.violate("ElectionSafety: nodes %d and %d are both leader in term %d", prev, id, st.Term)
			} else {
				s.leaderOfTerm[st.Term] = id
			}
			// 2. Leader Completeness: a leader's log contains every entry
			// committed in an earlier term.
			for i, c := range s.committed {
				if c.Term < st.Term {
					l := logs[id]
					if i >= len(l) || !sameEntry(l[i], c) {
						s.violate("LeaderCompleteness: leader %d (term %d) is missing committed entry index=%d term=%d", id, st.Term, c.Index, c.Term)
						break
					}
				}
			}
		}
	}

	// 3. Log Matching: if two logs have an entry with the same index and term,
	// the logs are identical up through that index.
	for ai, a := range s.ids {
		la, okA := logs[a]
		if !okA {
			continue
		}
		for _, b := range s.ids[ai+1:] {
			lb, okB := logs[b]
			if !okB {
				continue
			}
			n := min(len(la), len(lb))
			match := -1 // highest position where terms agree
			for i := n - 1; i >= 0; i-- {
				if la[i].Term == lb[i].Term {
					match = i
					break
				}
			}
			for i := 0; i <= match; i++ {
				if !sameEntry(la[i], lb[i]) {
					s.violate("LogMatching: nodes %d and %d agree on index %d but differ at index %d", a, b, match+1, i+1)
					break
				}
			}
		}
	}
}

// apply is called by the driver for every committed entry a node applies.
// 4. State Machine Safety: no two nodes ever apply different entries at the same index.
func (s *Sim) apply(h *host, e raft.Entry) {
	if e.Index != raft.Index(len(h.applied)+1) {
		s.violate("Apply: node %d applied index %d but expected %d (out of order / gap)", h.id, e.Index, len(h.applied)+1)
	}
	h.applied = append(h.applied, e)
	switch {
	case int(e.Index) == len(s.committed)+1:
		s.committed = append(s.committed, e)
	case int(e.Index) <= len(s.committed):
		if !sameEntry(s.committed[e.Index-1], e) {
			s.violate("StateMachineSafety: node %d applied a different entry at index %d than another node did", h.id, e.Index)
		}
	default:
		s.violate("Apply: node %d applied index %d beyond globally known commit %d", h.id, e.Index, len(s.committed))
	}
	s.logf("apply node=%d index=%d term=%d", h.id, e.Index, e.Term)
}
