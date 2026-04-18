//go:build linux

// Regression test for spec §11.2 "Daemon lifetime": an auto-spawned
// controller must outlive the CLI invocation that spawned it, even
// when the spawning process group is reaped by an automation harness.
//
// Gating: Linux-only because the PID-detection step reads /proc.
// The requirement holds on macOS and BSD too, but verifying it there
// would require `ps` parsing. Left as a TODO if the test is ever
// ported.

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/ipc"
)

func TestDaemon_SurvivesParentSIGKILL(t *testing.T) {
	tmuxSoc := startTmux(t)
	bin := buildBinary(t)

	xdg := t.TempDir()
	// Share XDG_RUNTIME_DIR between the test process and the bash
	// subprocess so both resolve to the same daemon socket path.
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

	// Parent: a bash that auto-spawns the daemon via `tpctl list`,
	// then sleeps so we can kill it mid-run. Placed in its own
	// session (Setsid) so SIGKILL to the parent's process group
	// does not accidentally reach anything outside it.
	parentCmd := fmt.Sprintf(
		"%s list --tmux-socket %s >/dev/null 2>&1; sleep 60",
		bin, tmuxSoc,
	)
	parent := exec.Command("bash", "-c", parentCmd)
	parent.Env = env
	parent.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := parent.Start(); err != nil {
		t.Fatalf("start parent: %v", err)
	}
	t.Cleanup(func() {
		_ = parent.Process.Kill()
		_, _ = parent.Process.Wait()
	})

	// Wait for the daemon to be reachable.
	client := ipc.NewClient(daemonSock)
	if err := waitForDaemon(client, 3*time.Second); err != nil {
		t.Fatalf("daemon never came up: %v", err)
	}

	// Record the daemon's PID so we can verify the SAME process
	// survives. A new daemon at the same socket would be a
	// silent regression: the test would still see Ping succeed
	// but a fresh daemon means the first one died.
	daemonPID := findDaemonPIDForSocket(t, bin, tmuxSoc)

	// Sanity: the daemon must not be in the parent's process
	// group — that's the whole point of setsid in autospawn.go.
	parentPGID, err := syscall.Getpgid(parent.Process.Pid)
	if err != nil {
		t.Fatalf("getpgid(parent): %v", err)
	}
	daemonPGID, err := syscall.Getpgid(daemonPID)
	if err != nil {
		t.Fatalf("getpgid(daemon %d): %v", daemonPID, err)
	}
	if daemonPGID == parentPGID {
		t.Fatalf("daemon (pgid=%d) is in the parent's process group (pgid=%d); setsid did not take effect",
			daemonPGID, parentPGID)
	}

	// Harsh parent kill: SIGKILL the parent's entire process group.
	// An un-detached daemon would be reaped here.
	if err := syscall.Kill(-parentPGID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill parent group -%d: %v", parentPGID, err)
	}
	_, _ = parent.Process.Wait()

	// Give the kernel a moment to deliver the signal. If the
	// daemon were going to die from it, it would be gone by now.
	time.Sleep(300 * time.Millisecond)

	// Verify the SAME daemon process is still alive.
	if err := syscall.Kill(daemonPID, 0); err != nil {
		t.Fatalf("daemon pid %d not alive after parent SIGKILL: %v", daemonPID, err)
	}

	// Verify it still responds to IPC.
	if err := client.Ping(); err != nil {
		t.Fatalf("daemon unresponsive after parent SIGKILL: %v", err)
	}

	// Cleanup: stop the daemon ourselves.
	_ = syscall.Kill(daemonPID, syscall.SIGTERM)
}

func waitForDaemon(client *ipc.Client, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if err := client.Ping(); err == nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return client.Ping()
}

// findDaemonPIDForSocket walks /proc looking for a process whose
// argv matches `<bin> daemon --tmux-socket <tmuxSoc> ...`. Linux-only.
func findDaemonPIDForSocket(t *testing.T, bin, tmuxSoc string) int {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("read /proc: %v", err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		data, err := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
		if len(args) < 2 || args[0] != bin || args[1] != "daemon" {
			continue
		}
		for i := 2; i+1 < len(args); i++ {
			if args[i] == "--tmux-socket" && args[i+1] == tmuxSoc {
				return pid
			}
		}
	}
	t.Fatalf("no running `%s daemon --tmux-socket %s` found in /proc", bin, tmuxSoc)
	return 0
}
