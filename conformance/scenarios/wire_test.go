// Wire-level JSON validity checks. The harness's MustJSON already
// decodes response stdout into typed structs via encoding/json, so
// obvious parse failures would surface there. These tests go one
// step further: they assert the RAW stdout bytes pass json.Valid
// BEFORE any decoding, which catches a specific regression class —
// an implementation that hand-rolls JSON serialization and forgets
// to escape control characters (raw \n inside a string value, for
// instance).
//
// Pane content with multiple lines is the interesting case because
// it forces the implementation to escape \n inside the text field.
// A parser that "works" on single-line output would silently break
// on multi-line output if the bytes weren't properly escaped.

package scenarios

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/conformance/harness"
)

// TestWire_SnapshotJSONValidWithMultilineText asserts that the raw
// stdout of snapshot --pane on a pane containing several lines of
// output is RFC 8259-valid JSON (no unescaped control characters
// in any string value).
func TestWire_SnapshotJSONValidWithMultilineText(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	e.Run("text", "--pane", pane,
		"echo wire-A; echo wire-B; echo wire-C", "--enter").MustMuted(t)
	e.WaitForText(t, pane, "wire-C", 5*time.Second)

	r := e.Run("snapshot", "--pane", pane)
	if r.Code != 0 {
		t.Fatalf("snapshot exit=%d stderr=%q", r.Code, r.Stderr)
	}

	raw := strings.TrimSpace(r.Stdout)
	if !json.Valid([]byte(raw)) {
		t.Fatalf("snapshot stdout is not valid JSON: %q", raw)
	}

	// Assert no raw control chars slipped through inside the JSON
	// payload. json.Valid already rejects these, but the explicit
	// byte check makes the diagnostic obvious when someone
	// introduces the regression.
	for i, b := range []byte(raw) {
		if b == '\n' || b == '\r' || b < 0x20 {
			// Allow no control characters anywhere in the payload.
			// The trailing newline from the CLI was trimmed above.
			t.Fatalf("raw control byte 0x%02x at offset %d in stdout: %q",
				b, i, raw)
		}
	}

	// Post-decode, the text field must contain real newlines
	// separating our markers — the escape should round-trip.
	var snap harness.SnapshotResponse
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range []string{"wire-A", "wire-B", "wire-C"} {
		if !strings.Contains(snap.Text, m) {
			t.Fatalf("text missing %q: %q", m, snap.Text)
		}
	}
}

// TestWire_ReadJSONValidWithMultilineText covers the same
// invariant on read.text, which is the other text-bearing
// response.
func TestWire_ReadJSONValidWithMultilineText(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	e.Run("text", "--pane", pane,
		"echo wire-D; echo wire-E", "--enter").MustMuted(t)
	e.WaitForText(t, pane, "wire-E", 5*time.Second)

	r := e.Run("read", "--pane", pane, "--after", snap.Next)
	if r.Code != 0 {
		t.Fatalf("read exit=%d stderr=%q", r.Code, r.Stderr)
	}

	raw := strings.TrimSpace(r.Stdout)
	if !json.Valid([]byte(raw)) {
		t.Fatalf("read stdout is not valid JSON: %q", raw)
	}

	var rr harness.ReadResponse
	if err := json.Unmarshal([]byte(raw), &rr); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range []string{"wire-D", "wire-E"} {
		if !strings.Contains(rr.Text, m) {
			t.Fatalf("text missing %q: %q", m, rr.Text)
		}
	}
}

// TestWire_SnapshotWithScrollbackJSONValid covers --history-lines,
// where the implementation has TWO text-bearing fields to escape
// (scrollback_text and text). A hand-rolled serializer that got
// text right but forgot scrollback_text would fail here and nowhere
// else.
func TestWire_SnapshotWithScrollbackJSONValid(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Push some content into scrollback.
	e.Run("text", "--pane", pane, "echo wire-F; echo wire-G", "--enter").MustMuted(t)
	e.WaitForText(t, pane, "wire-G", 5*time.Second)
	for i := 0; i < 30; i++ {
		e.Run("text", "--pane", pane, "", "--enter").MustMuted(t)
	}

	r := e.Run("snapshot", "--pane", pane, "--history-lines", "100")
	if r.Code != 0 {
		t.Fatalf("snapshot exit=%d stderr=%q", r.Code, r.Stderr)
	}

	raw := strings.TrimSpace(r.Stdout)
	if !json.Valid([]byte(raw)) {
		t.Fatalf("snapshot+history stdout is not valid JSON: %q", raw)
	}
	var snap harness.SnapshotResponse
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if snap.ScrollbackText == nil {
		t.Fatal("scrollback_text missing")
	}
}
