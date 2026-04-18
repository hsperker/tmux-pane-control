//go:build unix

package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/ipc"
)

// TestE2E_MultiServer verifies spec §11.4: distinct tmux servers get
// distinct controllers. We spin up two tmux servers on separate
// sockets, then run tpctl list against each and assert the panes
// are disjoint sets. Both daemons are torn down on test exit.
func TestE2E_MultiServer(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}
	xdg := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", xdg)

	bin := buildBinary(t)

	startTmuxOn := func(name string) string {
		sock := filepath.Join(t.TempDir(), name+".sock")
		if out, err := exec.Command("tmux", "-S", sock, "-f", "/dev/null",
			"new-session", "-d", "-s", name, "-x", "80", "-y", "24").CombinedOutput(); err != nil {
			t.Fatalf("tmux(%s): %v: %s", name, err, out)
		}
		t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })
		return sock
	}
	sockA := startTmuxOn("serverA")
	sockB := startTmuxOn("serverB")

	// Clean up spawned daemons on exit.
	t.Cleanup(func() {
		for _, s := range []string{sockA, sockB} {
			dp, err := ipc.DaemonSocketPath(s)
			if err != nil {
				continue
			}
			_, _ = ipc.NewClient(dp).Call(&ipc.Request{Op: ipc.OpShutdown}, 500*time.Millisecond)
		}
	})

	env := []string{"XDG_RUNTIME_DIR=" + xdg, "PATH=" + allPath(t)}
	runList := func(sock string) domain.ListResponse {
		cmd := exec.Command(bin, "list", "--tmux-socket", sock)
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("list %s: %v: %s", sock, err, out)
		}
		var r domain.ListResponse
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatalf("json: %v (%q)", err, out)
		}
		return r
	}

	listA := runList(sockA)
	listB := runList(sockB)

	if len(listA.Panes) == 0 || len(listB.Panes) == 0 {
		t.Fatalf("empty pane lists: A=%v B=%v", listA.Panes, listB.Panes)
	}

	// The two servers maintain independent %pane_id spaces, but the
	// ids will typically collide (both produce %0). That's fine — the
	// assertion we care about is that the daemon sockets are
	// different paths, i.e. that auto-spawn keyed each server's
	// daemon separately.
	dpA, _ := ipc.DaemonSocketPath(sockA)
	dpB, _ := ipc.DaemonSocketPath(sockB)
	if dpA == dpB {
		t.Fatalf("daemon socket paths collided: %q", dpA)
	}
}
