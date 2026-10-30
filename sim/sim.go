// Package sim is a deterministic, single-threaded discrete-event simulator.
//
// Everything that is nondeterministic in a real cluster (time, message delay,
// loss, duplication, crashes) is drawn from ONE seeded RNG and executed from
// ONE event queue ordered by (virtual time, insertion sequence). Same seed =>
// same run, bit for bit. A failing fuzz run is therefore a one-line repro:
//
//	go test ./test -run TestFuzz -seed=48213
//
// Rules for keeping it that way: no goroutines, no time.Now, no map iteration
// that influences behavior (always iterate s.ids), no global rand.
package sim

import (
	"container/heap"
	"fmt"
	"hash/fnv"
	"math/rand"

	"github.com/ANAXMD/raft-lab/raft"
)

// NodeFactory builds a node; tests can swap in fakes. Default is raft.New.
type NodeFactory func(cfg raft.Config, p raft.Persisted) raft.Node

type Config struct {
	Seed           int64
	N              int   // cluster size, node IDs are 1..N
	TickMs         int64 // virtual ms per logical tick (default 10)
	MinDelayMs     int64 // network delay range (default 1..20)
	MaxDelayMs     int64
	DropProb       float64
	DupProb        float64
	ElectionTicks  int // default 10
	HeartbeatTicks int // default 2
	PreVote        bool
	CheckQuorum    bool
	// SkipSync makes the driver send messages WITHOUT fsyncing first. It is a
	// deliberate bug switch: with a correct node + checker, crash tests must
	// start failing when this is on. That proves the harness has teeth.
	SkipSync bool
	NewNode  NodeFactory
	OnEvent  func(Event) // Optional hook to stream structured trace events
}

func (c *Config) defaults() {
	if c.N == 0 {
		c.N = 5
	}
	if c.TickMs == 0 {
		c.TickMs = 10
	}
	if c.MaxDelayMs == 0 {
		c.MinDelayMs, c.MaxDelayMs = 1, 20
	}
	if c.ElectionTicks == 0 {
		c.ElectionTicks = 10
	}
	if c.HeartbeatTicks == 0 {
		c.HeartbeatTicks = 2
	}
	if c.NewNode == nil {
		c.NewNode = raft.New
	}
}

type nodeSnap struct {
	term   raft.Term
	role   raft.Role
	commit raft.Index
	last   raft.Index
	up     bool
	init   bool
}

type host struct {
	id       raft.NodeID
	node     raft.Node
	disk     *Disk
	up       bool
	applied  []raft.Entry // this host's volatile state machine input
	rng      *rand.Rand
	lastSnap nodeSnap
}

type event struct {
	at    int64
	phase int // 0 = normal events, 1 = commands: a command at time T runs AFTER every other event at T
	seq   uint64
	fn    func()
}
type eventHeap []event

func (h eventHeap) Len() int { return len(h) }
func (h eventHeap) Less(i, j int) bool {
	if h[i].at != h[j].at {
		return h[i].at < h[j].at
	}
	if h[i].phase != h[j].phase {
		return h[i].phase < h[j].phase
	}
	return h[i].seq < h[j].seq
}
func (h eventHeap) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *eventHeap) Push(x any)   { *h = append(*h, x.(event)) }
func (h *eventHeap) Pop() any {
	old := *h
	e := old[len(old)-1]
	*h = old[:len(old)-1]
	return e
}

type link struct{ from, to raft.NodeID }

type Sim struct {
	cfg       Config
	onEvent   func(Event)
	nextMsgID int
	rng       *rand.Rand
	now       int64
	seq       uint64
	queue     eventHeap

	ids   []raft.NodeID // sorted; ALWAYS iterate this, never the map
	hosts map[raft.NodeID]*host
	cut   map[link]bool

	// checker state (see check.go)
	leaderOfTerm map[raft.Term]raft.NodeID
	committed    []raft.Entry // committed[i-1] is the globally committed entry at index i
	violations   []string

	trace     hashTrace
	TraceLog  []string // optional human-readable trace (the UI will consume this)
	KeepTrace bool

	initCfg SessionConfig // configuration at construction, saved into exported sessions
	session []Command     // every command applied so far, in order (see session.go)
}

type hashTrace struct{ h uint64 }

func (t *hashTrace) add(s string) {
	f := fnv.New64a()
	var b [8]byte
	for i := 0; i < 8; i++ {
		b[i] = byte(t.h >> (8 * i))
	}
	f.Write(b[:])
	f.Write([]byte(s))
	t.h = f.Sum64()
}

func New(cfg Config) *Sim {
	cfg.defaults()
	s := &Sim{
		cfg:          cfg,
		onEvent:      cfg.OnEvent,
		rng:          rand.New(rand.NewSource(cfg.Seed)),
		hosts:        map[raft.NodeID]*host{},
		cut:          map[link]bool{},
		leaderOfTerm: map[raft.Term]raft.NodeID{},
		initCfg:      sessionConfigOf(cfg),
	}
	for i := 1; i <= cfg.N; i++ {
		s.ids = append(s.ids, raft.NodeID(i))
	}
	for _, id := range s.ids {
		h := &host{id: id, disk: &Disk{}, rng: rand.New(rand.NewSource(s.rng.Int63()))}
		s.hosts[id] = h
		s.boot(h)
		s.checkNode(h)
		// Stagger tick phases deterministically so nodes aren't in lock-step.
		s.scheduleTick(h, 1+s.rng.Int63n(cfg.TickMs))
	}
	return s
}

func (s *Sim) boot(h *host) {
	h.node = s.cfg.NewNode(raft.Config{
		ID:             h.id,
		Peers:          s.ids,
		ElectionTicks:  s.cfg.ElectionTicks,
		HeartbeatTicks: s.cfg.HeartbeatTicks,
		PreVote:        s.cfg.PreVote,
		CheckQuorum:    s.cfg.CheckQuorum,
		Rand:           h.rng,
	}, h.disk.Load())
	h.up = true
	h.applied = nil // the state machine is volatile: rebuilt by replaying committed entries
}

// ---------- scheduling ----------

func (s *Sim) at(delay int64, fn func()) {
	s.seq++
	heap.Push(&s.queue, event{at: s.now + delay, seq: s.seq, fn: fn})
}

// atAbs schedules a command at absolute virtual time t, ordered after every
// normal event at the same instant (see event.phase). Live sessions and replays
// therefore apply commands at exactly the same point in the event order.
func (s *Sim) atAbs(t int64, fn func()) {
	s.seq++
	heap.Push(&s.queue, event{at: t, phase: 1, seq: s.seq, fn: fn})
}

func (s *Sim) scheduleTick(h *host, delay int64) {
	s.at(delay, func() {
		if h.up {
			h.node.Tick()
			s.pump(h)
			s.check()
			s.checkNode(h)
		}
		s.scheduleTick(h, s.cfg.TickMs)
	})
}

func (s *Sim) emit(e Event) {
	if s.onEvent != nil {
		s.onEvent(e)
	}
}

func (s *Sim) checkNode(h *host) {
	if s.onEvent == nil {
		return
	}
	var st raft.Status
	if h.node != nil {
		st = h.node.Status()
	}
	snap := nodeSnap{
		term:   st.Term,
		role:   st.Role,
		commit: st.Commit,
		last:   st.LastIndex,
		up:     h.up,
		init:   true,
	}
	if snap != h.lastSnap {
		h.lastSnap = snap
		s.emit(Event{
			T:      s.Now(),
			Ev:     "node",
			From:   h.id,
			Role:   st.Role,
			Term:   st.Term,
			Commit: st.Commit,
			Last:   st.LastIndex,
			Up:     h.up,
		})
	}
}

// Now returns virtual time in ms.
func (s *Sim) Now() int64 { return s.now }

// Run advances virtual time by d ms.
func (s *Sim) Run(d int64) { s.runTo(s.now + d) }

func (s *Sim) runTo(deadline int64) {
	for len(s.queue) > 0 && s.queue[0].at <= deadline {
		e := heap.Pop(&s.queue).(event)
		s.now = e.at
		e.fn()
	}
	s.now = deadline
}

// RunUntil steps until cond() is true or maxMs of virtual time passes.
func (s *Sim) RunUntil(maxMs int64, cond func() bool) bool {
	deadline := s.now + maxMs
	for {
		if cond() {
			return true
		}
		if len(s.queue) == 0 || s.queue[0].at > deadline {
			s.now = deadline
			return cond()
		}
		e := heap.Pop(&s.queue).(event)
		s.now = e.at
		e.fn()
	}
}

// ---------- the driver loop: Ready -> disk -> network -> apply -> Advance ----------

func (s *Sim) pump(h *host) {
	for h.up {
		rd, ok := h.node.Ready()
		if !ok {
			return
		}
		if rd.HardState != nil || len(rd.Entries) > 0 {
			h.disk.Save(rd.HardState, rd.Entries)
			if !s.cfg.SkipSync {
				h.disk.Sync() // durable BEFORE any message leaves
			}
			if len(rd.Entries) > 0 {
				s.emit(Event{
					T:       s.Now(),
					Ev:      "log",
					From:    h.id,
					Idx:     rd.Entries[0].Index,
					Entries: traceEntries(rd.Entries),
				})
			}
		}
		for _, m := range rd.Messages {
			s.send(m)
		}
		for _, e := range rd.Committed {
			s.apply(h, e)
		}
		h.node.Advance()
		s.checkNode(h)
	}
}

// ---------- network ----------

func (s *Sim) send(m raft.Message) {
	s.logf("send %v %d->%d term=%d", m.Type, m.From, m.To, m.Term)
	s.nextMsgID++
	msgID := s.nextMsgID
	s.emit(Event{
		T:    s.Now(),
		Ev:   "send",
		ID:   msgID,
		From: m.From,
		To:   m.To,
		Type: m.Type.String(),
		Term: m.Term,
		N:    len(m.Entries),
	})

	// RNG draws happen in a fixed order so runs stay reproducible.
	drop := s.rng.Float64() < s.cfg.DropProb
	delay := s.cfg.MinDelayMs + s.rng.Int63n(s.cfg.MaxDelayMs-s.cfg.MinDelayMs+1)
	dup := s.rng.Float64() < s.cfg.DupProb
	dupDelay := s.cfg.MinDelayMs + s.rng.Int63n(s.cfg.MaxDelayMs-s.cfg.MinDelayMs+1)
	if drop {
		s.logf("drop %v %d->%d", m.Type, m.From, m.To)
		s.emit(Event{
			T:    s.Now(),
			Ev:   "drop",
			ID:   msgID,
			From: m.From,
			To:   m.To,
			Type: m.Type.String(),
			Term: m.Term,
			Why:  "loss",
		})
		return
	}
	s.at(delay, func() { s.deliver(m, msgID) })
	if dup {
		s.at(dupDelay, func() { s.deliver(m, msgID) })
	}
}

func (s *Sim) deliver(m raft.Message, msgID int) {
	h, ok := s.hosts[m.To]
	if !ok || !h.up {
		s.logf("lost(down) %v %d->%d", m.Type, m.From, m.To)
		s.emit(Event{
			T:    s.Now(),
			Ev:   "drop",
			ID:   msgID,
			From: m.From,
			To:   m.To,
			Type: m.Type.String(),
			Term: m.Term,
			Why:  "down",
		})
		return
	}
	if s.cut[link{m.From, m.To}] {
		s.logf("lost(partition) %v %d->%d", m.Type, m.From, m.To)
		s.emit(Event{
			T:    s.Now(),
			Ev:   "drop",
			ID:   msgID,
			From: m.From,
			To:   m.To,
			Type: m.Type.String(),
			Term: m.Term,
			Why:  "partition",
		})
		return
	}
	s.logf("deliver %v %d->%d term=%d", m.Type, m.From, m.To, m.Term)
	s.emit(Event{
		T:    s.Now(),
		Ev:   "deliver",
		ID:   msgID,
		From: m.From,
		To:   m.To,
		Type: m.Type.String(),
		Term: m.Term,
	})
	h.node.Step(m)
	s.pump(h)
	s.check()
	s.checkNode(h)
}

// ---------- fault injection ----------

// cutLink blocks messages in both directions between a and b (no event).
func (s *Sim) cutLink(a, b raft.NodeID) { s.cut[link{a, b}], s.cut[link{b, a}] = true, true }

// cuts lists every currently blocked link once, as sorted [a b] pairs with a < b.
func (s *Sim) cuts() [][2]raft.NodeID {
	var out [][2]raft.NodeID
	for _, a := range s.ids {
		for _, b := range s.ids {
			if a < b && s.cut[link{a, b}] {
				out = append(out, [2]raft.NodeID{a, b})
			}
		}
	}
	return out
}

// emitFault announces a network fault together with the COMPLETE set of cut
// links afterwards, so a viewer never has to infer topology from deltas.
// An absent "cuts" field on a network fault means "no links are cut".
func (s *Sim) emitFault(why string, node raft.NodeID) {
	s.emit(Event{T: s.Now(), Ev: "fault", From: node, Why: why, Cuts: s.cuts()})
}

// Cut blocks messages in both directions between a and b.
func (s *Sim) Cut(a, b raft.NodeID) {
	s.cutLink(a, b)
	s.emitFault("cut", 0)
}

// Isolate cuts id off from everyone.
func (s *Sim) Isolate(id raft.NodeID) {
	for _, o := range s.ids {
		if o != id {
			s.cutLink(id, o)
		}
	}
	s.emitFault("isolate", id)
}

// Partition splits the cluster into groups that can only talk within a group.
func (s *Sim) Partition(groups ...[]raft.NodeID) {
	s.cut = map[link]bool{}
	gid := map[raft.NodeID]int{}
	for i, g := range groups {
		for _, id := range g {
			gid[id] = i + 1
		}
	}
	for _, a := range s.ids {
		for _, b := range s.ids {
			if a < b && gid[a] != gid[b] {
				s.cutLink(a, b)
			}
		}
	}
	s.emitFault("partition", 0)
}

// Heal removes every partition.
func (s *Sim) Heal() {
	s.cut = map[link]bool{}
	s.emitFault("heal", 0)
}

// Crash power-cuts a node: unsynced disk writes are lost, volatile state vanishes.
func (s *Sim) Crash(id raft.NodeID) {
	h := s.hosts[id]
	s.logf("CRASH %d", id)
	h.up = false
	h.disk.Crash()
	s.emit(Event{
		T:    s.Now(),
		Ev:   "fault",
		From: id,
		Why:  "crash",
	})
	s.checkNode(h)
}

// Restart boots a crashed node from whatever survived on disk.
func (s *Sim) Restart(id raft.NodeID) {
	h := s.hosts[id]
	if h.up {
		return
	}
	s.logf("RESTART %d", id)
	s.boot(h)
	s.emit(Event{
		T:    s.Now(),
		Ev:   "fault",
		From: id,
		Why:  "restart",
	})
	s.checkNode(h)
}

// ---------- client + inspection ----------

// Propose submits a command to a specific node. False if it is not the leader.
func (s *Sim) Propose(id raft.NodeID, data []byte) bool {
	h := s.hosts[id]
	if !h.up {
		return false
	}
	ok := h.node.Propose(data)
	s.pump(h)
	s.check()
	s.checkNode(h)
	return ok
}

// Leader returns the up node that is Leader in the highest term, if any.
func (s *Sim) Leader() (raft.NodeID, bool) {
	var best raft.NodeID
	var bestTerm raft.Term
	for _, id := range s.ids {
		h := s.hosts[id]
		if !h.up {
			continue
		}
		st := h.node.Status()
		if st.Role == raft.Leader && (best == 0 || st.Term > bestTerm) {
			best, bestTerm = id, st.Term
		}
	}
	return best, best != 0
}

func (s *Sim) Node(id raft.NodeID) raft.Node { return s.hosts[id].node }
func (s *Sim) IDs() []raft.NodeID            { return s.ids }
func (s *Sim) Up(id raft.NodeID) bool        { return s.hosts[id].up }
func (s *Sim) Applied(id raft.NodeID) []raft.Entry {
	return append([]raft.Entry(nil), s.hosts[id].applied...)
}

// TraceHash is a fingerprint of the whole run; equal seeds must give equal hashes.
func (s *Sim) TraceHash() uint64 { return s.trace.h }

func (s *Sim) logf(format string, args ...any) {
	line := fmt.Sprintf("t=%06d ", s.now) + fmt.Sprintf(format, args...)
	s.trace.add(line)
	if s.KeepTrace {
		s.TraceLog = append(s.TraceLog, line)
	}
}
