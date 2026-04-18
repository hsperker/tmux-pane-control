//go:build unix

package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/ipc"
)

// TestE2E_AutoSpawn_CrossInvocation verifies the spec §11 auto-spawn
// guarantee: a first CLI invocation spawns a daemon, a second
// invocation reuses it, and the controller's store persists across
// the two so snapshot.next from call #1 is a valid --after for call #2.
func TestE2E_AutoSpawn_CrossInvocation(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	// Isolate XDG_RUNTIME_DIR so this test's daemon socket path
	// doesn't collide with other tests or with developer daemons.
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

	// Ensure any daemon we spawn gets torn down when the test ends.
	t.Cleanup(func() {
		sock, err := ipc.DaemonSocketPath(tmuxSock)
		if err != nil {
			return
		}
		cl := ipc.NewClient(sock)
		_, _ = cl.Call(&ipc.Request{Op: ipc.OpShutdown}, 500*time.Millisecond)
	})

	env := []string{"XDG_RUNTIME_DIR=" + xdg, "PATH=" + allPath(t)}

	// --- Invocation 1: snapshot. Auto-spawns the daemon.
	cmd := exec.Command(bin, "snapshot", "--pane", "%0", "--tmux-socket", tmuxSock)
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("snapshot: %v: %s", err, out)
	}
	var snap domain.SnapshotResponse
	if err := json.Unmarshal(out, &snap); err != nil {
		t.Fatalf("snap json: %v (out=%q)", err, out)
	}
	if snap.Next == "" {
		t.Fatal("snap.Next empty")
	}

	// --- Produce output in the pane.
	if out, err := exec.Command("tmux", "-S", tmuxSock, "send-keys",
		"-t", "%0", "echo tpctl-e2e-probe", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("send-keys: %v: %s", err, out)
	}

	// --- Invocation 2: read using the token from invocation 1.
	// Poll briefly to let the daemon's drain loop pick up the output.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cmd = exec.Command(bin, "read", "--pane", "%0",
			"--after", string(snap.Next), "--tmux-socket", tmuxSock)
		cmd.Env = env
		out, err = cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("read: %v: %s", err, out)
		}
		var r domain.ReadResponse
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("read json: %v (out=%q)", err, out)
		}
		if strings.Contains(r.Text, "tpctl-e2e-probe") {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("never saw probe through read; last output=%q", out)
}

// allPath returns the current PATH so the daemon subprocess can find
// tmux and any required helpers.
func allPath(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", "echo $PATH").Output()
	if err != nil {
		t.Fatalf("PATH: %v", err)
	}
	return strings.TrimSpace(string(out))
}
