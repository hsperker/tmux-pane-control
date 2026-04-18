//go:build unix

package tmuxctl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
)

// Subscribe emits PaneOutput events for every byte appended to any
// pane. The implementation polls list-panes periodically and attaches
// `tmux pipe-pane` with a FIFO per pane. New panes are picked up on
// the next poll; destroyed panes have their trackers torn down.
func (a *Adapter) Subscribe(ctx context.Context) (<-chan PaneOutput, error) {
	out := make(chan PaneOutput, 64)
	s := &subscription{
		adapter:  a,
		out:      out,
		trackers: map[domain.PaneID]*paneTracker{},
	}
	go s.run(ctx)
	return out, nil
}

// subPollInterval is how often Subscribe refreshes its pane set from
// tmux. A short interval keeps lifecycle changes tight; the overhead
// is one `tmux list-panes` call.
const subPollInterval = 200 * time.Millisecond

// serverLostThreshold is the number of consecutive list-panes
// failures that triggers a ServerLost event (spec §11.9). At the
// 200ms poll interval, 3 failures ≈ 600ms of persistent failure —
// long enough to ignore transient blips, short enough that callers
// don't wait forever. This is a heuristic; see §11.9 non-normative
// notes in the plan retrospective.
const serverLostThreshold = 3

type subscription struct {
	adapter  *Adapter
	out      chan PaneOutput
	mu       sync.Mutex
	trackers map[domain.PaneID]*paneTracker
	wg       sync.WaitGroup

	// Detection state for spec §11.9 tmux server loss.
	consecutiveListFailures int
	lostNotified            bool
}

func (s *subscription) run(ctx context.Context) {
	defer close(s.out)
	s.sync(ctx)
	t := time.NewTicker(subPollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.teardown()
			s.wg.Wait()
			return
		case <-t.C:
			s.sync(ctx)
		}
	}
}

// sync diffs tmux's current pane set against the tracked set and
// starts/stops trackers as needed. Persistent list-panes failures
// are interpreted as tmux server loss per spec §11.9 and surfaced
// as a one-shot ServerLost event.
func (s *subscription) sync(ctx context.Context) {
	panes, err := s.adapter.ListPanes()
	if err != nil {
		s.consecutiveListFailures++
		if s.consecutiveListFailures >= serverLostThreshold && !s.lostNotified {
			s.lostNotified = true
			select {
			case s.out <- PaneOutput{ServerLost: true}:
			case <-ctx.Done():
			}
		}
		return
	}
	s.consecutiveListFailures = 0
	// lostNotified stays sticky: once we've told the controller the
	// server is gone, we do not retract that signal. The controller
	// is expected to be shutting down in response.
	seen := make(map[domain.PaneID]struct{}, len(panes))
	for _, p := range panes {
		seen[p] = struct{}{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range seen {
		if _, ok := s.trackers[p]; ok {
			continue
		}
		t, err := startPaneTracker(ctx, s.adapter, p, s.out, &s.wg)
		if err != nil {
			continue
		}
		s.trackers[p] = t
	}
	for p, t := range s.trackers {
		if _, ok := seen[p]; !ok {
			t.stop(s.adapter)
			delete(s.trackers, p)
			// Publish the closure so listeners (Controller → Store
			// → waiters) can react per spec §11.9.
			select {
			case s.out <- PaneOutput{ID: p, Closed: true}:
			case <-ctx.Done():
				return
			}
		}
	}
}

func (s *subscription) teardown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p, t := range s.trackers {
		t.stop(s.adapter)
		delete(s.trackers, p)
	}
}

// paneTracker owns the FIFO + reader goroutine for a single pane.
type paneTracker struct {
	pane     domain.PaneID
	dir      string // temp dir holding the fifo
	fifoPath string
	file     *os.File
}

func startPaneTracker(ctx context.Context, a *Adapter, pane domain.PaneID, out chan<- PaneOutput, wg *sync.WaitGroup) (*paneTracker, error) {
	dir, err := os.MkdirTemp("", "tpctl-pipe-*")
	if err != nil {
		return nil, err
	}
	fifoPath := filepath.Join(dir, "p")
	if err := syscall.Mkfifo(fifoPath, 0o600); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	// Open the FIFO O_RDWR so the open itself does not block waiting
	// for a writer — we'll Read from it once tmux's pipe-pane opens
	// the write end.
	f, err := os.OpenFile(fifoPath, os.O_RDWR, 0)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}

	// Ask tmux to copy pane output into the FIFO. Pass the FIFO path
	// through argv so shell quoting stays simple.
	if err := a.cmd("pipe-pane", "-t", string(pane),
		"exec cat >"+shellQuote(fifoPath)).Run(); err != nil {
		f.Close()
		os.RemoveAll(dir)
		return nil, err
	}

	t := &paneTracker{pane: pane, dir: dir, fifoPath: fifoPath, file: f}
	wg.Add(1)
	go t.read(ctx, out, wg)
	return t, nil
}

func (t *paneTracker) read(ctx context.Context, out chan<- PaneOutput, wg *sync.WaitGroup) {
	defer wg.Done()
	defer os.RemoveAll(t.dir)
	defer t.file.Close()

	buf := make([]byte, 4096)
	for {
		n, err := t.file.Read(buf)
		if n > 0 {
			data := make([]byte, n)
			copy(data, buf[:n])
			select {
			case out <- PaneOutput{ID: t.pane, Data: data}:
			case <-ctx.Done():
				return
			}
		}
		if err != nil {
			if errors.Is(err, os.ErrClosed) || errors.Is(err, syscall.EBADF) {
				return
			}
			// EOF only happens when all writers (tmux's pipe-pane
			// subprocess) are closed. Since we also hold O_RDWR on
			// the FIFO, we won't see EOF until Close() is called on
			// this file descriptor; a read after close returns
			// os.ErrClosed above.
			return
		}
	}
}

func (t *paneTracker) stop(a *Adapter) {
	// Tell tmux to stop piping this pane. This is best-effort; if
	// the pane is already gone, tmux returns an error which we
	// ignore.
	_ = a.cmd("pipe-pane", "-t", string(t.pane)).Run()
	t.file.Close()
}

// shellQuote wraps s in single quotes, escaping any embedded quotes.
// Used because tmux passes the shell-command through the user's
// default shell; we want the FIFO path to be literal.
func shellQuote(s string) string {
	b := make([]byte, 0, len(s)+2)
	b = append(b, '\'')
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			b = append(b, '\'', '\\', '\'', '\'')
			continue
		}
		b = append(b, s[i])
	}
	b = append(b, '\'')
	return string(b)
}
