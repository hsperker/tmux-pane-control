//go:build linux

package main

import (
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/ipc"
)

// TestE2E_DaemonStop_NoDaemon — `tpctl daemon --stop` against a
// socket with no running daemon is a clean no-op: exit 0, stderr
// says "no daemon running".
func TestE2E_DaemonStop_NoDaemon(t *testing.T) {
	bin := buildBinary(t)

	// A tmux-socket path with no daemon behind it. The daemon-socket
	// is derived from tmux-socket, so an unused tmux path guarantees
	// no daemon.
	unused := filepath.Join(t.TempDir(), "tmux-unused.sock")
	xdg := t.TempDir()

	cmd := exec.Command(bin, "daemon", "--stop", "--tmux-socket", unused)
	cmd.Env = []string{"XDG_RUNTIME_DIR=" + xdg, "PATH=" + allPath(t)}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("daemon --stop: %v: %s", err, out)
	}
	if !strings.Contains(string(out), "no daemon running") {
		t.Fatalf("stderr should say 'no daemon running': %q", out)
	}
}

// TestE2E_DaemonStop_StopsRunningDaemon — auto-spawn a daemon,
// then `tpctl daemon --stop` and verify the socket is gone AND the
// daemon process has exited. The socket check alone is not enough:
// the daemon can unlink its socket during shutdown while the process
// stays resident (see `t.stop` / `ctrl.Stop` paths that used to hang
// on pipe-pane during teardown).
func TestE2E_DaemonStop_StopsRunningDaemon(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)

	tmuxSock := filepath.Join(t.TempDir(), "tmux.sock")
	if out, err := exec.Command("tmux", "-S", tmuxSock, "-f", "/dev/null",
		"new-session", "-d", "-s", "t1", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", tmuxSock, "kill-server").Run()
	})

	bin := buildBinary(t)
	env := []string{"XDG_RUNTIME_DIR=" + xdg, "PATH=" + allPath(t)}

	// Auto-spawn the daemon by running any command.
	cmd := exec.Command(bin, "list", "--tmux-socket", tmuxSock)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("list (auto-spawn): %v: %s", err, out)
	}

	sock, err := ipc.DaemonSocketPath(tmuxSock)
	if err != nil {
		t.Fatalf("DaemonSocketPath: %v", err)
	}
	client := ipc.NewClient(sock)
	if err := client.Ping(); err != nil {
		t.Fatalf("daemon not reachable after auto-spawn: %v", err)
	}

	// Record the daemon's PID BEFORE stopping. If we only asserted
	// on socket disappearance, a "unlink socket but leak process"
	// bug would silently pass. Linux-only for the /proc scrape;
	// the test as a whole is unix-gated.
	daemonPID := findDaemonPIDForSocket(t, bin, tmuxSock)

	// Stop it.
	cmd = exec.Command(bin, "daemon", "--stop", "--tmux-socket", tmuxSock)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("daemon --stop: %v: %s", err, out)
	}

	// Socket must go away.
	sockGone := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.Ping(); err != nil {
			sockGone = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !sockGone {
		t.Fatalf("daemon still reachable on socket after --stop returned")
	}

	// Process must exit. Give it enough budget to cover the 2s
	// stopWithDeadline backstop in cli/daemon.go plus kernel delivery.
	processDeadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(processDeadline) {
		if err := syscall.Kill(daemonPID, 0); err != nil {
			// ESRCH or permission denied (shouldn't be the latter
			// within our own uid) — either way, process is gone.
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("daemon PID %d still alive 4s after --stop (socket was removed, process leaked)", daemonPID)
}
