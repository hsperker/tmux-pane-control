package tmuxctl

import "github.com/hsperker/tmux-pane-control/internal/domain"

// Fake is an in-memory Port for tests. Zero value is usable: no panes,
// no errors.
type Fake struct {
	Panes       []domain.PaneID
	ListPanesFn func() ([]domain.PaneID, error)
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

// Ensure Fake satisfies Port at compile time.
var _ Port = (*Fake)(nil)
