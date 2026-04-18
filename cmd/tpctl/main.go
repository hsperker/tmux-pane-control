package main

import (
	"os"

	"github.com/hsperker/tmux-pane-control/internal/cli"
)

func main() {
	app := &cli.App{Stdout: os.Stdout, Stderr: os.Stderr}
	os.Exit(app.Run(os.Args[1:]))
}
