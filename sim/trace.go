package sim

import (
	"github.com/ANAXMD/raft-lab/raft"
)

// Event is a single timestamped occurrence in the simulation trace.
//
// Wire contract (trace format v1). Fields marked "omitted = zero" use
// omitempty, so an absent field means the zero value:
//
//	send/deliver/drop  id links a packet's life; a dup'd packet is delivered twice
//	node               from = node id; role/term/commit/last/up snapshot (omitted = zero/false)
//	log                from = node id; idx = first replaced index; entries replace log[idx-1:]
//	fault              why = crash|restart|isolate|partition|cut|heal; from = node (crash/restart/isolate)
//	                   Network faults always carry the COMPLETE set of cut links in "cuts";
//	                   an absent "cuts" on a network fault means no links are cut.
//	propose            from = node asked (0 if none), why = accepted|rejected|no-leader, msg = payload
//	net                the network settings now in force
//	violation          msg = which safety rule broke
type Event struct {
	T    int64       `json:"t"`            // virtual ms
	Ev   string      `json:"ev"`           // send|deliver|drop|node|log|fault|propose|net|violation
	ID   int         `json:"id,omitempty"` // message id: links send -> deliver/drop
	From raft.NodeID `json:"from,omitempty"`
	To   raft.NodeID `json:"to,omitempty"`
	Type string      `json:"type,omitempty"`
	Term raft.Term   `json:"term,omitempty"`
	N    int         `json:"n,omitempty"`   // entry count for appends
	Why  string      `json:"why,omitempty"` // drop reason (loss|partition|down) or fault/propose detail

	// Node snapshot fields (emitted only on changes)
	Role   raft.Role  `json:"role,omitempty"`
	Commit raft.Index `json:"commit,omitempty"`
	Last   raft.Index `json:"last,omitempty"`
	Up     bool       `json:"up,omitempty"`

	// Log fields (mirrors Ready.Entries behavior)
	Idx     raft.Index   `json:"idx,omitempty"`
	Entries []TraceEntry `json:"entries,omitempty"`

	// Network topology and settings.
	Cuts [][2]raft.NodeID `json:"cuts,omitempty"`
	Net  *NetConfig       `json:"net,omitempty"`

	Msg string `json:"msg,omitempty"` // violations and propose payloads
}

// TraceEntry is a log entry as it appears in a trace. Data is plain text
// (nil for the empty no-op entry) rather than the base64 that a raw []byte
// would produce, so a viewer can show it directly.
type TraceEntry struct {
	Term  raft.Term
	Index raft.Index
	Data  *string
}

func traceEntries(es []raft.Entry) []TraceEntry {
	out := make([]TraceEntry, len(es))
	for i, e := range es {
		out[i] = TraceEntry{Term: e.Term, Index: e.Index}
		if len(e.Data) > 0 {
			d := string(e.Data)
			out[i].Data = &d
		}
	}
	return out
}

// NetConfig is the runtime-adjustable network model.
type NetConfig struct {
	DropProb   float64 `json:"drop"`
	DupProb    float64 `json:"dup"`
	MinDelayMs int64   `json:"minDelay"`
	MaxDelayMs int64   `json:"maxDelay"`
}

// TraceHeader is the first line emitted in the NDJSON stream.
type TraceHeader struct {
	V     int           `json:"v"`
	Seed  int64         `json:"seed"`
	Nodes []raft.NodeID `json:"nodes"`
}
