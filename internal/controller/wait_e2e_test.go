//go:build unix

package controller

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// TestWait_EndToEnd_Sentinel exercises the full path: real tmux,
// real Adapter.Subscribe (pipe-pane FIFOs), Controller, Wait.
func TestWait_EndToEnd_Sentinel(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not on PATH")
	}
	dir := t.TempDir()
	sock := filepath.Join(dir, "tmux.sock")
	if out, err := exec.Command("tmux", "-S", sock, "-f", "/dev/null",
		"new-session", "-d", "-s", "t1", "-x", "80", "-y", "24").CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", sock, "kill-server").Run() })

	port := tmuxctl.NewAdapter(tmuxctl.Opts{SocketPath: sock})
	c := New(port)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(c.Stop)

	// Find the single pane created above.
	panes, err := c.List()
	if err != nil || len(panes.Panes) == 0 {
		t.Fatalf("List: %v %v", err, panes)
	}
	pane := panes.Panes[0]

	// Give pipe-pane trackers a moment to attach before sending.
	time.Sleep(400 * time.Millisecond)

	snap, err := c.Snapshot(pane)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Send a command whose output yields a sentinel with exit code 7.
	if err := c.SendText(pane, `printf '__DONE__:run7:%d\n' 7`, true); err != nil {
		t.Fatalf("SendText: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := c.Wait(ctx, WaitRequest{
		PaneID:        pane,
		After:         snap.Next,
		Timeout:       5 * time.Second,
		Mode:          WaitModeSentinel,
		SentinelToken: "run7",
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if resp.Result != domain.WaitSentinel {
		t.Fatalf("result = %q", resp.Result)
	}
	if resp.ExitCode == nil || *resp.ExitCode != 7 {
		t.Fatalf("exit_code = %v", resp.ExitCode)
	}
}
