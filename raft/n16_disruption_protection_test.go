package raft_test

import (
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

// N16 ------------------------------------------------------------------------
// INVARIANT (F2 configuration): protection from a disruptive isolated node needs
// BOTH PreVote and CheckQuorum, and a system built on this package can detect
// when it is missing. Config.DisruptionProtected is true exactly for that
// combination.
// SETUP: the four combinations of the two flags.
// EXPECTED: only (PreVote, CheckQuorum) = (true, true) is protected.
// Evidence that each half is needed (one-way partition that heals, 100 ticks cut):
//   - PreVote off, CheckQuorum on: the victim's term climbs while cut off and the
//     leader is deposed when the link heals (lease alone is not enough);
//   - both on: leader and terms unchanged.
//
// FAILS IF: DisruptionProtected reports true for a partial configuration, or the
// "both on" cluster loses its leader after healing.
func TestN16_DisruptionProtectionNeedsPreVoteAndCheckQuorum(t *testing.T) {
	for _, tc := range []struct {
		pv, cq, want bool
	}{{false, false, false}, {true, false, false}, {false, true, false}, {true, true, true}} {
		c := makeCfg(1, peers3, opts{preVote: tc.pv, checkQuorum: tc.cq})
		if got := c.DisruptionProtected(); got != tc.want {
			t.Errorf("PreVote=%v CheckQuorum=%v: DisruptionProtected()=%v, want %v", tc.pv, tc.cq, got, tc.want)
		}
	}

	heal := func(pv bool) (victimTermWhileCut raft.Term, leaderUnchanged bool) {
		jit := map[raft.NodeID]int{1: 0, 2: 3, 3: 7}
		c := newCluster(t, peers3, func(id raft.NodeID) (raft.Config, raft.Persisted) {
			return makeCfg(id, peers3, opts{preVote: pv, checkQuorum: true, election: 10, heartbeat: 1, jitter: jit[id]}), raft.Persisted{}
		})
		for i := 0; i < 60; i++ {
			c.tickAll()
		}
		leader, ok := c.leader()
		if !ok {
			t.Fatalf("setup: no leader")
		}
		term := c.status(leader).Term
		c.block = func(from, to raft.NodeID) bool { return from == leader && to == 3 }
		for i := 0; i < 100; i++ {
			c.tickAll()
		}
		victimTermWhileCut = c.status(3).Term
		c.block = nil
		for i := 0; i < 40; i++ {
			c.tickAll()
		}
		l2, ok := c.leader()
		return victimTermWhileCut, ok && l2 == leader && c.status(l2).Term == term
	}

	if vt, same := heal(false); vt <= 1 || same {
		t.Errorf("PreVote off: victim term while cut = %d, leader unchanged after heal = %v; expected term inflation and a deposed leader (the evidence that PreVote is required)", vt, same)
	}
	if vt, same := heal(true); vt != 1 || !same {
		t.Errorf("PreVote and CheckQuorum on: victim term while cut = %d, leader unchanged after heal = %v; want 1 and true", vt, same)
	}
}
