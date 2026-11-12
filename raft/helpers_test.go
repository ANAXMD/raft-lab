package raft_test

// Test helpers for the node-level (simulator-free) audit tests.
//
// Rules for everything in this directory:
//   - nodes are built only with raft.New and driven only through the raft.Node
//     interface (Tick, Step, Propose, Ready, Advance, Status, Log);
//   - time advances only when a test calls Tick;
//   - randomness is a fixed offset (fixedRand), so election timeouts are exact
//     and every run is identical.

import (
	"reflect"
	"sort"
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

// fixedRand makes randomized election timeouts exact: the timeout becomes
// ElectionTicks + min(off, ElectionTicks-1).
type fixedRand struct{ off int }

func (r fixedRand) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	if r.off >= n {
		return n - 1
	}
	return r.off
}

// opts are the knobs the tests care about. Zero values mean the realistic
// defaults used throughout: heartbeat every 1 tick, election timeout 10 ticks
// (for example 100 ms ticks: 100 ms heartbeats, 1 s election timeout).
type opts struct {
	preVote, checkQuorum bool
	election, heartbeat  int
	jitter               int // fixedRand offset
	maxAppendBytes       int // applied only if raft.Config has a MaxAppendBytes field
}

func makeCfg(id raft.NodeID, peers []raft.NodeID, o opts) raft.Config {
	if o.election == 0 {
		o.election = 10
	}
	if o.heartbeat == 0 {
		o.heartbeat = 1
	}
	c := raft.Config{
		ID:             id,
		Peers:          append([]raft.NodeID(nil), peers...),
		ElectionTicks:  o.election,
		HeartbeatTicks: o.heartbeat,
		PreVote:        o.preVote,
		CheckQuorum:    o.checkQuorum,
		Rand:           fixedRand{o.jitter},
	}
	if o.maxAppendBytes > 0 {
		setOptionalField(&c, "MaxAppendBytes", o.maxAppendBytes)
	}
	return c
}

// setOptionalField sets a Config field by name if it exists. It lets tests
// compile against the current code and still exercise a field that a later fix
// adds (for example MaxAppendBytes). Returns false if the field is absent.
func setOptionalField(cfg *raft.Config, name string, v int) bool {
	f := reflect.ValueOf(cfg).Elem().FieldByName(name)
	if !f.IsValid() || !f.CanSet() {
		return false
	}
	switch f.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64:
		f.SetInt(int64(v))
	case reflect.Uint, reflect.Uint32, reflect.Uint64:
		f.SetUint(uint64(v))
	default:
		return false
	}
	return true
}

func hasConfigField(name string) bool {
	_, ok := reflect.TypeOf(raft.Config{}).FieldByName(name)
	return ok
}

// ents builds consecutive entries of one term starting at index first.
func ents(term raft.Term, first raft.Index, data ...string) []raft.Entry {
	out := make([]raft.Entry, len(data))
	for i, d := range data {
		out[i] = raft.Entry{Term: term, Index: first + raft.Index(i), Data: []byte(d)}
	}
	return out
}

// drain performs one full driver step: Ready, then Advance. It returns the Ready.
func drain(n raft.Node) raft.Ready {
	rd, ok := n.Ready()
	if !ok {
		return raft.Ready{}
	}
	n.Advance()
	return rd
}

func tickN(n raft.Node, k int) {
	for i := 0; i < k; i++ {
		n.Tick()
	}
}

// find returns the messages of one type addressed to one node.
func find(rd raft.Ready, typ raft.MsgType, to raft.NodeID) []raft.Message {
	var out []raft.Message
	for _, m := range rd.Messages {
		if m.Type == typ && m.To == to {
			out = append(out, m)
		}
	}
	return out
}

func mustOne(t *testing.T, rd raft.Ready, typ raft.MsgType, to raft.NodeID) raft.Message {
	t.Helper()
	ms := find(rd, typ, to)
	if len(ms) != 1 {
		t.Fatalf("want exactly one %v to node %d, got %d (all messages: %v)", typ, to, len(ms), summarize(rd.Messages))
	}
	return ms[0]
}

func summarize(ms []raft.Message) []string {
	var out []string
	for _, m := range ms {
		out = append(out, m.Type.String())
	}
	return out
}

func idxs(es []raft.Entry) []raft.Index {
	out := make([]raft.Index, len(es))
	for i, e := range es {
		out[i] = e.Index
	}
	return out
}

func terms(es []raft.Entry) []raft.Term {
	out := make([]raft.Term, len(es))
	for i, e := range es {
		out[i] = e.Term
	}
	return out
}

func sameEntries(a, b []raft.Entry) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Term != b[i].Term || a[i].Index != b[i].Index || string(a[i].Data) != string(b[i].Data) {
			return false
		}
	}
	return true
}

// tryCall runs f and reports a recovered panic value (nil if none).
func tryCall(f func()) (panicked any) {
	defer func() { panicked = recover() }()
	f()
	return nil
}

// electLeader builds node id, times it out (the election timeout is exact), and
// grants it votes, so it ends as a Leader. It returns the node and the Ready
// produced by winning (which holds the first MsgApp batch).
func electLeader(t *testing.T, id raft.NodeID, peers []raft.NodeID, p raft.Persisted, o opts, grantFrom ...raft.NodeID) (raft.Node, raft.Ready) {
	t.Helper()
	n := raft.New(makeCfg(id, peers, o), p)
	if o.election == 0 {
		o.election = 10
	}
	tickN(n, o.election)
	rd := drain(n) // votes (or nothing for a single-node cluster)
	if len(peers) > 1 {
		term := n.Status().Term
		for _, g := range grantFrom {
			n.Step(raft.Message{Type: raft.MsgVoteResp, From: g, To: id, Term: term})
		}
		rd = drain(n)
	}
	if n.Status().Role != raft.Leader {
		t.Fatalf("setup: node %d did not become leader (role %v)", id, n.Status().Role)
	}
	return n, rd
}

// ---------------------------------------------------------------------------
// A tiny in-memory cluster of real raft.Node values, driven explicitly.
// It enforces the Ready contract on every step and checks one invariant for the
// whole run: an entry is never handed out in Ready.Committed before it was
// handed out in Ready.Entries (persisted before applied).
// ---------------------------------------------------------------------------

type cluster struct {
	t        *testing.T
	ids      []raft.NodeID
	nodes    map[raft.NodeID]raft.Node
	plen     map[raft.NodeID]raft.Index // length of the log as handed to persistence
	down     map[raft.NodeID]bool
	block    func(from, to raft.NodeID) bool // true = drop this directed link
	blockMsg func(m raft.Message) bool       // true = drop this particular message (by type, direction, ...)
	queue    []raft.Message
	inspect  func(m raft.Message) // sees every message at send time
}

func newCluster(t *testing.T, ids []raft.NodeID, mk func(id raft.NodeID) (raft.Config, raft.Persisted)) *cluster {
	c := &cluster{
		t: t, ids: append([]raft.NodeID(nil), ids...),
		nodes: map[raft.NodeID]raft.Node{}, plen: map[raft.NodeID]raft.Index{}, down: map[raft.NodeID]bool{},
	}
	sort.Slice(c.ids, func(i, j int) bool { return c.ids[i] < c.ids[j] })
	for _, id := range c.ids {
		cfg, p := mk(id)
		c.nodes[id] = raft.New(cfg, p)
		c.plen[id] = raft.Index(len(p.Log))
	}
	return c
}

// collect runs one driver step for a node: Ready, check, queue messages, Advance.
func (c *cluster) collect(id raft.NodeID) {
	c.t.Helper()
	n := c.nodes[id]
	rd, ok := n.Ready()
	if !ok {
		return
	}
	if len(rd.Entries) > 0 {
		c.plen[id] = rd.Entries[0].Index - 1 + raft.Index(len(rd.Entries))
	}
	for _, e := range rd.Committed {
		if e.Index > c.plen[id] {
			c.t.Errorf("node %d: entry %d reached Ready.Committed before it was persisted (persisted up to %d)", id, e.Index, c.plen[id])
		}
	}
	for _, m := range rd.Messages {
		if c.inspect != nil {
			c.inspect(m)
		}
		c.queue = append(c.queue, m)
	}
	n.Advance()
}

func (c *cluster) tick(id raft.NodeID) {
	if c.down[id] {
		return
	}
	c.nodes[id].Tick()
	c.collect(id)
}

// tickAll advances every live node by one tick (in ID order) and then delivers
// every resulting message until the network is quiet.
func (c *cluster) tickAll() {
	for _, id := range c.ids {
		c.tick(id)
	}
	c.settle(100000)
}

// settle delivers queued messages FIFO until none are left.
func (c *cluster) settle(max int) {
	c.t.Helper()
	for n := 0; len(c.queue) > 0; n++ {
		if n > max {
			c.t.Fatalf("network did not go quiet after %d deliveries", max)
		}
		m := c.queue[0]
		c.queue = c.queue[1:]
		dst, ok := c.nodes[m.To]
		if !ok || c.down[m.To] || c.down[m.From] {
			continue
		}
		if c.block != nil && c.block(m.From, m.To) {
			continue
		}
		if c.blockMsg != nil && c.blockMsg(m) {
			continue
		}
		dst.Step(m)
		c.collect(m.To)
	}
}

func (c *cluster) status(id raft.NodeID) raft.Status { return c.nodes[id].Status() }

// leader returns the live node that is a Leader in the highest term.
func (c *cluster) leader() (raft.NodeID, bool) {
	var best raft.NodeID
	var bestTerm raft.Term
	for _, id := range c.ids {
		if c.down[id] {
			continue
		}
		st := c.nodes[id].Status()
		if st.Role == raft.Leader && (best == 0 || st.Term > bestTerm) {
			best, bestTerm = id, st.Term
		}
	}
	return best, best != 0
}
