package controller

import (
	"context"
	"sync"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/store"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// Controller is the in-process owner of the Store and the tmux
// subscription. It is the single-writer event loop (spec §10). In
// slice 14 this same type is fronted by an IPC boundary; the Go API
// stays the same.
type Controller struct {
	port  tmuxctl.Port
	store *store.Store

	startOnce sync.Once
	startErr  error
	cancel    context.CancelFunc
	done      chan struct{}
}

// New builds a controller with the spec-default per-pane retention
// (1 MiB). Tests can use NewWithCapacity to exercise eviction.
func New(port tmuxctl.Port) *Controller {
	return NewWithCapacity(port, store.DefaultCapacity)
}

// NewWithCapacity lets tests construct a controller with a small
// ring-buffer capacity, making eviction cheap to trigger.
func NewWithCapacity(port tmuxctl.Port, capacity int) *Controller {
	return &Controller{port: port, store: store.New(capacity)}
}

// Store exposes the underlying store for diagnostics and tests.
func (c *Controller) Store() *store.Store { return c.store }

// Start subscribes to pane output and begins draining it into the
// store. It returns only once the subscription is established.
// Subsequent calls are no-ops.
func (c *Controller) Start(ctx context.Context) error {
	c.startOnce.Do(func() {
		subCtx, cancel := context.WithCancel(ctx)
		c.cancel = cancel
		ch, err := c.port.Subscribe(subCtx)
		if err != nil {
			cancel()
			c.startErr = err
			return
		}
		c.done = make(chan struct{})
		go c.drain(ch)
	})
	return c.startErr
}

// drain copies subscription events into the store. It exits when the
// subscription channel closes (on ctx cancellation or adapter teardown).
func (c *Controller) drain(ch <-chan tmuxctl.PaneOutput) {
	defer close(c.done)
	for ev := range ch {
		c.store.Append(ev.ID, ev.Data)
	}
}

// Stop cancels the subscription and waits for cleanup.
func (c *Controller) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
	if c.done != nil {
		<-c.done
	}
}

// List implements `tpctl list` (spec §9.1).
func (c *Controller) List() (*domain.ListResponse, error) {
	return List(c.port)
}

// Snapshot implements `tpctl snapshot` (spec §9.2).
func (c *Controller) Snapshot(id domain.PaneID) (*domain.SnapshotResponse, error) {
	return Snapshot(c.port, c.store, id)
}

// Read implements `tpctl read` (spec §9.3).
func (c *Controller) Read(id domain.PaneID, after domain.Token) (*domain.ReadResponse, error) {
	return Read(c.store, id, after)
}
