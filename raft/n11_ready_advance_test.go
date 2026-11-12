package raft_test

// Node-level test of audit finding; see docs/M0_TEST_REPORT.md.

import (
	"reflect"
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

// N11 ------------------------------------------------------------------------
// THE DRIVER CONTRACT (F3), exactly:
//  1. After any input (Tick, Step, Propose) the driver calls Ready(); if it
//     returned ok it persists HardState+Entries (fsync), then sends Messages,
//     then applies Committed, then calls Advance() exactly once.
//  2. Between Ready() and Advance() the driver must NOT feed the node any
//     input.
//  3. If a driver breaks rule 2 (or calls Advance without Ready), the node must
//     either refuse loudly (panic) or keep the data: whatever the input
//     produced must still be returned by a later Ready. SILENT LOSS IS THE
//     ONE FORBIDDEN OUTCOME.
//  4. Ready() is idempotent until Advance().
//
// SETUP/EXPECTED: sub-tests below. FAILS IF: Advance clears output that Ready
// never returned (term/vote change, appended entries, queued messages), or an
// entry is applied (Committed) that was never handed to persistence.
func TestN11_InputBetweenReadyAndAdvanceIsNeverLost(t *testing.T) {
	voteMsg := func(from raft.NodeID, term raft.Term) raft.Message {
		return raft.Message{Type: raft.MsgVote, From: from, To: 2, Term: term}
	}

	t.Run("hardstate_change_after_ready_is_not_lost", func(t *testing.T) {
		n := raft.New(makeCfg(2, peers3, opts{}), raft.Persisted{})
		n.Step(voteMsg(1, 1))
		r1, ok := n.Ready()
		if !ok || r1.HardState == nil || r1.HardState.Term != 1 {
			t.Fatalf("setup: first Ready = %+v", r1)
		}
		if p := tryCall(func() { n.Step(voteMsg(3, 2)) }); p != nil {
			t.Logf("input between Ready and Advance refused loudly: %v", p)
			return
		}
		n.Advance()
		r2, _ := n.Ready()
		if r2.HardState == nil || r2.HardState.Term != 2 || r2.HardState.VotedFor != 3 {
			t.Fatalf("the term-2 vote for node 3 was silently lost: next Ready HardState=%v (it would never be persisted)", r2.HardState)
		}
		if len(find(r2, raft.MsgVoteResp, 3)) != 1 {
			t.Fatalf("the vote reply to node 3 was silently lost: next Ready messages=%v", summarize(r2.Messages))
		}
	})

	t.Run("appended_entries_after_ready_are_not_lost", func(t *testing.T) {
		n, first := electLeader(t, 1, []raft.NodeID{1}, raft.Persisted{}, opts{})
		persisted := map[raft.Index]string{}
		record := func(rd raft.Ready) {
			for _, e := range rd.Entries {
				for k := range persisted {
					if k >= e.Index {
						delete(persisted, k)
					}
				}
				persisted[e.Index] = string(e.Data)
			}
			for _, e := range rd.Committed {
				if _, ok := persisted[e.Index]; !ok {
					t.Errorf("entry %d (%q) was delivered for apply but never handed to persistence", e.Index, e.Data)
				}
			}
		}
		record(first)
		n.Propose([]byte("a"))
		r1, _ := n.Ready()
		record(r1)
		if p := tryCall(func() { n.Propose([]byte("b")) }); p != nil {
			t.Logf("input between Ready and Advance refused loudly: %v", p)
			return
		}
		n.Advance()
		for {
			r, ok := n.Ready()
			if !ok {
				break
			}
			record(r)
			n.Advance()
		}
		for _, e := range n.Log() {
			if persisted[e.Index] != string(e.Data) {
				t.Errorf("log entry %d (%q) is in memory but was never handed to persistence (persisted: %q)", e.Index, e.Data, persisted[e.Index])
			}
		}
	})

	t.Run("queued_messages_after_ready_are_not_lost", func(t *testing.T) {
		n, _ := electLeader(t, 1, peers3, raft.Persisted{}, opts{}, 2)
		n.Propose([]byte("x"))
		n.Ready() // outstanding
		if p := tryCall(func() { n.Propose([]byte("y")) }); p != nil {
			t.Logf("input between Ready and Advance refused loudly: %v", p)
			return
		}
		n.Advance()
		r2, _ := n.Ready()
		found := false
		for _, m := range find(r2, raft.MsgApp, 2) {
			for _, e := range m.Entries {
				if string(e.Data) == "y" {
					found = true
				}
			}
		}
		if !found {
			t.Fatalf("the AppendEntries carrying %q to node 2 was silently lost (next Ready messages: %v)", "y", summarize(r2.Messages))
		}
	})

	t.Run("advance_without_ready_does_not_discard_output", func(t *testing.T) {
		n := raft.New(makeCfg(2, peers3, opts{}), raft.Persisted{})
		n.Step(voteMsg(1, 1))
		if p := tryCall(func() { n.Advance() }); p != nil {
			t.Logf("Advance without Ready refused loudly: %v", p)
			return
		}
		r, ok := n.Ready()
		if !ok || r.HardState == nil || len(find(r, raft.MsgVoteResp, 1)) != 1 {
			t.Fatalf("output produced before an unmatched Advance was discarded: ok=%v hardstate=%v messages=%v", ok, r.HardState, summarize(r.Messages))
		}
	})

	t.Run("ready_is_idempotent_until_advance", func(t *testing.T) {
		n := raft.New(makeCfg(2, peers3, opts{}), raft.Persisted{})
		n.Step(voteMsg(1, 1))
		a, _ := n.Ready()
		b, _ := n.Ready()
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("two Ready() calls with no Advance in between returned different batches:\n%+v\n%+v", a, b)
		}
	})
}
