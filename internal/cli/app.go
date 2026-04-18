// Package cli is the process-entry layer for tpctl: it parses flags,
// dispatches to handlers, and owns stdout/stderr behavior per spec §7.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/hsperker/tmux-pane-control/internal/controller"
	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/tmuxctl"
)

// Usage is printed by --help and on missing/unknown commands.
const Usage = `tpctl - agent-driven tmux pane control

Usage:
  tpctl list
  tpctl snapshot --pane %ID [--history-lines N]
  tpctl read --pane %ID --after TOKEN
  tpctl text --pane %ID TEXT [--enter]
  tpctl key --pane %ID KEY [KEY...]
  tpctl wait --pane %ID --after TOKEN --for MODE ... --timeout-ms N
  tpctl daemon

Global flags:
  --tmux-socket PATH       tmux -S socket path
  --tmux-socket-name NAME  tmux -L socket shortname
  --help                   Show this help and exit

See docs/specs/tpctl-v1.md for the full specification.
`

// App holds the streams the CLI writes to. Injecting them makes the
// binary testable end-to-end.
type App struct {
	Stdout io.Writer
	Stderr io.Writer
}

// Run dispatches args[1:]-style arguments (i.e. without argv[0]).
// Return is the process exit code.
func (a *App) Run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(a.Stderr, Usage)
		return 2
	}
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Fprint(a.Stdout, Usage)
		return 0
	case "list":
		return a.runList(args[1:])
	case "snapshot":
		return a.runSnapshot(args[1:])
	case "read":
		return a.runRead(args[1:])
	default:
		fmt.Fprintf(a.Stderr, "tpctl: unknown command %q\n\n%s", args[0], Usage)
		return 2
	}
}

// globalFlags defines flags accepted by every subcommand. Subcommands
// call this helper so the set stays consistent.
type globalFlags struct {
	socketPath string
	socketName string
}

func (g *globalFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&g.socketPath, "tmux-socket", "", "tmux -S socket path")
	fs.StringVar(&g.socketName, "tmux-socket-name", "", "tmux -L socket shortname")
}

// portOrError validates the global flags and returns either a tmuxctl
// port or a command-level error. The caller decides how to surface it.
func (g *globalFlags) portOrError() (tmuxctl.Port, *domain.ErrorResponse) {
	if g.socketPath != "" && g.socketName != "" {
		return nil, &domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "--tmux-socket and --tmux-socket-name are mutually exclusive",
		}
	}
	return tmuxctl.NewAdapter(tmuxctl.Opts{
		SocketPath: g.socketPath,
		SocketName: g.socketName,
	}), nil
}

// startController builds a Controller wrapping the resolved port,
// starts its subscription, and returns it plus a teardown function.
// On subscription failure, a runtime error is returned instead.
func (g *globalFlags) startController(ctx context.Context) (*controller.Controller, func(), *domain.ErrorResponse, error) {
	port, cerr := g.portOrError()
	if cerr != nil {
		return nil, func() {}, cerr, nil
	}
	c := controller.New(port)
	if err := c.Start(ctx); err != nil {
		return nil, func() {}, nil, err
	}
	return c, c.Stop, nil, nil
}

// requirePaneFlag registers --pane on fs and returns a pointer to the
// parsed value. It does not validate format; that happens post-parse.
func registerPaneFlag(fs *flag.FlagSet) *string {
	return fs.String("pane", "", "target pane id, e.g. %42")
}

// validatePaneID rejects empty or non-"%"-prefixed pane identifiers.
// Spec §5 mandates tmux pane ids as the sole identity.
func validatePaneID(p string) *domain.ErrorResponse {
	if p == "" {
		return &domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "--pane is required",
		}
	}
	if !strings.HasPrefix(p, "%") {
		return &domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "--pane must be a tmux pane id (e.g. %42)",
		}
	}
	return nil
}

func (a *App) runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return a.emitCmdError(&domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "list takes no positional arguments",
		})
	}
	port, cerr := g.portOrError()
	if cerr != nil {
		return a.emitCmdError(cerr)
	}
	// list does not need an active subscription; the Port alone
	// suffices. Calling the handler directly avoids the cost of
	// starting up tracker goroutines.
	resp, err := controller.List(port)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl list: %v\n", err)
		return 1
	}
	return a.emitJSON(resp)
}

func (a *App) runSnapshot(args []string) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	pane := registerPaneFlag(fs)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return a.emitCmdError(&domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "snapshot takes no positional arguments",
		})
	}
	if e := validatePaneID(*pane); e != nil {
		return a.emitCmdError(e)
	}
	port, cerr := g.portOrError()
	if cerr != nil {
		return a.emitCmdError(cerr)
	}

	// Snapshot in slice 8 still runs with a per-invocation
	// controller; slice 14 makes the controller long-lived so tokens
	// survive across CLI calls.
	_ = port // unused outside of startController, kept for symmetry
	ctrl, stop, cerr2, rerr := g.startController(context.Background())
	if cerr2 != nil {
		return a.emitCmdError(cerr2)
	}
	if rerr != nil {
		fmt.Fprintf(a.Stderr, "tpctl snapshot: %v\n", rerr)
		return 1
	}
	defer stop()
	resp, err := ctrl.Snapshot(domain.PaneID(*pane))
	if err != nil {
		var ce *domain.ErrorResponse
		if errors.As(err, &ce) {
			return a.emitCmdError(ce)
		}
		fmt.Fprintf(a.Stderr, "tpctl snapshot: %v\n", err)
		return 1
	}
	return a.emitJSON(resp)
}

func (a *App) runRead(args []string) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	pane := registerPaneFlag(fs)
	after := fs.String("after", "", "checkpoint token from a prior snapshot or read")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		return a.emitCmdError(&domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "read takes no positional arguments",
		})
	}
	if e := validatePaneID(*pane); e != nil {
		return a.emitCmdError(e)
	}
	if *after == "" {
		// Spec §9.3: missing --after is MISSING_AFTER.
		return a.emitCmdError(&domain.ErrorResponse{
			PaneID:  *pane,
			Code:    domain.ErrMissingAfter,
			Message: "read requires --after; use snapshot to bootstrap",
		})
	}
	ctrl, stop, cerr2, rerr := g.startController(context.Background())
	if cerr2 != nil {
		return a.emitCmdError(cerr2)
	}
	if rerr != nil {
		fmt.Fprintf(a.Stderr, "tpctl read: %v\n", rerr)
		return 1
	}
	defer stop()
	resp, err := ctrl.Read(domain.PaneID(*pane), domain.Token(*after))
	if err != nil {
		var ce *domain.ErrorResponse
		if errors.As(err, &ce) {
			return a.emitCmdError(ce)
		}
		fmt.Fprintf(a.Stderr, "tpctl read: %v\n", err)
		return 1
	}
	return a.emitJSON(resp)
}

// emitJSON writes a compact JSON payload followed by a newline to
// stdout and returns 0.
func (a *App) emitJSON(v any) int {
	b, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl: marshal: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Stdout, string(b))
	return 0
}

// emitCmdError writes a command-level error (spec §7.2) as JSON on
// stdout and returns a nonzero exit code.
func (a *App) emitCmdError(e *domain.ErrorResponse) int {
	b, err := json.Marshal(e)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl: marshal: %v\n", err)
		return 1
	}
	fmt.Fprintln(a.Stdout, string(b))
	return 1
}
