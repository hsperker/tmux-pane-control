//go:build linux

// End-to-end test for spec §11.9 Exit-mode tmux server restart
// handling: when the tmux server dies out from under a running
// daemon, the daemon must detect it within a bounded time, log a
// runtime failure to stderr, exit with a nonzero status, and remove
// its socket. Linux-only because the PID sanity check reads /proc.

package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/ipc"
)

func TestDaemon_ExitsOnTmuxServerLoss(t *testing.T) {
	tmuxSoc := startTmux(t)
	bin := buildBinary(t)

	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)
	env := []string{
		"XDG_RUNTIME_DIR=" + xdg,
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}

	daemonSock, err := ipc.DaemonSocketPath(tmuxSoc)
	if err != nil {
		t.Fatalf("DaemonSocketPath: %v", err)
	}

	// Start the daemon in the foreground so we can watch it exit.
	// Capture stderr so we can assert the runtime-failure message.
	daemon := exec.Command(bin, "daemon",
		"--tmux-socket", tmuxSoc,
		"--daemon-socket", daemonSock,
	)
	daemon.Env = env
	var stderr strings.Builder
	daemon.Stderr = &stderr
	daemon.Stdout = nil
	// Put the daemon in its own process group so we can kill it
	// cleanly if the test itself fails partway through.
	daemon.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := daemon.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	t.Cleanup(func() {
		// Best-effort cleanup if the daemon is still alive.
		if daemon.Process != nil {
			_ = syscall.Kill(-daemon.Process.Pid, syscall.SIGKILL)
		}
	})

	// Wait for the daemon to come up.
	client := ipc.NewClient(daemonSock)
	if err := waitForDaemonReady(client, 3*time.Second); err != nil {
		t.Fatalf("daemon never came up: %v (stderr=%q)", err, stderr.String())
	}

	// Kill the tmux server out from under the daemon.
	if out, err := exec.Command("tmux", "-S", tmuxSoc, "kill-server").CombinedOutput(); err != nil {
		t.Fatalf("tmux kill-server: %v: %s", err, out)
	}

	// Daemon must exit within a bounded window. The detection
	// threshold is 3 consecutive poll failures at 200ms intervals,
	// i.e. ~600ms; we give it a generous 5s to handle CI noise.
	exitCh := make(chan error, 1)
	go func() { exitCh <- daemon.Wait() }()

	select {
	case err := <-exitCh:
		if err == nil {
			t.Fatalf("daemon exited 0; expected nonzero on server loss (stderr=%q)", stderr.String())
		}
		var ee *exec.ExitError
		if !errors.As(err, &ee) || ee.ExitCode() == 0 {
			t.Fatalf("daemon exited unexpectedly: %v (stderr=%q)", err, stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("daemon did not exit within 5s after tmux kill-server (stderr=%q)", stderr.String())
	}

	// Stderr must contain the §7.3 runtime-failure diagnostic.
	if !strings.Contains(stderr.String(), "tmux server connection lost") {
		t.Fatalf("stderr missing expected diagnostic: %q", stderr.String())
	}

	// Socket file must be gone so the next CLI invocation's
	// auto-spawn can bind a fresh one without stale-socket hacks.
	if _, err := os.Stat(daemonSock); !os.IsNotExist(err) {
		t.Fatalf("daemon socket still present after exit: %v", err)
	}

	// And the socket must not be dial-able — even if the path were
	// somehow still there, nothing is listening.
	if c, err := net.DialTimeout("unix", daemonSock, 100*time.Millisecond); err == nil {
		c.Close()
		t.Fatal("daemon socket still accepting connections after exit")
	}
}

func waitForDaemonReady(client *ipc.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := client.Ping(); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return client.Ping()
}

// The daemon_lifetime_test.go in this package has its own
// waitForDaemon helper; we duplicate a narrow one here rather than
// reshape that file. Keeping them separate makes each test file
// self-contained.
