// Binary tpctl is the command-line interface for tmux-pane-control.
// Everything lives in internal/cli; this file is a thin entrypoint.
// See docs/specs/tpctl-v1.md for the normative contract.
package main

import (
	"os"

	"github.com/hsperker/tmux-pane-control/internal/cli"
)

func main() {
	app := &cli.App{Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(app.Run(os.Args[1:]))
}
