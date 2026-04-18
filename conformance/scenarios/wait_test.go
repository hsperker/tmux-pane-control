// Scenarios for spec §17 criteria C12–C17 (wait contract) and
// C18–C24 (matching semantics). These are the most intricate parts
// of the spec; they exercise the race-free `snapshot → send → wait`
// idiom, all three wait modes, concurrency, and RE2 details.

package scenarios

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"tpctl-conformance/harness"
)

// C12 — wait requires --timeout-ms (§9.6).
func TestC12_WaitRequiresTimeoutMs(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)
	// Omit --timeout-ms; implementation must reject as INVALID_ARGS.
	r := e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "sentinel",
		"--token", "x",
	)
	if r.Code == 0 {
		t.Fatalf("wait without --timeout-ms must fail; stdout=%q", r.Stdout)
	}
}

// C13 — wait supports exactly sentinel, regex, quiescence (§9.6).
// An unknown mode must be rejected before execution.
func TestC13_WaitSupportsExactlyThreeModes(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	// Unknown mode: must fail, must not time out (i.e. must fail
	// before the timeout elapses).
	start := time.Now()
	r := e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "supernatural",
		"--timeout-ms", "5000",
	)
	if r.Code == 0 {
		t.Fatalf("unknown --for mode must fail; stdout=%q", r.Stdout)
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("unknown --for mode waited for the full timeout; must reject early")
	}
}

// C14 — wait --after TOKEN considers already-buffered post-token
// output at registration time, not just future arrivals (§9.6).
// This is the race-free guarantee.
func TestC14_WaitSeesAlreadyBufferedOutput(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	// Output arrives BEFORE wait is registered.
	e.PaneOutput(t, pane, "echo c14-already-buffered-marker")
	e.WaitForText(t, pane, "c14-already-buffered-marker", 2*time.Second)
	// Give pipe-pane / subscription a beat to deliver it to the
	// controller's buffer.
	time.Sleep(300 * time.Millisecond)

	var w harness.WaitResponse
	e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "regex",
		"--pattern", "c14-already-buffered-marker",
		"--timeout-ms", "3000",
	).MustJSON(t, &w)

	if w.Result != "regex" {
		t.Fatalf("result = %q", w.Result)
	}
}

// C15 — wait --after TOKEN cannot miss fast output that occurs
// after the checkpoint (§9.6). We start the wait in a goroutine,
// then send immediately; the wait must catch the output whether
// it arrived before or after registration.
func TestC15_WaitCannotMissFastOutput(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	done := make(chan harness.Result, 1)
	go func() {
		done <- e.Run("wait",
			"--pane", pane,
			"--after", snap.Next,
			"--for", "regex",
			"--pattern", "c15-fast-marker",
			"--timeout-ms", "5000",
		)
	}()
	// Send essentially immediately; the wait should catch this
	// regardless of whether it had registered yet.
	time.Sleep(50 * time.Millisecond)
	e.PaneOutput(t, pane, "echo c15-fast-marker")

	select {
	case r := <-done:
		var w harness.WaitResponse
		r.MustJSON(t, &w)
		if w.Result != "regex" {
			t.Fatalf("result = %q", w.Result)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("wait did not return in time; output was missed")
	}
}

// C16 — wait always returns next on success (§9.6).
func TestC16_WaitSuccessIncludesNext(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	var w harness.WaitResponse
	e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "quiescence",
		"--ms", "50",
		"--timeout-ms", "2000",
	).MustJSON(t, &w)
	if w.Next == "" {
		t.Fatal("wait.next empty on success")
	}
}

// C17 — multiple concurrent waits on the same pane are allowed
// and evaluated independently (§9.6).
func TestC17_MultipleConcurrentWaits(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	var wg sync.WaitGroup
	results := make(chan harness.WaitResponse, 2)
	errCh := make(chan harness.Result, 2)

	launch := func(pattern string) {
		defer wg.Done()
		r := e.Run("wait",
			"--pane", pane,
			"--after", snap.Next,
			"--for", "regex",
			"--pattern", pattern,
			"--timeout-ms", "5000",
		)
		if r.Code != 0 {
			errCh <- r
			return
		}
		var w harness.WaitResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &w); err != nil {
			errCh <- r
			return
		}
		results <- w
	}

	wg.Add(2)
	go launch("c17-marker-A")
	go launch("c17-marker-B")
	time.Sleep(100 * time.Millisecond)

	// Emit both markers; each wait should find its own.
	e.PaneOutput(t, pane, "echo c17-marker-A; echo c17-marker-B")

	wg.Wait()
	close(results)
	close(errCh)
	for r := range errCh {
		t.Fatalf("a wait failed: code=%d stdout=%q", r.Code, r.Stdout)
	}
	count := 0
	for w := range results {
		if w.Result != "regex" {
			t.Fatalf("unexpected result: %+v", w)
		}
		count++
	}
	if count != 2 {
		t.Fatalf("expected 2 successful waits, got %d", count)
	}
}

// C18 — wait --for regex uses RE2 (§9.6). RE2 does NOT support
// backreferences; a pattern with `\1` must be rejected at compile
// time before any timeout elapses.
func TestC18_RegexUsesRE2(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	start := time.Now()
	r := e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "regex",
		"--pattern", `(a)\1`, // PCRE backref, invalid in RE2
		"--timeout-ms", "5000",
	)
	if r.Code == 0 {
		t.Fatal("backref pattern must be rejected")
	}
	if time.Since(start) > 500*time.Millisecond {
		t.Fatal("regex compile rejection must not wait for timeout")
	}
}

// C19 — regex and sentinel matching operate on ANSI-stripped,
// \n-normalized post-checkpoint text (§9.6). We probe by emitting
// colored output and requiring a plain-text regex to match.
func TestC19_MatchInputIsANSIStripped(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	// Colored "READY" (red foreground, reset). tmux passes the
	// literal ANSI through because the shell's echo -e writes it.
	e.PaneOutput(t, pane, `printf '\033[31mREADY-C19\033[0m\n'`)
	e.WaitForText(t, pane, "READY-C19", 2*time.Second)

	// (?m) so ^/$ anchor per-line; the pane buffer also contains
	// the shell prompt around our line, so a full-string anchor
	// without (?m) would never match.
	var w harness.WaitResponse
	e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "regex",
		"--pattern", "(?m)^READY-C19$",
		"--timeout-ms", "3000",
	).MustJSON(t, &w)
	if w.Matched == nil || *w.Matched != "READY-C19" {
		t.Fatalf("matched = %v, want \"READY-C19\"", w.Matched)
	}
}

// C20 — no implicit anchoring (§9.6). A pattern matches anywhere
// in the post-checkpoint buffer.
func TestC20_NoImplicitAnchoring(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	e.PaneOutput(t, pane, "echo prefix-NEEDLE-C20-suffix")
	e.WaitForText(t, pane, "NEEDLE-C20", 2*time.Second)

	var w harness.WaitResponse
	e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "regex",
		"--pattern", "NEEDLE-C20",
		"--timeout-ms", "3000",
	).MustJSON(t, &w)
	if w.Matched == nil || *w.Matched != "NEEDLE-C20" {
		t.Fatalf("matched = %v", w.Matched)
	}
}

// C21 — wait --for sentinel --token T matches __DONE__:T:<N>
// (§9.6). C22 — the response includes the full matched literal
// AND exit_code as an integer. Tested together.
func TestC21_22_SentinelMatchesAndParsesExitCode(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	// Emit the sentinel with a specific exit code.
	e.PaneOutput(t, pane, harness.FormatSentinelCmd("(exit 17)", "c21"))

	var w harness.WaitResponse
	e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "sentinel",
		"--token", "c21",
		"--timeout-ms", "3000",
	).MustJSON(t, &w)

	if w.Result != "sentinel" {
		t.Fatalf("result = %q", w.Result)
	}
	if w.Matched == nil || *w.Matched != "__DONE__:c21:17" {
		t.Fatalf("matched = %v, want \"__DONE__:c21:17\"", w.Matched)
	}
	if w.ExitCode == nil || *w.ExitCode != 17 {
		t.Fatalf("exit_code = %v, want 17", w.ExitCode)
	}
}

// C23 — wait --for quiescence treats any appended byte after the
// checkpoint as activity (§9.6).
//
// We drive continuous activity from WITHIN the shell so pipe-pane
// stays saturated and the controller's stream sees a steady drip
// of bytes. The background loop emits a line every ~50ms for up to
// 5s — well under the 500ms quiet window at any point during the
// 2s wait, so quiescence must time out, not succeed.
func TestC23_QuiescenceCountsAnyAppendAsActivity(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Start a backgrounded emitter; ensure it's running before we
	// snapshot, so pipe-pane has bytes flowing when the wait
	// registers.
	e.PaneOutput(t, pane,
		`for i in $(seq 1 100); do echo noise-c23-$i; sleep 0.05; done &`)
	e.WaitForText(t, pane, "noise-c23-1", 2*time.Second)
	t.Cleanup(func() { e.PaneOutput(t, pane, "kill %1 2>/dev/null") })

	// Give pipe-pane a moment to relay the first chunk of output
	// into the controller's stream so T_last is fresh.
	time.Sleep(300 * time.Millisecond)

	snap := e.Snapshot(t, pane)
	r := e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "quiescence",
		"--ms", "500",
		"--timeout-ms", "2000",
	)
	if r.Code == 0 {
		t.Fatalf("quiescence succeeded despite continuous activity: stdout=%q", r.Stdout)
	}
	err := r.MustError(t)
	if err.Code != "TIMEOUT" {
		t.Fatalf("code = %q, want TIMEOUT", err.Code)
	}
}

// C24 — quiescence may succeed immediately if already satisfied
// at registration time (§9.6).
func TestC24_QuiescenceImmediatelyWhenIdle(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	// Give the pane a moment to settle so the mint time is older
	// than the quiet window.
	time.Sleep(200 * time.Millisecond)

	start := time.Now()
	var w harness.WaitResponse
	e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "quiescence",
		"--ms", "100",
		"--timeout-ms", "2000",
	).MustJSON(t, &w)
	elapsed := time.Since(start)

	if w.Result != "quiescence" {
		t.Fatalf("result = %q", w.Result)
	}
	// "Immediately" is implementation-dependent but must be well
	// under the quiet window. 200ms is a generous budget for
	// subprocess startup + IPC.
	if elapsed > 200*time.Millisecond {
		t.Fatalf("quiescence took %v; expected nearly immediate", elapsed)
	}
	if w.Matched != nil {
		t.Fatalf("quiescence must not return matched; got %q", *w.Matched)
	}
	if w.ExitCode != nil {
		t.Fatalf("quiescence must not return exit_code; got %d", *w.ExitCode)
	}
}

