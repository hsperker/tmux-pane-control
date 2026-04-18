package controller

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/store"
)

func TestWait_Sentinel_AlreadyBuffered(t *testing.T) {
	// Spec §9.6: wait considers already-buffered post-token output
	// at registration time, not just future arrivals.
	s := store.New(1024)
	tok := s.NewToken("%42")
	s.Append("%42", []byte("hello\n__DONE__:run1:0\nmore"))

	resp, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%42", After: tok, Timeout: time.Second,
		Mode: WaitModeSentinel, SentinelToken: "run1",
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if resp.Result != domain.WaitSentinel {
		t.Fatalf("result = %q", resp.Result)
	}
	if resp.Matched == nil || *resp.Matched != "__DONE__:run1:0" {
		t.Fatalf("matched = %v", resp.Matched)
	}
	if resp.ExitCode == nil || *resp.ExitCode != 0 {
		t.Fatalf("exit_code = %v", resp.ExitCode)
	}
	if resp.Next == "" {
		t.Fatal("next must be set")
	}
}

func TestWait_Sentinel_ArrivesLater(t *testing.T) {
	s := store.New(1024)
	tok := s.NewToken("%42")

	done := make(chan *domain.WaitResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := Wait(context.Background(), s, WaitRequest{
			PaneID: "%42", After: tok, Timeout: 2 * time.Second,
			Mode: WaitModeSentinel, SentinelToken: "run1",
		})
		if err != nil {
			errCh <- err
			return
		}
		done <- resp
	}()

	// Simulate incremental output with the sentinel arriving later.
	time.Sleep(20 * time.Millisecond)
	s.Append("%42", []byte("partial..."))
	time.Sleep(20 * time.Millisecond)
	s.Append("%42", []byte("\n__DONE__:run1:42\n"))

	select {
	case resp := <-done:
		if *resp.ExitCode != 42 {
			t.Fatalf("exit_code = %d", *resp.ExitCode)
		}
	case err := <-errCh:
		t.Fatalf("Wait errored: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("timed out")
	}
}

func TestWait_Sentinel_Timeout(t *testing.T) {
	s := store.New(1024)
	tok := s.NewToken("%42")
	_, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%42", After: tok, Timeout: 30 * time.Millisecond,
		Mode: WaitModeSentinel, SentinelToken: "run1",
	})
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if cerr.Code != domain.ErrTimeout {
		t.Fatalf("code = %q", cerr.Code)
	}
}

func TestWait_MissingAfter(t *testing.T) {
	s := store.New(1024)
	_, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%42", Timeout: time.Second,
		Mode: WaitModeSentinel, SentinelToken: "run1",
	})
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if cerr.Code != domain.ErrMissingAfter {
		t.Fatalf("code = %q", cerr.Code)
	}
}

func TestWait_InvalidAfterWrongPane(t *testing.T) {
	s := store.New(1024)
	tok := s.NewToken("%42")
	_, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%43", After: tok, Timeout: time.Second,
		Mode: WaitModeSentinel, SentinelToken: "run1",
	})
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if cerr.Code != domain.ErrInvalidAfter {
		t.Fatalf("code = %q", cerr.Code)
	}
}

func TestWait_SentinelTokenValidation(t *testing.T) {
	s := store.New(1024)
	tok := s.NewToken("%42")
	for _, bad := range []string{"", "has:colon", "has\nnewline"} {
		_, err := Wait(context.Background(), s, WaitRequest{
			PaneID: "%42", After: tok, Timeout: time.Second,
			Mode: WaitModeSentinel, SentinelToken: bad,
		})
		var cerr *domain.ErrorResponse
		if !errors.As(err, &cerr) {
			t.Fatalf("token=%q: want ErrorResponse, got %v", bad, err)
		}
		if cerr.Code != domain.ErrInvalidArgs {
			t.Fatalf("token=%q: code = %q", bad, cerr.Code)
		}
	}
}

func TestWait_Concurrent(t *testing.T) {
	// Spec §9.6 concurrency: multiple waits on the same pane evaluate
	// independently. Register two sentinel waits with different
	// tokens; a single append satisfies both.
	s := store.New(1024)
	tok := s.NewToken("%42")

	type result struct {
		r *domain.WaitResponse
		e error
	}
	r1 := make(chan result, 1)
	r2 := make(chan result, 1)
	go func() {
		r, e := Wait(context.Background(), s, WaitRequest{
			PaneID: "%42", After: tok, Timeout: 2 * time.Second,
			Mode: WaitModeSentinel, SentinelToken: "A",
		})
		r1 <- result{r, e}
	}()
	go func() {
		r, e := Wait(context.Background(), s, WaitRequest{
			PaneID: "%42", After: tok, Timeout: 2 * time.Second,
			Mode: WaitModeSentinel, SentinelToken: "B",
		})
		r2 <- result{r, e}
	}()

	time.Sleep(30 * time.Millisecond)
	s.Append("%42", []byte("__DONE__:A:1\n__DONE__:B:2\n"))

	for i, ch := range []chan result{r1, r2} {
		select {
		case res := <-ch:
			if res.e != nil {
				t.Fatalf("r%d: %v", i, res.e)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("r%d timed out", i)
		}
	}
}
