package raft

// Progress tracks peer progress and activity.
type Progress struct {
	RecentActive bool
}

// raftNode is the skeleton you will fill in. The plumbing (log storage,
// Ready/Advance bookkeeping, restore-from-disk) is done and tested through
// the simulator; the Raft *logic* is marked TODO(Mx) per milestone.
type raftNode struct {
	cfg Config

	// Persistent state (mirrored to disk through Ready).
	hs  HardState
	log []Entry // log[0] has Index 1

	// Volatile state.
	role    Role
	leader  NodeID
	commit  Index
	applied Index

	electionElapsed  int
	electionTimeout  int // randomized
	heartbeatElapsed int

	votes      map[NodeID]bool
	matchIndex map[NodeID]Index // Highest log entry known to be replicated on peer
	nextIndex  map[NodeID]Index // Next log entry to send to peer
	progress   map[NodeID]*Progress
	// Ready bookkeeping.
	hsDirty   bool
	dirtyFrom Index // lowest log index modified since the last Advance (0 = clean)
	msgs      []Message
	readyAt   Index // commit index captured by the last Ready(), applied on Advance
}

// New restores a node from persisted state. Always starts as a Follower.
func New(cfg Config, p Persisted) Node {
	n := &raftNode{
		cfg:        cfg,
		hs:         p.HardState,
		log:        append([]Entry(nil), p.Log...),
		votes:      make(map[NodeID]bool),
		matchIndex: make(map[NodeID]Index),
		nextIndex:  make(map[NodeID]Index),
		progress:   make(map[NodeID]*Progress),
	}
	for _, peer := range cfg.Peers {
		n.progress[peer] = &Progress{}
	}
	n.becomeFollower(n.hs.Term, 0)
	return n
}

// ---------- plumbing (done) ----------

func (n *raftNode) lastIndex() Index { return Index(len(n.log)) }

func (n *raftNode) lastTerm() Term {
	if len(n.log) == 0 {
		return 0
	}
	return n.log[len(n.log)-1].Term
}

// termAt returns the term at index i; (0, true) for i == 0; ok=false if i is beyond the log.
func (n *raftNode) termAt(i Index) (Term, bool) {
	if i == 0 {
		return 0, true
	}
	if i > n.lastIndex() {
		return 0, false
	}
	return n.log[i-1].Term, true
}

// appendLocal replaces the log from ents[0].Index onward and marks it dirty so
// the next Ready() asks the driver to persist it. Panics on a gap, because a
// gap is always a bug in the caller.
func (n *raftNode) appendLocal(ents ...Entry) {
	if len(ents) == 0 {
		return
	}
	first := ents[0].Index
	if first == 0 || first > n.lastIndex()+1 {
		panic("raft: log gap in appendLocal")
	}
	if first <= n.commit {
		panic("raft: attempt to overwrite committed entries") // safety invariant!
	}
	n.log = append(n.log[:first-1], ents...)
	if n.dirtyFrom == 0 || first < n.dirtyFrom {
		n.dirtyFrom = first
	}
}

func (n *raftNode) setHardState(term Term, votedFor NodeID) {
	if n.hs.Term == term && n.hs.VotedFor == votedFor {
		return
	}
	n.hs = HardState{Term: term, VotedFor: votedFor}
	n.hsDirty = true
}

func (n *raftNode) send(m Message) {
	m.From = n.cfg.ID
	n.msgs = append(n.msgs, m)
}

func (n *raftNode) resetElectionTimer() {
	n.electionElapsed = 0
	n.electionTimeout = n.cfg.ElectionTicks + n.cfg.Rand.Intn(n.cfg.ElectionTicks)
}

func (n *raftNode) becomeFollower(term Term, leader NodeID) {
	if term > n.hs.Term {
		n.setHardState(term, 0) // new term => forget our vote
	}
	n.role = Follower
	n.leader = leader
	n.heartbeatElapsed = 0
	n.resetElectionTimer()
}

func (n *raftNode) Status() Status {
	return Status{ID: n.cfg.ID, Term: n.hs.Term, Role: n.role, Leader: n.leader,
		Commit: n.commit, Applied: n.applied, LastIndex: n.lastIndex()}
}

func (n *raftNode) Log() []Entry { return append([]Entry(nil), n.log...) }

func (n *raftNode) Ready() (Ready, bool) {
	var rd Ready
	if n.hsDirty {
		hs := n.hs
		rd.HardState = &hs
	}
	if n.dirtyFrom != 0 {
		rd.Entries = append([]Entry(nil), n.log[n.dirtyFrom-1:]...)
	}
	rd.Messages = n.msgs
	if n.commit > n.applied {
		rd.Committed = append([]Entry(nil), n.log[n.applied:n.commit]...)
		n.readyAt = n.commit
	}
	if rd.Empty() {
		return Ready{}, false
	}
	return rd, true
}

func (n *raftNode) Advance() {
	n.hsDirty = false
	n.dirtyFrom = 0
	n.msgs = nil
	if n.readyAt > n.applied {
		n.applied = n.readyAt
	}
}

// ---------- the actual Raft (YOUR WORK) ----------

// Tick advances logical time by one unit.
func (n *raftNode) Tick() {
	switch n.role {
	case Follower, PreCandidate, Candidate:
		n.electionElapsed++
		if n.electionElapsed >= n.electionTimeout {
			if n.cfg.PreVote && n.role != Leader {
				n.preCampaign()
			} else {
				n.campaign()
			}
		}
	case Leader:
		n.tickHeartbeat()
	}
}

func (n *raftNode) tickHeartbeat() {
	if n.cfg.CheckQuorum {
		n.electionElapsed++
		if n.electionElapsed >= n.electionTimeout {
			n.resetElectionTimer()
			n.checkQuorum()
			if n.role != Leader {
				return
			}
		}
	}

	n.heartbeatElapsed++
	if n.heartbeatElapsed >= n.cfg.HeartbeatTicks {
		n.heartbeatElapsed = 0
		n.bcastAppend()
	}
}

func (n *raftNode) checkQuorum() {
	if n.role != Leader {
		return
	}

	active := 1 // Count the leader as active
	for _, peer := range n.cfg.Peers {
		if peer == n.cfg.ID {
			continue
		}
		if pr := n.progress[peer]; pr != nil && pr.RecentActive {
			active++
		}
	}

	quorum := (len(n.cfg.Peers) / 2) + 1
	if active < quorum {
		n.becomeFollower(n.hs.Term, 0)
	}

	for _, peer := range n.cfg.Peers {
		if pr := n.progress[peer]; pr != nil {
			pr.RecentActive = false
		}
	}
}

func (n *raftNode) preCampaign() {
	n.role = PreCandidate
	n.leader = 0
	n.votes = make(map[NodeID]bool)
	n.votes[n.cfg.ID] = true
	n.resetElectionTimer()

	if len(n.cfg.Peers) == 1 {
		n.campaign()
		return
	}

	for _, peer := range n.cfg.Peers {
		if peer == n.cfg.ID {
			continue
		}
		n.send(Message{
			Type:         MsgPreVote,
			To:           peer,
			Term:         n.hs.Term + 1, // Ask if they will vote for us in the NEXT term
			LastLogIndex: n.lastIndex(),
			LastLogTerm:  n.lastTerm(),
		})
	}
}

func (n *raftNode) campaign() {
	n.role = Candidate
	// Increment term and vote for ourselves. setHardState handles dirtying the struct for disk write.
	n.setHardState(n.hs.Term+1, n.cfg.ID)
	n.leader = 0

	n.votes = make(map[NodeID]bool)
	n.votes[n.cfg.ID] = true // Vote for self

	n.resetElectionTimer() // Reroll the random timeout

	// If we are a 1-node cluster, we win instantly
	if len(n.cfg.Peers) == 1 {
		n.becomeLeader()
		return
	}

	// Request votes from all peers
	for _, peer := range n.cfg.Peers {
		if peer == n.cfg.ID {
			continue
		}

		lastIndex := n.lastIndex()
		lastTerm := n.lastTerm()

		n.send(Message{
			Type:         MsgVote,
			To:           peer,
			Term:         n.hs.Term,
			LastLogIndex: lastIndex,
			LastLogTerm:  lastTerm,
		})
	}
}

func (n *raftNode) becomeLeader() {
	n.role = Leader
	n.leader = n.cfg.ID
	n.heartbeatElapsed = 0
	n.resetElectionTimer()

	n.matchIndex = make(map[NodeID]Index)
	n.nextIndex = make(map[NodeID]Index)
	n.progress = make(map[NodeID]*Progress)
	lastIdx := n.lastIndex()
	for _, peer := range n.cfg.Peers {
		n.matchIndex[peer] = 0
		n.nextIndex[peer] = lastIdx + 1
		n.progress[peer] = &Progress{}
	}

	// M3: Append a no-op entry in our own term to safely commit older entries
	n.appendLocal(Entry{
		Term:  n.hs.Term,
		Index: n.lastIndex() + 1,
		Data:  nil,
	})
	n.matchIndex[n.cfg.ID] = n.lastIndex()

	if len(n.cfg.Peers) == 1 {
		n.advanceCommit()
	}
	n.bcastAppend()
}

// Step processes one incoming message.
func (n *raftNode) Step(m Message) {
	// A rejected PreVote from a node in a higher term means we are behind: catch up.
	if m.Type == MsgPreVoteResp && m.Reject && m.Term > n.hs.Term {
		n.becomeFollower(m.Term, 0)
		return
	}
	// Rule 1: Always check terms before anything else.
	if m.Term > n.hs.Term {
		// PreVote messages evaluate a future term. Do not step down!
		if m.Type != MsgPreVote && m.Type != MsgPreVoteResp {
			n.becomeFollower(m.Term, 0)
		}
	} else if m.Term < n.hs.Term {
		// Ignore messages from older terms
		if m.Type == MsgVote {
			n.send(Message{To: m.From, Type: MsgVoteResp, Term: n.hs.Term, Reject: true})
		}
		if m.Type == MsgApp {
			n.send(Message{To: m.From, Type: MsgAppResp, Term: n.hs.Term, Reject: true})
		}
		if m.Type == MsgPreVote {
			n.send(Message{To: m.From, Type: MsgPreVoteResp, Term: n.hs.Term, Reject: true})
		}
		return
	}

	switch m.Type {
	case MsgPreVote:
		lastIndex := n.lastIndex()
		lastTerm := n.lastTerm()
		isUpToDate := m.LastLogTerm > lastTerm || (m.LastLogTerm == lastTerm && m.LastLogIndex >= lastIndex)

		if isUpToDate {
			// Echo the REQUESTED term: the pre-candidate's own term check would
			// otherwise drop this response as stale when our term is lower.
			n.send(Message{To: m.From, Type: MsgPreVoteResp, Term: m.Term, Reject: false})
		} else {
			n.send(Message{To: m.From, Type: MsgPreVoteResp, Term: n.hs.Term, Reject: true})
		}

	case MsgPreVoteResp:
		if n.role != PreCandidate {
			return
		}
		if !m.Reject {
			n.votes[m.From] = true
			quorum := (len(n.cfg.Peers) / 2) + 1
			if len(n.votes) >= quorum {
				n.campaign() // We got a PreVote majority! Start the REAL election.
			}
		}

	case MsgVote:
		// We can vote if we haven't voted yet, or if we already voted for this specific candidate in this term.
		canVote := n.hs.VotedFor == 0 || n.hs.VotedFor == m.From

		// Candidate's log must be at least as up-to-date as ours
		lastIndex := n.lastIndex()
		lastTerm := n.lastTerm()
		isUpToDate := m.LastLogTerm > lastTerm || (m.LastLogTerm == lastTerm && m.LastLogIndex >= lastIndex)

		if canVote && isUpToDate {
			n.setHardState(n.hs.Term, m.From)
			n.resetElectionTimer() // Don't start an election while a valid candidate is campaigning
			n.send(Message{To: m.From, Type: MsgVoteResp, Term: n.hs.Term, Reject: false})
		} else {
			n.send(Message{To: m.From, Type: MsgVoteResp, Term: n.hs.Term, Reject: true})
		}

	case MsgVoteResp:
		if n.role != Candidate {
			return
		}

		if !m.Reject {
			n.votes[m.From] = true
			// Check for quorum
			quorum := (len(n.cfg.Peers) / 2) + 1
			if len(n.votes) >= quorum {
				n.becomeLeader()
			}
		}

	case MsgApp:
		n.role = Follower
		n.leader = m.From
		n.resetElectionTimer()

		// Verify PrevLogTerm at PrevLogIndex.
		term, ok := n.termAt(m.PrevLogIndex)
		if !ok || term != m.PrevLogTerm {
			n.send(Message{
				To:         m.From,
				Type:       MsgAppResp,
				Term:       n.hs.Term,
				Reject:     true,
				RejectHint: n.lastIndex(),
			})
			return
		}

		// If it matches, truncate any conflicting log entries, append new ones.
		for i, entry := range m.Entries {
			if entry.Index <= n.lastIndex() {
				if n.log[entry.Index-1].Term != entry.Term {
					n.appendLocal(m.Entries[i:]...)
					break
				}
			} else {
				n.appendLocal(m.Entries[i:]...)
				break
			}
		}

		// Update n.commit to min(LeaderCommit, index of last new entry).
		lastNewIndex := m.PrevLogIndex + Index(len(m.Entries))
		if m.LeaderCommit > n.commit {
			newCommit := m.LeaderCommit
			if lastNewIndex < newCommit {
				newCommit = lastNewIndex
			}
			if newCommit > n.commit {
				n.commit = newCommit
			}
		}

		// Reply Reject: false with the MatchIndex.
		n.send(Message{
			To:         m.From,
			Type:       MsgAppResp,
			Term:       n.hs.Term,
			Reject:     false,
			MatchIndex: lastNewIndex,
		})

	case MsgAppResp:
		if n.role != Leader {
			return
		}
		if pr := n.progress[m.From]; pr != nil {
			pr.RecentActive = true
		} else {
			n.progress[m.From] = &Progress{RecentActive: true}
		}
		if m.Reject {
			if n.nextIndex[m.From] > 1 {
				n.nextIndex[m.From]--
				if m.RejectHint+1 < n.nextIndex[m.From] {
					n.nextIndex[m.From] = m.RejectHint + 1
				}
			}
			if n.nextIndex[m.From] < 1 {
				n.nextIndex[m.From] = 1
			}
			n.sendAppend(m.From)
		} else {
			if m.MatchIndex > n.matchIndex[m.From] {
				n.matchIndex[m.From] = m.MatchIndex
			}
			if m.MatchIndex+1 > n.nextIndex[m.From] {
				n.nextIndex[m.From] = m.MatchIndex + 1
			}
			n.advanceCommit()
		}
	}
}

// Propose appends a client command if we are the leader.
func (n *raftNode) Propose(data []byte) bool {
	if n.role != Leader {
		return false
	}
	n.appendLocal(Entry{
		Term:  n.hs.Term,
		Index: n.lastIndex() + 1,
		Data:  data,
	})
	n.matchIndex[n.cfg.ID] = n.lastIndex()
	if len(n.cfg.Peers) == 1 {
		n.advanceCommit()
	}
	n.bcastAppend()
	return true
}

func (n *raftNode) sendAppend(to NodeID) {
	ni := n.nextIndex[to]
	if ni == 0 {
		ni = 1
	}
	prevLogIndex := ni - 1
	prevLogTerm, _ := n.termAt(prevLogIndex)

	var ents []Entry
	if ni <= n.lastIndex() {
		ents = append([]Entry(nil), n.log[ni-1:]...)
	}

	n.send(Message{
		Type:         MsgApp,
		To:           to,
		Term:         n.hs.Term,
		PrevLogIndex: prevLogIndex,
		PrevLogTerm:  prevLogTerm,
		Entries:      ents,
		LeaderCommit: n.commit,
	})
}

func (n *raftNode) bcastAppend() {
	for _, peer := range n.cfg.Peers {
		if peer == n.cfg.ID {
			continue
		}
		n.sendAppend(peer)
	}
}

func (n *raftNode) advanceCommit() {
	if n.role != Leader {
		return
	}
	quorum := (len(n.cfg.Peers) / 2) + 1
	for N := n.lastIndex(); N > n.commit; N-- {
		if n.log[N-1].Term != n.hs.Term {
			continue
		}
		matchCount := 0
		for _, peer := range n.cfg.Peers {
			if n.matchIndex[peer] >= N {
				matchCount++
			}
		}
		if matchCount >= quorum {
			n.commit = N
			n.bcastAppend()
			break
		}
	}
}
