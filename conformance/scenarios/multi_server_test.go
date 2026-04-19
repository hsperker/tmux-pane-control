// Scenarios covering spec §11.4 tmux-server resolution and the
// cross-server isolation it is designed to produce. The reference
// harness's NewEnv only exercises --tmux-socket PATH; these tests
// cover the other resolution branches and verify that distinct
// servers get distinct controllers.

package scenarios

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hsperker/tmux-pane-control/conformance/harness"
)

// TestExt_MutuallyExclusiveSocketFlags — spec §11.4 says passing
// both --tmux-socket and --tmux-socket-name is a command-level
// error.
func TestExt_MutuallyExclusiveSocketFlags(t *testing.T) {
	t.Parallel()
	if harness.Binary() == "" {
		t.Skip("--binary not set")
	}

	cmd := exec.Command(harness.Binary(),
		"list",
		"--tmux-socket", "/tmp/bogus.sock",
		"--tmux-socket-name", "bogus",
	)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("both flags passed, exit was 0; stdout=%q", stdout.String())
	}

	// The error may surface either as a structured JSON error on
	// stdout (command-level) or as a runtime message on stderr.
	// Spec §7.2 prefers the JSON shape but §7.3 is acceptable
	// because this is an argument-level violation that may
	// surface before the subcommand dispatcher sees it.
	if strings.Contains(stdout.String(), `"code":`) {
		var e harness.ErrorResponse
		if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &e); err != nil {
			t.Fatalf("structured-error JSON unparseable: %q (%v)", stdout.String(), err)
		}
		if e.Code == "" {
			t.Fatalf("error code empty: %+v", e)
		}
		return
	}
	if stderr.Len() == 0 {
		t.Fatalf("expected JSON error on stdout OR diagnostic on stderr; got neither")
	}
}

// TestExt_SocketNameResolution — spec §11.4 resolves
// --tmux-socket-name NAME to /tmp/tmux-$UID/NAME. We start a tmux
// server at that exact path, point the CLI at it by NAME, and
// verify list returns the expected pane.
func TestExt_SocketNameResolution(t *testing.T) {
	t.Parallel()
	if harness.Binary() == "" {
		t.Skip("--binary not set")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	name := fmt.Sprintf("tpctl-conformance-sn-%d", os.Getpid())
	dir := fmt.Sprintf("/tmp/tmux-%d", os.Getuid())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	sockPath := filepath.Join(dir, name)
	// Ensure no leftover socket from a prior test run.
	_ = os.Remove(sockPath)
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", sockPath, "kill-server").Run()
		_ = os.Remove(sockPath)
	})

	if out, err := exec.Command("tmux", "-S", sockPath, "-f", "/dev/null",
		"new-session", "-d", "-s", "sn", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session on %s: %v: %s", sockPath, err, out)
	}

	// Run list with --tmux-socket-name and an isolated XDG so we
	// don't stomp on any existing daemon. Shell out directly;
	// NewEnv is tuned to --tmux-socket.
	xdg := t.TempDir()
	cmd := exec.Command(harness.Binary(), "list", "--tmux-socket-name", name)
	cmd.Env = []string{
		"XDG_RUNTIME_DIR=" + xdg,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("list via --tmux-socket-name: %v; stderr=%q", err, stderr.String())
	}

	var lr harness.ListResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &lr); err != nil {
		t.Fatalf("list JSON: %v (stdout=%q)", err, stdout.String())
	}
	if len(lr.Panes) == 0 {
		t.Fatalf("list returned no panes; --tmux-socket-name didn't reach the server")
	}
}

// TestExt_TmuxEnvResolution — spec §11.4 resolves $TMUX's leading
// component to a tmux socket path when no --tmux-socket flag is
// given. We set $TMUX in the subprocess env and verify list
// reaches the right server.
func TestExt_TmuxEnvResolution(t *testing.T) {
	t.Parallel()
	if harness.Binary() == "" {
		t.Skip("--binary not set")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	sockPath := filepath.Join(t.TempDir(), "tmux-env.sock")
	if out, err := exec.Command("tmux", "-S", sockPath, "-f", "/dev/null",
		"new-session", "-d", "-s", "envs", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", sockPath, "kill-server").Run()
	})

	// $TMUX format is "<socket-path>,<pid>,<session-id>" — the
	// spec only requires the leading socket-path component to be
	// honored. We build a plausible-looking value.
	tmuxVar := sockPath + ",1234,0"

	xdg := t.TempDir()
	cmd := exec.Command(harness.Binary(), "list")
	cmd.Env = []string{
		"XDG_RUNTIME_DIR=" + xdg,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"TMUX=" + tmuxVar,
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("list via $TMUX: %v; stderr=%q", err, stderr.String())
	}

	var lr harness.ListResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &lr); err != nil {
		t.Fatalf("list JSON: %v (stdout=%q)", err, stdout.String())
	}
	if len(lr.Panes) == 0 {
		t.Fatal("list returned no panes; $TMUX was not honored")
	}
}

// TestExt_MultiServerIsolation — spec §11.4 says distinct tmux
// servers get distinct controllers. A token from server A must
// not be valid against pane ids on server B. Since %pane_id
// numbering can collide across servers, this is a real risk if
// the controller maps panes by id alone.
func TestExt_MultiServerIsolation(t *testing.T) {
	t.Parallel()
	if harness.Binary() == "" {
		t.Skip("--binary not set")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	// Two independent tmux servers, each with its own %0.
	sockA := filepath.Join(t.TempDir(), "a.sock")
	sockB := filepath.Join(t.TempDir(), "b.sock")
	for _, s := range []string{sockA, sockB} {
		if out, err := exec.Command("tmux", "-S", s, "-f", "/dev/null",
			"new-session", "-d", "-s", "t", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
			t.Fatalf("tmux new-session on %s: %v: %s", s, err, out)
		}
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", sockA, "kill-server").Run()
		_ = exec.Command("tmux", "-S", sockB, "kill-server").Run()
	})

	xdg := t.TempDir()
	runOn := func(sock string, args ...string) (int, string, string) {
		all := append([]string{args[0], "--tmux-socket", sock}, args[1:]...)
		cmd := exec.Command(harness.Binary(), all...)
		cmd.Env = []string{
			"XDG_RUNTIME_DIR=" + xdg,
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
		}
		var stdout, stderr strings.Builder
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else if err != nil {
			t.Fatalf("exec %v: %v", all, err)
		}
		return code, stdout.String(), stderr.String()
	}

	// Snapshot %0 on server A to get a token.
	_, out, _ := runOn(sockA, "snapshot", "--pane", "%0")
	var snap harness.SnapshotResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &snap); err != nil {
		t.Fatalf("snapshot A JSON: %v (%q)", err, out)
	}
	if snap.Next == "" {
		t.Fatalf("snapshot A missing next: %q", out)
	}

	// Try that token against %0 on server B. Per §11.4, distinct
	// servers get distinct controllers, so the token must be
	// rejected. A spec-conformant response is INVALID_AFTER (the
	// controller on B doesn't recognize the token) or
	// PANE_NOT_FOUND if B's controller hasn't observed its own
	// %0 yet; both are acceptable — but success is NOT.
	code, out, _ := runOn(sockB, "read", "--pane", "%0", "--after", snap.Next)
	if code == 0 {
		t.Fatalf("cross-server read succeeded: stdout=%q", out)
	}
	var e harness.ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &e); err != nil {
		t.Fatalf("cross-server error not JSON: %q", out)
	}
	if e.Code != "INVALID_AFTER" && e.Code != "PANE_NOT_FOUND" {
		t.Fatalf("cross-server code = %q, want INVALID_AFTER or PANE_NOT_FOUND", e.Code)
	}
}
