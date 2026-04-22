package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/controller"
	"github.com/hsperker/tmux-pane-control/internal/ipc"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// shutdownDeadline caps controller teardown after the IPC listener
// has closed. If Stop does not return in this window, the daemon
// self-terminates with a nonzero exit rather than leaving a zombie
// process around (see `t.stop` timeouts in tmuxctl/subscribe_unix.go
// for the expected-case bound; this deadline is the backstop).
const shutdownDeadline = 2 * time.Second

// hardExitDeadline is a belt-and-suspenders timer armed the instant
// a shutdown is requested (SIGINT/SIGTERM, tmux server loss, or
// OpShutdown). If the daemon has not exited on its own by then,
// os.Exit(1) forces termination. This guarantees a bounded worst
// case independent of any teardown-path bug that might keep the
// process resident after the listener has already been released.
const hardExitDeadline = 3 * time.Second

// runDaemon is the `tpctl daemon` subcommand (spec §11.3). It
// resolves the tmux socket, derives the daemon socket (unless
// provided), binds it, starts the Controller, and serves requests
// until SIGINT/SIGTERM. With --stop it instead connects to a
// running daemon, asks it to exit, and waits for the socket to
// disappear.
func (a *App) runDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	daemonSocket := fs.String("daemon-socket", "",
		"override daemon socket path (default: derived from tmux socket)")
	stop := fs.Bool("stop", false,
		"stop the running daemon for this tmux server and exit")
	a.attachSubcommandHelp(fs,
		"daemon",
		"Run (or --stop) the controller. Auto-spawn starts the daemon on demand; --stop shuts it down cleanly without needing pkill.",
		"tpctl daemon --tmux-socket /tmp/tmux.sock\n  tpctl daemon --stop",
		"§11.3")
	bools := map[string]bool{"stop": true}
	for k, v := range boolFlagsCommon {
		bools[k] = v
	}
	if err := fs.Parse(reorderArgs(args, bools)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	tmuxSocket, err := ipc.ResolveTmuxSocket(g.socketPath, g.socketName)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl daemon: %v\n", err)
		return 2
	}
	sockPath := *daemonSocket
	if sockPath == "" {
		sockPath, err = ipc.DaemonSocketPath(tmuxSocket)
		if err != nil {
			fmt.Fprintf(a.Stderr, "tpctl daemon: %v\n", err)
			return 1
		}
	}
	if *stop {
		return a.runDaemonStop(sockPath)
	}

	ln, err := ipc.Listen(sockPath)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl daemon: %v\n", err)
		return 1
	}

	port := tmuxctl.NewAdapter(tmuxctl.Opts{SocketPath: tmuxSocket})
	ctrl := controller.New(port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := ctrl.Start(ctx); err != nil {
		fmt.Fprintf(a.Stderr, "tpctl daemon: controller start: %v\n", err)
		ln.Close()
		return 1
	}

	// requestShutdown is the single shutdown entry point. It arms the
	// hard-exit timer (so the process is guaranteed to die within
	// hardExitDeadline even if teardown wedges) and then cancels the
	// daemon ctx. Idempotent: time.AfterFunc + Once lets callers hit
	// this from OpShutdown, signals, and tmux server loss without
	// racing multiple hard-exit timers.
	var shutdownOnce sync.Once
	requestShutdown := func() {
		shutdownOnce.Do(func() {
			time.AfterFunc(hardExitDeadline, func() {
				fmt.Fprintln(a.Stderr, "tpctl daemon: hard exit deadline; terminating")
				os.Exit(1)
			})
		})
		cancel()
	}

	srv := &ipc.Server{
		Listener:   ln,
		Controller: ctrl,
		Shutdown:   requestShutdown,
	}

	// Propagate SIGINT/SIGTERM and controller-observed tmux server
	// loss (spec §11.9 Exit mode) into ctx so Serve unwinds.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	serverLost := false
	go func() {
		select {
		case <-sigCh:
		case <-ctrl.ServerLost():
			serverLost = true
			fmt.Fprintln(a.Stderr, "tpctl daemon: tmux server connection lost; exiting")
		}
		requestShutdown()
	}()

	if err := srv.Serve(ctx); err != nil && err != ipc.ErrServerClosed {
		fmt.Fprintf(a.Stderr, "tpctl daemon: serve: %v\n", err)
		stopWithDeadline(ctrl, a.Stderr)
		return 1
	}
	stopWithDeadline(ctrl, a.Stderr)
	if serverLost {
		// Runtime failure per §7.3: nonzero exit so supervisors and
		// the next CLI invocation's auto-spawn know a fresh
		// controller is needed.
		return 1
	}
	return 0
}

// stopWithDeadline calls ctrl.Stop() with a hard deadline. If the
// controller's subscription teardown doesn't complete in time (e.g.
// a hung `tmux pipe-pane` that somehow escaped the per-command
// timeout), the daemon exits directly rather than leaving a zombie
// process holding stale state.
func stopWithDeadline(ctrl *controller.Controller, stderr io.Writer) {
	done := make(chan struct{})
	go func() {
		ctrl.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(shutdownDeadline):
		fmt.Fprintf(stderr, "tpctl daemon: controller stop exceeded %s; exiting\n", shutdownDeadline)
		os.Exit(1)
	}
}

// daemonStopAckTimeout caps the OpShutdown round-trip. The daemon
// replies before starting its teardown, so this only bounds dial +
// write + read and does not depend on how long controller shutdown
// takes.
const daemonStopAckTimeout = 2 * time.Second

// daemonStopExitTimeout is how long to wait for the daemon to
// release its socket after acking shutdown. Slightly longer than
// hardExitDeadline so a daemon that needs the hard-exit backstop
// still reports "stopped cleanly" rather than a false negative.
const daemonStopExitTimeout = hardExitDeadline + 2*time.Second

// runDaemonStop asks the daemon at sockPath to shut down cleanly and
// waits for its process to exit. Exit codes:
//
//	0 — daemon was not running, or stopped cleanly (socket gone AND
//	    process exited).
//	1 — the daemon acked OpShutdown but the socket or process did
//	    not disappear within daemonStopExitTimeout. Caller should
//	    escalate to SIGKILL (`pkill -9 tpctl`).
//
// Checking the socket alone is not enough: a daemon can unlink its
// socket during shutdown while the process itself stays resident on
// a hung teardown path. The OpShutdown response carries the daemon
// PID so we can check process liveness via kill(pid, 0) too.
func (a *App) runDaemonStop(sockPath string) int {
	client := ipc.NewClient(sockPath)
	if err := client.Ping(); err != nil {
		fmt.Fprintln(a.Stderr, "tpctl daemon --stop: no daemon running")
		return 0
	}

	resp, err := client.Call(&ipc.Request{Op: ipc.OpShutdown}, daemonStopAckTimeout)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl daemon --stop: shutdown request failed: %v\n", err)
		return 1
	}
	pid := resp.Pid

	deadline := time.Now().Add(daemonStopExitTimeout)
	sockGone := false
	processGone := pid <= 0 // no PID reported → fall back to socket-only
	for time.Now().Before(deadline) {
		if !sockGone {
			if err := client.Ping(); err != nil {
				sockGone = true
			}
		}
		if !processGone && pid > 0 {
			if err := syscall.Kill(pid, 0); err != nil {
				processGone = true
			}
		}
		if sockGone && processGone {
			return 0
		}
		time.Sleep(25 * time.Millisecond)
	}

	switch {
	case !sockGone:
		fmt.Fprintf(a.Stderr,
			"tpctl daemon --stop: daemon still reachable on socket after %s; send SIGKILL if it is stuck\n",
			daemonStopExitTimeout)
	case !processGone:
		fmt.Fprintf(a.Stderr,
			"tpctl daemon --stop: socket released but daemon PID %d still alive after %s; send SIGKILL (`kill -9 %d`)\n",
			pid, daemonStopExitTimeout, pid)
	}
	return 1
}
