# M0 node-level test report

Baseline results below are from the unmodified implementation. After the fix commits on branch `m0-hardening` every test N01-N14 passes (`go test ./raft ./sim -race`). Fix commits: see `git log`.

Run against the unmodified `raft/node.go` (sha256 55844b4a...eaf1) and `raft/types.go` (cc9e5eec...d957).
Tests use only `raft.New`, `Tick`, `Step`, `Propose`, `Ready`, `Advance`, `Status`, `Log`. No simulator.

Run: `make test-node` (N07-N12 are EXPECTED to fail until fixed), `make mutation` (N14).

| #   | Test | Invariant | Baseline | Audit issue |
|-----|------|-----------|----------|-------------|
| N01 | StaleAndDuplicateAppendNeverTruncate | A stale or duplicate AppendEntries never removes entries the follower already has | PASS | Regression guard (core rule) |
| N02 | ConflictTruncatesExactlyFromFirstConflict | Truncate only at the first term conflict, keep everything before it | PASS | Regression guard (core rule) |
| N03 | FollowerCommitUsesLastNewEntry | Follower commit = min(leaderCommit, index of last NEW entry), not its own last index | PASS | Regression guard (core rule) |
| N04 | Figure8_PreviousTermNotCommittedByCounting | A leader never commits an earlier-term entry by counting replicas | PASS | Regression guard (Raft Figure 8) |
| N05 | RejectHintBoundsAndDuplicateRejects | Reject hints stay in range and duplicate rejects don't corrupt progress | PASS | F7 (coarse RejectHint): hint is in bounds; performance of hint not asserted |
| N06 | VotePersistedWithReplyAndSingleVotePerTerm | A vote is in HardState in the same Ready as the grant; one vote per term | PASS | Regression guard (vote safety). Extended after mutation M4 survived |
| N07 | AppendBytesBoundedAndLaggingFollowerCatchesUp | No AppendEntries exceeds the byte budget, and a follower still catches up from a larger backlog | FAIL -> fixed | F1 (unbounded MsgApp) |
| N08 | HealthyLeaderSurvivesOneWayPartition | A healthy leader is not deposed by a node that cannot hear it; a real leader crash still elects | FAIL (control passes); fixed | F2 (no leader lease on (Pre)Vote) |
| N09 | MalformedMessagesDoNotPanicOrMutate | Bad network input neither panics nor changes state or output | FAIL -> fixed | F4 (untrusted indexes / unknown senders) |
| N10 | ProposeCopiesCallerBuffer | The node owns proposal bytes; mutating the caller's buffer changes nothing | FAIL -> fixed | F5 (Propose aliasing) |
| N11 | InputBetweenReadyAndAdvanceIsNeverLost | Nothing produced between Ready and Advance is silently dropped | FAIL -> fixed | F3 (Advance clears all dirty state) |
| N12 | NewRejectsBadConfigAndLog | `raft.New` rejects invalid Config and persisted logs with a descriptive panic | FAIL -> fixed | F6 (no validation) |
| N13 | RestartRedeliversLogExactlyOnceInOrder | After restart every entry is applied once, in order | PASS | F10 (restart state) |
| N14 | Mutation check (scripts/mutation.py) | The guard tests actually detect 4 deliberate bugs | PASS (4/4 caught) | Test quality |

## Failure evidence

- N07: one AppendEntries carried 201 entries / 204800 payload bytes against an 8192 budget; 6 entries / 20480 bytes against 1024.
- N08: victim 3 (hears nothing from leader 1, can still send) deposed healthy leader 1 at tick 17 (election timeout 10, heartbeat 1). The crash control passes, so a fix must not simply block elections.
- N09: panics ("index out of range [18446744073709551615]", "log gap in appendLocal", "attempt to overwrite committed entries"); an AppendEntries from unknown node 99 appended entry 4 and acked; a vote request from unknown node 99 moved term 2->3 and was granted.
- N10: the entry handed to persistence, the log, and the outgoing message all showed "MUTATED!" after the caller reused its buffer.
- N11: a term-2 vote, entry "b" and a message carrying "y" were lost if they happened between Ready and Advance; entry 3 reached Ready.Committed without ever being in Ready.Entries; an Advance without a Ready discarded pending output.
- N12: 11 of 12 invalid inputs were silently accepted; nil Rand failed only by an accidental nil dereference.

## Mutation check (N14)

| Mutant | Bug | Result |
|--------|-----|--------|
| M1 | truncate on any overlapping entry | caught by N01 |
| M2 | commit uses own last index | caught by N03 |
| M3 | commit earlier term by counting | caught by N04 |
| M4 | vote granted but never marked for persistence | caught by N06 (first run SURVIVED; test gap fixed) |

## Decisions to review before any fix

1. N12 contract: invalid config/log must panic with a descriptive (non-runtime) error. Alternative: `New` returns an error (changes the API).
2. N11 contract: after Ready and before Advance, new input must be retained for the next Ready. Alternative: Advance panics if state changed.
3. N07 counts payload bytes only (entry Data), not header overhead. `Config.MaxAppendBytes` does not exist yet; the test sets it by reflection if present and asserts 8192 / 1024 otherwise. A single entry larger than the budget must still be sent alone.
4. N09 excludes the well-formed-but-impossible case of an overwrite of committed entries by a legitimate leader.
5. N05 only asserts hint bounds; it does not measure how fast a follower is repaired.
6. `test/` (Porcupine) cannot run in my sandbox; only `raft` ran here.

## Planned fix order (one test-backed commit each)

F4/N09, F5/N10, F6/N12, F3/N11, F1/N07, then F2/N08.
