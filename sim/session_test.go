package sim_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
	"github.com/ANAXMD/raft-lab/sim"
)

func fp(f float64) *float64 { return &f }
func ip(i int64) *int64     { return &i }

// collect returns a hook that appends every event to buf as NDJSON.
func collect(buf *bytes.Buffer) func(sim.Event) {
	enc := json.NewEncoder(buf)
	return func(e sim.Event) { enc.Encode(e) }
}

func waitLeader(t *testing.T, s *sim.Sim) raft.NodeID {
	t.Helper()
	if !s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok }) {
		t.Fatal("no leader within 5 virtual seconds")
	}
	l, _ := s.Leader()
	return l
}

// The headline property: however a live session is paced, its recording replays
// to a byte-identical event stream.
func TestSessionReplayIsByteIdentical(t *testing.T) {
	for _, seed := range []int64{1, 7, 182, 4242} {
		var live bytes.Buffer
		s := sim.New(sim.Config{Seed: seed, N: 5, DropProb: 0.05, PreVote: true, CheckQuorum: true, OnEvent: collect(&live)})

		// A "wall-clock driven" run: irregular slice lengths, commands issued
		// between slices, exactly like the WASM bridge does.
		slices := []int64{7, 13, 31, 10, 10, 47, 3, 90, 10, 22}
		script := map[int]sim.Command{
			20: {Kind: sim.CmdPropose, Key: "a", Value: "1"},
			24: {Kind: sim.CmdPropose, Key: "b", Value: "2"},
			30: {Kind: sim.CmdIsolate, Node: 3},
			34: {Kind: sim.CmdNet, Drop: fp(0.2), MaxDelay: ip(40)},
			40: {Kind: sim.CmdPropose, Key: "c", Value: "3"},
			48: {Kind: sim.CmdHeal},
			52: {Kind: sim.CmdCrash, Node: 2},
			60: {Kind: sim.CmdPartition, Groups: [][]raft.NodeID{{1, 2}, {3, 4, 5}}},
			66: {Kind: sim.CmdRestart, Node: 2},
			70: {Kind: sim.CmdHeal},
			72: {Kind: sim.CmdPropose, Key: "d", Value: "4"},
		}
		for i := 0; i < 80; i++ {
			s.Run(slices[i%len(slices)])
			if c, ok := script[i]; ok {
				if err := s.Do(c); err != nil {
					t.Fatalf("seed %d: Do(%s): %v", seed, c.Kind, err)
				}
			}
		}
		s.MustBeSafe(t)

		// Export, round-trip through JSON (what "Download session" produces), replay.
		raw, err := json.Marshal(s.Session())
		if err != nil {
			t.Fatal(err)
		}
		f, err := sim.ParseSession(raw)
		if err != nil {
			t.Fatal(err)
		}
		var replay bytes.Buffer
		r, err := sim.Replay(f, collect(&replay))
		if err != nil {
			t.Fatalf("seed %d: replay: %v", seed, err)
		}
		if !bytes.Equal(live.Bytes(), replay.Bytes()) {
			t.Fatalf("seed %d: replay diverged from the live run (live %d bytes, replay %d bytes)", seed, live.Len(), replay.Len())
		}
		if s.TraceHash() != r.TraceHash() {
			t.Fatalf("seed %d: trace hash differs", seed)
		}
		if len(f.Commands) != len(script) {
			t.Fatalf("seed %d: recorded %d commands, issued %d", seed, len(f.Commands), len(script))
		}
	}
}

// Two commands at the very same virtual instant must replay in the same order.
func TestSameInstantCommandsKeepOrder(t *testing.T) {
	var live bytes.Buffer
	s := sim.New(sim.Config{Seed: 9, N: 5, OnEvent: collect(&live)})
	waitLeader(t, s)
	for _, c := range []sim.Command{
		{Kind: sim.CmdIsolate, Node: 1},
		{Kind: sim.CmdHeal},
		{Kind: sim.CmdPartition, Groups: [][]raft.NodeID{{1}, {2, 3, 4, 5}}},
		{Kind: sim.CmdPropose, Key: "k", Value: "v"},
	} {
		if err := s.Do(c); err != nil {
			t.Fatal(err)
		}
	}
	s.Run(2_000)
	var replay bytes.Buffer
	if _, err := sim.Replay(s.Session(), collect(&replay)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(live.Bytes(), replay.Bytes()) {
		t.Fatal("same-instant commands replayed in a different order")
	}
}

// Item 1: proposals replicate, and log entries are readable text, not base64.
func TestProposeReplicatesAndEntriesAreText(t *testing.T) {
	var buf bytes.Buffer
	s := sim.New(sim.Config{Seed: 5, N: 5, OnEvent: collect(&buf)})
	waitLeader(t, s)
	for _, kv := range [][2]string{{"x", "1"}, {"y", "2"}, {"z", "3"}} {
		if err := s.Do(sim.Command{Kind: sim.CmdPropose, Key: kv[0], Value: kv[1]}); err != nil {
			t.Fatal(err)
		}
		s.Run(100)
	}
	s.Run(1_000)
	s.MustBeSafe(t)
	for _, id := range s.IDs() {
		var got []string
		for _, e := range s.Applied(id) {
			if len(e.Data) > 0 {
				got = append(got, string(e.Data))
			}
		}
		if strings.Join(got, ";") != "SET x=1;SET y=2;SET z=3" {
			t.Fatalf("node %d applied %q", id, got)
		}
	}
	out := buf.String()
	if !strings.Contains(out, `"Data":"SET x=1"`) {
		t.Fatal("log events do not carry plain-text entry data")
	}
	if !strings.Contains(out, `"ev":"propose"`) || !strings.Contains(out, `"why":"accepted"`) {
		t.Fatal("no propose/accepted event emitted")
	}
}

func TestProposeWithoutLeaderIsReportedNotLost(t *testing.T) {
	var buf bytes.Buffer
	s := sim.New(sim.Config{Seed: 5, N: 5, OnEvent: collect(&buf)})
	// t=0: nobody has been elected yet.
	if err := s.Do(sim.Command{Kind: sim.CmdPropose, Key: "x", Value: "1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"why":"no-leader"`) {
		t.Fatal("expected a no-leader propose event")
	}
	// Proposing straight to a follower is rejected, visibly.
	l := waitLeader(t, s)
	follower := raft.NodeID(1)
	if l == 1 {
		follower = 2
	}
	buf.Reset()
	if err := s.Do(sim.Command{Kind: sim.CmdPropose, Node: follower, Key: "x", Value: "1"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `"why":"rejected"`) {
		t.Fatal("expected a rejected propose event for a follower")
	}
}

// Item 2: network faults carry the complete set of cut links.
func TestFaultEventsCarryCuts(t *testing.T) {
	var events []sim.Event
	s := sim.New(sim.Config{Seed: 3, N: 5, OnEvent: func(e sim.Event) { events = append(events, e) }})
	last := func() sim.Event {
		for i := len(events) - 1; i >= 0; i-- {
			if events[i].Ev == "fault" {
				return events[i]
			}
		}
		t.Fatal("no fault event")
		return sim.Event{}
	}

	s.Do(sim.Command{Kind: sim.CmdIsolate, Node: 2})
	if e := last(); e.Why != "isolate" || e.From != 2 || len(e.Cuts) != 4 {
		t.Fatalf("isolate event wrong: %+v", e)
	}
	s.Do(sim.Command{Kind: sim.CmdPartition, Groups: [][]raft.NodeID{{1, 2}, {3, 4, 5}}})
	if e := last(); e.Why != "partition" || len(e.Cuts) != 6 { // 2*3 cross links
		t.Fatalf("partition event wrong (want 6 cuts): %+v", e)
	}
	// Partition replaces the previous topology and emits exactly one fault event.
	n := 0
	for _, e := range events {
		if e.Ev == "fault" && (e.Why == "partition" || e.Why == "heal") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("Partition emitted %d heal/partition events, want 1", n)
	}
	s.Do(sim.Command{Kind: sim.CmdCut, Node: 1, Peer: 2})
	if e := last(); e.Why != "cut" || len(e.Cuts) != 7 { // 1-2 was inside group A: now cut too
		t.Fatalf("cut event wrong (want 7 cuts): %+v", e)
	}
	s.Do(sim.Command{Kind: sim.CmdHeal})
	if e := last(); e.Why != "heal" || len(e.Cuts) != 0 {
		t.Fatalf("heal event must have no cuts: %+v", e)
	}
	raw, _ := json.Marshal(last())
	if strings.Contains(string(raw), "cuts") {
		t.Fatalf("an empty cut set should be omitted on the wire: %s", raw)
	}
}

// Item 1: loss/delay can be changed at runtime and the change is observable and recorded.
func TestNetCommandChangesBehaviour(t *testing.T) {
	var events []sim.Event
	s := sim.New(sim.Config{Seed: 11, N: 5, OnEvent: func(e sim.Event) { events = append(events, e) }})
	waitLeader(t, s)
	if err := s.Do(sim.Command{Kind: sim.CmdNet, Drop: fp(1.0)}); err != nil {
		t.Fatal(err)
	}
	mark := len(events)
	s.Run(1_000)
	delivered, lost := 0, 0
	for _, e := range events[mark:] {
		switch {
		case e.Ev == "deliver":
			delivered++
		case e.Ev == "drop" && e.Why == "loss":
			lost++
		}
	}
	// Messages sent before the change may still arrive; once their short delay
	// has elapsed, everything sent is lost.
	if lost == 0 {
		t.Fatal("drop=1.0 lost nothing")
	}
	mark = len(events)
	s.Run(1_000)
	for _, e := range events[mark:] {
		if e.Ev == "deliver" {
			t.Fatal("a message was delivered while drop=1.0")
		}
	}
	var netEv *sim.Event
	for i := range events {
		if events[i].Ev == "net" {
			netEv = &events[i]
		}
	}
	if netEv == nil || netEv.Net == nil || netEv.Net.DropProb != 1.0 {
		t.Fatalf("no net event describing the new settings: %+v", netEv)
	}
	s.Do(sim.Command{Kind: sim.CmdNet, Drop: fp(0)})
	mark = len(events)
	s.Run(2_000)
	got := false
	for _, e := range events[mark:] {
		got = got || e.Ev == "deliver"
	}
	if !got {
		t.Fatal("traffic did not resume after drop=0")
	}
}

// Invalid commands are rejected and never recorded.
func TestInvalidCommandsRejectedAndNotRecorded(t *testing.T) {
	s := sim.New(sim.Config{Seed: 1, N: 5})
	bad := []sim.Command{
		{Kind: "explode"},
		{Kind: sim.CmdCrash, Node: 9},
		{Kind: sim.CmdCrash},
		{Kind: sim.CmdCut, Node: 1, Peer: 1},
		{Kind: sim.CmdPartition, Groups: [][]raft.NodeID{{1, 2}}},
		{Kind: sim.CmdPartition, Groups: [][]raft.NodeID{{1, 2}, {2, 3}}},
		{Kind: sim.CmdPartition, Groups: [][]raft.NodeID{{1}, {42}}},
		{Kind: sim.CmdNet, Drop: fp(1.5)},
		{Kind: sim.CmdNet, MinDelay: ip(0)},
		{Kind: sim.CmdNet, MinDelay: ip(50), MaxDelay: ip(10)},
		{Kind: sim.CmdPropose, Node: 77},
	}
	for _, c := range bad {
		if err := s.Do(c); err == nil {
			t.Errorf("command %+v should have been rejected", c)
		}
	}
	if n := len(s.Session().Commands); n != 0 {
		t.Fatalf("%d invalid commands were recorded", n)
	}
}

func TestParseSessionRejectsBadInput(t *testing.T) {
	for _, in := range []string{`not json`, `{"v":99,"config":{"n":5}}`, `{"v":1,"config":{"n":0}}`} {
		if _, err := sim.ParseSession([]byte(in)); err == nil {
			t.Errorf("ParseSession(%q) should fail", in)
		}
	}
	s := sim.New(sim.Config{Seed: 1, N: 5})
	s.Run(100)
	f := s.Session()
	f.Commands = append(f.Commands, sim.Command{T: 5, Kind: sim.CmdCrash, Node: 99})
	if _, err := sim.Replay(f, nil); err == nil {
		t.Fatal("replaying a session with an invalid command must fail")
	}
}

// A session recorded with the bug switch on replays the same violation: the
// foundation for a "Break it" demo that can be shared and re-run.
func TestBrokenSessionReplaysTheSameViolation(t *testing.T) {
	s := sim.New(sim.Config{Seed: 1, N: 5, SkipSync: true})
	waitLeader(t, s)
	s.Do(sim.Command{Kind: sim.CmdPropose, Key: "doom", Value: "1"})
	s.RunUntil(5_000, func() bool { l, _ := s.Leader(); return len(s.Applied(l)) >= 2 })
	for _, id := range s.IDs() {
		s.Do(sim.Command{Kind: sim.CmdCrash, Node: id})
	}
	s.Run(50)
	for _, id := range s.IDs() {
		s.Do(sim.Command{Kind: sim.CmdRestart, Node: id})
	}
	waitLeader(t, s)
	s.Do(sim.Command{Kind: sim.CmdPropose, Key: "corrupt", Value: "2"})
	s.Run(3_000)
	if len(s.Violations()) == 0 {
		t.Fatal("SkipSync run produced no violation; harness lost its teeth")
	}
	r, err := sim.Replay(s.Session(), nil)
	if err != nil {
		t.Fatal(err)
	}
	a, b := s.Violations(), r.Violations()
	if len(a) != len(b) || a[0] != b[0] {
		t.Fatalf("replay reported different violations:\nlive:   %v\nreplay: %v", a, b)
	}
}
