// Package cli is the process-entry layer for tpctl: it parses flags,
// dispatches to handlers, and owns stdout/stderr behavior per spec §7.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"

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
	resp, err := controller.List(port)
	if err != nil {
		// Runtime failure (spec §7.3): stderr, nonzero exit.
		fmt.Fprintf(a.Stderr, "tpctl list: %v\n", err)
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
