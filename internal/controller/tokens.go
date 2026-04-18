package controller

import (
	"fmt"
	"sync/atomic"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// TokenIssuer produces checkpoint tokens for snapshot/read/wait.
// The real, ring-buffer-indexed implementation lands in a later slice;
// this interface lets slice 5 keep the snapshot handler shape stable.
type TokenIssuer interface {
	Next(id domain.PaneID) domain.Token
}

// CounterIssuer is a placeholder TokenIssuer used until the real
// stream-cursor-backed issuer arrives in slice 7. It produces
// monotonically increasing, pane-scoped tokens suitable for human
// inspection and for keeping the snapshot response shape stable.
type CounterIssuer struct {
	n uint64
}

// Next returns a fresh token. Tokens are opaque per spec §4.3; the
// "r_" prefix and zero-padding are implementation detail.
func (c *CounterIssuer) Next(_ domain.PaneID) domain.Token {
	v := atomic.AddUint64(&c.n, 1)
	return domain.Token(fmt.Sprintf("r_%06d", v))
}
