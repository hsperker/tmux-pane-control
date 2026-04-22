# tmux-pane-control

`tpctl` drives tmux panes from scripts and agents without the usual
`send-keys` + `capture-pane` races.

The CLI is short-lived. It talks to a daemon that owns tmux state,
per-pane ring buffers, and opaque checkpoint tokens. Every read and
every wait is anchored to a token. There is no "read from now."

## What it gives you

- **Checkpointed reads.** `snapshot` returns the visible screen and a
  token. `read --after TOKEN` returns what came next, exactly once.
- **Race-free waits.** `wait --for sentinel|regex|quiescence` scans
  from the token forward, including output buffered before the wait
  registered. Concurrent waits on the same pane are fine.
- **Send-ack.** `text` and `key` return only after tmux ack's the send.
- **Compact JSON.** Query commands print one JSON line on stdout.
  Mutating commands print nothing. Errors are JSON too, with canonical
  codes: `PANE_NOT_FOUND`, `INVALID_AFTER`, `TIMEOUT`, `PANE_CLOSED`,
  `MISSING_AFTER`.
- **No daemon babysitting.** The daemon spawns on first use and
  outlives the CLI that spawned it.
- **Conformance kit.** `conformance/` is a standalone Go module with
  ~55 spec-pinned scenarios. Point it at any tpctl-compatible binary
  in any language:
  `go test ./conformance/scenarios/ -parallel=4 -args --binary=/path/to/tpctl`.

## Install

```bash
go build -o tpctl ./cmd/tpctl
# or: go install github.com/hsperker/tmux-pane-control/cmd/tpctl@latest
```

Requires Go 1.24+ and tmux 3.x.

## Core concepts

- **Pane identity.** Panes are tmux `%N` ids, e.g. `%42`. Not
  `session:window.pane`. Use `tpctl list` to discover them.
- **Checkpoint token.** An opaque string from `snapshot`, `read`, or
  `wait`. Pass it back via `--after` to anchor the next observation.
  Tokens are pane-scoped and die with the controller. Do not parse
  them.
- **Two ways to see the pane.** `snapshot` returns the **rendered
  screen** (what you'd see if you looked at the pane right now —
  capped at the pane's size, TUI redraws collapsed). `read --after`
  returns the **raw byte stream** appended since a token (grows
  unbounded as output accumulates, nothing collapses). Pick based
  on what the pane is doing:
  - **Shell logs / incremental tail** → `read --after`. Byte-accurate,
    never loses lines to scrolling.
  - **Live TUI (editor, chat client, top, a coding agent)** → wait
    for the pane to settle, then `snapshot`. Redraw storms stay out
    of your context.
- **Wait modes.**
  - `sentinel` matches `__DONE__:TOKEN:EXITCODE`. The response parses
    the exit code as an integer.
  - `regex` is RE2, applied to ANSI-and-CR-stripped output. No
    submatches.
  - `quiescence` fires once the pane has been idle for `--ms`
    milliseconds. Fires immediately if it already was.

## Commands at a glance

| Command | Returns | Good for |
|---|---|---|
| `tpctl list` | `{"panes": ["%42", ...]}` | pane discovery |
| `tpctl snapshot --pane %N [--history-lines K]` | `pane_id`, `next`, `text`, optional `scrollback_text` | rendered TUI view |
| `tpctl read --pane %N --after TOKEN` | `pane_id`, `next`, `text` | incremental log tail |
| `tpctl text --pane %N "..." [--enter]` | empty stdout on success | send text input |
| `tpctl key --pane %N K1 K2 ...` | empty stdout on success | send named keys |
| `tpctl wait --pane %N --after TOKEN --for MODE ... --timeout-ms T` | `pane_id`, `next`, `result`, mode-specific fields | block on a condition |
| `tpctl daemon` | runs the controller in the foreground | debugging |

Target a specific tmux server with `--tmux-socket PATH` or
`--tmux-socket-name NAME` on any invocation.

## Agent usage

### 1. Discover panes

```bash
tpctl list
# {"panes":["%0","%42","%43"]}
PANE=$(tpctl list | jq -r '.panes[0]')
```

### 2. Snapshot

```bash
SNAP=$(tpctl snapshot --pane "$PANE")
TOKEN=$(echo "$SNAP" | jq -r .next)

# With scrollback:
tpctl snapshot --pane "$PANE" --history-lines 200 \
  | jq -r '.scrollback_text, .text'
```

### 3. Race-free command (text + wait sentinel)

```bash
SNAP=$(tpctl snapshot --pane "$PANE")
TOKEN=$(echo "$SNAP" | jq -r .next)
tpctl text --pane "$PANE" \
  'make test; printf "__DONE__:run1:%d\n" $?' --enter
tpctl wait --pane "$PANE" --after "$TOKEN" \
  --for sentinel --token run1 --timeout-ms 30000
```

Why it's race-free: `text` blocks on tmux ack. `wait --after TOKEN`
scans every byte after the token, including bytes that landed while
the wait was registering.

### 4. TUI (key + quiescence)

```bash
SNAP=$(tpctl snapshot --pane "$PANE")
TOKEN=$(echo "$SNAP" | jq -r .next)
tpctl key --pane "$PANE" Escape "/" "pods" Enter
tpctl wait --pane "$PANE" --after "$TOKEN" \
  --for quiescence --ms 250 --timeout-ms 3000
tpctl snapshot --pane "$PANE"
```

### 5. Regex match

```bash
SNAP=$(tpctl snapshot --pane "$PANE")
TOKEN=$(echo "$SNAP" | jq -r .next)
tpctl text --pane "$PANE" "kubectl get pods -w" --enter
tpctl wait --pane "$PANE" --after "$TOKEN" \
  --for regex --pattern '^[a-z0-9-]+\s+Running' --timeout-ms 60000
```

### 6. Tail

```bash
TOKEN=$(tpctl snapshot --pane "$PANE" | jq -r .next)
while sleep 1; do
  OUT=$(tpctl read --pane "$PANE" --after "$TOKEN")
  echo "$OUT" | jq -r .text
  TOKEN=$(echo "$OUT" | jq -r .next)
done
```

`read` returns empty text when nothing new has appeared. It is
always safe to call.

### 7. Foreground daemon

The daemon auto-spawns. Run it in the foreground only to debug:

```bash
tpctl daemon --tmux-socket /path/to/tmux.sock
```

### Errors

| Code | Meaning | Recovery |
|---|---|---|
| `MISSING_AFTER` | `read`/`wait` without `--after` | snapshot first |
| `INVALID_AFTER` | wrong pane, evicted, or stale controller | snapshot again |
| `PANE_NOT_FOUND` | pane gone at dispatch | call `list` |
| `PANE_CLOSED` | pane vanished during a pending `wait` | pick another pane |
| `TIMEOUT` | `wait` hit `--timeout-ms` | longer timeout or a different mode |

Command-level errors print JSON to stdout. Runtime failures print
diagnostics to stderr. Both exit nonzero. Parse stdout first; if it
is not JSON, read stderr.

## Human usage

```bash
tpctl list                                     # what panes exist
tpctl snapshot --pane %0 | jq -r .text         # what's on pane %0

# Last 40 lines of scrollback and the visible screen:
tpctl snapshot --pane %0 --history-lines 40 \
  | jq -r '.scrollback_text, .text'

# Delta since the last peek:
TOKEN=$(tpctl snapshot --pane %0 | jq -r .next)
# ... time passes ...
tpctl read --pane %0 --after "$TOKEN" | jq -r .text

tpctl text --pane %0 "date" --enter            # type without stealing focus
tpctl key --pane %0 Escape ":" "q" Enter       # quit vim

# Block until a pane prints READY:
TOKEN=$(tpctl snapshot --pane %0 | jq -r .next)
tpctl wait --pane %0 --after "$TOKEN" \
  --for regex --pattern 'READY' --timeout-ms 60000

# Block until a long build finishes; print its exit code:
TOKEN=$(tpctl snapshot --pane %0 | jq -r .next)
tpctl text --pane %0 \
  'make release; printf "__DONE__:build:%d\n" $?' --enter
tpctl wait --pane %0 --after "$TOKEN" \
  --for sentinel --token build --timeout-ms 600000 | jq '.exit_code'

# Inspect a busy pane once it goes idle:
TOKEN=$(tpctl snapshot --pane %0 | jq -r .next)
tpctl wait --pane %0 --after "$TOKEN" \
  --for quiescence --ms 250 --timeout-ms 5000
tpctl snapshot --pane %0 | jq -r .text

tpctl daemon                                   # foreground daemon, for debugging
```

Output is one-line JSON. It composes with `jq`, `grep`, and pipes.

## When to use tpctl

| Your situation | Use |
|---|---|
| Agent or script drives a shell you also want to watch | **tpctl** |
| Race-free waits on command output (sentinel, regex, idle) | **tpctl** |
| Open N windows, run N commands, walk away | [libtmux](https://github.com/tmux-python/libtmux) |
| Automate a CLI with no terminal, no tmux | [pexpect](https://pexpect.readthedocs.io/) / [node-pty](https://github.com/microsoft/node-pty) |
| Throwaway one-liner in bash | `tmux send-keys` + `tmux capture-pane` |

Reach for tpctl when:

- You cannot miss output between a send and a read. `text` waits for
  tmux to ack; `wait --after TOKEN` scans from the token forward,
  including buffered bytes. Race-free within 1 MiB retained per pane.
- Human and agent share the same pane in real time.
- You want the wait modes done for you. Sentinel carries an exit
  code. Regex is RE2. Quiescence fires when the pane goes idle.
- You want JSON out and structured JSON errors with canonical codes.

Reach for something else when:

- You just need to set up panes — libtmux is a Python library for
  that, not an observer of output.
- You're automating a CLI with no terminal. pexpect and node-pty
  run the child themselves; no tmux, no daemon, no visibility.
- The script runs once and you'll read its stdout after.
  `tmux send-keys` plus `tmux capture-pane` is fine.

## Architecture

```
cmd/tpctl/             CLI entrypoint
internal/cli/          argument parsing, stdout/stderr, dispatch
internal/ipc/          CLI ↔ daemon Unix-socket transport + auto-spawn
internal/controller/   event loop, request handlers, token issuance
internal/store/        per-pane ring buffers (1 MiB) + tokens
internal/waiter/       sentinel / regex / quiescence matchers
internal/textnorm/     §8.3 ANSI/CR/whitespace normalization
internal/tmuxctl/      tmux adapter (pipe-pane + list-panes poll)
internal/domain/       response types, error codes, JSON shapes
conformance/           standalone conformance kit (own go.mod)
```

One controller per tmux server, keyed on the resolved socket path.
The controller is a single-writer event loop. Handlers are thin.

See [`docs/architecture.md`](docs/architecture.md) for Mermaid
diagrams of the component layout, request lifecycle, race-free
timing, and the wait state machine.

## Testing

```bash
go test ./...                              # all impl tests
go test ./internal/textnorm/...            # fast unit tests
go test ./cmd/tpctl/ -run TestAcceptance   # §17 acceptance suite

# Conformance kit (separate module):
go build -o tpctl ./cmd/tpctl
cd conformance && go test ./scenarios/ -parallel=4 \
  -args --binary=$PWD/../tpctl
```

Tmux-backed tests skip when tmux is missing. The conformance kit is
cleanly separated from the reference implementation. It runs against
any tpctl-compatible binary in any language.

## Documentation

- [`docs/specs/tpctl-v1.md`](docs/specs/tpctl-v1.md) — the normative v1 spec.
- [`docs/architecture.md`](docs/architecture.md) — Mermaid diagrams of
  the components, request flow, and race-free wait timing.
- [`docs/plan/tpctl-v1-implementation.md`](docs/plan/tpctl-v1-implementation.md)
  — slice-by-slice build plan, retrospective, and known deviations.
- [`conformance/README.md`](conformance/README.md) — how to run the
  conformance kit against your implementation.

## License

See [LICENSE](LICENSE).
