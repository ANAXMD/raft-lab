package raft_test

// Node-level test of audit finding; see docs/M0_TEST_REPORT.md.

import (
	"runtime"
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

// N12 ------------------------------------------------------------------------
// INVARIANT (F6): raft.New refuses invalid input with a DESCRIPTIVE panic
// (a string or error that is not a Go runtime error), instead of building a node
// with wrong quorums, a crashing timer, or a corrupt log. Contract chosen for
// review: New panics (its signature stays unchanged); a valid configuration and
// valid persisted state must still be accepted.
// SETUP: one valid baseline (3 nodes, term 2, log [1:t1,2:t1]) and a series of
// single faults applied to it.
// EXPECTED: every faulty input panics with a non-runtime panic value; the valid
// input does not panic.
// FAILS IF: invalid input is silently accepted, or the failure is only an
// accidental nil dereference / index error.
func TestN12_NewRejectsBadConfigAndLog(t *testing.T) {
	good := func() (raft.Config, raft.Persisted) {
		return makeCfg(1, peers3, opts{}), raft.Persisted{HardState: raft.HardState{Term: 2}, Log: ents(1, 1, "a", "b")}
	}
	bad := []struct {
		name   string
		break_ func(*raft.Config, *raft.Persisted)
	}{
		{"nil Rand", func(c *raft.Config, p *raft.Persisted) { c.Rand = nil }},
		{"ElectionTicks is zero", func(c *raft.Config, p *raft.Persisted) { c.ElectionTicks = 0 }},
		{"HeartbeatTicks is zero", func(c *raft.Config, p *raft.Persisted) { c.HeartbeatTicks = 0 }},
		{"HeartbeatTicks >= ElectionTicks", func(c *raft.Config, p *raft.Persisted) { c.HeartbeatTicks = c.ElectionTicks }},
		{"node ID is zero (reserved for none)", func(c *raft.Config, p *raft.Persisted) { c.ID = 0 }},
		{"Peers does not contain own ID", func(c *raft.Config, p *raft.Persisted) { c.Peers = []raft.NodeID{2, 3, 4} }},
		{"Peers contains a duplicate", func(c *raft.Config, p *raft.Persisted) { c.Peers = []raft.NodeID{1, 2, 2, 3} }},
		{"Peers is empty", func(c *raft.Config, p *raft.Persisted) { c.Peers = nil }},
		{"log does not start at index 1", func(c *raft.Config, p *raft.Persisted) { p.Log = ents(1, 2, "a", "b") }},
		{"log has an index gap", func(c *raft.Config, p *raft.Persisted) { p.Log[1].Index = 3 }},
		{"log terms decrease", func(c *raft.Config, p *raft.Persisted) { p.Log[0].Term = 2; p.Log[1].Term = 1 }},
		{"log entry term is above the persisted term", func(c *raft.Config, p *raft.Persisted) { p.Log[1].Term = 3 }},
	}
	t.Run("valid_input_is_accepted", func(t *testing.T) {
		c, p := good()
		if v := tryCall(func() { raft.New(c, p) }); v != nil {
			t.Fatalf("valid config and log were rejected: %v", v)
		}
		single := makeCfg(1, []raft.NodeID{1}, opts{})
		if v := tryCall(func() { raft.New(single, raft.Persisted{}) }); v != nil {
			t.Fatalf("valid single-node config was rejected: %v", v)
		}
	})
	for _, tc := range bad {
		t.Run(tc.name, func(t *testing.T) {
			c, p := good()
			tc.break_(&c, &p)
			v := tryCall(func() { raft.New(c, p) })
			if v == nil {
				t.Fatalf("invalid input was silently accepted")
			}
			if re, ok := v.(runtime.Error); ok {
				t.Fatalf("rejected only by an accidental runtime error, not a descriptive check: %v", re)
			}
		})
	}
}
