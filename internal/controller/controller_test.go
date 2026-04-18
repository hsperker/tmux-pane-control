package controller

import (
	"context"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// waitUntil polls fn up to d, sleeping 5ms between checks. Fails the
// test if fn never returns true.
func waitUntil(t *testing.T, d time.Duration, fn func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %v", d)
}

func TestController_SnapshotReadRoundTrip(t *testing.T) {
	fake := &tmuxctl.Fake{
		Panes:   []domain.PaneID{"%42"},
		Screens: map[domain.PaneID]string{"%42": "$ "},
	}
	c := New(fake)
	ctx := context.Background()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(c.Stop)

	snap, err := c.Snapshot("%42", nil)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	// Emit pane output via the fake subscription.
	fake.Emit("%42", []byte("hello "))
	fake.Emit("%42", []byte("world"))

	// The drain goroutine is async; poll until it lands.
	waitUntil(t, time.Second, func() bool {
		r, err := c.Read("%42", snap.Next)
		if err != nil {
			return false
		}
		return r.Text == "hello world"
	})
}

func TestController_ReadEmptyWithoutOutput(t *testing.T) {
	fake := &tmuxctl.Fake{
		Panes:   []domain.PaneID{"%42"},
		Screens: map[domain.PaneID]string{"%42": "hi"},
	}
	c := New(fake)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(c.Stop)

	snap, err := c.Snapshot("%42", nil)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	r, err := c.Read("%42", snap.Next)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if r.Text != "" {
		t.Fatalf("want empty read, got %q", r.Text)
	}
}

func TestController_MultipleSnapshotsSamePane(t *testing.T) {
	fake := &tmuxctl.Fake{
		Panes:   []domain.PaneID{"%42"},
		Screens: map[domain.PaneID]string{"%42": "$ "},
	}
	c := New(fake)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(c.Stop)

	snap1, err := c.Snapshot("%42", nil)
	if err != nil {
		t.Fatalf("snap1: %v", err)
	}
	fake.Emit("%42", []byte("abc"))
	waitUntil(t, time.Second, func() bool {
		r, err := c.Read("%42", snap1.Next)
		return err == nil && r.Text == "abc"
	})
	// Second snapshot: the token advances past the appended bytes.
	snap2, err := c.Snapshot("%42", nil)
	if err != nil {
		t.Fatalf("snap2: %v", err)
	}
	if snap1.Next == snap2.Next {
		t.Fatal("snap2.Next must differ from snap1.Next after output")
	}
	// Read from snap2 gets nothing new.
	r, err := c.Read("%42", snap2.Next)
	if err != nil {
		t.Fatalf("read after snap2: %v", err)
	}
	if r.Text != "" {
		t.Fatalf("want empty, got %q", r.Text)
	}
}

func TestController_StopIsIdempotentAndSafe(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%1"}}
	c := New(fake)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	c.Stop()
	c.Stop() // no panic
}

func TestController_List(t *testing.T) {
	fake := &tmuxctl.Fake{Panes: []domain.PaneID{"%1", "%2"}}
	c := New(fake)
	if err := c.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(c.Stop)
	resp, err := c.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(resp.Panes) != 2 {
		t.Fatalf("panes = %v", resp.Panes)
	}
}
