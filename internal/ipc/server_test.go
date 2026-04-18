package ipc

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/controller"
	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// startInProcServer spins up a Server bound to a temp socket backed
// by a Fake Port. It returns the client plus a cleanup fn.
func startInProcServer(t *testing.T, fake *tmuxctl.Fake) (*Client, *controller.Controller, func()) {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "daemon.sock")
	ln, err := Listen(sock)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	c := controller.New(fake)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{Listener: ln, Controller: c, Shutdown: cancel}
	go func() { _ = srv.Serve(ctx) }()
	cl := NewClient(sock)
	return cl, c, func() {
		cancel()
		srv.Close()
		c.Stop()
	}
}

func TestServer_ListRoundTrip(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%1", "%2"}}
	cl, _, cleanup := startInProcServer(t, fake)
	defer cleanup()

	resp, err := cl.Call(&Request{Op: OpList}, 2*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !resp.OK {
		t.Fatalf("want OK, got %+v", resp)
	}
	var body domain.ListResponse
	if err := json.Unmarshal(resp.Body, &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if len(body.Panes) != 2 {
		t.Fatalf("panes = %v", body.Panes)
	}
}

func TestServer_SnapshotThenRead(t *testing.T) {
	// Verifies the daemon's Store persists across separate Call
	// invocations — the core cross-invocation guarantee spec §11
	// promises.
	fake := &tmuxctl.Fake{
		Panes:   []domain.PaneID{"%42"},
		Screens: map[domain.PaneID]string{"%42": "$ "},
	}
	cl, _, cleanup := startInProcServer(t, fake)
	defer cleanup()

	// First call: snapshot.
	sRaw, err := cl.Call(&Request{Op: OpSnapshot, Pane: "%42"}, 2*time.Second)
	if err != nil || !sRaw.OK {
		t.Fatalf("snapshot: err=%v resp=%+v", err, sRaw)
	}
	var snap domain.SnapshotResponse
	if err := json.Unmarshal(sRaw.Body, &snap); err != nil {
		t.Fatalf("snap body: %v", err)
	}

	// Emit output via the fake; the daemon drains it into the store.
	fake.Emit("%42", []byte("hello world\n"))

	// Second call: read. Poll briefly because drain is async.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		rRaw, err := cl.Call(&Request{Op: OpRead, Pane: "%42", After: snap.Next}, 2*time.Second)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if !rRaw.OK {
			t.Fatalf("read error: %+v", rRaw)
		}
		var r domain.ReadResponse
		if err := json.Unmarshal(rRaw.Body, &r); err != nil {
			t.Fatalf("read body: %v", err)
		}
		if r.Text == "hello world" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("never saw hello world")
}

func TestServer_MissingAfterOnRead(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%42"}}
	cl, _, cleanup := startInProcServer(t, fake)
	defer cleanup()
	resp, err := cl.Call(&Request{Op: OpRead, Pane: "%42"}, 2*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if resp.OK || resp.Error == nil {
		t.Fatalf("want error, got %+v", resp)
	}
	if resp.Error.Code != domain.ErrMissingAfter {
		t.Fatalf("code = %q", resp.Error.Code)
	}
}

func TestServer_TextReturnsEmptyOKPayload(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%42"}}
	cl, _, cleanup := startInProcServer(t, fake)
	defer cleanup()
	resp, err := cl.Call(&Request{Op: OpText, Pane: "%42", Text: "hi", Enter: true}, 2*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !resp.OK {
		t.Fatalf("want OK, got %+v", resp)
	}
	if len(resp.Body) != 0 {
		t.Fatalf("body should be empty, got %q", resp.Body)
	}
	sent := fake.SentText()
	if len(sent) != 1 || sent[0].Text != "hi" || !sent[0].Enter {
		t.Fatalf("got %+v", sent)
	}
}

func TestServer_WaitSentinel(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%42"}}
	cl, _, cleanup := startInProcServer(t, fake)
	defer cleanup()

	sRaw, _ := cl.Call(&Request{Op: OpSnapshot, Pane: "%42"}, 2*time.Second)
	var snap domain.SnapshotResponse
	_ = json.Unmarshal(sRaw.Body, &snap)

	go func() {
		time.Sleep(40 * time.Millisecond)
		fake.Emit("%42", []byte("__DONE__:run1:3\n"))
	}()

	resp, err := cl.Call(&Request{
		Op: OpWait, Pane: "%42", After: snap.Next,
		Mode: "sentinel", SentinelToken: "run1", TimeoutMs: 2000,
	}, 5*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !resp.OK {
		t.Fatalf("not OK: %+v", resp)
	}
	var w domain.WaitResponse
	if err := json.Unmarshal(resp.Body, &w); err != nil {
		t.Fatalf("body: %v", err)
	}
	if w.ExitCode == nil || *w.ExitCode != 3 {
		t.Fatalf("exit_code = %v", w.ExitCode)
	}
}

func TestServer_ShutdownOp(t *testing.T) {
	fake := &tmuxctl.Fake{}
	cl, _, cleanup := startInProcServer(t, fake)
	defer cleanup()
	resp, err := cl.Call(&Request{Op: OpShutdown}, 2*time.Second)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if !resp.OK {
		t.Fatalf("want OK, got %+v", resp)
	}
	// After shutdown the listener should refuse new connections
	// within a short window.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := cl.Call(&Request{Op: OpList}, 200*time.Millisecond); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server still accepting after shutdown")
}
