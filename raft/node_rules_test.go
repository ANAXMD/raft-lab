package raft_test

// Node-level tests of core Raft rules (audit items 1-6 and 13).
//
// Each test documents: the invariant, the exact setup and message sequence,
// the expected result, and what would make it fail.

import (
	"testing"

	"github.com/ANAXMD/raft-lab/raft"
)

var peers3 = []raft.NodeID{1, 2, 3}

func app(term raft.Term, prevIdx raft.Index, prevTerm raft.Term, commit raft.Index, es []raft.Entry) raft.Message {
	return raft.Message{Type: raft.MsgApp, From: 1, To: 2, Term: term,
		PrevLogIndex: prevIdx, PrevLogTerm: prevTerm, LeaderCommit: commit, Entries: es}
}

// N01 ------------------------------------------------------------------------
// INVARIANT: a follower truncates its log only on a real conflict (same index,
// different term). A stale or duplicated AppendEntries whose entries match what
// the follower already has must change nothing: no truncation, no re-persist, no
// commit regression.
// SETUP: follower 2 (term 1) receives entries 1..3 (term 1). Then, one at a time:
//
//	(a) the identical message again; (b) an older, shorter message [1];
//	(c) an older message [2] with prev=(1,t1); (d) an empty heartbeat prev=(3,t1).
//
// Then the follower commits 2 and the older messages are replayed again.
// EXPECTED: log stays [1,2,3] term 1; Ready.Entries empty; Ready.HardState nil;
// each reply is accepted with MatchIndex = that message's last entry; commit
// never goes down.
// FAILS IF: the follower truncates on a prefix/duplicate (mutation M1), or marks
// the log dirty and re-persists it, or lowers its commit.
func TestN01_StaleAndDuplicateAppendNeverTruncate(t *testing.T) {
	n := raft.New(makeCfg(2, peers3, opts{}), raft.Persisted{})
	full := ents(1, 1, "a", "b", "c")
	n.Step(app(1, 0, 0, 0, full))
	rd := drain(n)
	if len(rd.Entries) != 3 {
		t.Fatalf("setup: want 3 entries persisted, got %d", len(rd.Entries))
	}
	want := n.Log()

	replays := []struct {
		name  string
		m     raft.Message
		match raft.Index
	}{
		{"exact duplicate", app(1, 0, 0, 0, full), 3},
		{"older shorter message [1]", app(1, 0, 0, 0, ents(1, 1, "a")), 1},
		{"older message [2] with prev=(1,t1)", app(1, 1, 1, 0, ents(1, 2, "b")), 2},
		{"empty heartbeat prev=(3,t1)", app(1, 3, 1, 0, nil), 3},
	}
	check := func(phase string, commit raft.Index) {
		for _, r := range replays {
			m := r.m
			m.LeaderCommit = 0 // stale messages carry an old (lower) commit
			n.Step(m)
			rd := drain(n)
			if !sameEntries(n.Log(), want) {
				t.Fatalf("%s/%s: log changed to %v (terms %v), want it untouched", phase, r.name, idxs(n.Log()), terms(n.Log()))
			}
			if len(rd.Entries) != 0 || rd.HardState != nil {
				t.Fatalf("%s/%s: stale message caused persistence work: entries=%d hardstate=%v", phase, r.name, len(rd.Entries), rd.HardState)
			}
			resp := mustOne(t, rd, raft.MsgAppResp, 1)
			if resp.Reject || resp.MatchIndex != r.match {
				t.Fatalf("%s/%s: reply reject=%v match=%d, want accepted with match=%d", phase, r.name, resp.Reject, resp.MatchIndex, r.match)
			}
			if got := n.Status().Commit; got != commit {
				t.Fatalf("%s/%s: commit=%d, want %d (must never go down)", phase, r.name, got, commit)
			}
		}
	}
	check("phase1", 0)

	n.Step(app(1, 3, 1, 2, nil)) // leader tells the follower to commit 2
	drain(n)
	if n.Status().Commit != 2 {
		t.Fatalf("setup: commit=%d, want 2", n.Status().Commit)
	}
	check("phase2", 2)
}

// N02 ------------------------------------------------------------------------
// INVARIANT: on a real conflict the follower deletes the conflicting entry and
// everything after it, and persists the replacement from exactly that index;
// entries before the conflict are untouched; entries it already has are not
// rewritten.
// SETUP: follower 2 has [1:t1,2:t1,3:t1,4:t1]. Leader (term 2) sends
// prev=(2,t1) with [3:t2,4:t2,5:t2]. Then prev=(4,t2) with [5:t2,6:t2].
// EXPECTED: after the first, terms [1,1,2,2,2], Ready.Entries starts at index 3,
// MatchIndex 5. After the second, 5 is not rewritten: Ready.Entries is [6] only.
// FAILS IF: truncation happens at the wrong index, rewrites matching entries, or
// leaves a stale suffix.
func TestN02_ConflictTruncatesExactlyFromFirstConflict(t *testing.T) {
	p := raft.Persisted{HardState: raft.HardState{Term: 1}, Log: ents(1, 1, "a", "b", "c", "d")}
	n := raft.New(makeCfg(2, peers3, opts{}), p)

	n.Step(app(2, 2, 1, 0, ents(2, 3, "C", "D", "E")))
	rd := drain(n)
	if got := terms(n.Log()); len(got) != 5 || got[0] != 1 || got[1] != 1 || got[2] != 2 || got[3] != 2 || got[4] != 2 {
		t.Fatalf("log terms after conflict = %v, want [1 1 2 2 2]", got)
	}
	if len(rd.Entries) != 3 || rd.Entries[0].Index != 3 {
		t.Fatalf("persisted entries %v, want replacement starting at index 3 with 3 entries", idxs(rd.Entries))
	}
	if string(n.Log()[0].Data) != "a" || string(n.Log()[1].Data) != "b" {
		t.Fatalf("entries before the conflict were modified")
	}
	if r := mustOne(t, rd, raft.MsgAppResp, 1); r.Reject || r.MatchIndex != 5 {
		t.Fatalf("reply reject=%v match=%d, want accepted match=5", r.Reject, r.MatchIndex)
	}

	n.Step(app(2, 4, 2, 0, ents(2, 5, "E", "F")))
	rd = drain(n)
	if len(rd.Entries) != 1 || rd.Entries[0].Index != 6 {
		t.Fatalf("persisted %v, want only the new entry 6 (entry 5 already matched)", idxs(rd.Entries))
	}
}

// N03 ------------------------------------------------------------------------
// INVARIANT: a follower's commit index is min(leaderCommit, index of the last
// NEW entry in this message), not min(leaderCommit, its own last index). Entries
// beyond what this leader verified may belong to an old, different log.
// SETUP: follower 2 (term 1) has [1..5] term 1 (3..5 may be divergent). New
// leader (term 2) sends: (a) a heartbeat prev=(2,t1), no entries, commit=5;
// then (b) prev=(2,t1) with [3:t2], commit=5; then (c) prev=(3,t2), no entries,
// commit=9 (larger than the log).
// EXPECTED: (a) commit 2, committed entries [1,2]; (b) the stale 4,5 are removed,
// commit 3; (c) commit stays 3 (min(9,3)).
// FAILS IF: commit uses the follower's own last index (mutation M2): (a) would
// commit 5, applying entries the leader never confirmed.
func TestN03_FollowerCommitUsesLastNewEntry(t *testing.T) {
	p := raft.Persisted{HardState: raft.HardState{Term: 1}, Log: ents(1, 1, "a", "b", "c", "d", "e")}
	n := raft.New(makeCfg(2, peers3, opts{}), p)

	n.Step(app(2, 2, 1, 5, nil))
	rd := drain(n)
	if n.Status().Commit != 2 {
		t.Fatalf("(a) commit=%d, want 2 (heartbeat verified only up to index 2)", n.Status().Commit)
	}
	if got := idxs(rd.Committed); len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("(a) committed entries %v, want [1 2]", got)
	}

	n.Step(app(2, 2, 1, 5, ents(2, 3, "C")))
	rd = drain(n)
	if n.Status().Commit != 3 || n.Status().LastIndex != 3 {
		t.Fatalf("(b) commit=%d last=%d, want 3 and 3 (suffix replaced)", n.Status().Commit, n.Status().LastIndex)
	}
	if got := idxs(rd.Committed); len(got) != 1 || got[0] != 3 || string(rd.Committed[0].Data) != "C" {
		t.Fatalf("(b) committed %v, want only the new entry 3", got)
	}

	n.Step(app(2, 3, 2, 9, nil))
	drain(n)
	if n.Status().Commit != 3 {
		t.Fatalf("(c) commit=%d, want 3 (min(9, last new entry 3))", n.Status().Commit)
	}
}

// N04 ------------------------------------------------------------------------
// INVARIANT (Raft paper, Figure 8): a leader never commits an entry from a
// previous term by counting replicas. It commits only once an entry from its OWN
// term is on a quorum, which then commits the earlier entries indirectly.
// SETUP: node 1 has persisted [1:t1, 2:t2], term 2. It times out, wins term 3
// with a vote from node 2, appends its no-op at index 3. Node 2 then acks:
// first MatchIndex=2 (only the old-term entry), then MatchIndex=3.
// EXPECTED: after the first ack commit is still 0 although entry 2 is on a
// majority; after the second, commit is 3 and entries [1,2,3] are delivered.
// FAILS IF: previous-term entries are counted toward commit (mutation M3).
func TestN04_Figure8_PreviousTermNotCommittedByCounting(t *testing.T) {
	p := raft.Persisted{HardState: raft.HardState{Term: 2}, Log: []raft.Entry{
		{Term: 1, Index: 1, Data: []byte("a")}, {Term: 2, Index: 2, Data: []byte("b")}}}
	n, _ := electLeader(t, 1, peers3, p, opts{}, 2)
	term := n.Status().Term
	if term != 3 || n.Status().LastIndex != 3 {
		t.Fatalf("setup: term=%d last=%d, want term 3 and the no-op at index 3", term, n.Status().LastIndex)
	}

	n.Step(raft.Message{Type: raft.MsgAppResp, From: 2, To: 1, Term: term, MatchIndex: 2})
	rd := drain(n)
	if n.Status().Commit != 0 || len(rd.Committed) != 0 {
		t.Fatalf("commit=%d committed=%v after an ack of only the term-2 entry; must stay 0", n.Status().Commit, idxs(rd.Committed))
	}

	n.Step(raft.Message{Type: raft.MsgAppResp, From: 2, To: 1, Term: term, MatchIndex: 3})
	rd = drain(n)
	if n.Status().Commit != 3 {
		t.Fatalf("commit=%d after the current-term entry reached a quorum, want 3", n.Status().Commit)
	}
	if got := idxs(rd.Committed); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
		t.Fatalf("committed %v, want [1 2 3]", got)
	}
}

// N05 ------------------------------------------------------------------------
// INVARIANT: a rejection moves the leader's nextIndex for that follower
// strictly backwards (until it reaches 1), never below 1, never above the log,
// never up because of a large hint; every resend is internally consistent
// (entries start at prev+1, prevTerm matches the leader's log); and duplicated
// rejections cannot wedge or wrap the index.
// SETUP: leader 1 (term 2) with log [1..5 t1] + no-op 6. Node 2 rejects with
// hints 100, 100, 3, then 0 repeated 21 times (duplicates).
// EXPECTED: prevLogIndex never increases and stays within [0, 6]; it ends at 0;
// afterwards an accepted ack of 6 commits 6.
// FAILS IF: nextIndex underflows or jumps up, the resend is inconsistent, or the
// leader stops making progress after duplicate rejections.
func TestN05_RejectHintBoundsAndDuplicateRejects(t *testing.T) {
	p := raft.Persisted{HardState: raft.HardState{Term: 1}, Log: ents(1, 1, "a", "b", "c", "d", "e")}
	n, rd := electLeader(t, 1, peers3, p, opts{}, 2)
	term := n.Status().Term
	prev := mustOne(t, rd, raft.MsgApp, 2).PrevLogIndex
	if prev != 5 {
		t.Fatalf("setup: first append prev=%d, want 5", prev)
	}

	hints := []raft.Index{100, 100, 3}
	for i := 0; i < 21; i++ {
		hints = append(hints, 0)
	}
	for i, h := range hints {
		n.Step(raft.Message{Type: raft.MsgAppResp, From: 2, To: 1, Term: term, Reject: true, RejectHint: h})
		rd := drain(n)
		apps := find(rd, raft.MsgApp, 2)
		if len(apps) == 0 {
			t.Fatalf("reject #%d (hint %d): leader sent no retry", i, h)
		}
		m := apps[len(apps)-1]
		if m.PrevLogIndex > prev {
			t.Fatalf("reject #%d (hint %d): prev rose from %d to %d", i, h, prev, m.PrevLogIndex)
		}
		if i == 0 && m.PrevLogIndex >= prev {
			t.Fatalf("reject #%d: prev did not move back (still %d)", i, m.PrevLogIndex)
		}
		if m.PrevLogIndex > 6 {
			t.Fatalf("reject #%d: prev=%d is beyond the leader's log (6)", i, m.PrevLogIndex)
		}
		if len(m.Entries) > 0 && m.Entries[0].Index != m.PrevLogIndex+1 {
			t.Fatalf("reject #%d: entries start at %d but prev=%d", i, m.Entries[0].Index, m.PrevLogIndex)
		}
		if m.PrevLogIndex > 0 && m.PrevLogTerm != n.Log()[m.PrevLogIndex-1].Term {
			t.Fatalf("reject #%d: prevTerm=%d does not match the leader's log", i, m.PrevLogTerm)
		}
		prev = m.PrevLogIndex
	}
	if prev != 0 {
		t.Fatalf("after all rejections prev=%d, want 0", prev)
	}

	n.Step(raft.Message{Type: raft.MsgAppResp, From: 2, To: 1, Term: term, MatchIndex: 6})
	drain(n)
	if n.Status().Commit != 6 {
		t.Fatalf("commit=%d after an accepted ack of 6, want 6 (leader must still make progress)", n.Status().Commit)
	}
}

// N06 ------------------------------------------------------------------------
// INVARIANT: a vote is recorded in HardState in the SAME Ready as the reply that
// grants it, so a driver that persists before sending can never send a grant for
// a vote it could forget. At most one candidate is granted per term; granting the
// same candidate again is idempotent; a candidate with a less up-to-date log is
// refused (but a higher term is still adopted).
// SETUP / EXPECTED: see the sub-tests below.
// FAILS IF: a grant appears without HardState{VotedFor} in that Ready (mutation
// M4), a second candidate is granted in the same term, or a stale log wins.
func TestN06_VotePersistedWithReplyAndSingleVotePerTerm(t *testing.T) {
	vote := func(from raft.NodeID, term raft.Term, lastIdx raft.Index, lastTerm raft.Term) raft.Message {
		return raft.Message{Type: raft.MsgVote, From: from, To: 2, Term: term, LastLogIndex: lastIdx, LastLogTerm: lastTerm}
	}

	t.Run("grant_is_persisted_in_the_same_ready", func(t *testing.T) {
		n := raft.New(makeCfg(2, peers3, opts{}), raft.Persisted{})
		n.Step(vote(1, 1, 0, 0))
		rd := drain(n)
		if rd.HardState == nil || rd.HardState.Term != 1 || rd.HardState.VotedFor != 1 {
			t.Fatalf("HardState in the same Ready = %+v, want term 1 voted for 1", rd.HardState)
		}
		if r := mustOne(t, rd, raft.MsgVoteResp, 1); r.Reject {
			t.Fatalf("vote was refused, want granted")
		}
	})

	// The node is already in term 1 (no vote cast yet), so no term change marks
	// the state dirty: the grant itself must. This is the case that distinguishes
	// "vote recorded for persistence" from "vote only held in memory".
	t.Run("grant_in_current_term_is_persisted_without_term_change", func(t *testing.T) {
		n := raft.New(makeCfg(2, peers3, opts{}), raft.Persisted{HardState: raft.HardState{Term: 1}})
		n.Step(vote(1, 1, 0, 0))
		rd := drain(n)
		if r := mustOne(t, rd, raft.MsgVoteResp, 1); r.Reject {
			t.Fatalf("vote was refused, want granted")
		}
		if rd.HardState == nil || rd.HardState.Term != 1 || rd.HardState.VotedFor != 1 {
			t.Fatalf("granted a vote in the current term but HardState in the same Ready = %+v, want term 1 voted for 1", rd.HardState)
		}
	})

	t.Run("same_candidate_again_is_idempotent_and_other_candidate_refused", func(t *testing.T) {
		n := raft.New(makeCfg(2, peers3, opts{}), raft.Persisted{})
		n.Step(vote(1, 1, 0, 0))
		drain(n)

		n.Step(vote(1, 1, 0, 0))
		rd := drain(n)
		if r := mustOne(t, rd, raft.MsgVoteResp, 1); r.Reject {
			t.Fatalf("repeat request from the same candidate was refused")
		}
		if rd.HardState != nil {
			t.Fatalf("repeat grant changed HardState: %+v", rd.HardState)
		}

		n.Step(vote(3, 1, 0, 0))
		rd = drain(n)
		if r := mustOne(t, rd, raft.MsgVoteResp, 3); !r.Reject {
			t.Fatalf("a second candidate was granted a vote in the same term")
		}
		if rd.HardState != nil {
			t.Fatalf("refusing changed HardState: %+v", rd.HardState)
		}
	})

	t.Run("persisted_vote_survives_restart", func(t *testing.T) {
		p := raft.Persisted{HardState: raft.HardState{Term: 1, VotedFor: 1}}
		n := raft.New(makeCfg(2, peers3, opts{}), p)
		n.Step(vote(3, 1, 0, 0))
		if r := mustOne(t, drain(n), raft.MsgVoteResp, 3); !r.Reject {
			t.Fatalf("after restart the node granted a second vote in a term it had already voted in")
		}
	})

	t.Run("stale_log_refused_but_higher_term_adopted", func(t *testing.T) {
		p := raft.Persisted{HardState: raft.HardState{Term: 1}, Log: ents(1, 1, "a", "b")}
		n := raft.New(makeCfg(2, peers3, opts{}), p)
		n.Step(vote(3, 2, 1, 1)) // shorter log, same last term
		rd := drain(n)
		if r := mustOne(t, rd, raft.MsgVoteResp, 3); !r.Reject {
			t.Fatalf("granted a vote to a candidate with a shorter log")
		}
		if rd.HardState == nil || rd.HardState.Term != 2 || rd.HardState.VotedFor != 0 {
			t.Fatalf("HardState = %+v, want term 2 with no vote", rd.HardState)
		}
	})

	t.Run("higher_last_term_wins_even_with_shorter_log", func(t *testing.T) {
		p := raft.Persisted{HardState: raft.HardState{Term: 1}, Log: ents(1, 1, "a", "b")}
		n := raft.New(makeCfg(2, peers3, opts{}), p)
		n.Step(vote(3, 2, 1, 2)) // last term 2 beats last term 1
		rd := drain(n)
		if r := mustOne(t, rd, raft.MsgVoteResp, 3); r.Reject {
			t.Fatalf("refused a candidate whose last log term is higher")
		}
		if rd.HardState == nil || rd.HardState.Term != 2 || rd.HardState.VotedFor != 3 {
			t.Fatalf("HardState = %+v, want term 2 voted for 3 in the same Ready", rd.HardState)
		}
	})
}

// N13 ------------------------------------------------------------------------
// INVARIANT: after a restart (raft.New with persisted state) commit and applied
// start at 0, nothing is dirty, and as commit advances every log entry is
// delivered in Ready.Committed exactly once and in order. A second restart
// starts over from entry 1.
// SETUP: follower 2 restarted with [1..5] term 1. Leader heartbeats carry
// commit 3, then 5, then 5 again, then a stale 3. A single-node cluster is also
// restarted with [1,2].
// EXPECTED: first Ready is empty; committed batches [1,2,3], [4,5], none, none;
// commit never regresses. Single node: after winning, [1,2,3(no-op)] once.
// FAILS IF: entries are skipped, repeated, reordered, or re-persisted.
func TestN13_RestartRedeliversLogExactlyOnceInOrder(t *testing.T) {
	p := raft.Persisted{HardState: raft.HardState{Term: 1}, Log: ents(1, 1, "a", "b", "c", "d", "e")}

	t.Run("follower", func(t *testing.T) {
		for restart := 1; restart <= 2; restart++ {
			n := raft.New(makeCfg(2, peers3, opts{}), p)
			if st := n.Status(); st.Commit != 0 || st.Applied != 0 || st.LastIndex != 5 {
				t.Fatalf("restart %d: status %+v, want commit 0, applied 0, last 5", restart, st)
			}
			if rd := drain(n); !rd.Empty() {
				t.Fatalf("restart %d: first Ready is not empty: entries=%d hardstate=%v", restart, len(rd.Entries), rd.HardState)
			}
			step := func(commit raft.Index, wantIdx ...raft.Index) {
				t.Helper()
				n.Step(app(1, 5, 1, commit, nil))
				rd := drain(n)
				got := idxs(rd.Committed)
				if len(got) != len(wantIdx) {
					t.Fatalf("restart %d commit=%d: delivered %v, want %v", restart, commit, got, wantIdx)
				}
				for i := range got {
					if got[i] != wantIdx[i] || string(rd.Committed[i].Data) != string(p.Log[got[i]-1].Data) {
						t.Fatalf("restart %d commit=%d: delivered %v, want %v", restart, commit, got, wantIdx)
					}
				}
				if len(rd.Entries) != 0 {
					t.Fatalf("restart %d: committing re-persisted %d entries", restart, len(rd.Entries))
				}
			}
			step(3, 1, 2, 3)
			step(5, 4, 5)
			step(5)
			step(3) // stale, lower
			if n.Status().Commit != 5 {
				t.Fatalf("restart %d: commit regressed to %d", restart, n.Status().Commit)
			}
		}
	})

	t.Run("single_node_leader", func(t *testing.T) {
		sp := raft.Persisted{HardState: raft.HardState{Term: 1}, Log: ents(1, 1, "a", "b")}
		n := raft.New(makeCfg(1, []raft.NodeID{1}, opts{}), sp)
		tickN(n, 10)
		rd := drain(n)
		if got := idxs(rd.Committed); len(got) != 3 || got[0] != 1 || got[1] != 2 || got[2] != 3 {
			t.Fatalf("delivered %v, want [1 2 3] (the two old entries and the new no-op)", got)
		}
		if len(rd.Entries) != 1 || rd.Entries[0].Index != 3 {
			t.Fatalf("persisted %v, want only the no-op at 3", idxs(rd.Entries))
		}
		tickN(n, 30)
		if more := drain(n); len(more.Committed) != 0 {
			t.Fatalf("entries delivered again: %v", idxs(more.Committed))
		}
	})
}
