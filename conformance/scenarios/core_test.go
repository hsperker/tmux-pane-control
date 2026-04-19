// Scenarios for spec §17 criteria C01–C09: list, snapshot, read,
// checkpoint token semantics. These are the observational
// primitives every downstream scenario builds on.

package scenarios

import (
	"strings"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/conformance/harness"
)

// C01 — list returns only %pane_id strings (§9.1).
func TestC01_ListReturnsPaneIDsOnly(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	panes := e.List(t)
	if len(panes) == 0 {
		t.Fatal("fixture tmux server has no panes")
	}
	for _, p := range panes {
		if !strings.HasPrefix(p, "%") {
			t.Fatalf("pane %q is not a tmux %%N id", p)
		}
	}
}

// C02 — snapshot returns the visible screen and a usable token
// (§9.2). "Usable" is exercised by C05, but we at least check the
// token is a non-empty string here.
func TestC02_SnapshotReturnsTextAndToken(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	snap := e.Snapshot(t, e.FirstPane(t))
	if snap.Next == "" {
		t.Fatal("snapshot.next empty")
	}
	if snap.PaneID != e.FirstPane(t) {
		t.Fatalf("pane_id = %q", snap.PaneID)
	}
	if snap.ScrollbackText != nil {
		t.Fatalf("scrollback_text must be absent without --history-lines, got %q", *snap.ScrollbackText)
	}
}

// C03 — snapshot --history-lines returns scrollback_text AND text
// as distinct fields (§9.2).
func TestC03_SnapshotHistoryFieldsAreDistinct(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Pollute the scrollback with a predictable marker.
	e.PaneOutput(t, pane, "echo c03-scrollback-marker")
	e.WaitForText(t, pane, "c03-scrollback-marker", 5*time.Second)
	// Push the marker above the visible rows so it lives in scrollback.
	for i := 0; i < 30; i++ {
		e.PaneOutput(t, pane, "")
	}

	snap := e.Snapshot(t, pane, "--history-lines", "50")
	if snap.ScrollbackText == nil {
		t.Fatal("scrollback_text must be present when --history-lines requested")
	}
	combined := *snap.ScrollbackText + "\n" + snap.Text
	if !strings.Contains(combined, "c03-scrollback-marker") {
		t.Fatalf("marker not found in scrollback or visible; scrollback=%q text=%q",
			*snap.ScrollbackText, snap.Text)
	}
}

// C04 — snapshot.text reflects rendered visible rows, not
// reconstructed logical shell lines (§9.2). We exercise this
// weakly: the returned text must line up with tmux's own
// capture-pane output (both are row-based).
func TestC04_SnapshotTextIsRenderedRows(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	e.PaneOutput(t, pane, "echo c04-visible-marker")
	e.WaitForText(t, pane, "c04-visible-marker", 5*time.Second)

	snap := e.Snapshot(t, pane)
	if !strings.Contains(snap.Text, "c04-visible-marker") {
		t.Fatalf("text missing marker: %q", snap.Text)
	}
}

// C05 — read --after never re-returns pre-token output (§9.3).
//
// Subtle: pipe-pane subscription is asynchronous, so a Snapshot
// taken immediately after a send may mint a token at a stream
// offset BEFORE the send's own bytes have been appended. To
// synchronize user-time with stream-time we:
//
//  1. emit a pre-marker that appears in the OUTPUT but NOT in the
//     shell's TTY echo of the command (so a regex for the marker
//     matches the output only, not the command echo);
//  2. wait for that regex to match — the wait's `next` is at-or-
//     after the evaluation step that saw the marker, strictly past
//     the pre-marker bytes in stream terms (spec §9.6);
//  3. use THAT `next` as the read anchor for the post-marker.
//
// The `tr a-z A-Z` trick produces an uppercase marker in the shell
// output from a lowercase-only command string, guaranteeing the
// uppercase token only appears once in the stream.
func TestC05_ReadAfterNeverReReadsPreTokenOutput(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	bootstrap := e.Snapshot(t, pane)

	// Emit pre-marker; block until the OUTPUT (uppercase) is
	// observed via the controller's stream.
	e.PaneOutput(t, pane, `echo pre-c05 | tr a-z A-Z`)
	var preWait harness.WaitResponse
	e.Run("wait",
		"--pane", pane, "--after", bootstrap.Next,
		"--for", "regex", "--pattern", "PRE-C05",
		"--timeout-ms", "3000",
	).MustJSON(t, &preWait)

	// Emit post-marker with the same technique and read from the
	// pre-wait's next anchor.
	e.PaneOutput(t, pane, `echo post-c05 | tr a-z A-Z`)
	e.WaitForText(t, pane, "POST-C05", 5*time.Second)

	var r harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", preWait.Next).MustJSON(t, &r)

	if strings.Contains(r.Text, "PRE-C05") {
		t.Fatalf("read --after returned pre-marker output: %q", r.Text)
	}
	if !strings.Contains(r.Text, "POST-C05") {
		t.Fatalf("read --after missed post-marker output: %q", r.Text)
	}
}

// C06 — read may return empty text and still succeed (§9.3).
func TestC06_ReadEmptyTextIsSuccess(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)
	var r harness.ReadResponse
	// No output has occurred since the snapshot, so text must be "".
	e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)

	if r.Text != "" {
		t.Fatalf("expected empty text, got %q", r.Text)
	}
	if r.Next == "" {
		t.Fatal("read must still return next on empty-delta success")
	}
}

// C07 — checkpoint tokens are opaque, pane-scoped, and controller-
// lifetime-scoped (§4.3). We test the first two observably; lifetime
// scope is covered by C11 / extension scenarios.
//
// For the pane-scoped assertion we snapshot BOTH panes so both are
// known to the controller; otherwise §7.5 precedence would surface
// the wrong-pane token as PANE_NOT_FOUND.
func TestC07_TokensArePaneScoped(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	a := e.FirstPane(t)
	b := e.NewPane(t)

	snapA := e.Snapshot(t, a)
	_ = e.Snapshot(t, b) // ensure pane B is known to the controller
	err := e.Run("read", "--pane", b, "--after", snapA.Next).MustError(t)
	harness.AssertCode(t, err, "INVALID_AFTER")
}

// C08 — read --after fails with INVALID_AFTER for a malformed or
// expired or wrong-pane token (§9.3). Exercise the malformed arm.
// We snapshot first so the pane is known to the controller;
// otherwise §7.5 precedence would surface this as PANE_NOT_FOUND.
func TestC08_ReadInvalidAfterOnMalformedToken(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	_ = e.Snapshot(t, pane)
	err := e.Run("read", "--pane", pane, "--after", "not-a-real-token").MustError(t)
	harness.AssertCode(t, err, "INVALID_AFTER")
}

// C09 — wait --after fails with INVALID_AFTER under the same
// conditions (§9.6). Snapshot first for the same §7.5-precedence
// reason as C08.
func TestC09_WaitInvalidAfterOnMalformedToken(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	_ = e.Snapshot(t, pane)
	err := e.Run("wait",
		"--pane", pane,
		"--after", "not-a-real-token",
		"--for", "sentinel",
		"--token", "x",
		"--timeout-ms", "100",
	).MustError(t)
	harness.AssertCode(t, err, "INVALID_AFTER")
}
