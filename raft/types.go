// Package raft is a pure Raft state machine.
//
// DESIGN RULE: nothing in this package may touch a clock, a network, a disk,
// a goroutine, or global randomness. The node is driven entirely by:
//
//	Tick()    - one unit of logical time has passed
//	Step(m)   - a message arrived
//	Propose() - a client wants to append a command
//
// and it answers only through Ready(). That is what lets the simulator
// replay any run bit-for-bit from a seed.
package raft

// NodeID identifies a node. 0 means "none" (no vote, no leader).
type NodeID uint32

// Term is a Raft term. Index is a 1-based log index (0 = "before the log").
type (
	Term  uint64
	Index uint64
)

type Role uint8

const (
	Follower Role = iota
	PreCandidate
	Candidate
	Leader
)

func (r Role) String() string {
	switch r {
	case Follower:
		return "Follower"
	case PreCandidate:
		return "PreCandidate"
	case Candidate:
		return "Candidate"
	case Leader:
		return "Leader"
	}
	return "?"
}

type MsgType uint8

const (
	MsgNone MsgType = iota
	MsgPreVote
	MsgPreVoteResp
	MsgVote
	MsgVoteResp
	MsgApp // AppendEntries (also carries heartbeats if you want one message type)
	MsgAppResp
	MsgHeartbeat
	MsgHeartbeatResp
)

func (t MsgType) String() string {
	names := [...]string{"None", "PreVote", "PreVoteResp", "Vote", "VoteResp", "App", "AppResp", "Heartbeat", "HeartbeatResp"}
	if int(t) < len(names) {
		return names[t]
	}
	return "?"
}

// Entry is one log entry. Treat entries as immutable once created.
type Entry struct {
	Term  Term
	Index Index
	Data  []byte // nil/empty for the no-op a new leader appends
}

// Message is a single RPC request or response. One flat struct keeps the
// simulator trivial; only the fields relevant to Type are set.
// Treat messages as immutable once sent (the simulator may deliver the same
// message twice to simulate duplication).
type Message struct {
	Type     MsgType
	From, To NodeID
	Term     Term

	// Vote / PreVote request.
	LastLogIndex Index
	LastLogTerm  Term

	// App request.
	PrevLogIndex Index
	PrevLogTerm  Term
	Entries      []Entry
	LeaderCommit Index

	// Responses.
	Reject     bool
	MatchIndex Index // App response: highest index known replicated on the follower
	RejectHint Index // App response: follower's hint for faster nextIndex backoff
}

// HardState is what Raft must persist before answering any RPC:
// currentTerm and votedFor (the log is persisted via Ready.Entries).
type HardState struct {
	Term     Term
	VotedFor NodeID
}

// Persisted is everything a node reads back from disk on restart.
type Persisted struct {
	HardState HardState
	Log       []Entry // Log[0] has Index 1
}

// Ready is the node's output after it has processed inputs.
//
// The driver MUST, in this order:
//  1. persist HardState (if non-nil) and Entries, and fsync
//  2. send Messages          <- only AFTER step 1, or you break safety
//  3. apply Committed to the state machine
//  4. call Advance()
type Ready struct {
	HardState *HardState // non-nil if changed since the last Ready
	// Entries must be persisted by REPLACING the on-disk log from
	// Entries[0].Index onward (this is how conflicting suffixes get overwritten).
	Entries   []Entry
	Messages  []Message
	Committed []Entry // newly committed entries to apply, in order
}

func (r Ready) Empty() bool {
	return r.HardState == nil && len(r.Entries) == 0 && len(r.Messages) == 0 && len(r.Committed) == 0
}

// Config configures a node. Rand MUST be a seeded, per-node generator; the
// node must use it (and nothing else) for randomized election timeouts.
type Config struct {
	ID             NodeID
	Peers          []NodeID // all node IDs in the cluster, including ID
	ElectionTicks  int      // base election timeout; randomize in [ElectionTicks, 2*ElectionTicks)
	HeartbeatTicks int
	PreVote        bool
	CheckQuorum    bool
	Rand           Rand
}

// Rand is the only randomness a node may use.
type Rand interface {
	Intn(n int) int
}

// Status is a cheap read-only view used by tests, the checker and the UI.
type Status struct {
	ID        NodeID
	Term      Term
	Role      Role
	Leader    NodeID // 0 if unknown
	Commit    Index
	Applied   Index
	LastIndex Index
}

// Node is the interface the simulator (and later the real server) drives.
type Node interface {
	Tick()
	Step(m Message)
	// Propose asks the node to append a client command. It returns false if
	// this node is not the leader (the client should retry elsewhere).
	Propose(data []byte) bool
	Ready() (Ready, bool)
	Advance()

	// Inspection (for the safety checker / UI; must not mutate state).
	Status() Status
	Log() []Entry // a copy of the full log
}
