// Scenarios for spec §17 criteria C36–C38 (error conventions) and
// C39 (architecture). C39 is structural and cannot be tested
// externally; the scenario asserts the observable surface is
// at least self-consistent.

package scenarios

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hsperker/tmux-pane-control/conformance/harness"
)

// C36 — command-level failures emit structured JSON on stdout with
// a nonzero exit code (§7.2).
func TestC36_CmdErrorsAreJSONOnStdoutNonzeroExit(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)

	// A handful of known-bad invocations; each must fulfill the
	// contract shape.
	cases := []struct {
		name string
		args []string
	}{
		{"missing_after_read", []string{"read", "--pane", "%0"}},
		{"missing_after_wait_sentinel", []string{"wait",
			"--pane", "%0", "--for", "sentinel",
			"--token", "x", "--timeout-ms", "100"}},
		{"invalid_pane_format", []string{"snapshot", "--pane", "42"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := e.Run(tc.args...)
			if r.Code == 0 {
				t.Fatalf("expected nonzero exit; stdout=%q", r.Stdout)
			}
			if r.Stdout == "" {
				t.Fatalf("expected JSON on stdout; stdout empty, stderr=%q", r.Stderr)
			}
			var er harness.ErrorResponse
			if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &er); err != nil {
				t.Fatalf("stdout not JSON: %v (%q)", err, r.Stdout)
			}
			if er.Code == "" || er.Message == "" {
				t.Fatalf("error response missing code/message: %+v", er)
			}
		})
	}
}

// C37 — runtime or controller failures emit diagnostics on stderr
// with a nonzero exit code (§7.3).
func TestC37_RuntimeFailuresGoToStderr(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)

	// Point list at a bogus tmux socket — there's no tmux server
	// there, so either the daemon fails to connect or its
	// adapter surfaces a runtime error on stderr.
	r := e.RunArgs("list", "--tmux-socket", "/tmp/conformance-no-such-tmux.sock")
	if r.Code == 0 {
		t.Fatalf("expected nonzero exit; stdout=%q", r.Stdout)
	}
	if r.Stderr == "" {
		t.Fatalf("expected runtime diagnostic on stderr; got stdout=%q", r.Stdout)
	}
}

// C38 — the canonical error codes MISSING_AFTER, INVALID_AFTER,
// PANE_NOT_FOUND, PANE_CLOSED, and TIMEOUT are used where
// applicable (§7.5). We already exercise each of these in other
// scenarios; here we assert them as a group for §17 traceability.
func TestC38_CanonicalErrorCodes(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// MISSING_AFTER
	err := e.Run("read", "--pane", pane).MustError(t)
	harness.AssertCode(t, err, "MISSING_AFTER")

	// INVALID_AFTER. We first snapshot so the pane is known to the
	// controller; otherwise §7.5 precedence would surface this as
	// PANE_NOT_FOUND (pane-existence wins over token validation
	// when the target pane is unknown).
	_ = e.Snapshot(t, pane)
	err = e.Run("read", "--pane", pane, "--after", "garbage").MustError(t)
	harness.AssertCode(t, err, "INVALID_AFTER")

	// PANE_NOT_FOUND
	err = e.Run("snapshot", "--pane", "%999").MustError(t)
	harness.AssertCode(t, err, "PANE_NOT_FOUND")

	// TIMEOUT
	snap := e.Snapshot(t, pane)
	err = e.Run("wait",
		"--pane", pane, "--after", snap.Next,
		"--for", "regex", "--pattern", "never-c38",
		"--timeout-ms", "100",
	).MustError(t)
	harness.AssertCode(t, err, "TIMEOUT")

	// PANE_CLOSED is exercised by TestC34.
}

// C39 — architectural patterns (§10) are structural and cannot be
// observed externally. We assert the observable consequences: the
// spec's abstraction boundary (CLI, JSON-on-stdout, opaque tokens)
// holds across the full surface we exercise elsewhere. This test
// is a sanity pass for traceability.
func TestC39_ArchitectureIsExternallyConsistent(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Tokens round-trip as opaque strings: we never need to parse
	// them to use them.
	snap := e.Snapshot(t, pane)
	var r harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)

	// Success uses stdout, zero exit; errors use JSON stdout,
	// nonzero exit; mutations are quiet.
	e.Run("text", "--pane", pane, "echo ok", "--enter").MustMuted(t)

	errRes := e.Run("snapshot", "--pane", "%999").MustError(t)
	if errRes.Code == "" {
		t.Fatalf("error code missing: %+v", errRes)
	}
}
