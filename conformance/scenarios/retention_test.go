// Scenarios for spec §17 criteria C10–C11: per-pane retention
// policy. C10 is about the 1 MiB constant; C11 is about eviction
// invalidating old tokens.

package scenarios

import (
	"testing"
	"time"

	"tpctl-conformance/harness"
)

// C10 — the retained output stream is bounded to 1 MiB per pane
// (§11.8). This is a constant the implementation must honor; the
// conformance kit cannot observe the internal number directly,
// but it can observe the OBSERVABLE CONSEQUENCE: a large enough
// flood must eventually evict an earlier checkpoint. See C11 for
// the active probe. Here we assert the spec-mandated budget shape:
// any flood that comfortably exceeds 1 MiB causes eviction; any
// flood smaller than the budget does not.
//
// This test only verifies the "below budget keeps the token valid"
// half; C11 covers the "above budget evicts" half.
func TestC10_BelowBudgetKeepsTokenValid(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)
	// Push a modest amount of output — well under the 1 MiB budget.
	e.PaneOutput(t, pane, `head -c 1000 /dev/urandom | base64`)
	e.WaitForText(t, pane, "==", 2*time.Second) // base64 tail padding

	// Reading with the pre-flood token must succeed; the budget
	// was not exceeded.
	var r harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)
}

// C11 — eviction of retained output invalidates checkpoints that
// point before the new retained start (§11.8). We flood the pane
// with > 1 MiB + slack to force eviction regardless of exact
// byte accounting.
func TestC11_EvictionInvalidatesOldTokens(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)

	// base64 of /dev/urandom inflates by ~1.33x. 2 MB of random
	// bytes → ~2.7 MB of base64 output. Comfortably > 1 MiB budget.
	e.PaneOutput(t, pane, `head -c 2000000 /dev/urandom | base64 | tr -d "\n"; echo`)

	// Wait for the shell prompt again so we know the flood finished.
	// We use a deliberate echo after the flood as a marker.
	e.PaneOutput(t, pane, "echo c11-flood-done-marker")
	e.WaitForText(t, pane, "c11-flood-done-marker", 10*time.Second)

	// The pre-flood token must now be evicted.
	err := e.Run("read", "--pane", pane, "--after", snap.Next).MustError(t)
	harness.AssertCode(t, err, "INVALID_AFTER")
}
