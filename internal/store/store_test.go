package store

import (
	"errors"
	"testing"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

func TestStore_TokenPaneScoped_TargetPaneMissing(t *testing.T) {
	// Target pane %43 doesn't exist. Per spec §7.5 precedence, the
	// pane lookup failure wins over INVALID_AFTER → ErrPaneUnknown.
	s := New(16)
	t1 := s.NewToken("%42")
	_, _, err := s.Read("%43", t1)
	if !errors.Is(err, ErrPaneUnknown) {
		t.Fatalf("want ErrPaneUnknown, got %v", err)
	}
}

func TestStore_TokenPaneScoped_BothPanesExist(t *testing.T) {
	// Both %42 and %43 exist in the store; token is for the wrong
	// pane. Pane lookup succeeds, so the precedence rule does not
	// trigger and we get INVALID_AFTER via ErrTokenWrongPane.
	s := New(16)
	t1 := s.NewToken("%42")
	s.Ensure("%43")
	_, _, err := s.Read("%43", t1)
	if !errors.Is(err, ErrTokenWrongPane) {
		t.Fatalf("want ErrTokenWrongPane, got %v", err)
	}
}

func TestStore_TokenWrongInstance(t *testing.T) {
	s1 := New(16)
	t1 := s1.NewToken("%42")
	s2 := New(16) // new instance
	s2.Ensure("%42")
	_, _, err := s2.Read("%42", t1)
	if !errors.Is(err, ErrTokenWrongInstance) {
		t.Fatalf("want ErrTokenWrongInstance, got %v", err)
	}
}

func TestStore_TokenMalformed(t *testing.T) {
	s := New(16)
	s.Ensure("%42")
	_, _, err := s.Read("%42", "not-a-valid-token!!")
	if !errors.Is(err, ErrTokenMalformed) {
		t.Fatalf("want ErrTokenMalformed, got %v", err)
	}
}

func TestStore_TokenEvicted(t *testing.T) {
	s := New(4)
	t1 := s.NewToken("%42") // offset 0
	s.Append("%42", []byte("abcdefgh")) // overflows cap=4 → start=4
	_, _, err := s.Read("%42", t1)
	if !errors.Is(err, ErrTokenEvicted) {
		t.Fatalf("want ErrTokenEvicted, got %v", err)
	}
}

func TestStore_ReadDelta(t *testing.T) {
	s := New(64)
	s.Append("%42", []byte("prior output"))
	tok := s.NewToken("%42")
	s.Append("%42", []byte(" after"))
	out, next, err := s.Read("%42", tok)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if string(out) != " after" {
		t.Fatalf("got %q", out)
	}
	if next == tok {
		t.Fatal("next must advance")
	}
	// A second read with the new token yields no new bytes.
	out2, _, err := s.Read("%42", next)
	if err != nil {
		t.Fatalf("Read2: %v", err)
	}
	if len(out2) != 0 {
		t.Fatalf("got %q", out2)
	}
}

func TestStore_UnknownPane(t *testing.T) {
	s := New(16)
	// Forge a properly-instance-scoped token for an unregistered pane.
	tok := encodeToken(s.instance, "%99", 0, time.Now())
	_, _, err := s.Read("%99", tok)
	if !errors.Is(err, ErrPaneUnknown) {
		t.Fatalf("want ErrPaneUnknown, got %v", err)
	}
}

func TestStore_ForgetDropsBuffer(t *testing.T) {
	s := New(16)
	s.Ensure("%42")
	if !s.HasPane("%42") {
		t.Fatal("want pane present")
	}
	s.Forget("%42")
	if s.HasPane("%42") {
		t.Fatal("want pane forgotten")
	}
}

func TestStore_ReadEmpty(t *testing.T) {
	s := New(16)
	tok := s.NewToken("%42")
	out, next, err := s.Read("%42", tok)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("got %q", out)
	}
	// Tokens are opaque: the string may differ (timestamps advance),
	// but a re-read from the returned next must still be empty and
	// point at the same stream offset.
	out2, _, err := s.Read("%42", next)
	if err != nil {
		t.Fatalf("Read2: %v", err)
	}
	if len(out2) != 0 {
		t.Fatalf("re-read not empty: %q", out2)
	}
}

func TestStore_TokenIsOpaque(t *testing.T) {
	s := New(16)
	tok := s.NewToken("%42")
	// An agent should not be able to tell what offset this points to;
	// here we just assert it's non-empty and base64url-safe.
	if tok == "" {
		t.Fatal("empty token")
	}
	str := string(tok)
	for i := 0; i < len(str); i++ {
		c := str[i]
		if !(c == '_' || c == '-' ||
			(c >= 'a' && c <= 'z') ||
			(c >= 'A' && c <= 'Z') ||
			(c >= '0' && c <= '9')) {
			t.Fatalf("unexpected char %q in token %q", c, str)
		}
	}
}

// Sanity: Append+NewToken round-trips through Read correctly even when
// the buffer capacity is the spec default.
func TestStore_DefaultCapacity(t *testing.T) {
	s := New(DefaultCapacity)
	tok := s.NewToken("%42")
	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = 'x'
	}
	s.Append("%42", payload)
	out, _, err := s.Read("%42", tok)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(out) != len(payload) {
		t.Fatalf("got %d bytes, want %d", len(out), len(payload))
	}
}

// Compile-time assertion of error identity so callers can errors.Is.
func TestStore_ErrorsAreSentinels(t *testing.T) {
	for _, e := range []error{
		ErrTokenMalformed,
		ErrTokenWrongInstance,
		ErrTokenWrongPane,
		ErrTokenEvicted,
		ErrPaneUnknown,
	} {
		if e == nil {
			t.Fatal("nil sentinel")
		}
		if !errors.Is(e, e) {
			t.Fatalf("errors.Is self-check failed for %v", e)
		}
	}
}

// Ensure domain import is used (keeps goimports happy).
var _ = domain.PaneID("")
