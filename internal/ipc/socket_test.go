package ipc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDaemonSocketPath_StableHash(t *testing.T) {
	// Same input → same output; different inputs → different.
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	a, err := DaemonSocketPath("/tmp/tmux-1000/default")
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	b, err := DaemonSocketPath("/tmp/tmux-1000/default")
	if err != nil {
		t.Fatalf("b: %v", err)
	}
	if a != b {
		t.Fatalf("hash unstable: %q vs %q", a, b)
	}
	c, err := DaemonSocketPath("/tmp/tmux-1000/other")
	if err != nil {
		t.Fatalf("c: %v", err)
	}
	if a == c {
		t.Fatalf("different inputs hashed the same: %q", a)
	}
	if !strings.HasSuffix(a, ".sock") {
		t.Fatalf("want .sock suffix, got %q", a)
	}
}

func TestDaemonSocketPath_DirCreated(t *testing.T) {
	d := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", d)
	p, err := DaemonSocketPath("/tmp/tmux-1000/default")
	if err != nil {
		t.Fatalf("DaemonSocketPath: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(p)); err != nil {
		t.Fatalf("dir should exist: %v", err)
	}
}

func TestResolveTmuxSocket_Precedence(t *testing.T) {
	t.Setenv("TMUX", "/tmp/env-sock,12345,0")
	// Explicit socketPath wins over env.
	got, err := ResolveTmuxSocket("/tmp/x/a", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tmp/x/a" {
		t.Fatalf("got %q", got)
	}
	// socketName resolves under /tmp/tmux-<uid>/NAME.
	got, err = ResolveTmuxSocket("", "myname")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasSuffix(got, "/myname") {
		t.Fatalf("got %q", got)
	}
	// $TMUX when no flags.
	got, err = ResolveTmuxSocket("", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "/tmp/env-sock" {
		t.Fatalf("got %q", got)
	}
	// Default when nothing set.
	t.Setenv("TMUX", "")
	got, err = ResolveTmuxSocket("", "")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.HasSuffix(got, "/default") {
		t.Fatalf("got %q", got)
	}
}

func TestResolveTmuxSocket_BothFlagsRejected(t *testing.T) {
	_, err := ResolveTmuxSocket("/a", "b")
	if err == nil {
		t.Fatal("want error")
	}
}
