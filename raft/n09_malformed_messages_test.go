package raft_test

// Node-level test of audit finding; see docs/M0_TEST_REPORT.md.

import (
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

// N09 ------------------------------------------------------------------------
// INVARIANT (F4): a node never panics on input from the network and never
// mutates its state because of a message it cannot trust: malformed entries
// (index 0, gaps, non-contiguous, not starting at prev+1) and messages from a
// node that is not in the cluster. State = term, commit, log, HardState output,
// persisted entries, delivered entries; it must also never acknowledge the bad
// entries (no accepting reply).
// SETUP: follower 2, term 2, log [1:t1,2:t1,3:t1], commit 2. Each case sends one
// bad message to a fresh copy of that node.
// EXPECTED: no panic; term, commit and log unchanged; empty Ready (no HardState,
// no Entries, no Committed); no accepting AppendResp/VoteResp.
// FAILS IF: it panics (index 0, gap, below-commit), accepts non-contiguous
// entries, or follows a vote request from an unknown node.
func TestN09_MalformedMessagesDoNotPanicOrMutate(t *testing.T) {
	fresh := func(t *testing.T) raft.Node {
		p := raft.Persisted{HardState: raft.HardState{Term: 2}, Log: ents(1, 1, "a", "b", "c")}
		n := raft.New(makeCfg(2, peers3, opts{}), p)
		n.Step(app(2, 3, 1, 2, nil))
		drain(n)
		if n.Status().Commit != 2 {
			t.Fatalf("setup: commit=%d, want 2", n.Status().Commit)
		}
		return n
	}
	cases := []struct {
		name string
		m    raft.Message
	}{
		{"entry with index 0", app(2, 0, 0, 0, []raft.Entry{{Term: 2, Index: 0, Data: []byte("x")}})},
		{"gap between prev and first entry", app(2, 3, 1, 0, ents(2, 7, "x"))},
		{"non-contiguous entries", app(2, 1, 1, 0, []raft.Entry{{Term: 1, Index: 2, Data: []byte("b")}, {Term: 2, Index: 4, Data: []byte("x")}})},
		{"first entry does not start at prev+1 (below commit)", app(2, 3, 1, 0, ents(2, 2, "x"))},
		{"AppendEntries from an unknown node", raft.Message{Type: raft.MsgApp, From: 99, To: 2, Term: 2, PrevLogIndex: 3, PrevLogTerm: 1, Entries: ents(2, 4, "x")}},
		{"vote request from an unknown node", raft.Message{Type: raft.MsgVote, From: 99, To: 2, Term: 3, LastLogIndex: 9, LastLogTerm: 2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := fresh(t)
			before, wantLog := n.Status(), n.Log()

			var rd raft.Ready
			if p := tryCall(func() { n.Step(tc.m) }); p != nil {
				t.Fatalf("Step panicked on a bad network message: %v", p)
			}
			rd = drain(n)

			after := n.Status()
			if after.Term != before.Term || after.Commit != before.Commit {
				t.Errorf("state changed: term %d->%d commit %d->%d", before.Term, after.Term, before.Commit, after.Commit)
			}
			if !sameEntries(n.Log(), wantLog) {
				t.Errorf("log changed from %v to %v", idxs(wantLog), idxs(n.Log()))
			}
			if rd.HardState != nil || len(rd.Entries) != 0 || len(rd.Committed) != 0 {
				t.Errorf("produced persistence/apply work: hardstate=%v entries=%v committed=%v", rd.HardState, idxs(rd.Entries), idxs(rd.Committed))
			}
			for _, m := range rd.Messages {
				if (m.Type == raft.MsgAppResp || m.Type == raft.MsgVoteResp) && !m.Reject {
					t.Errorf("sent an accepting %v to node %d", m.Type, m.To)
				}
			}
		})
	}
}
