# tmux-pane-control

`tpctl` is a small CLI that lets coding agents and humans interact
with tmux panes programmatically, without the race conditions that
plague `send-keys` + `capture-pane` scripts.

A short-lived CLI frontend talks to an auto-spawned controller
process that owns tmux state, per-pane output-stream buffers, and
opaque checkpoint tokens. Every observation is anchored to a
checkpoint, so there is no "read from now" race.

## What it gives you

- **Checkpointed reads.** `snapshot` returns the visible pane plus a
  token; `read --after TOKEN` returns everything appended since,
  exactly once.
- **Race-free waits.** `wait --for sentinel|regex|quiescence` scans
  from a checkpoint, including output that arrived between the send
  and the wait registration. Multiple concurrent waits on the same
  pane are supported.
- **Send-ack semantics.** `text` and `key` return only after tmux has
  acknowledged the send.
- **Compact JSON.** Query commands print one-line JSON on stdout.
  Mutating commands print nothing. Command-level errors print
  structured JSON with canonical codes (`PANE_NOT_FOUND`,
  `INVALID_AFTER`, `TIMEOUT`, `PANE_CLOSED`, `MISSING_AFTER`).
- **No daemon babysitting.** The controller auto-spawns on first use
  and survives the spawning CLI (setsid + stdio detachment).

## Install

```bash
go build -o tpctl ./cmd/tpctl
# or: go install github.com/hsperker/tmux-pane-control/cmd/tpctl@latest
```

Requires Go 1.24+ and tmux 3.x.

## Core concepts

- **Pane identity.** Panes are named by tmux's `%N` id (e.g. `%42`),
  never `session:window.pane`. Use `tpctl list` to discover ids.
- **Checkpoint token.** An opaque string returned by `snapshot`,
  `read`, and `wait`. Pass it back via `--after` to anchor the next
  observation. Tokens are pane-scoped and controller-lifetime-
  scoped; don't try to parse them or reuse them across panes.
- **Wait modes.**
  - `sentinel` — matches `__DONE__:TOKEN:EXITCODE`; response parses
    the exit code as an integer.
  - `regex` — RE2, applied to the ANSI-and-CR-stripped post-
    checkpoint buffer. No submatches.
  - `quiescence` — succeeds once the pane has been idle (no output)
    for `--ms` milliseconds. Succeeds immediately if already idle.

## Commands at a glance

| Command | Returns |
|---|---|
| `tpctl list` | `{"panes": ["%42", ...]}` |
| `tpctl snapshot --pane %N [--history-lines K]` | `pane_id`, `next`, `text`, optional `scrollback_text` |
| `tpctl read --pane %N --after TOKEN` | `pane_id`, `next`, `text` |
| `tpctl text --pane %N "..." [--enter]` | empty stdout on success |
| `tpctl key --pane %N K1 K2 ...` | empty stdout on success |
| `tpctl wait --pane %N --after TOKEN --for MODE ... --timeout-ms T` | `pane_id`, `next`, `result`, mode-specific fields |
| `tpctl daemon` | runs the controller in the foreground |

Targeting a specific tmux server: `--tmux-socket PATH` or
`--tmux-socket-name NAME` on any invocation.

## Agent usage

Every command in the API gets exercised in an end-to-end session.

### 1. Discover panes (`list`)

```bash
tpctl list
# {"panes":["%0","%42","%43"]}

PANE=$(tpctl list | jq -r '.panes[0]')
```

### 2. Bootstrap observation (`snapshot`)

```bash
# Visible screen only — cheap, returns a checkpoint token
SNAP=$(tpctl snapshot --pane "$PANE")
TOKEN=$(echo "$SNAP" | jq -r .next)

# Or include scrollback for richer context at session start
tpctl snapshot --pane "$PANE" --history-lines 200 \
  | jq -r '.scrollback_text, .text'
```

### 3. Run a command race-free (`text` + `wait --for sentinel`)

```bash
# Snapshot first to anchor the wait
SNAP=$(tpctl snapshot --pane "$PANE")
TOKEN=$(echo "$SNAP" | jq -r .next)

# Send the command plus a sentinel that carries the exit code
tpctl text --pane "$PANE" \
  'make test; printf "__DONE__:run1:%d\n" $?' --enter

# Wait for __DONE__:run1:<N>. On success the response includes
# the full matched literal and exit_code as an integer.
tpctl wait --pane "$PANE" --after "$TOKEN" \
  --for sentinel --token run1 --timeout-ms 30000
```

Why it's race-free: `text` doesn't return until tmux has ack'd the
send, and `wait --after TOKEN` scans **everything** appended after
the token — including output that arrived while the wait was being
registered. Fast output can't slip through.

### 4. Drive a TUI (`key` + `wait --for quiescence`)

```bash
SNAP=$(tpctl snapshot --pane "$PANE")
TOKEN=$(echo "$SNAP" | jq -r .next)

# Escape, then /pods, then Enter — tmux key names
tpctl key --pane "$PANE" Escape "/" "pods" Enter

# Wait for the pane to stop producing output for 250ms
tpctl wait --pane "$PANE" --after "$TOKEN" --for quiescence \
  --ms 250 --timeout-ms 3000

# Now read the settled screen
tpctl snapshot --pane "$PANE"
```

### 5. Match loose patterns (`wait --for regex`)

```bash
SNAP=$(tpctl snapshot --pane "$PANE")
TOKEN=$(echo "$SNAP" | jq -r .next)

tpctl text --pane "$PANE" "kubectl get pods -w" --enter

# RE2 syntax; no submatches in the response, just the whole match
tpctl wait --pane "$PANE" --after "$TOKEN" \
  --for regex --pattern '^[a-z0-9-]+\s+Running' --timeout-ms 60000
```

### 6. Tail incrementally (`read`)

```bash
TOKEN=$(tpctl snapshot --pane "$PANE" | jq -r .next)
while sleep 1; do
  OUT=$(tpctl read --pane "$PANE" --after "$TOKEN")
  echo "$OUT" | jq -r .text
  TOKEN=$(echo "$OUT" | jq -r .next)
done
```

`read` returns empty `text` when nothing new has appeared; it's
always safe to call.

### 7. Run the controller explicitly (`daemon`)

Agents normally don't need this — the controller auto-spawns on
first use. But if you want to run it in the foreground for
debugging or under a supervisor:

```bash
tpctl daemon --tmux-socket /path/to/tmux.sock
```

### Error handling cheat sheet

| Code | Meaning | Recovery |
|---|---|---|
| `MISSING_AFTER` | `read`/`wait` called without `--after` | call `snapshot` first |
| `INVALID_AFTER` | token is wrong pane, evicted (>1 MiB ago), or from an old controller | call `snapshot` again |
| `PANE_NOT_FOUND` | pane doesn't exist at dispatch time | call `list` |
| `PANE_CLOSED` | pane vanished during a pending `wait` | pick another pane |
| `TIMEOUT` | `wait` hit `--timeout-ms` | increase timeout or switch modes |

Command-level errors are JSON on stdout with a nonzero exit.
Runtime/controller failures are diagnostics on stderr with a
nonzero exit. Agents should check stdout first — if it parses as
JSON with a `code` field, it's a command-level error; otherwise
check stderr for runtime diagnostics.

## Human usage

```bash
# What panes exist?
tpctl list

# What's on pane %0 right now?
tpctl snapshot --pane %0 | jq -r .text

# Peek at the last 40 lines of scrollback too
tpctl snapshot --pane %0 --history-lines 40 \
  | jq -r '.scrollback_text, .text'

# See what's new since the last peek (save the token between runs)
TOKEN=$(tpctl snapshot --pane %0 | jq -r .next)
# ... time passes, pane has produced output ...
tpctl read --pane %0 --after "$TOKEN" | jq -r .text

# Type something into a pane without stealing focus
tpctl text --pane %0 "date" --enter

# Press Escape-then-:q to quit a vim pane
tpctl key --pane %0 Escape ":" "q" Enter

# Block until a pane prints "READY" (e.g. in a shell script that
# waits for a dev server to come up before running smoke tests)
TOKEN=$(tpctl snapshot --pane %0 | jq -r .next)
tpctl wait --pane %0 --after "$TOKEN" \
  --for regex --pattern 'READY' --timeout-ms 60000

# Run a long build in a pane and block this script until it finishes,
# with the build's exit code surfaced as JSON
TOKEN=$(tpctl snapshot --pane %0 | jq -r .next)
tpctl text --pane %0 'make release; printf "__DONE__:build:%d\n" $?' --enter
tpctl wait --pane %0 --after "$TOKEN" \
  --for sentinel --token build --timeout-ms 600000 | jq '.exit_code'

# Wait for a busy pane to go idle for a quarter-second before
# inspecting it
TOKEN=$(tpctl snapshot --pane %0 | jq -r .next)
tpctl wait --pane %0 --after "$TOKEN" \
  --for quiescence --ms 250 --timeout-ms 5000
tpctl snapshot --pane %0 | jq -r .text

# Start the controller in the foreground (usually unnecessary —
# auto-spawn handles this — but useful for debugging)
tpctl daemon
```

The JSON output is compact and line-oriented, so it plays well with
`jq`, `grep`, and shell pipelines.

The JSON output is compact and line-oriented, so it plays well with
`jq`, `grep`, and shell pipelines.

## Architecture at a glance

```
cmd/tpctl/             CLI entrypoint
internal/cli/          argument parsing, stdout/stderr, dispatch
internal/ipc/          CLI ↔ daemon Unix-socket transport + auto-spawn
internal/controller/   event loop, request handlers, token issuance
internal/store/        per-pane ring buffers (1 MiB each) + tokens
internal/waiter/       sentinel / regex / quiescence matchers (pure)
internal/textnorm/     §8.3 ANSI/CR/whitespace normalization (pure)
internal/tmuxctl/      tmux adapter (pipe-pane + list-panes poll)
internal/domain/       response types, error codes, canonical JSON shapes
```

One controller per tmux server, keyed on the resolved tmux socket
path. The controller is a single-writer event loop that owns all
mutable pane state; handlers are thin and pure-ish.

## Building and testing

```bash
go test ./...                              # all tests
go test ./internal/textnorm/...            # fast unit tests
go test ./cmd/tpctl/ -run TestAcceptance   # §17 acceptance suite
```

Tests that need tmux skip themselves if tmux is not on `PATH`. The
acceptance suite spins up a disposable tmux server on a temp
socket and drives the built binary through the daemon.

## Documentation

- [`docs/specs/tpctl-v1.md`](docs/specs/tpctl-v1.md) — normative
  v1 specification. This is what implementations must conform to.
- [`docs/plan/tpctl-v1-implementation.md`](docs/plan/tpctl-v1-implementation.md)
  — the slice-by-slice plan that was used to build v1, plus a
  retrospective and the list of known deviations the code is still
  catching up on.

## License

See [LICENSE](LICENSE).
