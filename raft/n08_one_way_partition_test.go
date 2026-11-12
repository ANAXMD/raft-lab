package raft_test

// Node-level test of audit finding; see docs/M0_TEST_REPORT.md.

import (
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

// N08 ------------------------------------------------------------------------
// INVARIANT (F2): a node that has heard from a live leader within its election
// timeout must not help another node depose that leader (neither PreVote nor
// Vote is granted), so a one-way partition cannot topple a healthy leader. The
// lease must not block elections once the leader is really gone.
// SETUP: 3 nodes, PreVote and CheckQuorum on, heartbeat every 1 tick, election
// timeout 10 ticks plus per-node offsets 0, 3 and 7 (so timeouts of 10, 13 and
// 17 ticks, as with 100 ms ticks: 100 ms heartbeats, about 1-1.7 s election
// timeouts). A leader is elected and runs 30 ticks. Then the link
// leader -> victim is cut in that one direction only (the victim still sends to
// everyone and still hears the other follower) and the cluster runs for 150
// ticks, 15 election timeouts.
// EXPECTED: the leader stays leader in the same term, no node's term rises, and
// the victim really did try (it sent PreVote requests repeatedly).
// Control: with the leader crashed instead, a new leader is elected.
// FAILS IF: the other follower grants the victim's PreVote and Vote: the victim
// wins a higher term and the healthy leader steps down.
func TestN08_HealthyLeaderSurvivesOneWayPartition(t *testing.T) {
	jit := map[raft.NodeID]int{1: 0, 2: 3, 3: 7}
	mk := func(id raft.NodeID) (raft.Config, raft.Persisted) {
		return makeCfg(id, peers3, opts{preVote: true, checkQuorum: true, election: 10, heartbeat: 1, jitter: jit[id]}), raft.Persisted{}
	}
	elect := func(t *testing.T, c *cluster) (raft.NodeID, raft.Term) {
		t.Helper()
		for round := 0; round < 100; round++ {
			c.tickAll()
			if l, ok := c.leader(); ok {
				term := c.status(l).Term
				for i := 0; i < 30; i++ { // let heartbeats settle
					c.tickAll()
					if l2, ok := c.leader(); !ok || l2 != l || c.status(l).Term != term {
						t.Fatalf("setup: leadership unstable before any fault")
					}
				}
				return l, term
			}
		}
		t.Fatalf("setup: no leader after 100 ticks")
		return 0, 0
	}

	t.Run("one_way_partition_keeps_leader", func(t *testing.T) {
		c := newCluster(t, peers3, mk)
		preVotes := map[raft.NodeID]int{}
		c.inspect = func(m raft.Message) {
			if m.Type == raft.MsgPreVote {
				preVotes[m.From]++
			}
		}
		leader, term := elect(t, c)
		victim := raft.NodeID(0)
		for _, id := range peers3 {
			if id != leader {
				victim = id // the last follower in ID order
			}
		}
		c.block = func(from, to raft.NodeID) bool { return from == leader && to == victim }

		for round := 1; round <= 150; round++ {
			c.tickAll()
			for _, id := range peers3 {
				st := c.status(id)
				if st.Term != term || (id == leader && st.Role != raft.Leader) {
					t.Fatalf("round %d (tick %d after the cut): healthy leader %d was deposed. Node %d is now %v in term %d (original term %d). "+
						"Victim %d sent %d PreVote request(s)", round, round, leader, id, st.Role, st.Term, term, victim, preVotes[victim])
				}
			}
		}
		if preVotes[victim] < 3 {
			t.Fatalf("the victim sent only %d PreVote requests in 15 election timeouts; the scenario did not exercise the lease", preVotes[victim])
		}
	})

	t.Run("real_leader_crash_still_elects", func(t *testing.T) {
		c := newCluster(t, peers3, mk)
		leader, term := elect(t, c)
		c.down[leader] = true
		for round := 1; round <= 150; round++ {
			c.tickAll()
			if l, ok := c.leader(); ok && l != leader && c.status(l).Term > term {
				t.Logf("new leader %d (term %d) after %d ticks", l, c.status(l).Term, round)
				return
			}
		}
		t.Fatalf("no new leader within 150 ticks after the leader crashed; the lease must not block real elections")
	})
}
