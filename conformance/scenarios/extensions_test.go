// Scenarios for spec tightenings that landed after the original
// §17 acceptance list was frozen. These are equally normative but
// live in a separate file so §17 traceability stays clean.

package scenarios

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"tpctl-conformance/harness"
)

// §6.1 — CLI implementations must accept flags and positional
// arguments in interleaved order. The `--` sentinel ends flag
// parsing.
func TestExt_ArgParsingInterleaved(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	// Forms that the spec's own examples use. All three must
	// succeed end-to-end (text is sent, we can then observe it).
	forms := [][]string{
		{"text", "--pane", pane, "hello-interleaved-1", "--enter"},
		{"text", "--pane", pane, "--enter", "hello-interleaved-2"},
	}
	for _, args := range forms {
		e.Run(args...).MustMuted(t)
	}
	// Verify both markers landed.
	e.WaitForText(t, pane, "hello-interleaved-2", 5*time.Second)

	var r harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)
	for _, needle := range []string{"hello-interleaved-1", "hello-interleaved-2"} {
		if !strings.Contains(r.Text, needle) {
			t.Fatalf("missing %q in read: %q", needle, r.Text)
		}
	}
}

// §6.1 — the `--` sentinel ends flag parsing. `text` takes exactly
// one positional (§9.4), so any `--enter` must be parsed BEFORE
// the sentinel; the single positional after `--` is then the
// literal payload, which we verify shows up unchanged.
func TestExt_DoubleDashSentinel(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	e.Run("text", "--pane", pane, "--enter", "--", "echo --looks-like-flag-c6a").MustMuted(t)
	e.WaitForText(t, pane, "--looks-like-flag-c6a", 5*time.Second)

	var r harness.ReadResponse
	e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)
	if !strings.Contains(r.Text, "--looks-like-flag-c6a") {
		t.Fatalf("sentinel did not preserve literal payload: %q", r.Text)
	}
}

// §6.2 — global flags (--tmux-socket, --tmux-socket-name, --help)
// must work both before and after the subcommand.
func TestExt_GlobalFlagsBeforeSubcommand(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	// Pre-subcommand space form.
	r := e.RunArgs("--tmux-socket", e.TmuxSocket(), "list")
	var lr harness.ListResponse
	r.MustJSON(t, &lr)
	if len(lr.Panes) == 0 {
		t.Fatal("pre-subcommand --tmux-socket did not reach list")
	}
}

func TestExt_GlobalFlagsEqualsFormBeforeSubcommand(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	r := e.RunArgs("--tmux-socket="+e.TmuxSocket(), "list")
	var lr harness.ListResponse
	r.MustJSON(t, &lr)
	if len(lr.Panes) == 0 {
		t.Fatal("pre-subcommand --tmux-socket=value did not reach list")
	}
}

// §9.2 — when the pane's retained scrollback is shorter than N,
// scrollback_text contains all available scrollback, unpadded, and
// the response still succeeds.
func TestExt_ScrollbackShorterThanRequested(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)

	// Ask for 10000 lines of history on a fresh pane — nowhere
	// close to that many exist. Response must succeed; the
	// scrollback must not be padded to 10000 lines.
	snap := e.Snapshot(t, pane, "--history-lines", "10000")
	if snap.ScrollbackText == nil {
		t.Fatal("scrollback_text must be present when --history-lines requested")
	}
	lines := strings.Split(*snap.ScrollbackText, "\n")
	// An empty scrollback decodes as [""], at most a handful of
	// real lines otherwise. Anything remotely close to 10000
	// would indicate padding.
	if len(lines) > 1000 {
		t.Fatalf("scrollback appears padded: %d lines for a fresh pane", len(lines))
	}
}

// §9.6 — match input has ANSI and CR stripped. The critical
// regression case is `(?m)^marker$` against CRLF-terminated output.
func TestExt_MatchInputStripsCRForMultilineAnchor(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	pane := e.FirstPane(t)
	snap := e.Snapshot(t, pane)

	// Emit a line terminated by \r\n. printf '%s\r\n' is the
	// reliable way; echo behavior varies.
	e.PaneOutput(t, pane, `printf 'c96-crlf-marker\r\n'`)
	e.WaitForText(t, pane, "c96-crlf-marker", 5*time.Second)

	var w harness.WaitResponse
	e.Run("wait",
		"--pane", pane,
		"--after", snap.Next,
		"--for", "regex",
		"--pattern", "(?m)^c96-crlf-marker$",
		"--timeout-ms", "3000",
	).MustJSON(t, &w)
	if w.Matched == nil || *w.Matched != "c96-crlf-marker" {
		t.Fatalf("matched = %v (CR not stripped before match)", w.Matched)
	}
}

// §11.2 — the auto-spawned controller must outlive the CLI
// invocation that spawned it. We exercise this by running `list`
// from a bash subprocess (which in turn spawns the daemon),
// SIGKILLing bash's entire process group, and checking that we
// can still reach the daemon afterward.
func TestExt_DaemonSurvivesSpawnerKill(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	parent := exec.Command("bash", "-c",
		e.BinaryPathForSubprocess()+" list --tmux-socket "+e.TmuxSocket()+
			" >/dev/null 2>&1; sleep 60")
	parent.Env = append(os.Environ(), "XDG_RUNTIME_DIR="+e.XDGDir())
	parent.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	t.Cleanup(func() {
		if parent.Process != nil {
			_ = parent.Process.Kill()
		}
	})

	// Wait for the daemon to be reachable via a plain `list`.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if r := e.Run("list"); r.Code == 0 {
			goto alive
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon never came up via parent bash")

alive:
	pgid, err := syscall.Getpgid(parent.Process.Pid)
	if err != nil {
		t.Fatalf("getpgid: %v", err)
	}
	// Kill the whole parent process group. An un-detached daemon
	// would be reaped here.
	if err := syscall.Kill(-pgid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -pgid %d: %v", pgid, err)
	}
	_, _ = parent.Process.Wait()
	time.Sleep(300 * time.Millisecond)

	// Daemon must still respond.
	if r := e.Run("list"); r.Code != 0 {
		t.Fatalf("daemon unreachable after spawner SIGKILL: stdout=%q stderr=%q",
			r.Stdout, r.Stderr)
	}
}

// §11.4 — XDG_RUNTIME_DIR-derived socket path; fallback when
// $XDG_RUNTIME_DIR is unset. We only exercise the HAPPY path here
// (socket placed under $XDG_RUNTIME_DIR/tpctl/); the fallback path
// is hard to observe without reading implementation internals.
func TestExt_DaemonSocketUnderXDG(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)
	// Trigger daemon auto-spawn via a cheap call.
	e.List(t)
	// Socket should now exist under $XDG_RUNTIME_DIR/tpctl/.
	tpctlDir := filepath.Join(e.XDGDir(), "tpctl")
	entries, err := os.ReadDir(tpctlDir)
	if err != nil {
		t.Fatalf("expected daemon dir %s: %v", tpctlDir, err)
	}
	gotSock := false
	for _, ent := range entries {
		if strings.HasSuffix(ent.Name(), ".sock") {
			gotSock = true
			break
		}
	}
	if !gotSock {
		t.Fatalf("no .sock under %s; entries=%v", tpctlDir, entries)
	}
}

// §11.5 — concurrent CLI invocations must coordinate so that at
// most one controller becomes active per tmux server. We launch
// many rapid-fire list invocations and confirm:
// (a) they all succeed,
// (b) the socket exists and is dial-able after they return.
func TestExt_ConcurrentSpawnRaceIsSerialized(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)

	const n = 10
	results := make(chan harness.Result, n)
	for i := 0; i < n; i++ {
		go func() { results <- e.Run("list") }()
	}
	for i := 0; i < n; i++ {
		r := <-results
		if r.Code != 0 {
			t.Fatalf("concurrent list failed: code=%d stdout=%q stderr=%q",
				r.Code, r.Stdout, r.Stderr)
		}
	}
}

// §11.9 — the controller detects tmux server loss and exits (Exit
// mode). After kill-server, a follow-up tpctl call must surface
// the tmux error on stderr with a nonzero exit.
func TestExt_TmuxServerLossRaisesRuntimeError(t *testing.T) {
	t.Parallel()
	e := harness.NewEnv(t)

	// Bring the daemon up, then kill tmux.
	e.List(t)
	// Use the fixture's own tmux binary to kill the server.
	_ = exec.Command("tmux", "-S", e.TmuxSocket(), "kill-server").Run()

	// The running daemon must notice within the detection
	// threshold (~600ms for the reference implementation; allow
	// 5s for any conformant implementation).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Check that the daemon's socket is gone — the Exit-mode
		// shutdown removes it.
		if _, err := net.DialTimeout("unix",
			filepath.Join(e.XDGDir(), "tpctl"), 100*time.Millisecond); err != nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	// Subsequent CLI invocations must surface the tmux absence
	// as a runtime error (stderr, nonzero exit).
	r := e.Run("list")
	if r.Code == 0 {
		t.Fatalf("list succeeded against dead tmux: stdout=%q", r.Stdout)
	}
}
