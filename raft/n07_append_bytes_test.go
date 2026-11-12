package raft_test

// Node-level test of audit finding; see docs/M0_TEST_REPORT.md.

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

func msgPayload(m raft.Message) int {
	n := 0
	for _, e := range m.Entries {
		n += len(e.Data)
	}
	return n
}

// N07 ------------------------------------------------------------------------
// INVARIANT (F1): no AppendEntries message carries more entry payload than the
// configured byte budget (one entry is always allowed, so progress is
// guaranteed), AND a follower far behind still catches up completely and
// converges to an identical log.
// SETUP: leader 1 restarts with 200 entries of 1 KiB (200 KiB backlog) and wins
// term 2; follower 2 has an empty log; node 3 does not exist (its messages are
// dropped). Budget: 8 KiB (set through Config.MaxAppendBytes when that field
// exists; the 8 KiB bound is asserted either way). The leader is ticked only
// while the network is idle. A second case uses entries (4 KiB) larger than
// the budget (1 KiB).
// EXPECTED: every message with more than one entry has payload <= budget; the
// follower's log equals the leader's and its commit reaches the leader's.
// FAILS IF: any multi-entry message exceeds the budget (current code sends the
// entire suffix from nextIndex), or the follower never converges.
func TestN07_AppendBytesBoundedAndLaggingFollowerCatchesUp(t *testing.T) {
	run := func(t *testing.T, entryBytes, count, budget int) {
		if !hasConfigField("MaxAppendBytes") {
			t.Logf("raft.Config has no MaxAppendBytes field yet; asserting the %d-byte bound anyway", budget)
		}
		log := make([]raft.Entry, count)
		for i := range log {
			log[i] = raft.Entry{Term: 1, Index: raft.Index(i + 1), Data: bytes.Repeat([]byte{byte('a' + i%26)}, entryBytes)}
		}
		c := newCluster(t, []raft.NodeID{1, 2}, func(id raft.NodeID) (raft.Config, raft.Persisted) {
			p := raft.Persisted{}
			if id == 1 {
				p = raft.Persisted{HardState: raft.HardState{Term: 1}, Log: log}
			}
			return makeCfg(id, peers3, opts{maxAppendBytes: budget}), p
		})
		var biggest, oversized int
		var firstOver string
		c.inspect = func(m raft.Message) {
			if m.Type != raft.MsgApp || len(m.Entries) == 0 {
				return
			}
			b := msgPayload(m)
			if b > biggest {
				biggest = b
			}
			if len(m.Entries) > 1 && b > budget {
				oversized++
				if firstOver == "" {
					firstOver = fmt.Sprintf("%d entries / %d payload bytes", len(m.Entries), b)
				}
			}
		}

		tickN(c.nodes[1], 10) // exact election timeout: node 1 campaigns
		c.collect(1)
		c.settle(100000)
		if l, ok := c.leader(); !ok || l != 1 {
			t.Fatalf("setup: node 1 is not leader")
		}

		ticks := 0
		for iter := 0; iter < 5000; iter++ {
			c.settle(100000)
			s1, s2 := c.status(1), c.status(2)
			if s2.LastIndex == s1.LastIndex && s1.Commit == s1.LastIndex && s2.Commit == s1.Commit {
				break
			}
			c.tick(1) // only when the network is idle (a heartbeat)
			ticks++
		}
		s1, s2 := c.status(1), c.status(2)
		if s2.LastIndex != s1.LastIndex || s2.Commit != s1.Commit || s1.Commit != s1.LastIndex {
			t.Fatalf("follower did not catch up: leader last=%d commit=%d, follower last=%d commit=%d (after %d idle ticks)",
				s1.LastIndex, s1.Commit, s2.LastIndex, s2.Commit, ticks)
		}
		if !sameEntries(c.nodes[1].Log(), c.nodes[2].Log()) {
			t.Fatalf("logs differ after catch-up")
		}
		t.Logf("caught up; largest message payload %d bytes; idle ticks needed %d", biggest, ticks)
		if oversized > 0 {
			t.Fatalf("%d AppendEntries message(s) exceeded the %d-byte budget; first: %s", oversized, budget, firstOver)
		}
	}
	t.Run("backlog_larger_than_budget", func(t *testing.T) { run(t, 1024, 200, 8*1024) })
	t.Run("single_entry_larger_than_budget", func(t *testing.T) { run(t, 4096, 5, 1024) })
}
