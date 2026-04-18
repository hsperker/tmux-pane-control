package domain

import (
	"encoding/json"
	"testing"
)

func marshalCompact(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	return string(b)
}

func TestListResponse_Empty(t *testing.T) {
	got := marshalCompact(t, ListResponse{Panes: []PaneID{}})
	want := `{"panes":[]}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestListResponse_Populated(t *testing.T) {
	got := marshalCompact(t, ListResponse{Panes: []PaneID{"%42", "%43", "%44"}})
	want := `{"panes":["%42","%43","%44"]}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSnapshotResponse_NoHistory(t *testing.T) {
	// Spec §9.2 success example (no history).
	got := marshalCompact(t, SnapshotResponse{
		PaneID: "%42",
		Next:   "r_000205",
		Text:   "$ kubectl get pods\nNo resources found in default namespace.\n$ ",
	})
	want := `{"pane_id":"%42","next":"r_000205","text":"$ kubectl get pods\nNo resources found in default namespace.\n$ "}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSnapshotResponse_WithHistory(t *testing.T) {
	// Spec §9.2 success example (with history).
	got := marshalCompact(t, SnapshotResponse{
		PaneID:         "%42",
		Next:           "r_000205",
		ScrollbackText: NewString("previous line 1\nprevious line 2\n"),
		Text:           "$ current visible screen here",
	})
	want := `{"pane_id":"%42","next":"r_000205","scrollback_text":"previous line 1\nprevious line 2\n","text":"$ current visible screen here"}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSnapshotResponse_HistoryZeroLines(t *testing.T) {
	// Spec §9.2: N=0 means "history mode requested, zero lines";
	// scrollback_text: "" is included.
	got := marshalCompact(t, SnapshotResponse{
		PaneID:         "%42",
		Next:           "r_000205",
		ScrollbackText: NewString(""),
		Text:           "hi",
	})
	want := `{"pane_id":"%42","next":"r_000205","scrollback_text":"","text":"hi"}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestReadResponse_EmptyTextIsPresent(t *testing.T) {
	// Spec §9.3: empty text is still a success; the field stays present.
	got := marshalCompact(t, ReadResponse{PaneID: "%42", Next: "r_000220", Text: ""})
	want := `{"pane_id":"%42","next":"r_000220","text":""}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestReadResponse_WithOutput(t *testing.T) {
	got := marshalCompact(t, ReadResponse{
		PaneID: "%42",
		Next:   "r_000220",
		Text:   "kubectl get pods\nNo resources found in default namespace.\n$ ",
	})
	want := `{"pane_id":"%42","next":"r_000220","text":"kubectl get pods\nNo resources found in default namespace.\n$ "}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestWaitResponse_Sentinel(t *testing.T) {
	// Spec §9.6 success example (sentinel).
	got := marshalCompact(t, WaitResponse{
		PaneID:   "%42",
		Next:     "r_000240",
		Result:   WaitSentinel,
		Matched:  NewString("__DONE__:abc123:0"),
		ExitCode: NewInt(0),
	})
	want := `{"pane_id":"%42","next":"r_000240","result":"sentinel","matched":"__DONE__:abc123:0","exit_code":0}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestWaitResponse_Regex(t *testing.T) {
	got := marshalCompact(t, WaitResponse{
		PaneID:  "%42",
		Next:    "r_000248",
		Result:  WaitRegex,
		Matched: NewString("READY"),
	})
	want := `{"pane_id":"%42","next":"r_000248","result":"regex","matched":"READY"}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestWaitResponse_Quiescence(t *testing.T) {
	// Spec §9.6: quiescence returns only result and next. No matched.
	got := marshalCompact(t, WaitResponse{
		PaneID: "%42",
		Next:   "r_000252",
		Result: WaitQuiescence,
	})
	want := `{"pane_id":"%42","next":"r_000252","result":"quiescence"}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestErrorResponse_WithPane(t *testing.T) {
	got := marshalCompact(t, &ErrorResponse{
		PaneID:  "%42",
		Code:    ErrMissingAfter,
		Message: "read requires --after; use snapshot to bootstrap",
	})
	want := `{"pane_id":"%42","code":"MISSING_AFTER","message":"read requires --after; use snapshot to bootstrap"}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestErrorResponse_WithoutPane(t *testing.T) {
	// Spec §7.6: pane_id is omitted when the command is not pane-scoped
	// or pane resolution failed before a specific identity was established.
	got := marshalCompact(t, &ErrorResponse{
		Code:    ErrInvalidArgs,
		Message: "--tmux-socket and --tmux-socket-name are mutually exclusive",
	})
	want := `{"code":"INVALID_ARGS","message":"--tmux-socket and --tmux-socket-name are mutually exclusive"}`
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestErrorResponse_CanonicalCodes(t *testing.T) {
	// Spec §7.5 canonical codes must be stable strings.
	cases := map[ErrorCode]string{
		ErrMissingAfter: "MISSING_AFTER",
		ErrInvalidAfter: "INVALID_AFTER",
		ErrPaneNotFound: "PANE_NOT_FOUND",
		ErrPaneClosed:   "PANE_CLOSED",
		ErrTimeout:      "TIMEOUT",
	}
	for code, want := range cases {
		if string(code) != want {
			t.Errorf("code %v = %q, want %q", code, string(code), want)
		}
	}
}
