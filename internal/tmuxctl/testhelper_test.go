package tmuxctl

import (
	"os/exec"
	"path/filepath"
	"testing"
)

// newTmuxServer starts a fresh tmux server on a disposable socket with
// a single session. It skips the test if tmux is not on PATH.
//
// The server is killed on cleanup. Using -f /dev/null keeps the test
// immune to the user's tmux config.
func newTmuxServer(t *testing.T) (socketPath string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available on PATH")
	}
	sock := filepath.Join(t.TempDir(), "tmux.sock")
	cmd := exec.Command("tmux", "-S", sock, "-f", "/dev/null",
		"new-session", "-d", "-s", "t1", "-x", "80", "-y", "24")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tmux new-session: %v: %s", err, out)
	}
	t.Cleanup(func() {
		_ = exec.Command("tmux", "-S", sock, "kill-server").Run()
	})
	return sock
}

// tmuxCmd runs a tmux command against the given socket and returns
// CombinedOutput. Fails the test on error.
func tmuxCmd(t *testing.T, sock string, args ...string) []byte {
	t.Helper()
	full := append([]string{"-S", sock}, args...)
	out, err := exec.Command("tmux", full...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %v: %v: %s", args, err, out)
	}
	return out
}
