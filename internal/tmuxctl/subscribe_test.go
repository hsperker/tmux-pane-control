//go:build unix

package tmuxctl

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestAdapter_SubscribeEmitsPaneOutput(t *testing.T) {
	sock := newTmuxServer(t)
	a := NewAdapter(Opts{SocketPath: sock})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out, err := a.Subscribe(ctx)
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}

	// Wait for the subscription to establish a tracker.
	// The subscription polls every 200ms.
	time.Sleep(400 * time.Millisecond)

	// Send a command whose output will be visible in the pane.
	// send-keys writes to the pane's tty; pipe-pane captures it.
	if out2, err := exec.Command("tmux", "-S", sock, "send-keys", "-t", "%0",
		"echo tpctl-subscribe-probe", "Enter").CombinedOutput(); err != nil {
		t.Fatalf("send-keys: %v: %s", err, out2)
	}

	deadline := time.After(5 * time.Second)
	var acc strings.Builder
	for {
		select {
		case ev, ok := <-out:
			if !ok {
				t.Fatalf("subscription closed before seeing probe output: %q", acc.String())
			}
			acc.Write(ev.Data)
			if strings.Contains(acc.String(), "tpctl-subscribe-probe") {
				cancel()
				// Drain remaining until close.
				for range out {
				}
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for probe output: %q", acc.String())
		}
	}
}
