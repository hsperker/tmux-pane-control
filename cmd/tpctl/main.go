package main

import (
	"fmt"
	"io"
	"os"
)

const usage = `tpctl - agent-driven tmux pane control

Usage:
  tpctl list
  tpctl snapshot --pane %ID [--history-lines N]
  tpctl read --pane %ID --after TOKEN
  tpctl text --pane %ID TEXT [--enter]
  tpctl key --pane %ID KEY [KEY...]
  tpctl wait --pane %ID --after TOKEN --for MODE ... --timeout-ms N
  tpctl daemon

Flags:
  --help       Show this help and exit.

See docs/specs/tpctl-v1.md for the full specification.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "--help", "-h", "help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "tpctl: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
