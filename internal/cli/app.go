// Package cli is the process-entry layer for tpctl: it parses flags,
// dispatches to handlers, and owns stdout/stderr behavior per spec §7.
package cli

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/hsperker/tmux-pane-control/internal/domain"
	"github.com/hsperker/tmux-pane-control/internal/ipc"
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
  --help                   Show this help and exit (per-subcommand: tpctl <sub> --help)
  --version                Print version and exit (also: tpctl version)

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
//
// Global flags (--tmux-socket, --tmux-socket-name, --help) may appear
// before OR after the subcommand per spec §6.2. extractGlobals hoists
// any that appear BEFORE the subcommand out of args, then reinjects
// them at the front of the subcommand's own argv so its FlagSet can
// parse them normally. Flags that appear AFTER the subcommand are
// left in place and handled the same way.
func (a *App) Run(args []string) int {
	globals, helpSeen, versionSeen, remaining := extractGlobals(args)

	if versionSeen {
		fmt.Fprintln(a.Stdout, versionString())
		return 0
	}
	if helpSeen {
		fmt.Fprint(a.Stdout, Usage)
		return 0
	}
	if len(remaining) == 0 {
		fmt.Fprint(a.Stderr, Usage)
		return 2
	}

	// Explicit "help" / "version" / "-h" subcommand (not handled by
	// extractGlobals because they don't start with "--").
	switch remaining[0] {
	case "-h", "help":
		fmt.Fprint(a.Stdout, Usage)
		return 0
	case "version":
		fmt.Fprintln(a.Stdout, versionString())
		return 0
	}

	// Reinject globals at the front of the subcommand's argv so the
	// subcommand's FlagSet picks them up.
	subArgs := make([]string, 0, len(globals)+len(remaining)-1)
	subArgs = append(subArgs, globals...)
	subArgs = append(subArgs, remaining[1:]...)

	switch remaining[0] {
	case "list":
		return a.runList(subArgs)
	case "snapshot":
		return a.runSnapshot(subArgs)
	case "read":
		return a.runRead(subArgs)
	case "text":
		return a.runText(subArgs)
	case "key":
		return a.runKey(subArgs)
	case "wait":
		return a.runWait(subArgs)
	case "daemon":
		return a.runDaemon(subArgs)
	default:
		fmt.Fprintf(a.Stderr, "tpctl: unknown command %q\n\n%s", remaining[0], Usage)
		return 2
	}
}

// extractGlobals walks args from the start and pulls out any global
// flags (--tmux-socket VALUE, --tmux-socket=VALUE, --tmux-socket-name
// VALUE, --tmux-socket-name=VALUE, --help, -h, --version) that appear
// before the subcommand. The first argument that is not a recognized
// global flag ends extraction and is treated as the subcommand; the
// "--" sentinel also ends extraction (and is preserved in remaining).
//
// Returns:
//   - globals:     the global-flag tokens in order, ready to reinject
//   - helpSeen:    true if --help or -h appeared before the subcommand
//   - versionSeen: true if --version appeared before the subcommand
//   - remaining:   args starting at the subcommand (empty when no subcommand)
func extractGlobals(args []string) (globals []string, helpSeen, versionSeen bool, remaining []string) {
	i := 0
	for i < len(args) {
		a := args[i]
		if a == "--" {
			break
		}
		name := ""
		hasEq := false
		switch {
		case strings.HasPrefix(a, "--") && len(a) > 2:
			name = a[2:]
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				name = name[:eq]
				hasEq = true
			}
		case a == "-h":
			name = "h"
		default:
			// Not a flag form we recognize; treat as the subcommand.
			return globals, helpSeen, versionSeen, args[i:]
		}

		switch name {
		case "help", "h":
			helpSeen = true
			i++
		case "version":
			versionSeen = true
			i++
		case "tmux-socket", "tmux-socket-name":
			globals = append(globals, a)
			if hasEq {
				i++
				continue
			}
			if i+1 >= len(args) {
				// Missing value — hand the lonely flag to the
				// subcommand's FlagSet so it can produce the
				// "flag needs an argument" diagnostic.
				i++
				continue
			}
			globals = append(globals, args[i+1])
			i += 2
		default:
			// Unknown long flag before the subcommand — not a global;
			// hand off to the subcommand handler, which will reject it.
			return globals, helpSeen, versionSeen, args[i:]
		}
	}
	return globals, helpSeen, versionSeen, args[i:]
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

// requirePaneFlag registers --pane on fs and returns a pointer to the
// parsed value. It does not validate format; that happens post-parse.
func registerPaneFlag(fs *flag.FlagSet) *string {
	return fs.String("pane", "", "target pane id, e.g. %42")
}

// reorderArgs promotes all flags ahead of positionals so Go's flag
// package (which stops at the first non-flag) accepts the POSIX
// interleaved style of spec §9 examples, e.g.
//
//	tpctl text --pane %42 "payload" --enter
//
// boolFlags must name every flag that does NOT take a value so we
// can tell whether the next token is a value or a positional.
// A literal "--" argument ends option parsing; everything after is
// positional.
func reorderArgs(args []string, boolFlags map[string]bool) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if len(a) > 2 && a[:2] == "--" {
			name := a[2:]
			if eq := strings.IndexByte(name, '='); eq >= 0 {
				flags = append(flags, a)
				continue
			}
			flags = append(flags, a)
			if !boolFlags[name] && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		if len(a) > 1 && a[0] == '-' && a[1] != '-' {
			flags = append(flags, a)
			continue
		}
		positionals = append(positionals, a)
	}
	out := make([]string, 0, len(flags)+len(positionals)+1)
	out = append(out, flags...)
	out = append(out, "--")
	out = append(out, positionals...)
	return out
}

// boolFlagsCommon lists the boolean flags recognized by every
// subcommand. Subcommand-specific bool flags are merged in at call
// sites.
var boolFlagsCommon = map[string]bool{
	"help": true,
	"h":    true,
}

// attachSubcommandHelp wires a per-subcommand usage message onto
// fs. When the caller passes --help (or -h), Go's flag package
// invokes fs.Usage and returns flag.ErrHelp from Parse; the
// subcommand handler should detect that and return 0.
//
// usage is the argument pattern (what follows `tpctl ` in the
// `Usage:` banner). description is a one-line summary. example is
// a concrete invocation. specRef is the spec section pointer (e.g.
// "§9.4"); empty skips it.
func (a *App) attachSubcommandHelp(fs *flag.FlagSet, usage, description, example, specRef string) {
	fs.Usage = func() {
		var b strings.Builder
		fmt.Fprintf(&b, "Usage: tpctl %s\n\n", usage)
		if description != "" {
			fmt.Fprintf(&b, "%s\n\n", description)
		}
		fmt.Fprintln(&b, "Flags:")
		oldOut := fs.Output()
		fs.SetOutput(&b)
		fs.PrintDefaults()
		fs.SetOutput(oldOut)
		if example != "" {
			fmt.Fprintf(&b, "\nExample:\n  %s\n", example)
		}
		if specRef != "" {
			fmt.Fprintf(&b, "\nSee docs/specs/tpctl-v1.md %s for the full contract.\n", specRef)
		}
		fmt.Fprint(a.Stdout, b.String())
	}
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

// dial returns an IPC client for the tmux server identified by g,
// auto-spawning a daemon if one isn't already running (spec §11.2).
func (g *globalFlags) dial() (*ipc.Client, *domain.ErrorResponse, error) {
	tmuxSocket, err := ipc.ResolveTmuxSocket(g.socketPath, g.socketName)
	if err != nil {
		return nil, &domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: err.Error(),
		}, nil
	}
	client, err := ipc.EnsureDaemon(tmuxSocket)
	if err != nil {
		return nil, nil, err
	}
	return client, nil, nil
}

// relay takes an ipc Response and emits it to the CLI's streams per
// spec §7. Returns the process exit code.
func (a *App) relay(resp *ipc.Response) int {
	if resp == nil {
		fmt.Fprintln(a.Stderr, "tpctl: empty response")
		return 1
	}
	if resp.OK {
		if len(resp.Body) > 0 {
			fmt.Fprintln(a.Stdout, string(resp.Body))
		}
		return 0
	}
	if resp.Error != nil {
		return a.emitCmdError(resp.Error)
	}
	if resp.Runtime != "" {
		fmt.Fprintf(a.Stderr, "tpctl: %s\n", resp.Runtime)
	}
	return 1
}

func (a *App) runList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	a.attachSubcommandHelp(fs,
		"list",
		"Enumerate tmux pane ids on the target server.",
		"tpctl list",
		"§9.1")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		return a.emitCmdError(&domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "list takes no positional arguments",
		})
	}
	client, cerr, rerr := g.dial()
	if cerr != nil {
		return a.emitCmdError(cerr)
	}
	if rerr != nil {
		fmt.Fprintf(a.Stderr, "tpctl list: %v\n", rerr)
		return 1
	}
	resp, err := client.Call(&ipc.Request{Op: ipc.OpList}, 10*time.Second)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl list: %v\n", err)
		return 1
	}
	return a.relay(resp)
}

func (a *App) runSnapshot(args []string) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	pane := registerPaneFlag(fs)
	historyLines := fs.Int("history-lines", -1,
		"include N lines of scrollback above the visible screen")
	a.attachSubcommandHelp(fs,
		"snapshot --pane %ID [--history-lines N]",
		"Capture the pane's visible screen and return a checkpoint token.",
		"tpctl snapshot --pane %42 --history-lines 40",
		"§9.2")
	if err := fs.Parse(reorderArgs(args, boolFlagsCommon)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
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
	client, cerr, rerr := g.dial()
	if cerr != nil {
		return a.emitCmdError(cerr)
	}
	if rerr != nil {
		fmt.Fprintf(a.Stderr, "tpctl snapshot: %v\n", rerr)
		return 1
	}
	req := &ipc.Request{Op: ipc.OpSnapshot, Pane: domain.PaneID(*pane)}
	if *historyLines >= 0 {
		req.HasHistory = true
		req.HistoryLines = *historyLines
	}
	resp, err := client.Call(req, 15*time.Second)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl snapshot: %v\n", err)
		return 1
	}
	return a.relay(resp)
}

func (a *App) runRead(args []string) int {
	fs := flag.NewFlagSet("read", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	pane := registerPaneFlag(fs)
	after := fs.String("after", "", "checkpoint token from a prior snapshot or read")
	a.attachSubcommandHelp(fs,
		"read --pane %ID --after TOKEN",
		"Return pane output appended after the given checkpoint token.",
		"tpctl read --pane %42 --after r_000205",
		"§9.3")
	if err := fs.Parse(reorderArgs(args, boolFlagsCommon)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
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
	// Note: MISSING_AFTER enforcement lives in the server so the
	// message is consistent whether called from local or remote.
	client, cerr, rerr := g.dial()
	if cerr != nil {
		return a.emitCmdError(cerr)
	}
	if rerr != nil {
		fmt.Fprintf(a.Stderr, "tpctl read: %v\n", rerr)
		return 1
	}
	resp, err := client.Call(&ipc.Request{
		Op:    ipc.OpRead,
		Pane:  domain.PaneID(*pane),
		After: domain.Token(*after),
	}, 10*time.Second)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl read: %v\n", err)
		return 1
	}
	return a.relay(resp)
}

// explainTextPositionalMismatch returns a diagnostic message for
// `tpctl text` when the caller passed != 1 positional. The common
// trap is `tpctl text --pane %N -- 'cmd' --enter`: the `--`
// sentinel makes --enter a positional, the call fails, and the
// generic "exactly one" error does not hint at the cause. When
// one of the extra positionals matches a known flag name, spell
// that out and point the user at the fix.
func explainTextPositionalMismatch(fs *flag.FlagSet) string {
	args := fs.Args()
	var flagsAmongPositionals []string
	for _, a := range args {
		if strings.HasPrefix(a, "--") || strings.HasPrefix(a, "-") {
			flagsAmongPositionals = append(flagsAmongPositionals, a)
		}
	}
	if len(args) == 0 {
		return "text takes exactly one positional (the payload), got 0"
	}
	if len(flagsAmongPositionals) > 0 {
		return fmt.Sprintf(
			"text takes exactly one positional, got %d: %s. "+
				"Flags-looking tokens (%s) after `--` are treated as positional; "+
				"put `--enter` before `--`, or drop `--` entirely.",
			len(args), formatQuotedList(args),
			formatQuotedList(flagsAmongPositionals),
		)
	}
	return fmt.Sprintf(
		"text takes exactly one positional, got %d: %s",
		len(args), formatQuotedList(args),
	)
}

func formatQuotedList(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		parts[i] = fmt.Sprintf("%q", a)
	}
	return strings.Join(parts, ", ")
}

func (a *App) runText(args []string) int {
	fs := flag.NewFlagSet("text", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	pane := registerPaneFlag(fs)
	enter := fs.Bool("enter", false, "append an Enter key press after the literal payload")
	bools := map[string]bool{"enter": true}
	for k, v := range boolFlagsCommon {
		bools[k] = v
	}
	a.attachSubcommandHelp(fs,
		"text --pane %ID TEXT [--enter]",
		"Send literal text to the pane. Exactly one positional payload; --enter appends an Enter keystroke.",
		`tpctl text --pane %42 "kubectl get pods" --enter`,
		"§9.4")
	if err := fs.Parse(reorderArgs(args, bools)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		return a.emitCmdError(&domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: explainTextPositionalMismatch(fs),
		})
	}
	payload := fs.Arg(0)
	if e := validatePaneID(*pane); e != nil {
		return a.emitCmdError(e)
	}
	client, cerr, rerr := g.dial()
	if cerr != nil {
		return a.emitCmdError(cerr)
	}
	if rerr != nil {
		fmt.Fprintf(a.Stderr, "tpctl text: %v\n", rerr)
		return 1
	}
	resp, err := client.Call(&ipc.Request{
		Op:    ipc.OpText,
		Pane:  domain.PaneID(*pane),
		Text:  payload,
		Enter: *enter,
	}, 10*time.Second)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl text: %v\n", err)
		return 1
	}
	return a.relay(resp)
}

func (a *App) runKey(args []string) int {
	fs := flag.NewFlagSet("key", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	pane := registerPaneFlag(fs)
	a.attachSubcommandHelp(fs,
		"key --pane %ID KEY [KEY...]",
		"Send one or more named keys (tmux send-keys vocabulary) to the pane.",
		`tpctl key --pane %42 Escape ":" "q" Enter`,
		"§9.5")
	if err := fs.Parse(reorderArgs(args, boolFlagsCommon)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	keys := fs.Args()
	if len(keys) == 0 {
		return a.emitCmdError(&domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "key requires at least one key token",
		})
	}
	if e := validatePaneID(*pane); e != nil {
		return a.emitCmdError(e)
	}
	client, cerr, rerr := g.dial()
	if cerr != nil {
		return a.emitCmdError(cerr)
	}
	if rerr != nil {
		fmt.Fprintf(a.Stderr, "tpctl key: %v\n", rerr)
		return 1
	}
	resp, err := client.Call(&ipc.Request{
		Op:   ipc.OpKey,
		Pane: domain.PaneID(*pane),
		Keys: keys,
	}, 10*time.Second)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl key: %v\n", err)
		return 1
	}
	return a.relay(resp)
}

func (a *App) runWait(args []string) int {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(a.Stderr)
	var g globalFlags
	g.register(fs)
	pane := registerPaneFlag(fs)
	after := fs.String("after", "", "checkpoint token from a prior snapshot or read")
	forMode := fs.String("for", "", "match mode: sentinel|regex|quiescence")
	timeoutMs := fs.Int("timeout-ms", 0, "wait timeout in milliseconds (required)")
	sentinelToken := fs.String("token", "", "sentinel mode: literal token to expect")
	pattern := fs.String("pattern", "", "regex mode: RE2 pattern")
	quietMs := fs.Int("ms", 0, "quiescence mode: required quiet window in ms")
	a.attachSubcommandHelp(fs,
		"wait --pane %ID --after TOKEN --for MODE ... --timeout-ms T",
		"Wait for pane output to match a condition. Modes: sentinel, regex, quiescence.",
		`tpctl wait --pane %42 --after r_000205 --for sentinel --token run1 --timeout-ms 5000`,
		"§9.6")
	if err := fs.Parse(reorderArgs(args, boolFlagsCommon)); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		return a.emitCmdError(&domain.ErrorResponse{
			Code:    domain.ErrInvalidArgs,
			Message: "wait takes no positional arguments",
		})
	}
	if e := validatePaneID(*pane); e != nil {
		return a.emitCmdError(e)
	}
	client, cerr, rerr := g.dial()
	if cerr != nil {
		return a.emitCmdError(cerr)
	}
	if rerr != nil {
		fmt.Fprintf(a.Stderr, "tpctl wait: %v\n", rerr)
		return 1
	}
	grace := 2 * time.Second
	total := time.Duration(*timeoutMs)*time.Millisecond + grace
	resp, err := client.Call(&ipc.Request{
		Op:            ipc.OpWait,
		Pane:          domain.PaneID(*pane),
		After:         domain.Token(*after),
		Mode:          *forMode,
		SentinelToken: *sentinelToken,
		Pattern:       *pattern,
		QuietMs:       *quietMs,
		TimeoutMs:     *timeoutMs,
	}, total)
	if err != nil {
		fmt.Fprintf(a.Stderr, "tpctl wait: %v\n", err)
		return 1
	}
	return a.relay(resp)
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
