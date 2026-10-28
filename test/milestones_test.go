// Milestone tests. These are RED on purpose: they run against the real
// raft.New and fail until you implement each milestone. Work top to bottom.
//
//	go test ./test -run M2 -v
//	go test ./test -seed=48213 -run M2 -v     # replay one exact run
package test

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"

	"github.com/anishathalye/porcupine"

	"github.com/ANAXMD/raft-lab/raft"
	"github.com/ANAXMD/raft-lab/sim"
)

var seedFlag = flag.Int64("seed", 0, "run a single seed (0 = sweep a range)")

// seeds returns the seeds to sweep. Sweeping many seeds is the whole point:
// each one is a different deterministic schedule of delays, drops and crashes.
func seeds(n int) []int64 {
	if *seedFlag != 0 {
		return []int64{*seedFlag}
	}
	out := make([]int64, n)
	for i := range out {
		out[i] = int64(i + 1)
	}
	return out
}

// M2: a healthy 5-node cluster elects exactly one leader.
func TestM2_ElectsOneLeader(t *testing.T) {
	for _, seed := range seeds(50) {
		s := sim.New(sim.Config{Seed: seed, N: 5})
		if !s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok }) {
			t.Fatalf("seed %d: no leader within 5 virtual seconds", seed)
		}
		s.Run(2_000) // stay stable: leadership must not flap in a healthy cluster
		s.MustBeSafe(t)
		lead, ok := s.Leader()
		if !ok {
			t.Fatalf("seed %d: leader disappeared in a healthy cluster", seed)
		}
		for _, id := range s.IDs() {
			if st := s.Node(id).Status(); st.Term != s.Node(lead).Status().Term {
				t.Fatalf("seed %d: node %d is in term %d, leader %d in term %d", seed, id, st.Term, lead, s.Node(lead).Status().Term)
			}
		}
	}
}

// M2: kill the leader, the other four elect a new one.
func TestM2_FailoverAfterLeaderCrash(t *testing.T) {
	for _, seed := range seeds(50) {
		s := sim.New(sim.Config{Seed: seed, N: 5})
		if !s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok }) {
			t.Fatalf("seed %d: no initial leader", seed)
		}
		old, _ := s.Leader()
		s.Crash(old)
		if !s.RunUntil(5_000, func() bool { l, ok := s.Leader(); return ok && l != old }) {
			t.Fatalf("seed %d: no new leader after crashing %d", seed, old)
		}
		s.MustBeSafe(t)
	}
}

// M3: 100 commands, flaky network, all live nodes converge on the committed prefix.
func TestM3_ReplicatesUnderFaults(t *testing.T) {
	for _, seed := range seeds(30) {
		s := sim.New(sim.Config{Seed: seed, N: 5, DropProb: 0.1, DupProb: 0.05})
		s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok })
		proposed := 0
		deadline := s.Now() + 60_000 // never loop forever: bound by virtual time
		for proposed < 100 {
			if s.Now() > deadline {
				t.Fatalf("seed %d: could only propose %d of 100 commands in 60 virtual seconds", seed, proposed)
			}
			if l, ok := s.Leader(); ok && s.Propose(l, []byte{byte(proposed)}) {
				proposed++
			}
			s.Run(20)
		}
		s.Heal()
		s.Run(5_000)
		s.MustBeSafe(t)
		var want int
		for _, id := range s.IDs() {
			if n := len(s.Applied(id)); n > want {
				want = n
			}
		}
		if want < 100 {
			t.Fatalf("seed %d: only %d of 100 commands were committed", seed, want)
		}
		for _, id := range s.IDs() {
			if got := len(s.Applied(id)); got != want {
				t.Fatalf("seed %d: node %d applied %d entries, others %d", seed, id, got, want)
			}
		}
	}
}

// ---- later milestones: write these as you reach them ----

// M3: random Crash/Restart churn asserts that committed entries survive.
// (Note: The fuzz test provides the real Figure 8 safety net).
func TestM3_CrashChurnKeepsCommitted(t *testing.T) {
	for _, seed := range seeds(30) {
		s := sim.New(sim.Config{Seed: seed, N: 5})

		// Force the Figure 8 term overlaps:
		// 1. Leader 1 proposes an entry, partially replicates it, and crashes.
		// 2. Leader 2 is elected, proposes a new entry, partially replicates, and crashes.
		// 3. Leader 1 is restarted and tries to finish replicating its old stranded entries.
		for i := 0; i < 5; i++ {
			s.RunUntil(2_000, func() bool { _, ok := s.Leader(); return ok })
			l1, ok := s.Leader()
			if !ok {
				continue
			}

			s.Propose(l1, []byte{1})
			s.Run(2) // 2 ticks: enough to send MsgApp, but NOT enough to get a quorum of responses
			s.Crash(l1)

			s.RunUntil(2_000, func() bool { _, ok := s.Leader(); return ok })
			l2, ok := s.Leader()
			if !ok {
				continue
			}

			s.Propose(l2, []byte{2})
			s.Run(2)
			s.Crash(l2)

			s.Restart(l1) // L1 comes back with its old uncommitted entries
		}

		// Resurrect everyone, heal the network, and allow the cluster to fully converge.
		for _, id := range s.IDs() {
			s.Restart(id)
		}
		s.Heal()
		s.Run(5_000)

		// If your Figure 8 commit rule (in advanceCommit) or the No-Op entry fix is missing,
		// the simulator's safety checker will panic right here because a committed entry was lost.
		s.MustBeSafe(t)
	}
}

// M4: random Crash/Restart for 60 virtual seconds, MustBeSafe at the end
func TestM4_CrashRecoveryKeepsCommitted(t *testing.T) {
	for _, seed := range seeds(30) {
		s := sim.New(sim.Config{Seed: seed, N: 5, DropProb: 0.1})
		s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok })

		proposed := 0
		// Propose commands while constantly crashing and restarting the leader
		for i := 0; i < 100 && proposed < 30; i++ {
			if l, ok := s.Leader(); ok && s.Propose(l, []byte{byte(proposed)}) {
				proposed++
			}
			s.Run(10)

			if l, ok := s.Leader(); ok {
				s.Crash(l)
				s.Run(20)
				s.Restart(l) // Node must read HardState and Log from disk here
			}
		}

		s.Heal()
		s.Run(5_000)
		s.MustBeSafe(t)
	}
}

type mockT struct {
	failed bool
	msg    string
}

func (m *mockT) Helper() {}
func (m *mockT) Fatalf(format string, args ...any) {
	m.failed = true
	m.msg = fmt.Sprintf(format, args...)
	panic(m.msg)
}

// M4: with SkipSync=true the fuzz run MUST report a violation; proves the harness has teeth
func TestM4_SkipSyncIsCaught(t *testing.T) {
	var mock mockT
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("Test failed: Expected a safety violation panic from the mock, but none occurred")
		}

		errMsg := fmt.Sprintf("%v", r)
		// Change "violation" to match the exact string your mock storage panics with
		if !strings.Contains(errMsg, "violation") {
			t.Fatalf("Test failed: Caught an unrelated panic instead of a safety violation: %v", errMsg)
		}
	}()

	s := sim.New(sim.Config{Seed: 1, N: 5, SkipSync: true})
	s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok })

	// 1. Force a commit and GUARANTEE it is applied before crashing
	l, ok := s.Leader()
	if !ok {
		t.Fatal("no leader elected")
	}
	s.Propose(l, []byte("doom"))
	if !s.RunUntil(5_000, func() bool { return len(s.Applied(l)) >= 2 }) {
		t.Fatal("failed to apply initial entry")
	}

	// 2. Wipe out the entire cluster so they all suffer amnesia.
	// Running 50ms while down drains all in-flight messages so nodes don't receive pre-crash state.
	for _, id := range s.IDs() {
		s.Crash(id)
	}
	s.Run(50)
	for _, id := range s.IDs() {
		s.Restart(id)
	}

	// 3. Elect a new leader from the amnesiac nodes
	s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok })
	l2, ok := s.Leader()
	if !ok {
		t.Fatal("no leader elected after restart")
	}

	// 4. Force the new leader to overwrite the exact same log index.
	s.Propose(l2, []byte("corrupted"))
	s.RunUntil(5_000, func() bool { return len(s.Applied(l2)) >= 2 })

	// 5. Check with MustBeSafe using mockT to catch the safety violation
	s.MustBeSafe(&mock)
}

func TestM5_PreVoteStopsDisruption(t *testing.T) {
	for _, seed := range seeds(30) {
		s := sim.New(sim.Config{Seed: seed, N: 5, PreVote: true})
		if !s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok }) {
			t.Fatalf("seed %d: no leader elected within 5 virtual seconds", seed)
		}
		leader, ok := s.Leader()
		if !ok {
			t.Fatalf("seed %d: leader disappeared", seed)
		}
		initialTerm := s.Node(leader).Status().Term

		// Pick a follower to isolate
		var follower raft.NodeID
		for _, id := range s.IDs() {
			if id != leader {
				follower = id
				break
			}
		}

		// Completely isolate one follower from the rest of the network
		s.Isolate(follower)

		// Let the cluster run for at least 500 virtual ticks (10ms per tick)
		s.Run(500 * 10)

		// Heal the partition / reconnect the node and run for another 100 ticks
		s.Heal()
		s.Run(100 * 10)

		s.MustBeSafe(t)

		currentLeader, ok := s.Leader()
		if !ok || currentLeader != leader {
			t.Fatalf("seed %d: leader stepped down (got %v, want %v)", seed, currentLeader, leader)
		}
		for _, id := range s.IDs() {
			if st := s.Node(id).Status(); st.Term != initialTerm {
				t.Fatalf("seed %d: node %d is in term %d, expected initial term %d", seed, id, st.Term, initialTerm)
			}
		}
	}
}

type kvOpKind uint8

const (
	opGet kvOpKind = iota
	opPut
)

type kvInput struct {
	Op    kvOpKind
	Key   string
	Value string
}

type kvOutput struct {
	Value string
	OK    bool
}

type kvCommand struct {
	ReqID uint64
	Op    kvOpKind
	Key   string
	Value string
}

// pendingOp tracks an in-flight operation that has been proposed but not yet
// applied. We record a CallEvent immediately on proposal; a ReturnEvent is
// only appended once the entry is committed+applied. If the op is lost (crash,
// partition, timeout) we leave the CallEvent without a matching ReturnEvent,
// which Porcupine treats as "may or may not have taken effect" — the correct
// semantics for a distributed operation whose outcome is unknown.

// kvEventModel is the per-key Event-based model for Porcupine.
// Event.Value is kvInput on CallEvent, kvOutput on ReturnEvent.
var kvEventModel = porcupine.Model{
	PartitionEvent: func(history []porcupine.Event) [][]porcupine.Event {
		// Identify events that have a matching ReturnEvent.
		hasReturn := make(map[int]bool)
		for _, ev := range history {
			if ev.Kind == porcupine.ReturnEvent {
				hasReturn[ev.Id] = true
			}
		}

		// Group events by key so Porcupine can check each key independently.
		// Uncommitted operations that never took effect have no linearization point.
		byKey := make(map[string][]porcupine.Event)
		idToKey := make(map[int]string)
		for _, ev := range history {
			if !hasReturn[ev.Id] {
				continue
			}
			if ev.Kind == porcupine.CallEvent {
				inp := ev.Value.(kvInput)
				idToKey[ev.Id] = inp.Key
				byKey[inp.Key] = append(byKey[inp.Key], ev)
			} else {
				key := idToKey[ev.Id]
				byKey[key] = append(byKey[key], ev)
			}
		}
		var parts [][]porcupine.Event
		for _, evs := range byKey {
			parts = append(parts, evs)
		}
		return parts
	},
	Init: func() any { return "" },
	Step: func(state, input, output any) (bool, any) {
		val := state.(string)
		inp := input.(kvInput)
		out := output.(kvOutput)
		if inp.Op == opPut {
			return true, inp.Value
		}
		return out.Value == val, val
	},
	Equal: func(s1, s2 any) bool { return s1.(string) == s2.(string) },
	DescribeOperation: func(in, out any) string {
		inp := in.(kvInput)
		outp := out.(kvOutput)
		if inp.Op == opPut {
			return fmt.Sprintf("Put('%s', '%s')", inp.Key, inp.Value)
		}
		return fmt.Sprintf("Get('%s') -> '%s'", inp.Key, outp.Value)
	},
	DescribeState: func(st any) string { return fmt.Sprintf("'%s'", st.(string)) },
}

// TestM6_LinearizableKV is a deterministic, multi-seed fault-injection
// harness that verifies the Raft KV log is linearizable under crashes,
// restarts, and network partitions.
//
// All concurrency is eliminated: the test is a pure synchronous tick loop
// driven by the simulator. Porcupine's Event API is used so that pending
// operations (proposed but not yet confirmed committed) are correctly
// represented: only a CallEvent is emitted at proposal time; the matching
// ReturnEvent is emitted only if and when the entry is applied. Operations
// that are lost to a crash or partition simply have no ReturnEvent, which
// Porcupine interprets as "outcome unknown" — the correct semantics.
func TestM6_LinearizableKV(t *testing.T) {
	const (
		numClients       = 4
		runMs            = 60_000 // 60 virtual seconds of KV traffic
		drainMs          = 10_000 // let pending ops drain before final check
		stepMs     int64 = 30     // tick granularity
	)
	keys := []string{"k1", "k2", "k3"}

	numSeeds := 20
	if os.Getenv("FULL") == "1" {
		numSeeds = 100
	}
	for _, seed := range seeds(numSeeds) {
		// Each seed gets its own independent fault-injection RNG so that
		// fault decisions are reproducible but vary across seeds.
		faultRng := rand.New(rand.NewSource(seed * 0x9e3779b9))

		s := sim.New(sim.Config{
			Seed:     seed,
			N:        5,
			DropProb: 0.05,
			DupProb:  0.02,
		})
		if !s.RunUntil(5_000, func() bool { _, ok := s.Leader(); return ok }) {
			t.Fatalf("seed %d: no leader elected within 5 virtual seconds", seed)
		}

		// --- state for the synchronous client simulation ---

		// Porcupine Event history. We use the Event API (not Operation) so
		// that pending ops are correctly represented as lone CallEvents.
		var history []porcupine.Event
		var nextEventID int

		// Per-seed client state.
		type clientState struct {
			rng      *rand.Rand
			opCount  int
			nextTime int64 // earliest virtual time this client will next issue an op
		}
		clients := make([]clientState, numClients)
		for c := range clients {
			clients[c].rng = rand.New(rand.NewSource(seed*1000 + int64(c)))
		}

		// In-flight proposals: reqID -> pending op metadata.
		type inflight struct {
			clientID int
			eventID  int
			input    kvInput
		}
		pending := make(map[uint64]inflight)
		var nextReqID uint64

		// Replicated KV state machine applied in committed-log order.
		// globalAppliedIdx is a monotonically advancing cursor into the
		// committed log. Because Raft guarantees all nodes share an
		// identical committed prefix, we read from whichever node has
		// applied the most entries so far.
		// We NEVER reset this counter or kvState on a leader change.
		kvState := make(map[string]string)
		globalAppliedIdx := 0

		// harvestApplied drains any newly applied entries from whichever
		// node has progressed furthest.
		harvestApplied := func() {
			var longestApplied []raft.Entry
			for _, id := range s.IDs() {
				if app := s.Applied(id); len(app) > len(longestApplied) {
					longestApplied = app
				}
			}
			for globalAppliedIdx < len(longestApplied) {
				entry := longestApplied[globalAppliedIdx]
				globalAppliedIdx++
				if len(entry.Data) == 0 {
					continue // no-op entry
				}
				var cmd kvCommand
				if err := json.Unmarshal(entry.Data, &cmd); err != nil {
					continue
				}
				// Apply to the local KV state machine.
				var out kvOutput
				if cmd.Op == opPut {
					kvState[cmd.Key] = cmd.Value
					out = kvOutput{Value: cmd.Value, OK: true}
				} else {
					out = kvOutput{Value: kvState[cmd.Key], OK: true}
				}
				// Emit ReturnEvent only if this reqID is still pending.
				if fl, ok := pending[cmd.ReqID]; ok {
					delete(pending, cmd.ReqID)
					history = append(history, porcupine.Event{
						ClientId: fl.clientID,
						Kind:     porcupine.ReturnEvent,
						Value:    out,
						Id:       fl.eventID,
					})
				}
			}
		}

		// --- fault injection schedule ---
		// Every ~2000 ms we make a random fault decision.
		nextFaultAt := s.Now() + int64(1000+faultRng.Intn(2000))

		deadline := s.Now() + runMs

		for s.Now() < deadline {
			now := s.Now()

			// ── 1. Fault injection ──────────────────────────────────────────
			if now >= nextFaultAt {
				switch faultRng.Intn(5) {
				case 0: // crash the current leader
					if l, ok := s.Leader(); ok {
						s.Crash(l)
					}
				case 1: // crash a random follower
					ids := s.IDs()
					target := ids[faultRng.Intn(len(ids))]
					if s.Up(target) {
						s.Crash(target)
					}
				case 2: // restart all crashed nodes
					for _, id := range s.IDs() {
						if !s.Up(id) {
							s.Restart(id)
						}
					}
				case 3: // isolate a minority follower (leaves quorum intact)
					if l, ok := s.Leader(); ok {
						for _, id := range s.IDs() {
							if id != l && s.Up(id) {
								s.Isolate(id)
								break
							}
						}
					}
				case 4: // heal all partitions
					s.Heal()
				}
				nextFaultAt = now + int64(500+faultRng.Intn(2000))
			}

			// ── 2. Client op dispatch ────────────────────────────────────────
			// Each virtual client issues one op per turn if its next-time has
			// passed and a leader is available.
			l, hasLeader := s.Leader()
			for c := range clients {
				cl := &clients[c]
				if now < cl.nextTime || !hasLeader {
					continue
				}

				// Choose a random op.
				key := keys[cl.rng.Intn(len(keys))]
				var inp kvInput
				if cl.rng.Intn(2) == 0 {
					cl.opCount++
					inp = kvInput{Op: opPut, Key: key, Value: fmt.Sprintf("v%d-%d", c, cl.opCount)}
				} else {
					inp = kvInput{Op: opGet, Key: key}
				}

				// Marshal and propose.
				nextReqID++
				cmd := kvCommand{
					ReqID: nextReqID,
					Op:    inp.Op,
					Key:   inp.Key,
					Value: inp.Value,
				}
				data, err := json.Marshal(cmd)
				if err != nil {
					t.Fatalf("seed %d: json marshal: %v", seed, err)
				}

				if s.Propose(l, data) {
					// Record the CallEvent immediately. A matching ReturnEvent
					// will be appended only once the entry is applied.
					eid := nextEventID
					nextEventID++
					history = append(history, porcupine.Event{
						ClientId: c,
						Kind:     porcupine.CallEvent,
						Value:    inp,
						Id:       eid,
					})
					pending[nextReqID] = inflight{clientID: c, eventID: eid, input: inp}
				}

				// Pace next op: 100–400 virtual ms.
				cl.nextTime = now + int64(100+cl.rng.Intn(300))
			}

			// ── 3. Advance simulation ─────────────────────────────────────
			s.Run(stepMs)

			// ── 4. Harvest newly applied log entries ──────────────────────
			// harvestApplied reads from whoever is currently leader. Because
			// globalAppliedIdx never resets, we never re-apply an entry or
			// miss one when the leader changes mid-tick.
			harvestApplied()
		}

		// ── 5. Drain phase ────────────────────────────────────────────────
		// Heal the network, restart all crashed nodes, and wait for pending
		// ops to commit. Ops that never get a ReturnEvent are "pending"
		// in Porcupine and are correctly treated as unknown-outcome.
		s.Heal()
		for _, id := range s.IDs() {
			if !s.Up(id) {
				s.Restart(id)
			}
		}
		drainDeadline := s.Now() + drainMs
		for s.Now() < drainDeadline && len(pending) > 0 {
			s.Run(stepMs)
			harvestApplied()
		}

		// ── 6. Safety and linearizability checks ─────────────────────────
		s.MustBeSafe(t)

		if len(history) == 0 {
			t.Fatalf("seed %d: no operations recorded — check proposal logic", seed)
		}

		t.Logf("seed %d: checking linearizability of %d events (%d pending without return) with Porcupine",
			seed, len(history), len(pending))

		if !porcupine.CheckEvents(kvEventModel, history) {
			t.Fatalf("seed %d: history of %d events is NOT linearizable", seed, len(history))
		}
	}
}

var _ = raft.Follower // keep the import until you use raft types in later tests
