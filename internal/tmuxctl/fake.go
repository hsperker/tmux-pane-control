package tmuxctl

import (
	"context"
	"sync"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// Fake is an in-memory Port for tests. Zero value is usable: no panes,
// no errors.
//
// Emit sends synthetic pane output to every live Subscribe channel.
type Fake struct {
	Panes         []domain.PaneID
	Screens       map[domain.PaneID]string
	ListPanesFn   func() ([]domain.PaneID, error)
	CapturePaneFn func(domain.PaneID) (string, error)

	mu   sync.Mutex
	subs []chan PaneOutput
}

// ListPanes returns either the ListPanesFn result (if set) or the Panes
// slice. It exists so tests can inject errors without replacing the
// whole fake.
func (f *Fake) ListPanes() ([]domain.PaneID, error) {
	if f.ListPanesFn != nil {
		return f.ListPanesFn()
	}
	return append([]domain.PaneID(nil), f.Panes...), nil
}

// CapturePane returns the pre-set Screens entry for the pane, or
// ErrPaneNotFound if the pane is not in the Screens map or Panes list.
func (f *Fake) CapturePane(id domain.PaneID) (string, error) {
	if f.CapturePaneFn != nil {
		return f.CapturePaneFn(id)
	}
	if f.Screens != nil {
		if text, ok := f.Screens[id]; ok {
			return text, nil
		}
	}
	for _, p := range f.Panes {
		if p == id {
			return "", nil
		}
	}
	return "", ErrPaneNotFound
}

// Subscribe returns a channel that receives events emitted via Emit
// after the subscription is registered. The channel is closed when
// ctx is cancelled.
func (f *Fake) Subscribe(ctx context.Context) (<-chan PaneOutput, error) {
	ch := make(chan PaneOutput, 64)
	f.mu.Lock()
	f.subs = append(f.subs, ch)
	f.mu.Unlock()
	go func() {
		<-ctx.Done()
		f.mu.Lock()
		defer f.mu.Unlock()
		for i, c := range f.subs {
			if c == ch {
				f.subs = append(f.subs[:i], f.subs[i+1:]...)
				break
			}
		}
		close(ch)
	}()
	return ch, nil
}

// Emit synchronously delivers an output event to all current
// subscribers. It blocks briefly if any subscriber's channel is full.
func (f *Fake) Emit(id domain.PaneID, data []byte) {
	f.mu.Lock()
	subs := append([]chan PaneOutput(nil), f.subs...)
	f.mu.Unlock()
	for _, c := range subs {
		c <- PaneOutput{ID: id, Data: append([]byte(nil), data...)}
	}
}

// Ensure Fake satisfies Port at compile time.
var _ Port = (*Fake)(nil)
