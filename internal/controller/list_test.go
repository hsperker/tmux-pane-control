package controller

import (
	"errors"
	"reflect"
	"testing"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

func TestList_Empty(t *testing.T) {
	resp, err := List(&tmuxctl.Fake{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if resp == nil || resp.Panes == nil {
		t.Fatalf("panes must be non-nil empty slice, got %+v", resp)
	}
	if len(resp.Panes) != 0 {
		t.Fatalf("want empty, got %v", resp.Panes)
	}
}

func TestList_Populated(t *testing.T) {
	want := []domain.PaneID{"%42", "%43", "%44"}
	resp, err := List(&tmuxctl.Fake{Panes: want})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !reflect.DeepEqual(resp.Panes, want) {
		t.Fatalf("got %v, want %v", resp.Panes, want)
	}
}

func TestList_PortError(t *testing.T) {
	boom := errors.New("boom")
	_, err := List(&tmuxctl.Fake{ListPanesFn: func() ([]domain.PaneID, error) {
		return nil, boom
	}})
	if !errors.Is(err, boom) {
		t.Fatalf("want boom, got %v", err)
	}
}
