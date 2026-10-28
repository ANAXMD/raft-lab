package sim_test

import (
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
	"github.com/ANAXMD/raft-lab/sim"
)

// pingNode is a fake raft.Node used ONLY to test the simulator itself: it
// broadcasts a heartbeat every few ticks and counts what it receives.
type pingNode struct {
	cfg         raft.Config
	ticks       int
	received    map[raft.NodeID]int
	out         []raft.Message
	claimLeader bool // used to prove the checker catches election-safety bugs
	crashes     *int
}

func (p *pingNode) Tick() {
	p.ticks++
	if p.ticks%3 == 0 {
		for _, id := range p.cfg.Peers {
			if id != p.cfg.ID {
				p.out = append(p.out, raft.Message{Type: raft.MsgHeartbeat, From: p.cfg.ID, To: id, Term: 1})
			}
		}
	}
}
func (p *pingNode) Step(m raft.Message) { p.received[m.From]++ }
func (p *pingNode) Propose([]byte) bool { return false }
func (p *pingNode) Advance()            { p.out = nil }
func (p *pingNode) Log() []raft.Entry   { return nil }
func (p *pingNode) Ready() (raft.Ready, bool) {
	if len(p.out) == 0 {
		return raft.Ready{}, false
	}
	return raft.Ready{Messages: p.out}, true
}
func (p *pingNode) Status() raft.Status {
	r := raft.Follower
	if p.claimLeader {
		r = raft.Leader
	}
	return raft.Status{ID: p.cfg.ID, Term: 1, Role: r}
}

func pingFactory(nodes map[raft.NodeID]*pingNode, claimLeader bool) sim.NodeFactory {
	return func(cfg raft.Config, _ raft.Persisted) raft.Node {
		p := &pingNode{cfg: cfg, received: map[raft.NodeID]int{}, claimLeader: claimLeader}
		nodes[cfg.ID] = p
		return p
	}
}

func run(seed int64, drop float64) uint64 {
	nodes := map[raft.NodeID]*pingNode{}
	s := sim.New(sim.Config{Seed: seed, N: 5, DropProb: drop, DupProb: 0.05, NewNode: pingFactory(nodes, false)})
	s.Run(5000)
	return s.TraceHash()
}

func TestSameSeedSameRun(t *testing.T) {
	for _, seed := range []int64{1, 2, 99, 48213} {
		if a, b := run(seed, 0.2), run(seed, 0.2); a != b {
			t.Fatalf("seed %d is NOT deterministic: %x != %x", seed, a, b)
		}
	}
}

func TestDifferentSeedsDiffer(t *testing.T) {
	if run(1, 0.2) == run(2, 0.2) {
		t.Fatal("different seeds produced identical runs; RNG is not wired in")
	}
}

func TestPartitionBlocksTraffic(t *testing.T) {
	nodes := map[raft.NodeID]*pingNode{}
	s := sim.New(sim.Config{Seed: 7, N: 5, NewNode: pingFactory(nodes, false)})
	s.Run(500)
	s.Isolate(1)
	before := nodes[1].received[2] + nodes[1].received[3]
	other := nodes[2].received[3]
	s.Run(1000)
	if nodes[1].received[2]+nodes[1].received[3] > before+3 { // allow a few in-flight messages
		t.Fatalf("isolated node 1 kept receiving traffic")
	}
	if nodes[2].received[3] <= other {
		t.Fatalf("healthy nodes 2<->3 stopped talking")
	}
	s.Heal()
	b := nodes[1].received[2]
	s.Run(1000)
	if nodes[1].received[2] <= b {
		t.Fatalf("traffic did not resume after Heal")
	}
}

func TestCrashedNodeReceivesNothingAndRestartsFresh(t *testing.T) {
	nodes := map[raft.NodeID]*pingNode{}
	s := sim.New(sim.Config{Seed: 3, N: 3, NewNode: pingFactory(nodes, false)})
	s.Run(300)
	old := nodes[2]
	s.Crash(2)
	got := old.received[1]
	s.Run(500)
	if old.received[1] != got {
		t.Fatal("crashed node still received messages")
	}
	s.Restart(2)
	if nodes[2] == old {
		t.Fatal("restart must build a brand-new node (volatile state lost)")
	}
}

func TestDiskLosesUnsyncedWrites(t *testing.T) {
	d := &sim.Disk{}
	d.Save(&raft.HardState{Term: 1, VotedFor: 2}, []raft.Entry{{Term: 1, Index: 1}})
	d.Sync()
	d.Save(&raft.HardState{Term: 2, VotedFor: 3}, []raft.Entry{{Term: 2, Index: 2}})
	d.Crash() // power cut before the second Sync
	p := d.Load()
	if p.HardState.Term != 1 || len(p.Log) != 1 {
		t.Fatalf("unsynced write survived the crash: %+v", p)
	}
}

func TestDiskOverwritesConflictingSuffix(t *testing.T) {
	d := &sim.Disk{}
	d.Save(nil, []raft.Entry{{Term: 1, Index: 1}, {Term: 1, Index: 2}, {Term: 1, Index: 3}})
	d.Sync()
	d.Save(nil, []raft.Entry{{Term: 2, Index: 2}})
	d.Sync()
	p := d.Load()
	if len(p.Log) != 2 || p.Log[1].Term != 2 {
		t.Fatalf("suffix not replaced from index 2: %+v", p.Log)
	}
}

// The checker must have teeth: two nodes claiming leadership in the same term is a bug.
func TestCheckerCatchesTwoLeadersInOneTerm(t *testing.T) {
	nodes := map[raft.NodeID]*pingNode{}
	s := sim.New(sim.Config{Seed: 5, N: 3, NewNode: pingFactory(nodes, true)})
	s.Run(200)
	if len(s.Violations()) == 0 {
		t.Fatal("checker missed an ElectionSafety violation")
	}
}

func TestCheckerQuietOnHealthyRun(t *testing.T) {
	nodes := map[raft.NodeID]*pingNode{}
	s := sim.New(sim.Config{Seed: 5, N: 3, NewNode: pingFactory(nodes, false)})
	s.Run(2000)
	s.MustBeSafe(t)
}
