.PHONY: test-node mutation

# Node-level tests (no simulator). Until the audit fixes land, N07-N12 are
# EXPECTED to fail; see docs/M0_TEST_REPORT.md.
test-node:
	go test ./raft -count=1

# Each deliberate bug must be caught by its guarding test.
mutation:
	python3 scripts/mutation.py
