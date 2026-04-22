package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/hsperker/tmux-pane-control/internal/controller"
)

// Server wraps a listening Unix socket and a Controller, dispatching
// each incoming connection's Request to the shared controller and
// writing back one Response per request.
type Server struct {
	Listener   net.Listener
	Controller *controller.Controller

	// Shutdown is called by the OpShutdown handler. Set by the daemon
	// main so a signal-style cancellation path propagates correctly.
	Shutdown func()

	mu      sync.Mutex
	closed  bool
	acceptW sync.WaitGroup
}

// Listen binds a Unix socket at path and returns a *Server ready to
// Serve. If the path already exists and does not belong to a running
// server, the stale socket is removed before binding. The caller is
// responsible for coordination against concurrent spawns (§11.5).
func Listen(path string) (net.Listener, error) {
	// Try to connect first to detect a live peer; a successful dial
	// means someone else already owns this socket.
	if c, err := net.Dial("unix", path); err == nil {
		c.Close()
		return nil, fmt.Errorf("daemon socket already bound: %s", path)
	}
	// Not alive — clean up stale file before bind.
	_ = os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		l.Close()
		return nil, err
	}
	return l, nil
}

// Serve accepts connections until ctx is cancelled or the listener
// closes. Each connection handles one request before closing.
func (s *Server) Serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		s.Close()
	}()
	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			s.mu.Lock()
			closed := s.closed
			s.mu.Unlock()
			if closed {
				s.acceptW.Wait()
				return nil
			}
			return err
		}
		s.acceptW.Add(1)
		go func() {
			defer s.acceptW.Done()
			s.handle(ctx, conn)
		}()
	}
}

// Close stops accepting, waits for in-flight handlers, and removes
// the socket file.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	s.mu.Unlock()
	err := s.Listener.Close()
	_ = os.Remove(s.Listener.Addr().String())
	return err
}

func (s *Server) handle(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil {
		return
	}
	var req Request
	if err := json.Unmarshal(line, &req); err != nil {
		writeResponse(conn, runtimeErr(fmt.Errorf("malformed request: %w", err)))
		return
	}
	if req.Op == OpShutdown {
		writeResponse(conn, &Response{OK: true, Pid: os.Getpid()})
		if s.Shutdown != nil {
			go s.Shutdown()
		}
		return
	}
	resp := Dispatch(ctx, s.Controller, &req)
	writeResponse(conn, resp)
}

func writeResponse(w net.Conn, r *Response) {
	b, err := json.Marshal(r)
	if err != nil {
		b, _ = json.Marshal(runtimeErr(err))
	}
	b = append(b, '\n')
	_, _ = w.Write(b)
}

// ErrServerClosed is returned by Serve after the server has shut down.
var ErrServerClosed = errors.New("ipc: server closed")
