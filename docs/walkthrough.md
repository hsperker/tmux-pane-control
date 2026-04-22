# tpctl internals: a code-walkthrough

*2026-04-22T19:31:00Z by Showboat 0.6.1*
<!-- showboat-id: 643fdeb7-35ca-4af8-a900-2d95f132e907 -->

This document tours the **tpctl** codebase in the order that a single request travels through it. The goal is to understand *why* each package exists and *where* the single-writer invariant lives.

tpctl is a hexagonal Go system: a short-lived CLI frontend, a long-lived controller daemon that owns mutable state, and a single adapter that speaks tmux. Every observation is anchored to an opaque checkpoint token; there is no "read from now".

## 1. The top-down layout

Before diving in, here is the package tree. Keep this open in your head — every later section refers to one of these directories.

```bash
find cmd internal -maxdepth 2 -type d | sort
```

```output
cmd
cmd/tpctl
internal
internal/cli
internal/controller
internal/domain
internal/ipc
internal/store
internal/textnorm
internal/tmuxctl
internal/waiter
```

Roles in two sentences each:

- **cmd/tpctl** — Go main package. 15 lines. Delegates to `internal/cli`.
- **internal/cli** — argv parsing, stdout/stderr, exit codes. Dumbest layer; no state.
- **internal/ipc** — Unix-socket transport between CLI and daemon, plus the auto-spawn-with-flock handshake.
- **internal/controller** — the single-writer event loop. Every mutation funnels here.
- **internal/store** — per-pane ring buffers (1 MiB each) and the opaque checkpoint tokens.
- **internal/waiter** — stateless matchers for sentinel / regex / quiescence.
- **internal/tmuxctl** — the only package that knows tmux syntax (`send-keys`, `pipe-pane`, `capture-pane`, `list-panes`).
- **internal/textnorm** — pure-function ANSI / CR / whitespace normalization per spec §8.3.
- **internal/domain** — the shared types and the canonical error codes.

## 2. cmd/tpctl — the entrypoint is deliberately tiny

There is no business logic in `main`. All it does is hand os.Args and stdio to `cli.App` and propagate its exit code. This keeps the CLI testable without a process boundary.

```bash
cat cmd/tpctl/main.go
```

```output
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
```

## 3. internal/cli — argv in, JSON out

`cli.App.Run` is a big switch on `args[0]`. For every subcommand it does three jobs:

1. Parse and validate flags.
2. Get a client to the daemon (auto-spawning one if needed).
3. Marshal the response and pick an exit code.

The heavy lifting sits in `app.go`. Let's look at its dispatch table — the first case for every CLI verb.

```bash
grep -n 'case "' internal/cli/app.go | head -20
```

```output
74:	case "-h", "help":
77:	case "version":
89:	case "list":
91:	case "snapshot":
93:	case "read":
95:	case "text":
97:	case "key":
99:	case "wait":
101:	case "daemon":
145:		case "help", "h":
148:		case "version":
151:		case "tmux-socket", "tmux-socket-name":
```

The order is significant: `daemon` is handled in-process (it runs the controller rather than calling it). Every other verb is a thin RPC client.

## 4. internal/ipc — the wire

The CLI talks to the daemon over a Unix socket using **line-delimited JSON**: one Request per line, one Response per line, no keep-alive state. Here is the protocol, which is also the source of truth for the set of operations.

```bash
sed -n '14,28p' internal/ipc/protocol.go
```

```output
// Op identifies the operation carried in a Request.
type Op string

const (
	OpList     Op = "list"
	OpSnapshot Op = "snapshot"
	OpRead     Op = "read"
	OpText     Op = "text"
	OpKey      Op = "key"
	OpWait     Op = "wait"
	// OpShutdown asks the daemon to exit cleanly. Not part of the
	// public CLI; used by tests and by controlled teardown.
	OpShutdown Op = "shutdown"
)

```

Six public verbs plus a private `shutdown` used only by tests and `tpctl daemon --stop`.

The **auto-spawn** dance is in `internal/ipc/autospawn.go`. This is how "no daemon babysitting" actually works: the CLI tries to ping the socket, and if nothing answers, it grabs a per-server flock and execs itself with `daemon` — but only after re-pinging under the lock so we don't get two daemons in a race.

```bash
sed -n '20,67p' internal/ipc/autospawn.go
```

```output
func EnsureDaemon(tmuxSocket string) (*Client, error) {
	sockPath, err := DaemonSocketPath(tmuxSocket)
	if err != nil {
		return nil, err
	}
	client := NewClient(sockPath)

	// Fast path: already running.
	if err := client.Ping(); err == nil {
		return client, nil
	}

	// Lock; re-check after acquiring in case another process raced
	// us to the spawn.
	lockPath := sockPath + ".lock"
	lf, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	defer lf.Close()
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX); err != nil {
		return nil, fmt.Errorf("flock %s: %w", lockPath, err)
	}
	defer syscall.Flock(int(lf.Fd()), syscall.LOCK_UN)

	if err := client.Ping(); err == nil {
		return client, nil
	}

	// Spawn a detached `tpctl daemon` with explicit sockets so it
	// doesn't need to re-derive them.
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("os.Executable: %w", err)
	}
	cmd := exec.Command(exe, "daemon",
		"--tmux-socket", tmuxSocket,
		"--daemon-socket", sockPath,
	)
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("spawn daemon: %w", err)
	}
	// Detach: don't wait on the child.
	go func() { _ = cmd.Wait() }()
```

Key details:

- **Per-server socket.** `DaemonSocketPath(tmuxSocket)` derives one daemon socket per *resolved* tmux server path, so two different tmux servers yield two independent controllers.
- **`Setsid`.** The spawned daemon detaches from the CLI's process group, so it outlives its parent.
- **Self-exec.** `os.Executable()` re-runs *this same binary* with `daemon`. There is no installed service.

Once a Client hand is obtained, the CLI calls `Dispatch` server-side:

```bash
sed -n '15,59p' internal/ipc/dispatch.go
```

```output
// Dispatch runs one request against the controller and returns the
// corresponding Response. It never touches stdio; the server and test
// harness wrap this with transport.
func Dispatch(ctx context.Context, c *controller.Controller, req *Request) *Response {
	switch req.Op {
	case OpList:
		return marshalBody(c.List())
	case OpSnapshot:
		var hl *int
		if req.HasHistory {
			hl = &req.HistoryLines
		}
		return marshalBody(c.Snapshot(req.Pane, hl))
	case OpRead:
		if req.After == "" {
			return cmdErr(&domain.ErrorResponse{
				PaneID:  string(req.Pane),
				Code:    domain.ErrMissingAfter,
				Message: "read requires --after; use snapshot to bootstrap",
			})
		}
		return marshalBody(c.Read(req.Pane, req.After))
	case OpText:
		if err := c.SendText(req.Pane, req.Text, req.Enter); err != nil {
			return classifyErr(err)
		}
		return &Response{OK: true}
	case OpKey:
		if len(req.Keys) == 0 {
			return cmdErr(&domain.ErrorResponse{
				PaneID:  string(req.Pane),
				Code:    domain.ErrInvalidArgs,
				Message: "key requires at least one token",
			})
		}
		if err := c.SendKeys(req.Pane, req.Keys); err != nil {
			return classifyErr(err)
		}
		return &Response{OK: true}
	case OpWait:
		return dispatchWait(ctx, c, req)
	default:
		return runtimeErr(fmt.Errorf("unknown op %q", req.Op))
	}
}
```

`Dispatch` is stdio-free. The server in `server.go` pulls a Request off the socket, calls `Dispatch`, writes the Response back, and closes the connection. This shape is what lets the whole stack be tested without any network.

## 5. internal/controller — the single-writer event loop

The controller owns the tmux subscription and the store. It is constructed once per tmux server; every handler method delegates to a file-scoped function (`Snapshot`, `Read`, `SendText`, `Wait`, …) that does not touch goroutines directly. The *loop* is the `drain` goroutine that copies pane events into the store.

```bash
sed -n '54,91p' internal/controller/controller.go
```

```output
// Start subscribes to pane output and begins draining it into the
// store. It returns only once the subscription is established.
// Subsequent calls are no-ops.
func (c *Controller) Start(ctx context.Context) error {
	c.startOnce.Do(func() {
		subCtx, cancel := context.WithCancel(ctx)
		c.cancel = cancel
		ch, err := c.port.Subscribe(subCtx)
		if err != nil {
			cancel()
			c.startErr = err
			return
		}
		c.done = make(chan struct{})
		go c.drain(ch)
	})
	return c.startErr
}

// drain copies subscription events into the store. It exits when the
// subscription channel closes (on ctx cancellation or adapter teardown).
//   - Closed events cause Forget so pending waits see the pane
//     destruction per spec §11.9.
//   - ServerLost events close ServerLost() so the daemon main can
//     trigger Exit-mode shutdown per spec §11.9.
func (c *Controller) drain(ch <-chan tmuxctl.PaneOutput) {
	defer close(c.done)
	for ev := range ch {
		switch {
		case ev.ServerLost:
			c.lostOnce.Do(func() { close(c.serverLost) })
		case ev.Closed:
			c.store.Forget(ev.ID)
		default:
			c.store.Append(ev.ID, ev.Data)
		}
	}
}
```

Three observations:

1. **One subscription per controller.** `port.Subscribe` is called once from `startOnce.Do`. All output flows down that single channel.
2. **Forget on close.** When a pane closes, `store.Forget` drops the buffer and kicks every waiter registered on that pane — that is how a pending `wait` gets `PANE_CLOSED` rather than hanging.
3. **ServerLost is terminal.** A closed `ServerLost()` channel tells the daemon main loop to trigger Exit-mode shutdown: when the tmux server dies, the daemon dies with it.

## 6. internal/store — the ring buffer and the token

This is where the "no read-from-now" guarantee is made real. Each pane has:

- A **byte ring** (`Buffer`) capped at 1 MiB.
- An **absolute offset** that never decreases — even after bytes are evicted.

The token encodes (controller-instance, pane-id, absolute offset, mint time). `Read` takes a token, decodes the offset, and copies `[offset, end)` out of the ring. If `offset < start` the bytes were evicted and the store returns `ErrTokenEvicted`.

Here is the buffer's core loop. Note that the invariant `0 <= end-start <= capacity` is maintained after *every* append:

```bash
sed -n '57,88p' internal/store/buffer.go
```

```output
func (b *Buffer) Append(p []byte) {
	n := len(p)
	if n == 0 {
		return
	}
	b.lastAppend = time.Now()
	// If p is larger than the capacity, the leading bytes are
	// immediately evicted. Advance end for those bytes and drop them
	// from p before writing; this preserves the ring invariant that
	// data[offset%cap] holds the byte at that absolute offset.
	if n > b.capacity {
		dropped := n - b.capacity
		b.end += int64(dropped)
		if b.end-b.start > int64(b.capacity) {
			b.start = b.end - int64(b.capacity)
		}
		p = p[dropped:]
		n = b.capacity
	}
	head := int(b.end % int64(b.capacity))
	first := b.capacity - head
	if first >= n {
		copy(b.data[head:head+n], p)
	} else {
		copy(b.data[head:], p[:first])
		copy(b.data[:n-first], p[first:])
	}
	b.end += int64(n)
	if b.end-b.start > int64(b.capacity) {
		b.start = b.end - int64(b.capacity)
	}
}
```

Notice that `data[offset % cap]` holds the byte at absolute `offset`. That is what lets us decode a token (which holds an absolute offset), compute `offset % cap`, and slice a two-part copy out of the ring. The *monotonic* part (`end`) is what goes into tokens; the *wrap* happens only inside the copy math.

Tokens themselves are base64-encoded JSON — opaque to the caller, debuggable to the author:

```bash
sed -n '19,45p' internal/store/token.go
```

```output
type tokenPayload struct {
	I string `json:"i"` // controller instance id
	P string `json:"p"` // pane id, e.g. "%42"
	O int64  `json:"o"` // stream offset
	T int64  `json:"t"` // mint time, unix nanoseconds
}

var (
	// ErrTokenMalformed is returned when a token fails to decode.
	ErrTokenMalformed = errors.New("token malformed")
	// ErrTokenWrongInstance is returned when a token was issued by a
	// different controller instance.
	ErrTokenWrongInstance = errors.New("token issued by a different controller instance")
	// ErrTokenWrongPane is returned when a token's pane id does not
	// match the request's pane id.
	ErrTokenWrongPane = errors.New("token is for a different pane")
	// ErrTokenEvicted is returned when a token's offset is below the
	// buffer's retained window (spec §11.8).
	ErrTokenEvicted = errors.New("token offset is no longer retained")
)

func encodeToken(instance string, pane domain.PaneID, offset int64, mintedAt time.Time) domain.Token {
	raw, _ := json.Marshal(tokenPayload{
		I: instance, P: string(pane), O: offset, T: mintedAt.UnixNano(),
	})
	return domain.Token(base64.RawURLEncoding.EncodeToString(raw))
}
```

Four failure modes — `Malformed`, `WrongInstance`, `WrongPane`, `Evicted` — all collapse to `INVALID_AFTER` at the CLI boundary. The **instance id** is what makes tokens non-survivable across daemon restarts: the controller generates a fresh random id on startup, so any token minted by a previous controller fails decode.

`Store.Read` is where the precedence rule from spec §7.5 lives — "prefer PANE_NOT_FOUND over INVALID_AFTER":

```bash
sed -n '183,224p' internal/store/store.go
```

```output
// Read returns the bytes appended since the token's offset and a new
// token pointing at the new stream head. It is the store half of the
// `tpctl read` handler.
//
// Errors:
//   - ErrTokenMalformed / ErrTokenWrongInstance / ErrTokenWrongPane /
//     ErrTokenEvicted: translate to INVALID_AFTER (spec §7.5)
//   - ErrPaneUnknown: translate to PANE_NOT_FOUND
func (s *Store) Read(id domain.PaneID, after domain.Token) ([]byte, domain.Token, error) {
	// Spec §7.5 precedence: "a token is invalid AND the pane lookup
	// already fails → prefer PANE_NOT_FOUND over INVALID_AFTER".
	// So check pane existence before validating the token's pane.
	s.mu.Lock()
	b, paneKnown := s.bufs[id]
	s.mu.Unlock()
	if !paneKnown {
		return nil, "", ErrPaneUnknown
	}
	tp, err := decodeToken(after)
	if err != nil {
		return nil, "", err
	}
	if tp.I != s.instance {
		return nil, "", ErrTokenWrongInstance
	}
	if domain.PaneID(tp.P) != id {
		return nil, "", ErrTokenWrongPane
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Re-check after reacquiring in case the pane was forgotten
	// between the two critical sections.
	b, ok := s.bufs[id]
	if !ok {
		return nil, "", ErrPaneUnknown
	}
	out, end, ok := b.Read(tp.O)
	if !ok {
		return nil, "", ErrTokenEvicted
	}
	return out, encodeToken(s.instance, id, end, time.Now()), nil
}
```

The subtle bit is the **double-check** on the pane. The method releases `s.mu` during token decode (because decode is pure and slow-ish), but the store could forget the pane in that window. Re-acquiring and re-checking collapses the race.

`Watch` is the other half of the store that matters: it gives waiters an edge-triggered signal channel so a pending wait gets unblocked every time Append or Forget touches its pane.

```bash
sed -n '106,143p' internal/store/store.go
```

```output
func (s *Store) Append(id domain.PaneID, p []byte) {
	if len(p) == 0 {
		return
	}
	s.mu.Lock()
	s.bufferLocked(id).Append(p)
	// Notify watchers under the lock to avoid a race with Forget.
	for _, w := range s.watchers {
		if w.pane != id {
			continue
		}
		select {
		case w.ch <- struct{}{}:
		default:
		}
	}
	s.mu.Unlock()
}

// Watch registers an observer for the given pane. The returned
// channel receives a (possibly coalesced) signal after every Append
// or Forget that involves this pane. The cancel func unregisters.
func (s *Store) Watch(id domain.PaneID) (<-chan struct{}, func()) {
	w := &paneWatcher{pane: id, ch: make(chan struct{}, 1)}
	s.mu.Lock()
	s.watchers = append(s.watchers, w)
	s.mu.Unlock()
	return w.ch, func() {
		s.mu.Lock()
		for i, x := range s.watchers {
			if x == w {
				s.watchers = append(s.watchers[:i], s.watchers[i+1:]...)
				break
			}
		}
		s.mu.Unlock()
	}
}
```

`select { case w.ch <- struct{}{}: default: }` is the coalescing primitive. Channels are buffered-of-1, so at most one signal is pending at a time; any Append after that is a no-op until the waiter drains. This keeps a fast-producing pane from flooding a slow waiter's goroutine.

## 7. internal/waiter — three stateless evaluators

`waiter` does not own state. It takes a byte slice (already ANSI-stripped by `textnorm`) and says "matches here" or "no match". Keeping it stateless is why `Wait` in the controller can re-evaluate on every signal without setting up and tearing down evaluator state.

```bash
sed -n '36,65p' internal/waiter/sentinel.go
```

```output
func MatchSentinel(buf []byte, token string) (SentinelMatch, bool) {
	prefix := []byte("__DONE__:" + token + ":")
	offset := 0
	for {
		idx := bytes.Index(buf[offset:], prefix)
		if idx < 0 {
			return SentinelMatch{}, false
		}
		idx += offset
		start := idx + len(prefix)
		end := start
		for end < len(buf) && buf[end] >= '0' && buf[end] <= '9' {
			end++
		}
		if end > start {
			digits := string(buf[start:end])
			code, err := strconv.Atoi(digits)
			if err == nil {
				return SentinelMatch{
					Matched:  string(buf[idx:end]),
					ExitCode: code,
					End:      end,
				}, true
			}
			// Overflow on absurd digit counts; treat as no match
			// at this position and keep searching.
		}
		offset = idx + 1
	}
}
```

Sentinel is deliberately **un**-signed: signed exit codes like `-1` don't match. Agents are expected to print `__DONE__:TOKEN:EXITCODE` where EXITCODE is the shell's `$?` — which is always 0..255.

Regex is trivial by comparison — `re.FindIndex` and report the match:

```bash
sed -n '21,30p' internal/waiter/regex.go
```

```output
func MatchRegex(buf []byte, re *regexp.Regexp) (RegexMatch, bool) {
	loc := re.FindIndex(buf)
	if loc == nil {
		return RegexMatch{}, false
	}
	return RegexMatch{
		Matched: string(buf[loc[0]:loc[1]]),
		End:     loc[1],
	}, true
}
```

Quiescence is not in the waiter package — there is no byte pattern to match. It lives in the controller's `Wait` loop and uses only `LastAppend` + the token's mint time.

Now the loop that glues watcher + evaluator together. This is the **race-free wait** machinery:

```bash
sed -n '111,145p' internal/controller/wait.go
```

```output
	// Register the watcher BEFORE the first read so no append between
	// the read and the signal subscription is missed (spec §9.6's
	// "cannot miss fast output that occurs after the checkpoint").
	sig, cancel := st.Watch(req.PaneID)
	defer cancel()

	timeout := time.NewTimer(req.Timeout)
	defer timeout.Stop()

	// Decode the checkpoint mint time for quiescence's formula.
	checkpointAt, _ := st.TokenTime(req.After)

	// Track whether the pane was known when this wait registered.
	// If it was and later becomes unknown, the pane was destroyed
	// during the wait and the correct code is PANE_CLOSED (§11.9).
	paneKnownAtStart := false

	for {
		// Validate token + pane each iteration so a Forget (pane
		// destroyed) surfaces as PANE_NOT_FOUND even if already
		// buffered bytes still match. The current cost is low.
		bytes, next, err := st.Read(req.PaneID, req.After)
		if err != nil {
			if paneKnownAtStart && errors.Is(err, store.ErrPaneUnknown) {
				return nil, &domain.ErrorResponse{
					PaneID:  string(req.PaneID),
					Code:    domain.ErrPaneClosed,
					Message: "pane was destroyed while wait was pending",
				}
			}
			return nil, mapWaitStoreError(req.PaneID, err)
		}
		paneKnownAtStart = true

		switch req.Mode {
```

The comment captures the invariant: **`Watch` before `Read`**. If we registered after the first read, an Append between the two would neither land in our read output *nor* wake us up — we would sleep forever on bytes that had already arrived. By registering first, any Append strictly after `Watch` lights the signal; the *next* iteration's `Read` will see those bytes.

Notice also that a pane becoming unknown is translated differently depending on *when* it happened: unknown-from-the-start is `PANE_NOT_FOUND`, unknown-mid-wait is `PANE_CLOSED`. The `paneKnownAtStart` bookkeeping carries that distinction.

## 8. internal/tmuxctl — the only package that knows tmux syntax

Everything else uses the `Port` interface. The real implementation shells out to the `tmux` binary per call. This means the unit tests can swap in `fake.go` (a Go-level fake pane server) without touching any other layer.

```bash
sed -n '96,118p' internal/tmuxctl/adapter.go
```

```output
func (a *Adapter) SendText(id domain.PaneID, text string, enter bool) error {
	var stderr bytes.Buffer
	c := a.cmd("send-keys", "-l", "-t", string(id), "--", text)
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		if isPaneMissingStderr(stderr.String()) {
			return ErrPaneNotFound
		}
		return fmt.Errorf("tmux send-keys -l: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	if enter {
		stderr.Reset()
		c2 := a.cmd("send-keys", "-t", string(id), "--", "Enter")
		c2.Stderr = &stderr
		if err := c2.Run(); err != nil {
			if isPaneMissingStderr(stderr.String()) {
				return ErrPaneNotFound
			}
			return fmt.Errorf("tmux send-keys Enter: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	return nil
}
```

Details that matter in practice:

- **`-l` = literal.** Text is sent verbatim, not interpreted as key names.
- **`--` = end-of-options.** User text containing a leading dash doesn't poison tmux's argv parser.
- **`Enter` is a second send.** The spec explicitly forbids embedding `\n`; tmux has a real `Enter` key token and it's what gets sent.
- **`send-keys` blocks until tmux ack.** When `c.Run()` returns, tmux has accepted the text. That's the basis of the "send-ack" promise in the README.

The subscription side (how output gets *in*) uses `pipe-pane` with a Unix FIFO, implemented in `subscribe_unix.go` — out of scope for this tour, but worth knowing that's where `port.Subscribe` delivers its `PaneOutput` events from.

## 9. internal/textnorm — the ANSI filter

The controller hands `Wait` the raw ring-buffer bytes. Before regex or sentinel scans, those bytes pass through `StripANSIAndCR`. Note what is **not** applied:

```bash
sed -n '40,58p' internal/textnorm/normalize.go
```

```output
// StripANSIAndCR applies spec §9.6's match-input normalization:
//  1. remove ANSI / ESC-introduced control sequences
//  2. remove carriage returns — \r\n collapses to \n, stray \r drops
//
// Newlines (\n) are preserved. Trailing-whitespace trimming and
// trailing-blank-line trimming are deliberately NOT applied; those
// belong to §8.3 (text-bearing JSON fields), not to wait match input.
//
// This matters for RE2 multiline anchors: `(?m)^ready$` matching
// against "ready\r\n" fails if \r is preserved, because \r sits
// between "ready" and the end-of-line anchor. Stripping \r first
// makes the regex behave as written.
func StripANSIAndCR(s string) string {
	s = stripANSI(s)
	if strings.IndexByte(s, '\r') >= 0 {
		s = strings.ReplaceAll(s, "\r", "")
	}
	return s
}
```

Two variants exist for a reason: `Normalize` is for JSON output (humans will see it), `StripANSIAndCR` is for wait-match input (regexes will see it). Trimming trailing whitespace in match input would break anchored regexes against the live stream.

## 10. internal/domain — the shared vocabulary

This package has no dependencies. It holds the types everyone else speaks: `PaneID`, `Token`, the response envelopes, and the canonical error codes.

```bash
sed -n '7,19p' internal/domain/errors.go
```

```output

const (
	ErrMissingAfter ErrorCode = "MISSING_AFTER"
	ErrInvalidAfter ErrorCode = "INVALID_AFTER"
	ErrPaneNotFound ErrorCode = "PANE_NOT_FOUND"
	ErrPaneClosed   ErrorCode = "PANE_CLOSED"
	ErrTimeout      ErrorCode = "TIMEOUT"

	// Implementation-defined (spec §7.5 allows additional codes).
	ErrInvalidKey   ErrorCode = "INVALID_KEY"
	ErrInvalidArgs  ErrorCode = "INVALID_ARGS"
	ErrInvalidRegex ErrorCode = "INVALID_REGEX"
)
```

The five canonical codes are the ones a conformant implementation in any language must emit. The three implementation-defined codes (`INVALID_KEY`, `INVALID_ARGS`, `INVALID_REGEX`) are Go-specific niceties the spec permits but doesn't require.

## 11. The path of a single `tpctl wait` request

Let's trace one call through every layer. Consider:

    tpctl wait --pane %42 --after TOKEN --for regex --pattern 'READY' --timeout-ms 5000

1. **cmd/tpctl/main.go** builds `cli.App` and calls `Run(["wait", …])`.
2. **internal/cli/app.go** parses flags, validates `--for regex` and `--pattern`, and calls `ipc.EnsureDaemon`.
3. **internal/ipc/autospawn.go** pings the socket. If no daemon answers, it flocks, re-pings, and exec-spawns `tpctl daemon` detached. When a client is up, it returns to the CLI.
4. **internal/ipc/client.go** writes a JSON Request with `op:"wait"` to the socket.
5. **internal/ipc/server.go** reads the line and calls `Dispatch`.
6. **internal/ipc/dispatch.go** compiles the regex (returning `INVALID_REGEX` on failure) and calls `controller.Wait`.
7. **internal/controller/wait.go**:
   - registers a `Watch` on `%42`,
   - loops `Read` → `StripANSIAndCR` → `MatchRegex`,
   - sleeps on `sig` / `timeout.C` / `ctx.Done()`.
8. **internal/store/store.go**'s `Append` (driven by the controller's `drain` goroutine, fed by **internal/tmuxctl**'s `pipe-pane` subscription) signals `sig` every time the pane emits output.
9. When `MatchRegex` returns ok, `Wait` emits a `domain.WaitResponse` containing the new `next` token.
10. The response unwinds back through `Dispatch` → server → client → CLI → stdout.

Let's prove the build is healthy and the shape is right by building `tpctl` and asking it for its version:

```bash
go build -o /tmp/tpctl ./cmd/tpctl && /tmp/tpctl --help
```

```output
tpctl - agent-driven tmux pane control

Usage:
  tpctl list
  tpctl snapshot --pane %ID [--history-lines N]
  tpctl read --pane %ID --after TOKEN
  tpctl text --pane %ID TEXT [--enter]
  tpctl key --pane %ID KEY [KEY...]
  tpctl wait --pane %ID --after TOKEN --for MODE ... --timeout-ms N
  tpctl daemon [--stop]

Global flags:
  --tmux-socket PATH       tmux -S socket path
  --tmux-socket-name NAME  tmux -L socket shortname
  --help                   Show this help and exit (per-subcommand: tpctl <sub> --help)
  --version                Print version and exit (also: tpctl version)

See docs/specs/tpctl-v1.md for the full specification.
```

The six verbs in that help text map one-to-one to the six `Op` constants in `internal/ipc/protocol.go` — which is the point: the CLI is a transport to the controller, and the controller's JSON shape is the product.

## 12. The shape of the whole

Line counts per non-test file give a sense of where the complexity lives:

```bash
find cmd internal -name '*.go' -not -name '*_test.go' | xargs wc -l | sort -n | tail -12
```

```output
   135 internal/controller/controller.go
   135 internal/ipc/server.go
   141 internal/textnorm/normalize.go
   144 internal/ipc/dispatch.go
   188 internal/tmuxctl/fake.go
   202 internal/tmuxctl/adapter.go
   224 internal/store/store.go
   238 internal/tmuxctl/subscribe_unix.go
   241 internal/cli/daemon.go
   258 internal/controller/wait.go
   691 internal/cli/app.go
  3572 total
```

Under 3,600 lines of non-test Go. The CLI surface (`cli/app.go`) is the biggest file because every verb has its own flag-parsing block. The interesting-code lives in `controller/wait.go` (the race-free loop), `store/store.go` (tokens + ring), and `tmuxctl/subscribe_unix.go` (the pipe-pane FIFO drain).

## 13. Where to read next

- [`docs/specs/tpctl-v1.md`](specs/tpctl-v1.md) — the normative spec. Every "§X.Y" comment in the code refers to a section here.
- [`docs/architecture.md`](architecture.md) — Mermaid diagrams of the same layout we just walked.
- [`conformance/`](../conformance/) — a standalone Go module that validates any tpctl-compatible binary in any language against ~55 scenarios.
- [`internal/store/buffer.go`](../internal/store/buffer.go) — if you only read *one* file, read this. It's the smallest self-contained piece of the design.

What makes tpctl worth studying is not the code — it's the **discipline**: a single-writer event loop, an opaque token for every observation, and a deliberately thin adapter to the outside world. Everything else falls out of those three choices.
