// Package harness is the runtime support for tpctl v1 conformance
// scenarios. It owns the per-test tmux server, the tpctl invocation
// machinery, and a few assertion helpers — everything a scenario
// needs to drive the binary under test through its spec-defined CLI
// surface.
//
// The harness intentionally imports nothing from any particular
// tpctl implementation. Response types are redefined locally from
// spec §9 examples; tokens are treated as opaque strings.
package harness

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// binaryFlag names the tpctl (or compatible) binary under test.
// Registered at package init so scenario packages see it via
// go test's normal flag parsing.
var binaryFlag = flag.String("binary", "",
	"path to the tpctl (or conformant) binary under test; required")

// tmuxFlag lets an operator swap in a non-default tmux binary, e.g.
// to test against multiple tmux versions from one suite run.
var tmuxFlag = flag.String("tmux", "tmux",
	"path or name of tmux binary to use for fixtures")

// Binary returns the binary path from the --binary flag. Scenarios
// call this (via requireBinary) to fail fast when unconfigured.
func Binary() string { return *binaryFlag }

// invocation captures one CLI call for failure-time diagnostics.
type invocation struct {
	when time.Time
	args []string
	code int
	// stdout/stderr are truncated to avoid wall-of-text dumps.
	stdout string
	stderr string
}

// Env is one conformance scenario's world: a fresh tmux server on a
// disposable socket, a dedicated XDG_RUNTIME_DIR so the daemon's
// socket is isolated, and the binary under test. Cleanup is
// registered with t.Cleanup at creation time.
type Env struct {
	t       *testing.T
	bin     string
	tmux    string
	tmuxSoc string
	xdgDir  string

	// Safe for t.Parallel(): mu guards the scenario-local state
	// that tests may touch from goroutines (concurrent waits, etc).
	mu       sync.Mutex
	history  []invocation // bounded — last ~32 invocations
	panes    []string     // panes we've explicitly tracked via NewPane
}

// NewEnv provisions a scenario-local environment. It skips the test
// if --binary is unset or tmux is unavailable, so the suite degrades
// gracefully on machines missing prerequisites.
//
// NewEnv does NOT set XDG_RUNTIME_DIR in the test process env — it
// only forwards the value into subprocesses — so scenarios calling
// NewEnv may call t.Parallel() freely. (t.Setenv is incompatible
// with t.Parallel().)
func NewEnv(t *testing.T) *Env {
	t.Helper()
	if *binaryFlag == "" {
		t.Skip("--binary flag not set; pass the tpctl-under-test binary path")
	}
	if _, err := os.Stat(*binaryFlag); err != nil {
		t.Fatalf("--binary %q not found: %v", *binaryFlag, err)
	}
	if _, err := exec.LookPath(*tmuxFlag); err != nil {
		t.Skipf("tmux (%q) not on PATH", *tmuxFlag)
	}

	xdg := t.TempDir()

	soc := filepath.Join(t.TempDir(), "tmux.sock")
	// Explicit `bash -i` ensures an interactive shell that
	// produces a prompt and processes typed commands. Without
	// this, some environments (including tmux over gVisor/runsc)
	// start bash in a non-interactive mode where typed commands
	// never execute — scenarios that drive the shell via
	// send-keys then hang waiting for output.
	cmd := exec.Command(*tmuxFlag, "-S", soc, "-f", "/dev/null",
		"new-session", "-d", "-s", "t", "-x", "80", "-y", "24",
		"bash", "-i")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}

	e := &Env{
		t:       t,
		bin:     *binaryFlag,
		tmux:    *tmuxFlag,
		tmuxSoc: soc,
		xdgDir:  xdg,
	}
	t.Cleanup(e.teardown)
	return e
}

func (e *Env) teardown() {
	// If the test failed, dump collected diagnostics BEFORE we
	// kill tmux — otherwise capture-pane returns nothing useful.
	if e.t.Failed() {
		e.dumpDiagnostics()
	}
	// Kill the tmux server; any daemon will notice and exit via
	// the §11.9 path.
	_ = exec.Command(e.tmux, "-S", e.tmuxSoc, "kill-server").Run()

	// Poll for daemon exit up to 3s. Detection threshold in
	// real implementations is ~600ms (3 × 200ms poll); we budget
	// well above that to tolerate parallel-run subprocess
	// scheduling lag. Once the socket is no longer dial-able,
	// the daemon is gone.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pids := e.findLeakedDaemons(); len(pids) == 0 {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	// Still alive after 3s — log as a warning; don't fail here
	// because the scenario may have a more specific assertion to
	// report, and TestMain does the suite-level zombie check.
	if pids := e.findLeakedDaemons(); len(pids) > 0 {
		e.t.Logf("conformance: tpctl daemon still bound to %s after 3s", e.xdgDir)
	}
}

// dumpDiagnostics writes a failure context summary to the test log:
// the last few CLI invocations plus tmux's capture-pane for every
// tracked pane. Called only when the scenario has already failed,
// so the cost is paid only for red tests.
func (e *Env) dumpDiagnostics() {
	e.mu.Lock()
	hist := append([]invocation(nil), e.history...)
	panes := append([]string(nil), e.panes...)
	e.mu.Unlock()

	var b strings.Builder
	b.WriteString("\n──── conformance failure-time diagnostics ────\n")

	// Recent invocations — most recent last.
	n := len(hist)
	start := 0
	if n > 6 {
		start = n - 6
	}
	if n > 0 {
		fmt.Fprintf(&b, "Last %d tpctl invocation(s):\n", n-start)
		for i := start; i < n; i++ {
			inv := hist[i]
			fmt.Fprintf(&b, "  [%s] tpctl %s → code=%d\n",
				inv.when.Format("15:04:05.000"),
				strings.Join(inv.args, " "),
				inv.code,
			)
			if s := truncate(inv.stdout, 200); s != "" {
				fmt.Fprintf(&b, "    stdout: %s\n", s)
			}
			if s := truncate(inv.stderr, 200); s != "" {
				fmt.Fprintf(&b, "    stderr: %s\n", s)
			}
		}
	}

	// Pane state at failure time.
	if len(panes) > 0 {
		b.WriteString("Pane captures (tmux capture-pane -p):\n")
		for _, p := range panes {
			out, err := exec.Command(e.tmux, "-S", e.tmuxSoc,
				"capture-pane", "-p", "-t", p).CombinedOutput()
			if err != nil {
				fmt.Fprintf(&b, "  %s: <unavailable: %v>\n", p, err)
				continue
			}
			fmt.Fprintf(&b, "  %s:\n%s", p, indent(string(out), "    "))
		}
	}

	b.WriteString("──── end diagnostics ────")
	e.t.Log(b.String())
}

// findLeakedDaemons returns pids of any tpctl daemon process still
// bound to this env's daemon socket directory.
func (e *Env) findLeakedDaemons() []int {
	// Cheap probe: does the socket dir still contain a .sock that
	// something is listening on? If so, some daemon is still alive.
	// We avoid a /proc walk to keep this portable across Linux/Mac.
	sockDir := filepath.Join(e.xdgDir, "tpctl")
	entries, err := os.ReadDir(sockDir)
	if err != nil {
		return nil
	}
	var leaked []int
	for _, ent := range entries {
		if !strings.HasSuffix(ent.Name(), ".sock") {
			continue
		}
		// Try to dial — if the connection is accepted, a daemon
		// is still bound. pid=0 means "unknown pid, just alive".
		full := filepath.Join(sockDir, ent.Name())
		c, err := net.DialTimeout("unix", full, 50*time.Millisecond)
		if err == nil {
			c.Close()
			leaked = append(leaked, 0)
		}
	}
	return leaked
}

func truncate(s string, n int) string {
	s = strings.TrimRight(s, "\n")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func indent(s, prefix string) string {
	if s == "" {
		return prefix + "<empty>\n"
	}
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n") + "\n"
}

// TmuxSocket exposes the fixture socket so scenarios can drive tmux
// directly when needed (e.g. to kill a pane).
func (e *Env) TmuxSocket() string { return e.tmuxSoc }

// XDGDir returns the per-env XDG_RUNTIME_DIR. Useful to scenarios
// that need to inspect the daemon socket path or forward the
// variable into a subprocess.
func (e *Env) XDGDir() string { return e.xdgDir }

// BinaryPathForSubprocess returns the binary path for use in
// subprocesses (e.g. a bash-launched parent that auto-spawns the
// daemon). It's a thin alias for readability at call sites.
func (e *Env) BinaryPathForSubprocess() string { return e.bin }

// Tmux runs a tmux command against the scenario's socket. Fatal on
// error; returns combined stdout+stderr.
func (e *Env) Tmux(args ...string) string {
	e.t.Helper()
	full := append([]string{"-S", e.tmuxSoc}, args...)
	out, err := exec.Command(e.tmux, full...).CombinedOutput()
	if err != nil {
		e.t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return string(out)
}

// Result is the tri-part outcome of a tpctl invocation.
type Result struct {
	Code   int
	Stdout string
	Stderr string
}

// RunArgs runs the binary under test with arbitrary args. Global
// flags are NOT auto-injected here; scenarios that want the tmux
// socket call Run.
func (e *Env) RunArgs(args ...string) Result {
	e.t.Helper()
	cmd := exec.Command(e.bin, args...)
	cmd.Env = []string{
		"XDG_RUNTIME_DIR=" + e.xdgDir,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	started := time.Now()
	err := cmd.Run()
	code := 0
	if ee := (&exec.ExitError{}); errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		e.t.Fatalf("exec %s %v: %v", e.bin, args, err)
	}
	r := Result{Code: code, Stdout: stdout.String(), Stderr: stderr.String()}

	// Record the invocation for failure-time diagnostics. Keep a
	// bounded window so history doesn't grow without limit in
	// long-running scenarios (tail loops, concurrency tests).
	e.mu.Lock()
	e.history = append(e.history, invocation{
		when:   started,
		args:   append([]string(nil), args...),
		code:   code,
		stdout: r.Stdout,
		stderr: r.Stderr,
	})
	if len(e.history) > 32 {
		e.history = e.history[len(e.history)-32:]
	}
	e.mu.Unlock()

	return r
}

// Run invokes the binary with the fixture's --tmux-socket injected
// after the first argument (the subcommand). Global-flag-position
// coverage (§6.2) is exercised elsewhere.
func (e *Env) Run(args ...string) Result {
	if len(args) == 0 {
		return e.RunArgs()
	}
	full := make([]string, 0, len(args)+2)
	full = append(full, args[0], "--tmux-socket", e.tmuxSoc)
	full = append(full, args[1:]...)
	return e.RunArgs(full...)
}

// MustJSON decodes r.Stdout into v or fails the test with a useful
// diagnostic. Also asserts a zero exit code.
func (r Result) MustJSON(t *testing.T, v any) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("expected exit 0, got %d; stdout=%q stderr=%q",
			r.Code, r.Stdout, r.Stderr)
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), v); err != nil {
		t.Fatalf("decode stdout as JSON: %v; stdout=%q", err, r.Stdout)
	}
}

// MustError decodes r.Stdout as a spec §7.2 error response and
// asserts a nonzero exit code. Returns the decoded error for
// further assertions.
func (r Result) MustError(t *testing.T) ErrorResponse {
	t.Helper()
	if r.Code == 0 {
		t.Fatalf("expected nonzero exit, got 0; stdout=%q stderr=%q",
			r.Stdout, r.Stderr)
	}
	var e ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &e); err != nil {
		t.Fatalf("decode stdout as error JSON: %v; stdout=%q", err, r.Stdout)
	}
	if e.Code == "" {
		t.Fatalf("error response missing code; stdout=%q", r.Stdout)
	}
	if e.Message == "" {
		t.Fatalf("error response missing message (spec §7.2); stdout=%q", r.Stdout)
	}
	return e
}

// MustMuted asserts the command printed nothing on stdout and exited
// zero. Used for mutating commands (text, key) per spec §7.1.
func (r Result) MustMuted(t *testing.T) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", r.Code, r.Stderr)
	}
	if r.Stdout != "" {
		t.Fatalf("expected empty stdout, got %q", r.Stdout)
	}
}

// ListResponse mirrors spec §9.1.
type ListResponse struct {
	Panes []string `json:"panes"`
}

// SnapshotResponse mirrors spec §9.2. ScrollbackText is a pointer so
// nil vs empty string distinguishes "not requested" from "requested
// but empty" per §8.1.
type SnapshotResponse struct {
	PaneID         string  `json:"pane_id"`
	Next           string  `json:"next"`
	Text           string  `json:"text"`
	ScrollbackText *string `json:"scrollback_text,omitempty"`
}

// ReadResponse mirrors spec §9.3.
type ReadResponse struct {
	PaneID string `json:"pane_id"`
	Next   string `json:"next"`
	Text   string `json:"text"`
}

// WaitResponse mirrors spec §9.6. Matched and ExitCode are pointers
// so the per-mode field-presence matrix (§9.6) is observable.
type WaitResponse struct {
	PaneID   string  `json:"pane_id"`
	Next     string  `json:"next"`
	Result   string  `json:"result"`
	Matched  *string `json:"matched,omitempty"`
	ExitCode *int    `json:"exit_code,omitempty"`
}

// ErrorResponse mirrors spec §7.2 / §7.6.
type ErrorResponse struct {
	PaneID  string `json:"pane_id,omitempty"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Snapshot is a convenience helper that snapshots a pane and
// returns the decoded response.
func (e *Env) Snapshot(t *testing.T, pane string, extraArgs ...string) SnapshotResponse {
	t.Helper()
	args := append([]string{"snapshot", "--pane", pane}, extraArgs...)
	var s SnapshotResponse
	e.Run(args...).MustJSON(t, &s)
	return s
}

// List returns all panes tmux currently reports via tpctl.
func (e *Env) List(t *testing.T) []string {
	t.Helper()
	var r ListResponse
	e.Run("list").MustJSON(t, &r)
	return r.Panes
}

// FirstPane returns the first pane id on the fixture tmux server,
// failing the test if none exist. The returned pane is tracked so
// failure-time diagnostics can capture its state.
func (e *Env) FirstPane(t *testing.T) string {
	t.Helper()
	panes := e.List(t)
	if len(panes) == 0 {
		t.Fatal("no panes in fixture tmux server")
	}
	e.mu.Lock()
	seen := false
	for _, p := range e.panes {
		if p == panes[0] {
			seen = true
			break
		}
	}
	if !seen {
		e.panes = append(e.panes, panes[0])
	}
	e.mu.Unlock()
	return panes[0]
}

// NewPane opens a new window in the fixture tmux server and returns
// the new pane's id. Scenarios that need multiple or disposable
// panes use this. The pane is tracked so failure-time diagnostics
// can capture its state.
func (e *Env) NewPane(t *testing.T) string {
	t.Helper()
	// `new-window` returns the new pane id if we ask for it.
	out := strings.TrimSpace(e.Tmux("new-window", "-t", "t:", "-P", "-F", "#{pane_id}"))
	if !strings.HasPrefix(out, "%") {
		t.Fatalf("unexpected new-window output: %q", out)
	}
	e.mu.Lock()
	e.panes = append(e.panes, out)
	e.mu.Unlock()
	return out
}

// KillPane destroys a pane via tmux. Used to exercise §11.9 pane
// lifecycle and §7.5 PANE_CLOSED / PANE_NOT_FOUND precedence.
func (e *Env) KillPane(t *testing.T, id string) {
	t.Helper()
	e.Tmux("kill-pane", "-t", id)
}

// PaneOutput streams a shell command into the pane, Enter-terminated.
// It returns once tmux has ack'd the send — which is the spec §9.4
// send-ack contract used by tpctl's own `text` handler, but here we
// use tmux directly so scenarios can set up buffer state without
// going through the controller under test.
func (e *Env) PaneOutput(t *testing.T, pane, cmd string) {
	t.Helper()
	e.Tmux("send-keys", "-t", pane, cmd, "Enter")
}

// WaitForText polls the pane (via tmux capture-pane, NOT through
// tpctl) until the predicate matches or timeout elapses. Used only
// in fixture setup to know when shell output has landed.
func (e *Env) WaitForText(t *testing.T, pane string, contains string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		out := e.Tmux("capture-pane", "-p", "-t", pane)
		if strings.Contains(out, contains) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("pane %s never contained %q within %v", pane, contains, timeout)
}

// requireBinary is a convenience for tests that don't call NewEnv
// but still need the binary path.
func RequireBinary(t *testing.T) string {
	t.Helper()
	if *binaryFlag == "" {
		t.Skip("--binary not set")
	}
	return *binaryFlag
}

// FormatSentinelCmd builds a shell command that runs `cmd` and
// appends the spec §9.6 sentinel carrying its exit code, e.g.
//
//	FormatSentinelCmd("make test", "run1")
//	→ `make test; printf '__DONE__:run1:%d\n' $?`
//
// This is a convention for scenarios, not a spec requirement.
func FormatSentinelCmd(cmd, token string) string {
	return fmt.Sprintf(`%s; printf '__DONE__:%s:%%d\n' $?`, cmd, token)
}

// AssertCode fails the test if the given ErrorResponse does not
// carry the expected code. The message is included in the failure
// diagnostic for easier triage.
func AssertCode(t *testing.T, got ErrorResponse, want string) {
	t.Helper()
	if got.Code != want {
		t.Fatalf("code = %q, want %q; message=%q", got.Code, want, got.Message)
	}
}

// AtoiOrFail is a small helper that fails the test with context if
// s does not parse as an int. Used when scenarios need to do
// arithmetic on response numbers.
func AtoiOrFail(t *testing.T, s, context string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("%s: parse %q as int: %v", context, s, err)
	}
	return n
}
