package raft_test

// Node-level test of audit finding; see docs/M0_TEST_REPORT.md.

import (
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

// N10 ------------------------------------------------------------------------
// INVARIANT (F5): Propose copies its argument. A caller may reuse or overwrite
// its buffer after Propose returns, and the log, the entries handed to
// persistence, and the messages already queued for peers must not change.
// SETUP: (a) single-node leader; (b) 3-node leader. Propose("original"), then
// overwrite the original buffer BEFORE calling Ready, then again after Ready.
// EXPECTED: Ready.Entries, the queued MsgApp entries and Log() all still hold
// "original".
// FAILS IF: any of them aliases the caller's buffer.
func TestN10_ProposeCopiesCallerBuffer(t *testing.T) {
	last := func(es []raft.Entry) string {
		if len(es) == 0 {
			return "<none>"
		}
		return string(es[len(es)-1].Data)
	}
	run := func(t *testing.T, n raft.Node, watch raft.NodeID) {
		buf := []byte("original")
		if !n.Propose(buf) {
			t.Fatalf("setup: Propose refused")
		}
		copy(buf, "MUTATED!") // before Ready
		rd := drain(n)
		if got := last(rd.Entries); got != "original" {
			t.Errorf("entry handed to persistence = %q, want %q", got, "original")
		}
		if watch != 0 {
			ms := find(rd, raft.MsgApp, watch)
			if len(ms) == 0 {
				t.Errorf("no AppendEntries was queued for node %d", watch)
			} else if got := last(ms[len(ms)-1].Entries); got != "original" {
				t.Errorf("entry in the message to node %d = %q, want %q", watch, got, "original")
			}
		}
		copy(buf, "AGAIN!!!") // after Ready
		if got := last(n.Log()); got != "original" {
			t.Errorf("entry in the log = %q after the caller reused its buffer, want %q", got, "original")
		}
	}
	t.Run("single_node_leader", func(t *testing.T) {
		n, _ := electLeader(t, 1, []raft.NodeID{1}, raft.Persisted{}, opts{})
		run(t, n, 0)
	})
	t.Run("three_node_leader", func(t *testing.T) {
		n, _ := electLeader(t, 1, peers3, raft.Persisted{}, opts{}, 2)
		run(t, n, 2)
	})
}
