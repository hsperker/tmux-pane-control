package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

func runApp(args []string) (code int, stdout, stderr string) {
	var so, se bytes.Buffer
	app := &App{Stdout: &so, Stderr: &se}
	code = app.Run(args)
	return code, so.String(), se.String()
}

func TestHelp(t *testing.T) {
	code, stdout, stderr := runApp([]string{"--help"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "tpctl") {
		t.Fatalf("stdout missing tpctl: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr not empty: %q", stderr)
	}
}

func TestNoArgs(t *testing.T) {
	code, _, stderr := runApp(nil)
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	if stderr == "" {
		t.Fatal("want usage on stderr")
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := runApp([]string{"bogus"})
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("stderr: %q", stderr)
	}
}

func TestListMutuallyExclusiveSocketFlags(t *testing.T) {
	code, stdout, _ := runApp([]string{"list", "--tmux-socket", "/tmp/a", "--tmux-socket-name", "foo"})
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	var e domain.ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e); err != nil {
		t.Fatalf("stdout not JSON: %q (%v)", stdout, err)
	}
	if e.Code != domain.ErrInvalidArgs {
		t.Fatalf("code = %q", e.Code)
	}
}

func TestListRejectsPositionals(t *testing.T) {
	code, stdout, _ := runApp([]string{"list", "extra"})
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	var e domain.ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e); err != nil {
		t.Fatalf("stdout not JSON: %q (%v)", stdout, err)
	}
	if e.Code != domain.ErrInvalidArgs {
		t.Fatalf("code = %q", e.Code)
	}
	if e.Message == "" {
		t.Fatal("message must not be empty (spec §7.2)")
	}
}

// TestTextRequiresExactlyOnePositional guards spec §9.4's hard rule:
// "Passing zero or more than one positional argument is a command-
// level error." The CLI layer must enforce this before any IPC work.
func TestTextRequiresExactlyOnePositional(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"zero", []string{"text", "--pane", "%0"}},
		{"two", []string{"text", "--pane", "%0", "one", "two"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, _ := runApp(tc.args)
			if code == 0 {
				t.Fatal("want nonzero exit")
			}
			var e domain.ErrorResponse
			if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e); err != nil {
				t.Fatalf("stdout not JSON: %q", stdout)
			}
			if e.Code != domain.ErrInvalidArgs {
				t.Fatalf("code = %q", e.Code)
			}
			if !strings.Contains(e.Message, "one positional") {
				t.Fatalf("message = %q, should mention 'one positional'", e.Message)
			}
		})
	}
}

// TestTextEmptyPayloadIsValid confirms spec §9.4's explicit allowance:
// "an empty payload is valid: tpctl text --pane %42 '' --enter".
// We can't reach the daemon here (no tmux), so we just assert the CLI
// passes argument validation and reaches the dial stage, surfacing a
// runtime error rather than ErrInvalidArgs.
func TestTextEmptyPayloadPassesArgValidation(t *testing.T) {
	// Force a tmux socket that doesn't exist so dial fails AFTER
	// positional validation succeeded.
	code, stdout, stderr := runApp([]string{"text",
		"--pane", "%0",
		"--tmux-socket", "/dev/null/nope",
		"", "--enter",
	})
	if code == 0 {
		t.Fatal("want nonzero (dial must fail for bogus socket)")
	}
	// If the error came from arg validation, it'd be ErrInvalidArgs
	// on stdout. A dial-stage failure goes to stderr.
	if strings.Contains(stdout, `"code":"INVALID_ARGS"`) &&
		strings.Contains(stdout, "positional") {
		t.Fatalf("empty payload wrongly rejected by arg validation: %q", stdout)
	}
	_ = stderr
}

// TestKeyRequiresAtLeastOneToken guards spec §9.5: at least one key
// token must follow --pane.
func TestKeyRequiresAtLeastOneToken(t *testing.T) {
	code, stdout, _ := runApp([]string{"key", "--pane", "%0"})
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	var e domain.ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e); err != nil {
		t.Fatalf("stdout not JSON: %q", stdout)
	}
	if e.Code != domain.ErrInvalidArgs {
		t.Fatalf("code = %q", e.Code)
	}
}

// TestPaneFlagRequired guards spec §5.1: every pane-taking command
// must reject a missing or malformed --pane.
func TestPaneFlagRequired(t *testing.T) {
	for _, op := range []string{"snapshot", "read", "text", "key", "wait"} {
		t.Run(op, func(t *testing.T) {
			code, stdout, _ := runApp([]string{op})
			if code == 0 {
				t.Fatalf("%s: want nonzero exit", op)
			}
			var e domain.ErrorResponse
			_ = json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e)
			if e.Code != domain.ErrInvalidArgs {
				t.Fatalf("%s: code = %q", op, e.Code)
			}
		})
	}
}

// TestPaneFlagMustStartWithPercent guards spec §5: the canonical pane
// identity is a tmux `%N` id; anything else is rejected at the CLI.
func TestPaneFlagMustStartWithPercent(t *testing.T) {
	code, stdout, _ := runApp([]string{"snapshot", "--pane", "0"})
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	var e domain.ErrorResponse
	_ = json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e)
	if e.Code != domain.ErrInvalidArgs {
		t.Fatalf("code = %q", e.Code)
	}
	if !strings.Contains(e.Message, "tmux pane id") {
		t.Fatalf("message = %q", e.Message)
	}
}

// TestGlobalFlagsBeforeSubcommand pins spec §6.2: global flags
// (--tmux-socket, --tmux-socket-name, --help) must work both
// BEFORE and AFTER the subcommand. Extraction must not mis-read
// an unknown-to-globals flag as a global.
func TestGlobalFlagsBeforeSubcommand(t *testing.T) {
	// --tmux-socket before subcommand: should NOT fall into the
	// "unknown command" branch; instead extraction hoists it.
	// We can't easily dial a real daemon here, but we CAN verify
	// that --pane-less snapshot still surfaces an INVALID_ARGS
	// from the snapshot handler (proving dispatch reached it).
	code, stdout, _ := runApp([]string{
		"--tmux-socket", "/tmp/nope.sock", "snapshot",
	})
	if code == 0 {
		t.Fatal("want nonzero (missing --pane)")
	}
	var e domain.ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e); err != nil {
		t.Fatalf("stdout not JSON, dispatch didn't reach snapshot: %q", stdout)
	}
	if e.Code != domain.ErrInvalidArgs {
		t.Fatalf("code = %q", e.Code)
	}
}

func TestGlobalFlagsEqualsFormBeforeSubcommand(t *testing.T) {
	code, stdout, _ := runApp([]string{
		"--tmux-socket=/tmp/nope.sock", "snapshot",
	})
	if code == 0 {
		t.Fatal("want nonzero")
	}
	var e domain.ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e); err != nil {
		t.Fatalf("stdout not JSON: %q", stdout)
	}
	if e.Code != domain.ErrInvalidArgs {
		t.Fatalf("code = %q", e.Code)
	}
}

func TestHelpBeforeSubcommand(t *testing.T) {
	// --help before anything else: top-level usage, exit 0.
	code, stdout, stderr := runApp([]string{"--help"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "tpctl") {
		t.Fatalf("stdout: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr: %q", stderr)
	}
}

func TestHelpAfterGlobalFlags(t *testing.T) {
	// --tmux-socket X --help: global flags before --help still
	// yield top-level help; no dial attempt.
	code, stdout, _ := runApp([]string{
		"--tmux-socket", "/tmp/nope.sock", "--help",
	})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "tpctl") {
		t.Fatalf("stdout: %q", stdout)
	}
}

// TestExtractGlobals_PureLogic exercises the extractor directly so
// we catch regressions in edge cases the end-to-end tests don't
// cover (unknown long flags, -h shortcut, -- sentinel, etc.).
func TestExtractGlobals_PureLogic(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantGlobals []string
		wantHelp    bool
		wantRest    []string
	}{
		{"no_globals", []string{"list"}, nil, false, []string{"list"}},
		{"socket_space_form", []string{"--tmux-socket", "/s", "list"},
			[]string{"--tmux-socket", "/s"}, false, []string{"list"}},
		{"socket_equals_form", []string{"--tmux-socket=/s", "list"},
			[]string{"--tmux-socket=/s"}, false, []string{"list"}},
		{"socket_name_form", []string{"--tmux-socket-name", "mux", "list"},
			[]string{"--tmux-socket-name", "mux"}, false, []string{"list"}},
		{"help_long", []string{"--help"}, nil, true, []string{}},
		{"help_short", []string{"-h"}, nil, true, []string{}},
		{"help_then_subcmd", []string{"--help", "list"}, nil, true, []string{"list"}},
		{"global_then_help", []string{"--tmux-socket", "/s", "--help"},
			[]string{"--tmux-socket", "/s"}, true, []string{}},
		{"double_dash_halts", []string{"--", "list"}, nil, false, []string{"--", "list"}},
		{"unknown_flag_is_subcmd", []string{"--bogus", "list"},
			nil, false, []string{"--bogus", "list"}},
		{"subcmd_only", []string{"snapshot", "--pane", "%0"},
			nil, false, []string{"snapshot", "--pane", "%0"}},
		{"empty", nil, nil, false, []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, h, r := extractGlobals(tc.args)
			if !stringSliceEqual(g, tc.wantGlobals) {
				t.Errorf("globals = %v, want %v", g, tc.wantGlobals)
			}
			if h != tc.wantHelp {
				t.Errorf("helpSeen = %v, want %v", h, tc.wantHelp)
			}
			if !stringSliceEqual(r, tc.wantRest) {
				t.Errorf("remaining = %v, want %v", r, tc.wantRest)
			}
		})
	}
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
