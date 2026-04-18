package tmuxctl

import "github.com/hsperker/tmux-pane-control/internal/domain"

// Fake is an in-memory Port for tests. Zero value is usable: no panes,
// no errors.
type Fake struct {
	Panes         []domain.PaneID
	Screens       map[domain.PaneID]string
	ListPanesFn   func() ([]domain.PaneID, error)
	CapturePaneFn func(domain.PaneID) (string, error)
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

// Ensure Fake satisfies Port at compile time.
var _ Port = (*Fake)(nil)
