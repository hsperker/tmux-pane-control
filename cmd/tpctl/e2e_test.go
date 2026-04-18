package main

import (
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// buildBinary compiles the tpctl binary into the test's temp dir and
// returns the path. Skips the test if the build fails (e.g. offline CI
// without cached modules).
func buildBinary(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	bin := filepath.Join(dir, "tpctl")
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/tpctl")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build: %v: %s", err, out)
	}
	return bin
}

// repoRoot walks up from the test working directory to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("go env GOMOD: %v", err)
	}
	return filepath.Dir(strings.TrimSpace(string(out)))
}

func startTmux(t *testing.T) string {
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

func TestE2E_List(t *testing.T) {
	sock := startTmux(t)
	bin := buildBinary(t)

	out, err := exec.Command(bin, "list", "--tmux-socket", sock).Output()
	if err != nil {
		t.Fatalf("tpctl list: %v", err)
	}
	var resp domain.ListResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatalf("unmarshal: %v (out=%q)", err, out)
	}
	if len(resp.Panes) != 1 {
		t.Fatalf("want 1 pane, got %d: %v", len(resp.Panes), resp.Panes)
	}
	if !strings.HasPrefix(string(resp.Panes[0]), "%") {
		t.Fatalf("pane id missing %% prefix: %q", resp.Panes[0])
	}
}
