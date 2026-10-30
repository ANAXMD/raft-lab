package sim

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/ANAXMD/raft-lab/raft"
)

// A session is a seed, an initial configuration, and the list of commands a
// user issued, each stamped with the VIRTUAL time at which it took effect.
// Because the simulator is deterministic, that is a complete recording: replaying
// it reproduces the run bit for bit, however the live run was paced in wall-clock
// time. That is what makes "download my session" and "replay this bug" work.
//
// Ordering rule: a command at time T takes effect AFTER every other event at T
// (see event.phase). Commands may not create zero-delay events (network delays
// are validated to be >= 1 ms), so a live run and its replay see the same order.

// SessionVersion is the on-disk format version.
const SessionVersion = 1

// SessionConfig is the serializable subset of Config that shapes a run.
type SessionConfig struct {
	N              int     `json:"n"`
	TickMs         int64   `json:"tickMs"`
	MinDelayMs     int64   `json:"minDelayMs"`
	MaxDelayMs     int64   `json:"maxDelayMs"`
	DropProb       float64 `json:"dropProb"`
	DupProb        float64 `json:"dupProb"`
	ElectionTicks  int     `json:"electionTicks"`
	HeartbeatTicks int     `json:"heartbeatTicks"`
	PreVote        bool    `json:"preVote"`
	CheckQuorum    bool    `json:"checkQuorum"`
	SkipSync       bool    `json:"skipSync"` // the deliberate "break it" switch
}

func sessionConfigOf(c Config) SessionConfig {
	return SessionConfig{N: c.N, TickMs: c.TickMs, MinDelayMs: c.MinDelayMs, MaxDelayMs: c.MaxDelayMs,
		DropProb: c.DropProb, DupProb: c.DupProb, ElectionTicks: c.ElectionTicks,
		HeartbeatTicks: c.HeartbeatTicks, PreVote: c.PreVote, CheckQuorum: c.CheckQuorum, SkipSync: c.SkipSync}
}

// Config converts back to a runnable Config (callers add hooks such as OnEvent).
func (sc SessionConfig) Config(seed int64) Config {
	return Config{Seed: seed, N: sc.N, TickMs: sc.TickMs, MinDelayMs: sc.MinDelayMs, MaxDelayMs: sc.MaxDelayMs,
		DropProb: sc.DropProb, DupProb: sc.DupProb, ElectionTicks: sc.ElectionTicks,
		HeartbeatTicks: sc.HeartbeatTicks, PreVote: sc.PreVote, CheckQuorum: sc.CheckQuorum, SkipSync: sc.SkipSync}
}

// Command kinds.
const (
	CmdCrash     = "crash"     // node
	CmdRestart   = "restart"   // node
	CmdIsolate   = "isolate"   // node
	CmdCut       = "cut"       // node, peer
	CmdPartition = "partition" // groups
	CmdHeal      = "heal"
	CmdPropose   = "propose" // key, value; node optional (0 = current leader)
	CmdNet       = "net"     // any of drop, dup, minDelay, maxDelay
)

// Command is one user action. Unused fields are omitted.
type Command struct {
	T    int64  `json:"t"` // virtual ms at which it takes effect (set by the simulator)
	Kind string `json:"kind"`

	Node   raft.NodeID     `json:"node,omitempty"`
	Peer   raft.NodeID     `json:"peer,omitempty"`
	Groups [][]raft.NodeID `json:"groups,omitempty"`
	Key    string          `json:"key,omitempty"`
	Value  string          `json:"value,omitempty"`

	// net: only the fields that are set change.
	Drop     *float64 `json:"drop,omitempty"`
	Dup      *float64 `json:"dup,omitempty"`
	MinDelay *int64   `json:"minDelay,omitempty"`
	MaxDelay *int64   `json:"maxDelay,omitempty"`
}

// SessionFile is the exported recording of a run.
type SessionFile struct {
	V        int           `json:"v"`
	Seed     int64         `json:"seed"`
	Config   SessionConfig `json:"config"`
	Commands []Command     `json:"commands"`
	// Until is the virtual time at which the session was exported; a replay
	// runs to exactly this instant.
	Until int64 `json:"until"`
}

// ParseSession decodes and validates a session file.
func ParseSession(data []byte) (SessionFile, error) {
	var f SessionFile
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("session: %w", err)
	}
	if f.V != SessionVersion {
		return f, fmt.Errorf("session: unsupported version %d (want %d)", f.V, SessionVersion)
	}
	if f.Config.N < 1 {
		return f, fmt.Errorf("session: config.n must be >= 1")
	}
	return f, nil
}

// ---------- validation ----------

func (s *Sim) validNode(id raft.NodeID) error {
	if _, ok := s.hosts[id]; !ok {
		return fmt.Errorf("unknown node %d", id)
	}
	return nil
}

// Validate reports whether c could be applied to this cluster. It never
// changes anything, so an invalid command is never recorded.
func (s *Sim) Validate(c Command) error {
	switch c.Kind {
	case CmdCrash, CmdRestart, CmdIsolate:
		return s.validNode(c.Node)
	case CmdCut:
		if err := s.validNode(c.Node); err != nil {
			return err
		}
		if err := s.validNode(c.Peer); err != nil {
			return err
		}
		if c.Node == c.Peer {
			return fmt.Errorf("cut: node and peer must differ")
		}
	case CmdPartition:
		if len(c.Groups) < 2 {
			return fmt.Errorf("partition: need at least 2 groups")
		}
		seen := map[raft.NodeID]bool{}
		for _, g := range c.Groups {
			for _, id := range g {
				if err := s.validNode(id); err != nil {
					return err
				}
				if seen[id] {
					return fmt.Errorf("partition: node %d appears in more than one group", id)
				}
				seen[id] = true
			}
		}
	case CmdHeal:
	case CmdPropose:
		if c.Node != 0 {
			return s.validNode(c.Node)
		}
	case CmdNet:
		n := s.net()
		if c.Drop != nil {
			n.DropProb = *c.Drop
		}
		if c.Dup != nil {
			n.DupProb = *c.Dup
		}
		if c.MinDelay != nil {
			n.MinDelayMs = *c.MinDelay
		}
		if c.MaxDelay != nil {
			n.MaxDelayMs = *c.MaxDelay
		}
		return validNet(n)
	default:
		return fmt.Errorf("unknown command kind %q", c.Kind)
	}
	return nil
}

func validNet(n NetConfig) error {
	if n.DropProb < 0 || n.DropProb > 1 || n.DupProb < 0 || n.DupProb > 1 {
		return fmt.Errorf("net: drop and dup must be within [0,1]")
	}
	// Delays must be >= 1 ms: a zero-delay message would be created at the same
	// instant as the command that caused it and break replay ordering.
	if n.MinDelayMs < 1 || n.MaxDelayMs < n.MinDelayMs {
		return fmt.Errorf("net: need 1 <= minDelay <= maxDelay")
	}
	return nil
}

func (s *Sim) net() NetConfig {
	return NetConfig{DropProb: s.cfg.DropProb, DupProb: s.cfg.DupProb,
		MinDelayMs: s.cfg.MinDelayMs, MaxDelayMs: s.cfg.MaxDelayMs}
}

// ---------- applying ----------

// Do validates c, stamps it with the current virtual time, records it, and
// applies it immediately. This is the entry point for interactive use. Call it
// only between Run calls (never from inside a node).
func (s *Sim) Do(c Command) error {
	if err := s.Validate(c); err != nil {
		return err
	}
	c.T = s.now
	s.applyCommand(c)
	return nil
}

// Schedule queues c to take effect at virtual time c.T. Used to replay sessions.
func (s *Sim) Schedule(c Command) error {
	if err := s.Validate(c); err != nil {
		return err
	}
	if c.T < s.now {
		return fmt.Errorf("command at t=%d is in the past (now=%d)", c.T, s.now)
	}
	s.atAbs(c.T, func() { s.applyCommand(c) })
	return nil
}

func (s *Sim) applyCommand(c Command) {
	s.session = append(s.session, c)
	s.logf("COMMAND %s node=%d", c.Kind, c.Node)
	switch c.Kind {
	case CmdCrash:
		s.Crash(c.Node)
	case CmdRestart:
		s.Restart(c.Node)
	case CmdIsolate:
		s.Isolate(c.Node)
	case CmdCut:
		s.Cut(c.Node, c.Peer)
	case CmdPartition:
		s.Partition(c.Groups...)
	case CmdHeal:
		s.Heal()
	case CmdPropose:
		s.proposeCommand(c)
	case CmdNet:
		if c.Drop != nil {
			s.cfg.DropProb = *c.Drop
		}
		if c.Dup != nil {
			s.cfg.DupProb = *c.Dup
		}
		if c.MinDelay != nil {
			s.cfg.MinDelayMs = *c.MinDelay
		}
		if c.MaxDelay != nil {
			s.cfg.MaxDelayMs = *c.MaxDelay
		}
		n := s.net()
		s.emit(Event{T: s.Now(), Ev: "net", Net: &n})
	}
}

func (s *Sim) proposeCommand(c Command) {
	data := fmt.Sprintf("SET %s=%s", c.Key, c.Value)
	target := c.Node
	if target == 0 {
		l, ok := s.Leader()
		if !ok {
			s.emit(Event{T: s.Now(), Ev: "propose", Why: "no-leader", Msg: data})
			return
		}
		target = l
	}
	why := "rejected" // the node is not the leader (or is down)
	if s.Propose(target, []byte(data)) {
		why = "accepted"
	}
	s.emit(Event{T: s.Now(), Ev: "propose", From: target, Why: why, Msg: data})
}

// ---------- export / replay ----------

// Session returns the recording of this run so far. Until is the current
// virtual time.
func (s *Sim) Session() SessionFile {
	cmds := append([]Command(nil), s.session...)
	sort.SliceStable(cmds, func(i, j int) bool { return cmds[i].T < cmds[j].T })
	return SessionFile{V: SessionVersion, Seed: s.cfg.Seed, Config: s.initCfg, Commands: cmds, Until: s.now}
}

// Replay builds a simulator from a session, schedules every command, and runs
// to f.Until. hook (optional) receives the full event stream from t=0.
func Replay(f SessionFile, hook func(Event)) (*Sim, error) {
	cfg := f.Config.Config(f.Seed)
	cfg.OnEvent = hook
	s := New(cfg)
	for _, c := range f.Commands {
		if err := s.Schedule(c); err != nil {
			return nil, fmt.Errorf("replay: %w", err)
		}
	}
	s.runTo(f.Until)
	return s, nil
}
