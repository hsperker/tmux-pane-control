package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

func runApp(args []string) (code int, stdout, stderr string) {
	var so, se bytes.Buffer
	app := &App{Stdout: &so, Stderr: &se}
	code = app.Run(args)
	return code, so.String(), se.String()
}

func TestHelp(t *testing.T) {
	code, stdout, stderr := runApp([]string{"--help"})
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(stdout, "tpctl") {
		t.Fatalf("stdout missing tpctl: %q", stdout)
	}
	if stderr != "" {
		t.Fatalf("stderr not empty: %q", stderr)
	}
}

func TestNoArgs(t *testing.T) {
	code, _, stderr := runApp(nil)
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	if stderr == "" {
		t.Fatal("want usage on stderr")
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, stderr := runApp([]string{"bogus"})
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	if !strings.Contains(stderr, "unknown command") {
		t.Fatalf("stderr: %q", stderr)
	}
}

func TestListMutuallyExclusiveSocketFlags(t *testing.T) {
	code, stdout, _ := runApp([]string{"list", "--tmux-socket", "/tmp/a", "--tmux-socket-name", "foo"})
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	var e domain.ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e); err != nil {
		t.Fatalf("stdout not JSON: %q (%v)", stdout, err)
	}
	if e.Code != domain.ErrInvalidArgs {
		t.Fatalf("code = %q", e.Code)
	}
}

func TestListRejectsPositionals(t *testing.T) {
	code, stdout, _ := runApp([]string{"list", "extra"})
	if code == 0 {
		t.Fatal("want nonzero exit")
	}
	var e domain.ErrorResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &e); err != nil {
		t.Fatalf("stdout not JSON: %q (%v)", stdout, err)
	}
	if e.Code != domain.ErrInvalidArgs {
		t.Fatalf("code = %q", e.Code)
	}
}
