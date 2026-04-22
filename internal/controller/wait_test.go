package controller

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/store"
)

// TestWait_Regex_MultilineAnchorAgainstCRLF pins spec §9.6's CR-strip
// rule end-to-end. TTY output routinely ends lines with \r\n; a
// regex like (?m)^ready$ must still match, which only works if the
// wait handler normalizes \r before running the matcher.
func TestWait_Regex_MultilineAnchorAgainstCRLF(t *testing.T) {
	s := store.New(1024)
	tok := s.NewToken("%42")
	s.Append("%42", []byte("prefix\r\nready\r\nsuffix\r\n"))

	re := regexp.MustCompile(`(?m)^ready$`)
	resp, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%42", After: tok, Timeout: time.Second,
		Mode: WaitModeRegex, Regex: re,
	})
	if err != nil {
		t.Fatalf("Wait: %v (CR likely not stripped before match)", err)
	}
	if resp.Result != domain.WaitRegex {
		t.Fatalf("result = %q", resp.Result)
	}
	if resp.Matched == nil || *resp.Matched != "ready" {
		t.Fatalf("matched = %v, want \"ready\"", resp.Matched)
	}
}

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

func TestWait_InvalidAfterWrongPane_BothExist(t *testing.T) {
	// Both panes exist → token-for-wrong-pane surfaces as
	// INVALID_AFTER (spec §7.5 precedence).
	s := store.New(1024)
	tok := s.NewToken("%42")
	s.Ensure("%43")
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

func TestWait_PaneNotFoundWhenTargetMissing(t *testing.T) {
	// Target pane doesn't exist → PANE_NOT_FOUND (spec §7.5 wins
	// over INVALID_AFTER even though the token is also wrong).
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
	if cerr.Code != domain.ErrPaneNotFound {
		t.Fatalf("code = %q want PANE_NOT_FOUND", cerr.Code)
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

func TestWait_Regex_AlreadyBuffered(t *testing.T) {
	s := store.New(1024)
	tok := s.NewToken("%42")
	s.Append("%42", []byte("server READY at port 8080"))
	re := regexp.MustCompile(`READY`)
	resp, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%42", After: tok, Timeout: time.Second,
		Mode: WaitModeRegex, Regex: re,
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if resp.Result != domain.WaitRegex {
		t.Fatalf("result = %q", resp.Result)
	}
	if resp.Matched == nil || *resp.Matched != "READY" {
		t.Fatalf("matched = %v", resp.Matched)
	}
	if resp.ExitCode != nil {
		t.Fatalf("exit_code must be nil for regex, got %v", *resp.ExitCode)
	}
}

func TestWait_Regex_ArrivesLater(t *testing.T) {
	s := store.New(1024)
	tok := s.NewToken("%42")
	re := regexp.MustCompile(`[A-Z]+-[0-9]+`)
	done := make(chan *domain.WaitResponse, 1)
	go func() {
		r, err := Wait(context.Background(), s, WaitRequest{
			PaneID: "%42", After: tok, Timeout: 2 * time.Second,
			Mode: WaitModeRegex, Regex: re,
		})
		if err == nil {
			done <- r
		}
	}()
	time.Sleep(30 * time.Millisecond)
	s.Append("%42", []byte("ident=FOO-123 ok"))
	select {
	case r := <-done:
		if *r.Matched != "FOO-123" {
			t.Fatalf("matched = %q", *r.Matched)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("regex match did not land")
	}
}

func TestWait_Regex_NoAnchorNoDotall(t *testing.T) {
	// Spec §9.6: no implicit anchoring; `.` does not match newline
	// by default — verified via the pure matcher tests. Here we just
	// confirm Wait relays `(?s)` correctly when the caller adds it.
	s := store.New(1024)
	tok := s.NewToken("%42")
	s.Append("%42", []byte("a\nb"))
	re := regexp.MustCompile(`(?s)a.b`)
	resp, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%42", After: tok, Timeout: time.Second,
		Mode: WaitModeRegex, Regex: re,
	})
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if *resp.Matched != "a\nb" {
		t.Fatalf("matched = %q", *resp.Matched)
	}
}

// TestWait_Quiescence_RejectsWindowGEQTimeout pins the
// argument-validation gap reported as a post-v0.2.0 bug: a
// --ms value that is >= --timeout-ms can never succeed by the
// normal wait-and-observe path (the timeout fires before --ms
// of idle accumulates), so the wait should fail immediately
// with INVALID_ARGS instead of running out the clock and
// returning an opaque TIMEOUT.
//
// The timing assertion (<50ms) guards against a future
// regression where the validation gets removed and this test
// still "passes" via the TIMEOUT path.
func TestWait_Quiescence_RejectsWindowGEQTimeout(t *testing.T) {
	s := store.New(1024)
	tok := s.NewToken("%42")

	cases := []struct {
		name         string
		quiet, total time.Duration
	}{
		{"greater", 5 * time.Second, 2 * time.Second},
		{"equal", time.Second, time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Now()
			_, err := Wait(context.Background(), s, WaitRequest{
				PaneID: "%42", After: tok, Timeout: tc.total,
				Mode: WaitModeQuiescence, QuietWindow: tc.quiet,
			})
			elapsed := time.Since(start)
			var cerr *domain.ErrorResponse
			if !errors.As(err, &cerr) {
				t.Fatalf("want *ErrorResponse, got %T", err)
			}
			if cerr.Code != domain.ErrInvalidArgs {
				t.Fatalf("code = %q, want INVALID_ARGS", cerr.Code)
			}
			if elapsed > 50*time.Millisecond {
				t.Fatalf("rejection took %v; must not run the clock", elapsed)
			}
			// Message must name both values so the user can spot
			// the mistake without reading the spec.
			for _, needle := range []string{"--ms", "--timeout-ms"} {
				if !strings.Contains(cerr.Message, needle) {
					t.Errorf("message missing %q: %q", needle, cerr.Message)
				}
			}
		})
	}
}

func TestWait_Quiescence_ImmediatelySatisfied(t *testing.T) {
	// Spec §9.6: if the pane is already quiet for at least --ms
	// when the wait is registered, succeed IMMEDIATELY. We verify
	// "immediate" by measuring wall-clock elapsed time: it must be
	// well under the quiet window (e.g. we waited 60ms beyond the
	// checkpoint but Wait should return in <20ms regardless of the
	// quiet window value).
	s := store.New(1024)
	tok := s.NewToken("%42")
	time.Sleep(60 * time.Millisecond)
	start := time.Now()
	resp, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%42", After: tok, Timeout: time.Second,
		Mode: WaitModeQuiescence, QuietWindow: 50 * time.Millisecond,
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if elapsed > 25*time.Millisecond {
		t.Fatalf("wait took %v, expected <25ms (not 'immediate')", elapsed)
	}
	if resp.Result != domain.WaitQuiescence {
		t.Fatalf("result = %q", resp.Result)
	}
	if resp.Matched != nil {
		t.Fatalf("matched must be nil, got %v", *resp.Matched)
	}
	if resp.ExitCode != nil {
		t.Fatalf("exit_code must be nil, got %v", *resp.ExitCode)
	}
}

func TestWait_Quiescence_ResetOnOutput(t *testing.T) {
	// Spec §9.6: any byte appended after the checkpoint resets the
	// quiet timer. Keep appending inside the quiet window; wait only
	// succeeds once we stop.
	s := store.New(1024)
	tok := s.NewToken("%42")

	done := make(chan *domain.WaitResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		r, e := Wait(context.Background(), s, WaitRequest{
			PaneID: "%42", After: tok, Timeout: 3 * time.Second,
			Mode: WaitModeQuiescence, QuietWindow: 100 * time.Millisecond,
		})
		if e != nil {
			errCh <- e
			return
		}
		done <- r
	}()

	// Keep the pane "active" by appending every 30ms for 200ms total.
	for i := 0; i < 6; i++ {
		time.Sleep(30 * time.Millisecond)
		s.Append("%42", []byte("."))
	}
	// Then stop and wait for success.
	select {
	case r := <-done:
		if r.Result != domain.WaitQuiescence {
			t.Fatalf("result = %q", r.Result)
		}
	case e := <-errCh:
		t.Fatalf("Wait errored: %v", e)
	case <-time.After(2 * time.Second):
		t.Fatal("quiescence did not settle in time")
	}
}

func TestWait_Quiescence_TimeoutUnderSteadyOutput(t *testing.T) {
	// If output never stops, quiescence never triggers; we should
	// hit the wait timeout.
	s := store.New(1024)
	tok := s.NewToken("%42")

	// Background feeder: spam output faster than the quiet window.
	feederCtx, feederCancel := context.WithCancel(context.Background())
	defer feederCancel()
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-feederCtx.Done():
				return
			case <-t.C:
				s.Append("%42", []byte("."))
			}
		}
	}()

	_, err := Wait(context.Background(), s, WaitRequest{
		PaneID: "%42", After: tok, Timeout: 150 * time.Millisecond,
		Mode: WaitModeQuiescence, QuietWindow: 100 * time.Millisecond,
	})
	feederCancel()
	var cerr *domain.ErrorResponse
	if !errors.As(err, &cerr) {
		t.Fatalf("want ErrorResponse, got %v", err)
	}
	if cerr.Code != domain.ErrTimeout {
		t.Fatalf("code = %q", cerr.Code)
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
