---
name: tmux-pane-ctrl
description: "Remote control tmux sessions for interactive CLIs (python, lldb, psql, node, chat TUIs). Pane I/O goes through tpctl for race-free reads and waits. Defaults to the user's current tmux server (ride-along) when invoked from inside tmux, otherwise uses an isolated socket."
license: MIT
compatibility: Requires tpctl on PATH and tmux 3.x
---

# tmux-pane-ctrl Skill

Drive interactive TTY programs by sending keystrokes to a tmux pane and reading its output. All pane I/O goes through `tpctl`, which anchors every read and wait to a checkpoint token so no output is missed between sending input and observing the response.

## Prerequisites

`tpctl` must be on PATH. Verify once per session:

```bash
tpctl --version >/dev/null
```

Use `--version` (not `tpctl list`) for the binary-presence check: `list` needs a reachable tmux server, which doesn't yet exist in isolated mode and isn't guaranteed before ride-along has confirmed `$TMUX`. A failing `list` would conflate "no tpctl binary" with "no tmux server yet."

If the binary is missing, stop and tell the user. Do not fall back to raw `tmux send-keys` + `capture-pane`; the races those introduce are exactly what tpctl exists to eliminate.

For flag details on any subcommand, run `tpctl <subcommand> --help` (e.g. `tpctl text --help`).

## Mode selection

Pick a mode based on `$TMUX`.

**Ride-along** (reuse the user's current tmux server, user sees everything live):

```bash
TPCTL_FLAGS=()
CURRENT_SESSION=$(tmux display-message -p '#S')
WINDOW_NAME="agent-$(date +%s)"
PANE=$(tmux new-window -t "$CURRENT_SESSION" -n "$WINDOW_NAME" -P -F '#{pane_id}')
MODE_NOTE="ride-along in your tmux session '$CURRENT_SESSION', window '$WINDOW_NAME', pane $PANE"
```

**Isolated** (private socket, does not touch the user's tmux):

```bash
SOCKET_DIR="${TPCTL_AGENT_SOCKET_DIR:-${TMPDIR:-/tmp}/tpctl-agent-sockets}"
mkdir -p "$SOCKET_DIR"
SOCKET="$SOCKET_DIR/agent.sock"
SESSION="agent-$(date +%s)"
PANE=$(tmux -S "$SOCKET" new-session -d -s "$SESSION" -n shell -P -F '#{pane_id}')
TPCTL_FLAGS=(--tmux-socket "$SOCKET")
MODE_NOTE="isolated session '$SESSION' on socket '$SOCKET', pane $PANE"
```

Use `"${TPCTL_FLAGS[@]}"` on every subsequent `tpctl` call so both modes work the same way.

### Attach variant: existing pane

If the user explicitly directs you to a pane they already created, skip the creation step and set `PANE` to the pane ID they named (`%N`). Because you did not create that pane, the safety rules apply with extra care: run `tpctl snapshot` first to verify what program is running there, report it back to the user, and do not send any keys until they have explicitly authorized driving that pane.

Discover panes on the default server with `tpctl list`. On a specific isolated socket: `tpctl --tmux-socket "$SOCKET" list`. For richer context (session, window, running command) use tmux directly:

```bash
tmux list-panes -a -F '#{pane_id}  #{session_name}:#{window_name}  #{pane_current_command}'
```

## Announce to the user (ALWAYS)

Immediately after creating the pane, print a copy/paste monitor command. Do it again when you finish.

Ride-along:

```
Working in $MODE_NOTE.
Switch to it with: Ctrl+b then <window-number>
  (or)  tmux select-window -t "$CURRENT_SESSION:$WINDOW_NAME"
```

Isolated:

```
Working in $MODE_NOTE.
Attach with:   tmux -S "$SOCKET" attach -t "$SESSION"
Detach again with Ctrl+b d. Snapshot once without attaching:
  tpctl --tmux-socket "$SOCKET" snapshot --pane "$PANE" --history-lines 200
```

## Token flow

Every `snapshot`, `read`, and `wait` response includes a `next` field. That string is the checkpoint for the following call. Carry it forward as `TOKEN`.

Seed a token at the start of the session:

```bash
tpctl "${TPCTL_FLAGS[@]}" snapshot --pane "$PANE"
# → {"pane_id":"%N","next":"r_000001","text":"..."}
```

Read the JSON from the tool output and remember `next` as `TOKEN`. After each subsequent `read` or `wait`, update `TOKEN` with the new `next` before the next call.

Tokens are opaque, pane-scoped, and valid until the tpctl controller restarts or the pane's 1 MiB retention buffer evicts the checkpoint. If a call returns `INVALID_AFTER`, re-seed with a fresh `tpctl snapshot` and continue.

**Shell parsing tip.** When extracting fields from a captured tpctl response in bash, prefer `jq <<< "$VAR"` (here-string) or `printf '%s' "$VAR" | jq` over `echo "$VAR" | jq`. Some bash environments interpret `\n` inside the captured JSON as a real newline, corrupting the JSON before jq sees it. The here-string form passes bytes through verbatim. For one-shot extraction, pipe directly: `TOKEN=$(tpctl snapshot --pane "$PANE" | jq -r .next)`.

## Sending input

Literal text with no shell splitting or expansion:

```bash
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" 'your command here' --enter
```

`--enter` appends an Enter keypress. Omit it when building input across multiple calls (rare).

If the text itself starts with a dash, protect it with `--` so tpctl does not try to parse it as a flag. `--` stops flag parsing, so `--enter` must come before `--`:

```bash
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" --enter -- '-starts-with-dash'
```

Control keys, one or more per call:

```bash
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" C-c
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" Escape
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" Up Up Enter
```

Both `text` and `key` return only after tmux acknowledges the input, so a following `wait` or `read` always observes what the input produced. No sleep or grace period needed between send and wait.

## Reading output

Incremental read since last token:

```bash
tpctl "${TPCTL_FLAGS[@]}" read --pane "$PANE" --after "$TOKEN"
# → {"pane_id":"%N","next":"r_000042","text":"..."}
```

`text` may be the empty string if nothing new arrived. Save the new `next` as `TOKEN` for the following call.

Full visible screen, optionally with scrollback, without advancing the stream:

```bash
tpctl "${TPCTL_FLAGS[@]}" snapshot --pane "$PANE" --history-lines 500
```

## Waiting

`wait` blocks until a condition matches or the timeout fires. Three modes.

### Sentinel (preferred for shell commands)

Print a sentinel from the command itself, then wait for it. The response includes the command's exit code, so you know success or failure without parsing output.

```bash
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" \
  'make test; printf "__DONE__:%s:%d\n" run1 $?' --enter
tpctl "${TPCTL_FLAGS[@]}" wait --pane "$PANE" --after "$TOKEN" \
  --for sentinel --token run1 --timeout-ms 600000
# → {"pane_id":"%N","next":"r_000101","result":"sentinel","matched":"__DONE__:run1:0","exit_code":0}
```

Pick a fresh `--token` per invocation (`run1`, `run2`, ...) so overlapping waits do not collide.

### Regex (for interactive prompts)

```bash
tpctl "${TPCTL_FLAGS[@]}" wait --pane "$PANE" --after "$TOKEN" \
  --for regex --pattern '^>>> ' --timeout-ms 15000
```

Patterns are RE2 (Go regexp flavor). No backreferences or lookarounds.

**Word boundaries on redraw streams.** Programs that paint with cursor positioning (`watch`, `top`, full-screen TUIs) emit no real `\n` between successive frames. After ANSI/CR strip, the stream concatenates frame after frame, so a value that looks standalone in the rendered grid is bracketed by neighboring frames' bytes in the stream. Word-boundary anchors (`\b…\b`) can therefore fail unexpectedly. For redraw programs prefer simple substring patterns, or fall back to the snapshot-and-parse pattern under "Observation-driven decisions" above.

### Quiescence (for chat TUIs and LLM agents)

When no stable prompt exists, wait until output has been silent for a window.

```bash
tpctl "${TPCTL_FLAGS[@]}" wait --pane "$PANE" --after "$TOKEN" \
  --for quiescence --ms 20000 --timeout-ms 600000
```

`--ms` is the silence window required to declare idle. Short Q&A settles in roughly 20 seconds (`--ms 20000`). Long generation with tool loops has silent stretches of 40 to 90 seconds, so use `--ms 60000` or higher. Too short and you misread a pause as complete. `--timeout-ms` must be larger than `--ms`.

Every `wait` returns a new `next`. To collect what arrived, pick the read primitive that matches the pane's shape:

- `tpctl read --pane "$PANE" --after "$TOKEN"` returns the raw byte stream since the token. Right for shell logs, incremental tails, and sentinel or regex waits on a REPL where output is append-only.
- `tpctl snapshot --pane "$PANE"` returns tmux's rendered view of the current pane contents. Right for chat TUIs and any pane whose output involves cursor rewrites, spinners, or status-bar redraws. tmux has already collapsed those to the final grid state, so a single snapshot of the settled pane is both cheaper and cleaner than tailing the byte stream.

Rule of thumb: if the pane would look readable in `tmux capture-pane -p`, use `snapshot`. If the pane is producing append-only log output, use `read --after`.

**`snapshot` is lossy on transient content.** The rendered screen reflects only what is *currently* visible: anything cleared (`clear`, Ctrl-L), scrolled off, or overwritten before your snapshot lands is gone. For "describe what just happened" on a shell pane, tail with `read --after`; the byte stream is lossless within the 1 MiB retention window. Reserve `snapshot` for panes whose redraws (chat TUIs, nvim, top, spinners) would otherwise dominate.

## Passive observation (live tail)

To follow along on a pane the user is driving — describe what they type, capture transient output, narrate idle vs active — combine `wait --for regex '(?s).'` (block on activity) with `read --after` (drain bytes). Lossless within the retention window, so a `clear` between bursts does not erase what was on screen.

The springy version: run the loop as a backgrounded task whose stdout lines stream back to the agent as events. In Claude Code that is the `Monitor` tool with `persistent: true`; other harnesses provide equivalent primitives. Each burst arrives as its own notification, so the conversation reflects pane activity in near-real-time.

```bash
reseed() { tpctl "${TPCTL_FLAGS[@]}" snapshot --pane "$PANE" 2>/dev/null | jq -r .next; }
TOKEN=$(reseed)
while true; do
  if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then sleep 1; TOKEN=$(reseed); continue; fi
  W=$(tpctl "${TPCTL_FLAGS[@]}" wait --pane "$PANE" --after "$TOKEN" \
        --for regex --pattern '(?s).' --timeout-ms 600000)
  if jq -e 'has("code")' <<< "$W" >/dev/null 2>&1; then
    case "$(jq -r .code <<< "$W")" in
      INVALID_AFTER|PANE_NOT_FOUND) TOKEN=$(reseed) ;;   # buffer evicted or pane gone
      *) : ;;                                            # TIMEOUT etc.
    esac
    continue
  fi
  sleep 0.5                                              # debounce: let the burst accumulate
  D=$(tpctl "${TPCTL_FLAGS[@]}" read --pane "$PANE" --after "$TOKEN")
  TOKEN=$(jq -r .next <<< "$D")
  TEXT=$(jq -r .text <<< "$D")
  [ -z "$TEXT" ] && continue
  # Flatten internal newlines so each burst is one notification line.
  printf '[%s] %s\n' "$(date +%H:%M:%S)" \
    "$(printf '%s' "$TEXT" | tr '\n' '|')"
done
```

Two non-obvious bits of robustness:

- **`INVALID_AFTER` recovery via `reseed`.** A long-running pane (especially one running a redraw-heavy TUI like nvim) can rotate 1 MiB through its ring buffer in minutes; the seed token gets evicted and every subsequent `wait` returns `INVALID_AFTER`. Without the case branch the loop spin-fails silently. Re-seed by snapshotting fresh.
- **`sleep 0.5` debounce.** Fixed-cadence streamers (`ping`, `top`, `watch -n 1`) push close to async-event-channel rate limits if every byte triggers an emit. The half-second nap lets multiple bytes coalesce into one drained burst.

Without an async-event facility, run the same loop synchronously inside one tool call with a fixed deadline; you trade real-time feedback for a single batch dump at the end.

Caveats:

- Shell autosuggest tooling (fish, zsh) redraws the prompt line on every keystroke, so the byte stream looks noisy. The user's actual command is in there interleaved with the autosuggest buffer; skim past it.
- For chat TUIs and other redraw-heavy programs, prefer `snapshot` after a short quiescence (see "Chat style TUIs" below) so tmux collapses the redraws for free.
- Keep the regex pattern broad — `(?s).` matches any byte. The point is to surface whatever the user does, not to grep for known patterns.
- Periodic-output programs (`ping`, `watch -n 1`, chatty `tail -f`) can still trip an async-event channel's rate limit even with the debounce. If the harness terminates the monitor with a "too much output" warning, increase the `sleep`, or switch to the snapshot-with-quiescence loop documented under "Chat style TUIs" below.

## Observation-driven decisions

When the exit condition is something tpctl's matchers don't express cleanly — multi-field correlation, computed thresholds, semantic checks — drive the loop yourself: snapshot in a tight cadence, parse the rendered text, decide, act. Cost is more bytes over the wire (one snapshot per poll); benefit is arbitrary decision logic in agent code rather than a single regex.

Example: wait for `watch -n 1 'date +%S'` to display `42`, then send Ctrl-C.

```bash
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" 'watch -n 1 "date +%S"' --enter
DEADLINE=$(($(date +%s) + 90))
while [ $(date +%s) -lt $DEADLINE ]; do
  sleep 0.4
  TEXT=$(tpctl "${TPCTL_FLAGS[@]}" snapshot --pane "$PANE" | jq -r .text)
  CUR=$(printf '%s' "$TEXT" | grep -E '^[0-9]{2}$' | tail -1)
  [ "$CUR" = "42" ] && break
done
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" C-c
```

Polling-cadence rule of thumb: keep it slightly faster than the source's update rate so each value appears in at least two consecutive snapshots. Sub-tick polling (e.g. 0.25s on a 1s tick) guarantees catching every value at the cost of more snapshots; 0.4s on 1s ticks usually catches them all but drops the occasional value as cadence drifts.

This pattern is also the right answer when wait's regex matchers behave unexpectedly on redraw streams (see the "Word boundaries" caveat under Regex above) — `snapshot` returns the rendered grid, free of cursor-positioning artifacts.

## Interactive tool recipes

Same pattern every time: start the program, wait for its prompt, send literal input, read output.

**Python REPL.** Export `PYTHON_BASIC_REPL=1` first; the fancy REPL rewrites the display and breaks literal sends.

```bash
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" \
  'PYTHON_BASIC_REPL=1 python3 -q' --enter
tpctl "${TPCTL_FLAGS[@]}" wait --pane "$PANE" --after "$TOKEN" \
  --for regex --pattern '^>>> ' --timeout-ms 10000
```

**lldb** (default debugger on macOS):

```bash
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" 'lldb ./a.out' --enter
tpctl "${TPCTL_FLAGS[@]}" wait --pane "$PANE" --after "$TOKEN" \
  --for regex --pattern '\(lldb\)' --timeout-ms 10000
```

**Other TTY apps** (ipdb, psql, mysql, node, bash): same shape. Start program, wait for its prompt regex, send literal text with `--enter`, read or wait on the response.

**vim / nvim** has two driving-specific gotchas:

1. **Auto-completion plugins (e.g. `blink.cmp`, `nvim-cmp`) consume `Enter`.** If a popup is open when you send `tpctl key Enter`, the keystroke accepts the current suggestion or expands a snippet — your next "newline" turns into a literal completion replacement and the buffer's content is not what you typed. Workaround: enter `:set paste` before insert mode, send the whole multi-line content as one `tpctl text` payload with embedded newlines (bash `$'...\n...'`), then `:set nopaste`. Paste mode disables auto-indent, auto-pairs, and completion engines for the duration of the next insert.
2. **Mid-edit `tail -N` of the snapshot is misleading.** A small file's content sits at the top of the buffer; `~` empty-line markers fill the rest of the rendered grid. Use `head -N` or the full snapshot when verifying state mid-edit.

```bash
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" 'nvim file.md' --enter
sleep 1
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" Escape          # ensure normal mode
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" ':set paste'
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" Enter
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" G
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" o
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" $'first line\nsecond line\nthird line'
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" Escape
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" ':set nopaste'
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" Enter
tpctl "${TPCTL_FLAGS[@]}" text --pane "$PANE" ':wq'
tpctl "${TPCTL_FLAGS[@]}" key --pane "$PANE" Enter
```

## Chat style TUIs (LLM-driven agent CLIs, etc.)

Chat TUIs break the REPL recipe in three ways:

- **No deterministic prompt during generation.** The input box is a single `❯` redrawn in place with a mutating spinner. Regex has nothing stable; use `--for quiescence`.
- **Newlines submit.** Any newline inside the `text` positional is sent as Enter and submits a partial message. Flatten content that spans paragraphs into one long line.
- **Escape cancels generation.** Use `tpctl key --pane "$PANE" Escape` (not `C-c`) to stop a response while keeping the session alive. `C-c` usually exits the program entirely.
- **Multi-line pastes show as opaque placeholders during composition.** Some chat TUIs (Claude Code in particular) render a `[Pasted text #N +M lines]` token in the input field instead of the actual content while the user is still composing; the content appears in the chat scroll only after submission. A `tpctl snapshot` taken mid-compose can describe the placeholder but not the bytes it stands for; expect to learn the paste content only on the next settled frame post-submit.

Input is queued during generation, so a follow-up message sent while a response is still streaming will be processed on the next turn.

### Running a quiescence wait in the background

For long generation across many turns, run the `wait` as a background task in your harness so each stabilization becomes a notification rather than blocking the conversation. In Claude Code that is the `Monitor` tool with `persistent: true`; other harnesses provide equivalent primitives. The pattern: wait for any byte (`regex '(?s).'`), then wait for ~6s of quiescence to let streaming settle, then snapshot the rendered state and dedupe by hash. One settled frame per response cycle, not per token.

```bash
reseed() { tpctl "${TPCTL_FLAGS[@]}" snapshot --pane "$PANE" 2>/dev/null | jq -r .next; }
TOKEN=$(reseed)
LAST_HASH=""
while true; do
  if [ -z "$TOKEN" ] || [ "$TOKEN" = "null" ]; then sleep 1; TOKEN=$(reseed); continue; fi
  W=$(tpctl "${TPCTL_FLAGS[@]}" wait --pane "$PANE" --after "$TOKEN" \
        --for regex --pattern '(?s).' --timeout-ms 600000)
  if jq -e 'has("code")' <<< "$W" >/dev/null 2>&1; then
    case "$(jq -r .code <<< "$W")" in
      INVALID_AFTER|PANE_NOT_FOUND) TOKEN=$(reseed) ;;
      *) : ;;
    esac
    continue
  fi
  T2=$(jq -r .next <<< "$W")
  tpctl "${TPCTL_FLAGS[@]}" wait --pane "$PANE" --after "$T2" \
    --for quiescence --ms 6000 --timeout-ms 600000 >/dev/null 2>&1
  SNAP=$(tpctl "${TPCTL_FLAGS[@]}" snapshot --pane "$PANE")
  TOKEN=$(jq -r .next <<< "$SNAP")
  TEXT=$(jq -r .text <<< "$SNAP")
  # Strip mutating status-bar lines (e.g. "● Stewing… (3m · ↓ 12k tokens)")
  # and the "※ recap:" block before hashing, so dedup compares content only.
  HASH=$(printf '%s' "$TEXT" \
    | awk '/^●/ {next} /^※ recap:/,/disable recaps in/ {next} {print}' \
    | shasum -a 1 | cut -c1-12)
  [ "$HASH" = "$LAST_HASH" ] && continue
  LAST_HASH="$HASH"
  printf '\n[%s settled]\n%s\n' "$(date +%H:%M:%S)" "$(printf '%s' "$TEXT" | tail -20)"
done
```

Do not use `read --after` for chat TUIs: it returns the raw byte stream including every spinner frame and status-bar redraw, which tmux has already collapsed to its final grid state inside `snapshot`. `read --after` is for log-style panes where output is append-only.

## Errors

Command-level failures arrive as JSON on stdout with a nonzero exit code. Runtime failures (no tmux server reachable, IPC broken, etc.) print plain diagnostics on stderr. Branch on stdout JSON; treat stderr as opaque text for the user.

The canonical codes you may encounter:

| Code | When | Recovery |
|---|---|---|
| `MISSING_AFTER` | `read` or `wait` invoked without `--after` | snapshot first, carry the `next` token |
| `INVALID_AFTER` | token is wrong pane, evicted past the 1 MiB window, or issued by a controller that has since restarted | re-seed with `tpctl snapshot` and continue |
| `PANE_NOT_FOUND` | target pane no longer exists at dispatch | call `tpctl list` to rediscover, or pick a different pane |
| `PANE_CLOSED` | a pending `wait` observed its pane being destroyed | the user closed the window or the program exited; pick another pane |
| `TIMEOUT` | `wait` hit `--timeout-ms` before its condition matched | retry with a longer timeout, or switch modes (e.g. quiescence on a chat TUI when no stable prompt exists) |

Tokens are opaque: never parse, compare, or construct them. The recovery for any token-related failure is always "snapshot again."

## Cleanup

**Ride-along.** Close the window you created, leave the user's session alone:

```bash
tmux kill-window -t "$CURRENT_SESSION:$WINDOW_NAME"
```

**Isolated.** Kill the session. If no other sessions remain on the socket, the server exits on its own:

```bash
tmux -S "$SOCKET" kill-session -t "$SESSION"
```

When finished, print the monitor or attach command one more time so the user can still inspect any output they missed.

## Safety notes

- Never send keys to a pane you did not create unless the user has explicitly authorized the attach variant. Even then, `tpctl snapshot` first to verify what program is running there before sending a single key.
- Print `$PANE` and `$MODE_NOTE` before the first `tpctl text` or `tpctl key` so the user can sanity check.
- In ride-along mode, do NOT `kill-session`; only `kill-window` on the window you opened. In the attach variant, do NOT kill anything; the user owns the pane's lifecycle.
- On `INVALID_AFTER` (controller restart or buffer eviction), re-seed with `tpctl snapshot` and carry on. Do not invent or parse token values; they are opaque.
