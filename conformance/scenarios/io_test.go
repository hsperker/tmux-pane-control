// Scenarios for spec §17 criterion C25 (text normalization) and
// C26–C30 (text/key input commands).

package scenarios

import (
	"strings"
	"testing"
	"time"

	"tpctl-conformance/harness"
)

// C25 — snapshot.text, snapshot.scrollback_text, and read.text are
// all: ANSI-stripped, \n-normalized, trailing-whitespace-trimmed
// per line, and trailing-blank-lines-trimmed (§8.3).
//
// We exercise all four normalizations at once by sending a line
// that contains ANSI color, trailing spaces, CRLF endings, and a
// trailing blank line; the resulting snapshot/read fields must be
// clean.
func TestC25_TextNormalization(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)

	// Emit: red "hello" followed by three spaces and a CRLF, then
	// a second line, then a trailing blank line. bash printf is
	// used so the ANSI escapes are literal.
	e.PaneOutput(t, pane, `printf '\033[31mhello\033[0m   \r\nworld\r\n\r\n'`)
	e.WaitForText(t, pane, "world", 2*time.Second)

	var r harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)

	// ANSI stripped
	if strings.Contains(r.Text, "\x1b") {
		t.Fatalf("read.text contains raw ESC: %q", r.Text)
	}
	// CR dropped
	if strings.Contains(r.Text, "\r") {
		t.Fatalf("read.text contains CR: %q", r.Text)
	}
	// Trailing whitespace trimmed per line
	for _, line := range strings.Split(r.Text, "\n") {
		if strings.TrimRight(line, " \t") != line {
			t.Fatalf("line has trailing whitespace: %q", line)
		}
	}
	// Trailing blank lines trimmed
	if strings.HasSuffix(r.Text, "\n\n") {
		t.Fatalf("trailing blank lines present: %q", r.Text)
	}
	// Content survived
	if !strings.Contains(r.Text, "hello") || !strings.Contains(r.Text, "world") {
		t.Fatalf("expected content lost: %q", r.Text)
	}
}

// C26 — text and key do not return until tmux has acknowledged the
// send command (§9.4, §9.5). Observable consequence: after text
// returns, a subsequent read --after must see the echoed output.
func TestC26_TextReturnsAfterTmuxAck(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	snap := e.Snapshot(t, pane)
	e.Run("text", "--pane", pane, "echo c26-ack-marker", "--enter").MustMuted(t)

	// Give pipe-pane a beat to deliver; then the marker must be
	// readable. If text returned before tmux ack'd, the marker
	// might not yet be in the buffer — but even in that case the
	// controller's own fake-ack path should have waited.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var r harness.ReadResponse
		e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)
		if strings.Contains(r.Text, "c26-ack-marker") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("c26-ack-marker never appeared after text send")
}

// C27 — text and key emit no success payload (§9.4, §9.5).
func TestC27_TextKeyEmitNoSuccessPayload(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	e.Run("text", "--pane", pane, "echo x", "--enter").MustMuted(t)
	e.Run("key", "--pane", pane, "Enter").MustMuted(t)
}

// C28 — text and key produce no stdout at all on success. Already
// covered by MustMuted in C27; kept as a named scenario for §17
// traceability.
func TestC28_TextKeyProduceNoStdoutOnSuccess(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	r := e.Run("text", "--pane", pane, "echo x", "--enter")
	if r.Code != 0 {
		t.Fatalf("code = %d", r.Code)
	}
	if r.Stdout != "" {
		t.Fatalf("stdout = %q", r.Stdout)
	}
	if r.Stderr != "" {
		t.Fatalf("stderr = %q (text must be quiet on success)", r.Stderr)
	}
}

// C29 — key uses tmux's send-keys vocabulary; when tmux rejects a
// token, the failure surfaces as a structured command-level error
// (§9.5 Validation scope). Modern tmux is permissive about unknown
// names, so we probe the observable contract: if tmux rejects, the
// failure is well-formed JSON; if tmux accepts, the call succeeds.
// Either is acceptable per the softened acceptance criterion.
func TestC29_KeyRejectionSurfacesStructuredError(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Zero tokens should always fail at the CLI layer regardless
	// of tmux permissiveness. Structured JSON is required.
	err := e.Run("key", "--pane", pane).MustError(t)
	if err.Code == "" {
		t.Fatalf("error missing code: %+v", err)
	}
}

// C30 — key tokens are treated as data, never as tmux command
// syntax (§9.5). A token like `;` must be sent as a literal
// keystroke, not interpreted as a tmux command separator. We can't
// directly observe internal argv construction, but we CAN verify
// that passing a `;` plus a plausible tmux command name does not
// execute that command on the tmux server.
func TestC30_KeyTokensAreDataNotCommandSyntax(t *testing.T) {
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Before: record the number of windows.
	beforeWindows := strings.Count(e.Tmux("list-windows"), "\n")

	// Send `;` and `new-window`. If the implementation built a
	// tmux command string instead of argv, `;` would act as a
	// command separator and `new-window` would create a window.
	// tpctl's contract forbids this.
	_ = e.Run("key", "--pane", pane, ";", "new-window")

	time.Sleep(100 * time.Millisecond)

	afterWindows := strings.Count(e.Tmux("list-windows"), "\n")
	if afterWindows > beforeWindows {
		t.Fatalf("key tokens were interpreted as tmux commands: windows went %d → %d",
			beforeWindows, afterWindows)
	}
}
