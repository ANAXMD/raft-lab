#!/usr/bin/env python3
"""Mutation checks for raft/node.go (audit item 14).

For each mutant: copy the module's raft package to a temp directory, apply one
deliberate bug to node.go, run ONLY the tests that are supposed to catch it, and
require them to FAIL. The same tests must PASS on the unmodified code, otherwise
the comparison means nothing. The repository itself is never modified.

Usage: python3 scripts/mutation.py          (from anywhere)
Exit status: 0 if every mutant is caught, 1 otherwise.
"""
import os
import shutil
import subprocess
import sys
import tempfile

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))

# (name, description, [(old, new), ...], test regex that must catch it)
MUTANTS = [
    ("M1-truncate-on-stale-append",
     "follower truncates on any overlapping entry instead of only on a term conflict",
     [("if n.log[entry.Index-1].Term != entry.Term {", "if true {")],
     "^TestN01_StaleAndDuplicateAppendNeverTruncate$"),
    ("M2-commit-uses-own-last-index",
     "follower commit = min(leaderCommit, its own last index) instead of the last NEW entry",
     [("if lastNewIndex < newCommit {", "if n.lastIndex() < newCommit {"),
      ("newCommit = lastNewIndex", "newCommit = n.lastIndex()")],
     "^TestN03_FollowerCommitUsesLastNewEntry$"),
    ("M3-commit-previous-term-by-counting",
     "leader commits earlier-term entries by counting replicas (Figure 8 bug)",
     [("if n.log[N-1].Term != n.hs.Term {", "if false {")],
     "^TestN04_Figure8_PreviousTermNotCommittedByCounting$"),
    ("M4-vote-not-recorded-for-persistence",
     "a vote is granted but never marked dirty, so it never reaches persistence",
     [("n.setHardState(n.hs.Term, m.From)", "n.hs.VotedFor = m.From")],
     "^TestN06_VotePersistedWithReplyAndSingleVotePerTerm$"),
]


def run_tests(workdir, regex):
    p = subprocess.run(["go", "test", "./raft", "-count=1", "-run", regex],
                       cwd=workdir, capture_output=True, text=True)
    out = p.stdout + p.stderr
    broken = "build failed" in out or "setup failed" in out
    return p.returncode, broken, out


def make_copy(dst):
    os.makedirs(os.path.join(dst, "raft"))
    for f in ("go.mod", "go.sum"):
        shutil.copy(os.path.join(ROOT, f), dst)
    for f in os.listdir(os.path.join(ROOT, "raft")):
        if f.endswith(".go"):
            shutil.copy(os.path.join(ROOT, "raft", f), os.path.join(dst, "raft", f))


def main():
    results, ok = [], True
    for name, desc, edits, regex in MUTANTS:
        with tempfile.TemporaryDirectory() as tmp:
            make_copy(tmp)
            code, broken, out = run_tests(tmp, regex)
            if code != 0:
                results.append((name, "BASELINE FAILS", "the guarding test fails on unmodified code"))
                ok = False
                continue
            path = os.path.join(tmp, "raft", "node.go")
            src = open(path).read()
            for old, new in edits:
                if src.count(old) != 1:
                    results.append((name, "ANCHOR NOT FOUND", f"{old!r} occurs {src.count(old)} times; node.go changed, update this script"))
                    ok = False
                    break
                src = src.replace(old, new)
            else:
                open(path, "w").write(src)
                code, broken, out = run_tests(tmp, regex)
                if broken:
                    results.append((name, "INVALID MUTANT", "mutant does not compile"))
                    ok = False
                elif code == 0:
                    results.append((name, "SURVIVED", "tests still pass: a gap in the tests"))
                    ok = False
                else:
                    results.append((name, "CAUGHT", desc))
    width = max(len(r[0]) for r in results)
    for name, status, note in results:
        print(f"{name.ljust(width)}  {status.ljust(16)}  {note}")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
