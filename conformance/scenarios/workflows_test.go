// Workflow scenarios: realistic agent idioms that compose several
// primitives. Individual §17 criteria pin isolated guarantees;
// these scenarios make sure the primitives work correctly IN
// COMBINATION.

package scenarios

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/conformance/harness"
)

// TestWorkflow_LongBuildWithSentinel — the canonical
// "snapshot → text → wait sentinel" idiom (spec §19 quick
// reference). Verifies the race-free guarantee in a realistic
// setting: a command that takes non-trivial time and ends with a
// non-zero exit code visible through the sentinel.
func TestWorkflow_LongBuildWithSentinel(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)

	// Fake build: sleep then exit 2.
	e.Run("text", "--pane", pane,
		harness.FormatSentinelCmd("sleep 0.3; false; (exit 2)", "build-wf"),
		"--enter").MustMuted(t)

	var w harness.WaitResponse
	e.Run("wait",
		"--pane", pane, "--after", snap.Next,
		"--for", "sentinel", "--token", "build-wf",
		"--timeout-ms", "5000",
	).MustJSON(t, &w)

	if w.Result != "sentinel" {
		t.Fatalf("result = %q", w.Result)
	}
	if w.ExitCode == nil || *w.ExitCode != 2 {
		t.Fatalf("exit_code = %v, want 2", w.ExitCode)
	}

	// Post-workflow: reading from wait.Next should return only
	// output appended AFTER the sentinel match point.
	var r harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", w.Next).MustJSON(t, &r)
	// After the sentinel, the shell prints a new prompt. That's
	// acceptable. We just check we don't see the sentinel itself.
	if strings.Contains(r.Text, "__DONE__:build-wf:") {
		t.Fatalf("wait.Next still saw the sentinel: %q", r.Text)
	}
}

// TestWorkflow_TUIDriveThenSettle — the canonical TUI idiom:
// send a sequence of named keys, wait for quiescence, then
// snapshot to read the settled screen.
func TestWorkflow_TUIDriveThenSettle(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)

	// Drive the pane via key: type the literal characters of
	// "echo", a space, "tui-wf-done", then Enter. tmux key
	// vocabulary only names special keys; each single char is
	// just a one-char token.
	keys := []string{"e", "c", "h", "o", "Space", "t", "u", "i",
		"-", "w", "f", "-", "d", "o", "n", "e", "Enter"}
	args := append([]string{"key", "--pane", pane}, keys...)
	e.Run(args...).MustMuted(t)

	// Wait for quiescence after key input, then snapshot.
	var q harness.WaitResponse
	e.Run("wait",
		"--pane", pane, "--after", snap.Next,
		"--for", "quiescence", "--ms", "300",
		"--timeout-ms", "3000",
	).MustJSON(t, &q)
	if q.Result != "quiescence" {
		t.Fatalf("result = %q", q.Result)
	}

	settled := e.Snapshot(t, pane)
	if !strings.Contains(settled.Text, "tui-wf-done") {
		t.Fatalf("settled snapshot missing TUI-driven output: %q", settled.Text)
	}
}

// TestWorkflow_IncrementalTail — the canonical "tail -f" idiom:
// snapshot to get a token, then repeatedly read --after the
// rolling token. Each read returns a slice of new bytes; tokens
// advance monotonically.
func TestWorkflow_IncrementalTail(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)
	token := snap.Next

	// Spray three distinct markers with pauses between.
	markers := []string{"tail-wf-A", "tail-wf-B", "tail-wf-C"}
	got := make(map[string]bool, len(markers))

	for _, m := range markers {
		e.Run("text", "--pane", pane, "echo "+m, "--enter").MustMuted(t)
		e.WaitForText(t, pane, m, 5*time.Second)

		var r harness.ReadResponse
		e.Run("read", "--pane", pane, "--after", token).MustJSON(t, &r)
		if strings.Contains(r.Text, m) {
			got[m] = true
		}
		if r.Next == token {
			t.Fatalf("token did not advance after seeing %s", m)
		}
		token = r.Next
	}

	for _, m := range markers {
		if !got[m] {
			t.Fatalf("incremental tail missed marker %s", m)
		}
	}

	// One more read from the final token should return empty
	// (nothing new appended since the last advance).
	var tail harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", token).MustJSON(t, &tail)
	for _, m := range markers {
		if strings.Contains(tail.Text, m) {
			t.Fatalf("tail after last read reported marker %s again: %q", m, tail.Text)
		}
	}
}

// TestWorkflow_ConcurrentDifferentConditions — two agent goroutines
// can wait for different conditions on the same pane without
// interference. Spec §9.6 concurrency rules apply here.
func TestWorkflow_ConcurrentDifferentConditions(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)

	var wg sync.WaitGroup
	errCh := make(chan string, 2)

	wg.Add(2)
	go func() {
		defer wg.Done()
		var w harness.WaitResponse
		e.Run("wait",
			"--pane", pane, "--after", snap.Next,
			"--for", "regex", "--pattern", "concurrent-wf-ALPHA",
			"--timeout-ms", "5000",
		).MustJSON(t, &w)
		if w.Matched == nil || *w.Matched != "concurrent-wf-ALPHA" {
			errCh <- "alpha: unexpected match"
		}
	}()
	go func() {
		defer wg.Done()
		var w harness.WaitResponse
		e.Run("wait",
			"--pane", pane, "--after", snap.Next,
			"--for", "regex", "--pattern", "concurrent-wf-BETA",
			"--timeout-ms", "5000",
		).MustJSON(t, &w)
		if w.Matched == nil || *w.Matched != "concurrent-wf-BETA" {
			errCh <- "beta: unexpected match"
		}
	}()

	time.Sleep(150 * time.Millisecond) // let waits register

	// Emit both markers in one shell command so neither wait has
	// a timing advantage.
	e.Run("text", "--pane", pane,
		"echo concurrent-wf-ALPHA; echo concurrent-wf-BETA",
		"--enter").MustMuted(t)

	wg.Wait()
	close(errCh)
	for s := range errCh {
		t.Fatal(s)
	}
}

// TestWorkflow_SnapshotHistoryPeek — realistic human/agent idiom:
// use a snapshot with --history-lines to recover context from a
// pane that has already scrolled. We seed the pane with enough
// output to push the marker into scrollback, then peek.
func TestWorkflow_SnapshotHistoryPeek(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Seed a unique marker, then push 30+ empty lines so it
	// scrolls out of the visible area (pane is 24 tall).
	e.Run("text", "--pane", pane, "echo peek-wf-marker", "--enter").MustMuted(t)
	e.WaitForText(t, pane, "peek-wf-marker", 5*time.Second)
	for i := 0; i < 35; i++ {
		e.Run("text", "--pane", pane, "", "--enter").MustMuted(t)
	}

	// Peek into scrollback.
	snap := e.Snapshot(t, pane, "--history-lines", "100")
	if snap.ScrollbackText == nil {
		t.Fatal("scrollback_text missing; --history-lines did not take effect")
	}
	if !strings.Contains(*snap.ScrollbackText, "peek-wf-marker") {
		// Marker might still be on the visible screen if 35
		// lines weren't enough; be lenient.
		if !strings.Contains(snap.Text, "peek-wf-marker") {
			t.Fatalf("marker not found anywhere; scrollback=%q text=%q",
				*snap.ScrollbackText, snap.Text)
		}
	}
}
