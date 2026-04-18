package store

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// DefaultCapacity is the per-pane retention budget required by
// spec §11.8: 1 MiB.
const DefaultCapacity = 1 << 20

// ErrPaneUnknown is returned by Read for a pane that the store has
// never seen a token for. Controllers translate this to the
// appropriate command-level code.
var ErrPaneUnknown = errors.New("pane not known to store")

// Store holds per-pane output buffers and mediates checkpoint tokens.
// Methods are safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	capacity int
	instance string
	bufs     map[domain.PaneID]*Buffer
}

// New returns a store with the given per-pane capacity. The instance
// id is generated randomly so tokens from prior controller instances
// are rejected (spec §11.7).
func New(capacity int) *Store {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return &Store{
		capacity: capacity,
		instance: hex.EncodeToString(b[:]),
		bufs:     map[domain.PaneID]*Buffer{},
	}
}

// Instance exposes the controller-instance id used for token scoping.
// Exposed for tests and diagnostics.
func (s *Store) Instance() string { return s.instance }

// bufferLocked returns the Buffer for id, creating it if missing.
// Callers must hold s.mu.
func (s *Store) bufferLocked(id domain.PaneID) *Buffer {
	b, ok := s.bufs[id]
	if !ok {
		b = NewBuffer(s.capacity)
		s.bufs[id] = b
	}
	return b
}

// Ensure creates an empty buffer for id if none exists. Called by the
// controller when it learns of a new pane before any output arrives.
func (s *Store) Ensure(id domain.PaneID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bufferLocked(id)
}

// HasPane reports whether the store has a buffer for id. Used to
// distinguish PANE_NOT_FOUND (no buffer) from INVALID_AFTER (buffer
// exists but token is stale).
func (s *Store) HasPane(id domain.PaneID) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.bufs[id]
	return ok
}

// Forget drops the buffer for id. Called when a pane is destroyed
// (spec §11.9).
func (s *Store) Forget(id domain.PaneID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.bufs, id)
}

// Append writes output bytes for the given pane.
func (s *Store) Append(id domain.PaneID, p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bufferLocked(id).Append(p)
}

// NewToken returns a token pointing at the current stream head for
// the given pane (i.e. snapshot's "next"). The buffer is created if
// missing so future Append calls are recorded.
func (s *Store) NewToken(id domain.PaneID) domain.Token {
	s.mu.Lock()
	defer s.mu.Unlock()
	b := s.bufferLocked(id)
	return encodeToken(s.instance, id, b.End())
}

// Read returns the bytes appended since the token's offset and a new
// token pointing at the new stream head. It is the store half of the
// `tpctl read` handler.
//
// Errors:
//   - ErrTokenMalformed / ErrTokenWrongInstance / ErrTokenWrongPane /
//     ErrTokenEvicted: translate to INVALID_AFTER (spec §7.5)
//   - ErrPaneUnknown: translate to PANE_NOT_FOUND
func (s *Store) Read(id domain.PaneID, after domain.Token) ([]byte, domain.Token, error) {
	tp, err := decodeToken(after)
	if err != nil {
		return nil, "", err
	}
	if tp.I != s.instance {
		return nil, "", ErrTokenWrongInstance
	}
	if domain.PaneID(tp.P) != id {
		return nil, "", ErrTokenWrongPane
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.bufs[id]
	if !ok {
		return nil, "", ErrPaneUnknown
	}
	out, end, ok := b.Read(tp.O)
	if !ok {
		return nil, "", ErrTokenEvicted
	}
	return out, encodeToken(s.instance, id, end), nil
}
