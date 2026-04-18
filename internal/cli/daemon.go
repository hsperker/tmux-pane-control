package cli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/hsperker/tmux-pane-control/internal/controller"
	"github.com/hsperker/tmux-pane-control/internal/ipc"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// runDaemon is the `tpctl daemon` subcommand (spec §11.3). It
// resolves the tmux socket, derives the daemon socket (unless
// provided), binds it, starts the Controller, and serves requests
// until SIGINT/SIGTERM.
func (a *App) runDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	daemonSocket := fs.String("daemon-socket", "",
		"override daemon socket path (default: derived from tmux socket)")
	if err := fs.Parse(reorderArgs(args, boolFlagsCommon)); err != nil {
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

	srv := &ipc.Server{
		Listener:   ln,
		Controller: ctrl,
		Shutdown:   cancel,
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
		cancel()
	}()

	if err := srv.Serve(ctx); err != nil && err != ipc.ErrServerClosed {
		fmt.Fprintf(a.Stderr, "tpctl daemon: serve: %v\n", err)
		ctrl.Stop()
		return 1
	}
	ctrl.Stop()
	if serverLost {
		// Runtime failure per §7.3: nonzero exit so supervisors and
		// the next CLI invocation's auto-spawn know a fresh
		// controller is needed.
		return 1
	}
	return 0
}
