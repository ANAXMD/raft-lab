package raft_test

// Node-level regression test for the leader's own lease (audit finding F2,
// follow-up found in review of the first F2 fix).

import (
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

// N15 ------------------------------------------------------------------------
// INVARIANT (F2): while CheckQuorum is on, a leader is in its lease for the
// whole time it is leader, not only for part of each cycle. It ignores PreVote
// and Vote requests for a higher term and does not adopt that term, so a node
// that can send but not hear cannot depose it. Only the leader's own CheckQuorum
// step-down ends the leadership.
// BACKGROUND: the follower lease is "heard a leader less than ElectionTicks ago".
// A leader resets its clock every electionTimeout, which is randomized in
// [ElectionTicks, 2*ElectionTicks). If the lease were also "clock < ElectionTicks",
// a leader whose timeout is, say, 17 would be out of lease for ticks 10..16 of
// every cycle and would grant votes then. N08 uses a leader with offset 0 (timeout
// 10), where that window is empty, so it cannot see this.
// SETUP (unit): a leader with CheckQuorum, ElectionTicks 10 and offset 7 (timeout
// 17). For each tick count k = 1..16 a fresh leader is ticked k times (quorum stays
// active), then a PreVote and, separately, a Vote for term+1 from node 3 with an
// up-to-date log is delivered.
// EXPECTED (unit): no reply, still Leader, same term, for every k.
// SETUP (scenario): 3 nodes, PreVote and CheckQuorum on, offsets 7, 8 and 9 (leader
// timeout 17). After a stable leader, ONLY the leader's AppendEntries (heartbeats) to
// node 3 are dropped; every vote message in both directions is delivered. 300 ticks.
// EXPECTED (scenario): node 3 really sends PreVotes to the leader, and the leader
// stays leader in the same term. Control: crashing the leader still gives a new
// leader.
// FAILS IF: the leader, out of lease, grants the PreVote and the Vote: node 3 wins
// term+1 and the healthy leader steps down.
func TestN15_LeaderStaysInLeaseWhileCheckQuorum(t *testing.T) {
	const election, jitter = 10, 7 // leader timeout 17

	newLeader := func(t *testing.T, checkQuorum bool) raft.Node {
		t.Helper()
		n := raft.New(makeCfg(1, peers3, opts{preVote: true, checkQuorum: checkQuorum, election: election, heartbeat: 1, jitter: jitter}), raft.Persisted{})
		tickN(n, election+jitter)
		drain(n) // PreVote requests
		n.Step(raft.Message{Type: raft.MsgPreVoteResp, From: 2, To: 1, Term: n.Status().Term + 1})
		drain(n) // campaign: term+1, Vote requests
		n.Step(raft.Message{Type: raft.MsgVoteResp, From: 2, To: 1, Term: n.Status().Term})
		drain(n)
		if n.Status().Role != raft.Leader {
			t.Fatalf("setup: node 1 is %v, want Leader", n.Status().Role)
		}
		return n
	}
	request := func(typ raft.MsgType, term raft.Term) raft.Message {
		return raft.Message{Type: typ, From: 3, To: 1, Term: term + 1, LastLogIndex: 50, LastLogTerm: term}
	}

	t.Run("unit_ignores_requests_at_every_tick_of_the_cycle", func(t *testing.T) {
		for k := 1; k < election+jitter; k++ { // the leader's own CheckQuorum step comes at 17
			for _, typ := range []raft.MsgType{raft.MsgPreVote, raft.MsgVote} {
				n := newLeader(t, true)
				term := n.Status().Term
				for i := 0; i < k; i++ {
					n.Step(raft.Message{Type: raft.MsgAppResp, From: 2, To: 1, Term: term, MatchIndex: 1})
					n.Tick()
					drain(n)
				}
				n.Step(request(typ, term))
				rd := drain(n)
				if len(rd.Messages) != 0 || n.Status().Role != raft.Leader || n.Status().Term != term {
					t.Fatalf("%v after %d ticks: leader answered a higher-term request (messages %v, role %v, term %d->%d); it must ignore it",
						typ, k, summarize(rd.Messages), n.Status().Role, term, n.Status().Term)
				}
			}
		}
	})

	// Control: without CheckQuorum the leader's clock does not run and it could never
	// notice that it is isolated, so it must not refuse to hear a higher term.
	t.Run("control_no_checkquorum_no_lease", func(t *testing.T) {
		n := newLeader(t, false)
		term := n.Status().Term
		n.Step(request(raft.MsgVote, term))
		rd := drain(n)
		if n.Status().Role == raft.Leader || n.Status().Term != term+1 {
			t.Fatalf("leader without CheckQuorum ignored a higher-term Vote (role %v, term %d); it must step down", n.Status().Role, n.Status().Term)
		}
		if r := find(rd, raft.MsgVoteResp, 3); len(r) != 1 || r[0].Reject {
			t.Fatalf("expected a granted VoteResp to node 3, got %v", summarize(rd.Messages))
		}
	})

	// Distinct timeouts 17, 18 and 19 (fixedRand caps an offset at ElectionTicks-1 = 9).
	jit := map[raft.NodeID]int{1: 7, 2: 8, 3: 9}
	mk := func(id raft.NodeID) (raft.Config, raft.Persisted) {
		return makeCfg(id, peers3, opts{preVote: true, checkQuorum: true, election: election, heartbeat: 1, jitter: jit[id]}), raft.Persisted{}
	}
	stable := func(t *testing.T, c *cluster) (raft.NodeID, raft.Term) {
		t.Helper()
		for round := 0; round < 200; round++ {
			c.tickAll()
			if l, ok := c.leader(); ok {
				term := c.status(l).Term
				for i := 0; i < 30; i++ {
					c.tickAll()
				}
				if l2, ok := c.leader(); !ok || l2 != l || c.status(l).Term != term {
					t.Fatalf("setup: leadership unstable before any fault")
				}
				return l, term
			}
		}
		t.Fatalf("setup: no leader")
		return 0, 0
	}

	t.Run("scenario_heartbeats_dropped_votes_working", func(t *testing.T) {
		c := newCluster(t, peers3, mk)
		leader, term := stable(t, c)
		if leader != 1 {
			t.Fatalf("setup: expected node 1 (timeout 17) to lead, got %d", leader)
		}
		const victim = raft.NodeID(3)
		preVotesToLeader := 0
		c.inspect = func(m raft.Message) {
			if m.Type == raft.MsgPreVote && m.From == victim && m.To == leader {
				preVotesToLeader++
			}
		}
		c.blockMsg = func(m raft.Message) bool { return m.From == leader && m.To == victim && m.Type == raft.MsgApp }
		for tick := 1; tick <= 300; tick++ {
			c.tickAll()
			if st := c.status(leader); st.Role != raft.Leader || st.Term != term {
				t.Fatalf("tick %d: healthy leader %d deposed (role %v, term %d, was %d); node %d's term is %d",
					tick, leader, st.Role, st.Term, term, victim, c.status(victim).Term)
			}
		}
		if preVotesToLeader < 5 {
			t.Fatalf("scenario did not exercise the lease: node %d sent only %d PreVotes to the leader", victim, preVotesToLeader)
		}
	})

	t.Run("control_leader_crash_still_elects", func(t *testing.T) {
		c := newCluster(t, peers3, mk)
		leader, term := stable(t, c)
		c.down[leader] = true
		for tick := 1; tick <= 150; tick++ {
			c.tickAll()
			if l, ok := c.leader(); ok && l != leader {
				if c.status(l).Term <= term {
					t.Fatalf("new leader %d has term %d, want above %d", l, c.status(l).Term, term)
				}
				return
			}
		}
		t.Fatalf("no new leader within 150 ticks after the leader crashed")
	})
}
