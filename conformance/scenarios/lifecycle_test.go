// Scenarios for spec §17 criteria C31–C33 (controller model) and
// C34–C35 (pane lifecycle).

package scenarios

import (
	"strings"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/conformance/harness"
)

// C31 — there is exactly one controller per tmux server. We probe
// this observably: two rapid CLI invocations must share state.
// If a second controller had started, the second invocation would
// see a different instance id and our snapshot-then-read token
// reuse would fail with INVALID_AFTER.
func TestC31_OneControllerPerTmuxServer(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Two separate CLI invocations, with a real pause between.
	// Both must see the same controller instance, so the token
	// from the first invocation must remain valid across the
	// second.
	snap := e.Snapshot(t, pane)
	time.Sleep(100 * time.Millisecond)
	var r harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)
}

// C32 — the CLI auto-spawns a controller on demand when none is
// running for the target server (§11.2). The harness already
// exercises this — every Env starts with no daemon running and
// the first Run implicitly spawns one. We assert there's no
// explicit pre-flight the caller must perform.
func TestC32_AutoSpawnOnDemand(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	// No explicit `tpctl daemon` call; just run list and expect
	// success.
	panes := e.List(t)
	if len(panes) == 0 {
		t.Fatal("list returned no panes on auto-spawned daemon")
	}
}

// C33 — an explicit tpctl daemon subcommand is provided (§11.3).
// We can check the help text advertises it without actually
// starting the daemon in the foreground (which would block).
func TestC33_DaemonSubcommandExists(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	r := e.RunArgs("--help")
	if r.Code != 0 {
		t.Fatalf("--help exit = %d", r.Code)
	}
	if !strings.Contains(r.Stdout, "daemon") {
		t.Fatalf("--help does not advertise daemon subcommand: %q", r.Stdout)
	}
}

// C34 — a pending wait whose pane is destroyed fails with
// PANE_CLOSED, distinct from TIMEOUT and PANE_NOT_FOUND (§11.9).
func TestC34_PaneClosedWhileWaitPending(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)

	// Create a second pane so we can kill it without wrecking the
	// fixture.
	pane := e.NewPane(t)
	snap := e.Snapshot(t, pane)

	done := make(chan harness.Result, 1)
	go func() {
		done <- e.Run("wait",
			"--pane", pane,
			"--after", snap.Next,
			"--for", "regex",
			"--pattern", "never-c34",
			"--timeout-ms", "5000",
		)
	}()

	// Give the wait a beat to register.
	time.Sleep(200 * time.Millisecond)

	// Destroy the pane.
	e.KillPane(t, pane)

	select {
	case r := <-done:
		err := r.MustError(t)
		if err.Code != "PANE_CLOSED" {
			t.Fatalf("code = %q, want PANE_CLOSED", err.Code)
		}
	case <-time.After(5500 * time.Millisecond):
		t.Fatal("wait never returned after pane was killed")
	}
}

// C35 — when a pane is destroyed, later commands for that
// %pane_id fail with PANE_NOT_FOUND, not INVALID_AFTER (§11.9 and
// §7.5 precedence). We ensure the subscription has observed the
// pane before killing it, then poll up to 5s for the controller
// to drop it. NOT t.Parallel(): detection latency depends on the
// poll tick and is sensitive to CPU contention.
func TestC35_DestroyedPaneSurfacesPaneNotFound(t *testing.T) {
	e := harness.NewEnv(t)

	pane := e.NewPane(t)
	snap := e.Snapshot(t, pane)

	// Force the subscription to have observed this pane by
	// producing output and waiting for it through the controller.
	// This sidesteps the edge case where a pane is created and
	// destroyed within a single subscription poll tick.
	e.PaneOutput(t, pane, "echo c35-tracked-marker")
	e.Run("wait",
		"--pane", pane, "--after", snap.Next,
		"--for", "regex", "--pattern", "c35-tracked-marker",
		"--timeout-ms", "3000",
	).MustJSON(t, new(harness.WaitResponse))

	e.KillPane(t, pane)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		r := e.Run("read", "--pane", pane, "--after", snap.Next)
		if r.Code != 0 {
			err := r.MustError(t)
			if err.Code == "PANE_NOT_FOUND" {
				return
			}
			t.Fatalf("code = %q, want PANE_NOT_FOUND (spec §7.5 precedence)", err.Code)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("controller never reported pane as PANE_NOT_FOUND within 5s")
}
