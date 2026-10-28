package sim

import (
	"github.com/ANAXMD/raft-lab/raft"
)

// Event is a single timestamped occurrence in the simulation trace.
type Event struct {
	T       int64        `json:"t"`              // virtual ms
	Ev      string       `json:"ev"`             // "send", "deliver", "drop", "node", "log", "fault", "violation"
	ID      int          `json:"id,omitempty"`   // message id: links send -> deliver/drop
	From    raft.NodeID  `json:"from,omitempty"`
	To      raft.NodeID  `json:"to,omitempty"`
	Type    string       `json:"type,omitempty"`
	Term    raft.Term    `json:"term,omitempty"`
	N       int          `json:"n,omitempty"`    // entry count for appends
	Why     string       `json:"why,omitempty"`  // drop reason: "loss", "partition", "down"

	// Node snapshot fields (emitted only on changes)
	Role    raft.Role    `json:"role,omitempty"`
	Commit  raft.Index   `json:"commit,omitempty"`
	Last    raft.Index   `json:"last,omitempty"`
	Up      bool         `json:"up,omitempty"`

	// Log fields (mirrors Ready.Entries behavior)
	Idx     raft.Index   `json:"idx,omitempty"`
	Entries []raft.Entry `json:"entries,omitempty"`

	Msg     string       `json:"msg,omitempty"` // For violations or general text
}

// TraceHeader is the first line emitted in the NDJSON stream.
type TraceHeader struct {
	V     int           `json:"v"`
	Seed  int64         `json:"seed"`
	Nodes []raft.NodeID `json:"nodes"`
}
