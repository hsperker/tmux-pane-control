# tpctl v1 Specification

## 1. Purpose

`tpctl` is a small CLI for agent-driven interaction with tmux panes.

It is designed for coding agents such as Codex that need to:

- discover tmux panes
- capture the current visible pane state
- continue reading pane output incrementally without races
- send text and keys to a pane
- wait for output conditions without polling heuristics

The design is intentionally simple, tmux-specific, and optimized for machine consumption.

---

## 2. Design goals

- Keep the API small and explicit.
- Use tmux-native terminology where it helps clarity.
- Avoid leaky exposure of tmux control-mode mechanics.
- Avoid race-prone "read from now" semantics.
- Favor stable, compact, agent-friendly JSON.
- Keep the implementation modular without overengineering.

---

## 3. Non-goals for v1

The following are explicitly out of scope for v1:

- prompt detection
- semantic screen diffs / delta APIs
- pane-kind inference in the public API
- plugin system
- remote tmux servers
- multi-user / cross-host coordination
- durable persistence or database storage
- rich terminal semantics beyond what is required for snapshots and incremental reads

---

## 4. Core model

A pane is modeled as:

1. a **current visible screen**
2. an **output stream**
3. an **opaque checkpoint token** into that stream

This is the core abstraction.

### 4.1 Consequences

- `snapshot` returns the current visible screen and a checkpoint token.
- `read --after TOKEN` returns output after that checkpoint.
- `wait --after TOKEN ...` waits on output after that checkpoint.
- `text` and `key` inject input into the pane.

### 4.2 Why this model exists

It avoids races.

Without explicit checkpoints, output that appears between `text`/`key` and `wait` can be missed.

### 4.3 Checkpoint token semantics

Checkpoint tokens are:

- **opaque** — agents must not parse, compare, or construct them
- **pane-scoped** — a token issued for `%42` is not valid for any other pane
- **controller-lifetime scoped** — a token is invalidated when the controller process restarts
- **bounded by retained stream state** — a token may become invalid if the controller has discarded the referenced portion of the output stream

A token remains valid until one of the following occurs:

- the pane is destroyed
- the controller process restarts
- the controller drops the referenced portion of the buffered output stream

A token is an in-memory cursor, not a durable bookmark. v1 makes no guarantee of persistence, cross-process portability, or cross-pane validity.

If a command receives a token that is not valid for the target pane or is no longer retained, it must fail with the `INVALID_AFTER` error code (see §9.3, §9.6).

---

## 5. Pane identity

The API uses **tmux pane IDs** like `%42` as the canonical pane identity.

The API does **not** use `session:window.pane` as the primary identity.

Reasons:

- `%pane_id` is the stable automation identity for the life of the pane.
- session/window/pane location is useful for humans, but is not the canonical identity.
- using one identity system keeps the agent contract uniform.

### 5.1 Command argument shape

All pane-taking commands use:

```bash
tpctl <command> --pane %42
```

---

## 6. CLI surface

The minimal v1 command set is:

```bash
tpctl list

tpctl snapshot --pane %42
tpctl snapshot --pane %42 --history-lines 40

tpctl read --pane %42 --after TOKEN

tpctl text --pane %42 "kubectl get pods" --enter
tpctl key --pane %42 Escape ":" "q" Enter

tpctl wait --pane %42 --after TOKEN --for sentinel --token abc123 --timeout-ms 5000
tpctl wait --pane %42 --after TOKEN --for regex --pattern "READY" --timeout-ms 5000
tpctl wait --pane %42 --after TOKEN --for quiescence --ms 250 --timeout-ms 5000
```

Commands intentionally omitted from v1:

- `inspect`
- `status`
- `delta`
- `list --details`

These may be added later if needed, but are not part of the minimal v1 contract.

---

## 7. Output and error conventions

### 7.1 Success

- success uses exit code `0`
- query-like commands print compact JSON to `stdout`
- mutating commands (`text`, `key`) print **nothing** on success

### 7.2 Command-level failure

These are failures within the tool's normal contract, such as:

- pane not found
- missing `--after`
- timeout
- invalid regex
- invalid argument combination

Behavior:

- exit code is nonzero
- structured JSON error payload is written to `stdout`

### 7.3 Tool/runtime failure

These are failures where the command contract itself is breaking down, such as:

- controller startup failure
- tmux unavailable
- IPC failure
- unhandled internal failure

Behavior:

- exit code is nonzero
- diagnostics go to `stderr`

### 7.4 No fixed exit-code taxonomy in v1

For v1, only zero vs nonzero matters.

### 7.5 Error code taxonomy

v1 defines a small canonical set of error codes for common command-level failures. Implementations **must** use these codes where applicable:

- `MISSING_AFTER` — a command that requires `--after` was invoked without one
- `INVALID_AFTER` — the supplied checkpoint token is not valid for the target pane or is no longer retained
- `PANE_NOT_FOUND` — the target pane does not exist at dispatch time
- `PANE_CLOSED` — the pane existed when the operation began but was destroyed before it could complete (see §11.9)
- `TIMEOUT` — `wait` exceeded its `--timeout-ms` before its condition matched

### Precedence

When multiple canonical conditions could apply, use this order:

1. a pending `wait` observes its pane being destroyed → `PANE_CLOSED`
2. a command is dispatched for a pane that no longer exists → `PANE_NOT_FOUND`
3. a token is invalid **and** the pane lookup already fails → prefer `PANE_NOT_FOUND` over `INVALID_AFTER`

Implementations **may** emit additional error codes for other command-level failures (for example, regex compile failure, invalid argument combinations, controller unavailable, pane closed mid-operation), but must preserve the standard JSON error shape defined in §7.2 and §8.

Rule of thumb:

- canonical cases **must** use the canonical names above
- non-canonical failures **may** use implementation-defined codes

---

## 8. JSON response design

### 8.1 General rules

- compact JSON by default
- no `status` field
- no `op` field
- no `schema` field
- no redundant wrappers such as `data`
- omit absent fields
- use short but readable field names

### 8.2 Field naming

Use:

- `pane_id`
- `next`
- `text`
- `scrollback_text`
- `result`
- `matched`
- `exit_code`
- `code`
- `message`

### 8.3 Text normalization

All text-bearing JSON fields in v1 use a single uniform representation:

- **ANSI-stripped** — control sequences (CSI, OSC, etc.) are removed
- **newline-normalized** — line endings are normalized to `\n`; carriage returns are not preserved
- **trailing whitespace trimmed per line** — right-margin padding is removed, internal spacing and indentation are preserved
- **trailing blank lines trimmed** — empty lines at the end of the field value are removed

This applies uniformly to:

- `snapshot.text`
- `snapshot.scrollback_text`
- `read.text`

For `snapshot`, `text` represents the pane's **rendered visible screen rows**, not reconstructed logical shell lines. Wrapped output appears as the wrapped rows tmux is showing.

This is not full terminal emulation. Spinners, progress bars, and other carriage-return-based redraw effects are flattened into their post-normalization text form.

---

## 9. Command contracts

## 9.1 `list`

Returns pane IDs only.

### Success example

```json
{
  "panes": ["%42", "%43", "%44"]
}
```

### Notes

- no additional metadata in v1
- no human-readable location data in v1

---

## 9.2 `snapshot`

Captures the current visible pane state and returns a checkpoint token.

### Semantics

- `text` = current visible screen
- `next` = checkpoint token for future `read`/`wait`
- snapshot is the bootstrap operation for observation

### Default behavior

Visible screen only.

### Optional behavior

`--history-lines N` includes bounded scrollback context above the visible screen.

When history is requested:

- `scrollback_text` = prior out-of-view lines included from scrollback
- `text` = current visible screen

### Success example (no history)

```json
{
  "pane_id": "%42",
  "next": "r_000205",
  "text": "$ kubectl get pods\nNo resources found in default namespace.\n$ "
}
```

### Success example (with history)

```json
{
  "pane_id": "%42",
  "next": "r_000205",
  "scrollback_text": "previous line 1\nprevious line 2\n",
  "text": "$ current visible screen here"
}
```

---

## 9.3 `read`

Returns output after an explicit checkpoint token.

### Required arguments

- `--pane %...`
- `--after TOKEN`

### Semantics

- reads the pane output stream after `TOKEN`
- always returns `next`
- `text` may be empty
- empty `text` is still a success

### Success example with new output

```json
{
  "pane_id": "%42",
  "next": "r_000220",
  "text": "kubectl get pods\nNo resources found in default namespace.\n$ "
}
```

### Success example with no new output

```json
{
  "pane_id": "%42",
  "next": "r_000220",
  "text": ""
}
```

### Failure example: missing token

```json
{
  "pane_id": "%42",
  "code": "MISSING_AFTER",
  "message": "read requires --after; use snapshot to bootstrap"
}
```

### Failure example: invalid or expired token

```json
{
  "pane_id": "%42",
  "code": "INVALID_AFTER",
  "message": "checkpoint token is not valid for this pane or is no longer retained"
}
```

---

## 9.4 `text`

Sends literal text to a pane.

### Required arguments

- `--pane %...`
- text argument

### Optional arguments

- `--enter`

### Success behavior

- prints nothing
- exits `0`

### Ordering guarantee

`text` returns only after the controller has received tmux's acknowledgement that the send command was processed. This is an acknowledgement of tmux command processing — **not** proof that the target program has already reacted to the input.

Combined with `wait`'s semantics in §9.6, this gives the race-free `snapshot → text → wait` pattern.

### Failure example

```json
{
  "pane_id": "%42",
  "code": "PANE_NOT_FOUND",
  "message": "pane not found"
}
```

---

## 9.5 `key`

Sends named keys and key-like tokens to a pane.

### Required arguments

- `--pane %...`
- one or more key tokens

### Examples

```bash
tpctl key --pane %42 Escape Enter
tpctl key --pane %42 C-c
tpctl key --pane %42 Up Down Enter
tpctl key --pane %42 Escape ":" "q" Enter
```

### Key vocabulary

`tpctl key` uses tmux's `send-keys` key vocabulary. The full table of accepted key names and modifier forms (e.g. `Enter`, `Escape`, `Tab`, `BSpace`, arrow keys, `F1`–`F12`, `C-<x>`, `M-<x>`, `S-<x>`) is defined by `tmux(1)`. The spec does not duplicate that table.

### Token handling rules

- each CLI argument after `--pane` is one key token
- tokens are treated as **data**, not as tmux command syntax
- the controller must invoke tmux with argv-style arguments, never by building a shell command string
- there is no shell interpolation, no command concatenation, no `;` separator behavior, and no way to escape into arbitrary tmux commands
- a token such as `";"` is a literal token (or an invalid key token), never a command separator

### When to use `text` vs `key`

- use `text` for arbitrary literal text input
- use `key` for named keys, modifiers, navigation, and control sequences

Using `key` to type long text is discouraged.

### Success behavior

- prints nothing
- exits `0`

### Ordering guarantee

`key` returns only after the controller has received tmux's acknowledgement that the send command was processed. The same caveat as §9.4 applies: this is not proof that the target program has reacted.

### Failure example: invalid key token

```json
{
  "pane_id": "%42",
  "code": "INVALID_KEY",
  "message": "invalid key token: FooBarKey"
}
```

`INVALID_KEY` is an implementation-defined code in v1 (§7.5), not part of the canonical set.

---

## 9.6 `wait`

Waits on output **after an explicit checkpoint token**.

### Required arguments

- `--pane %...`
- `--after TOKEN`
- `--timeout-ms N`
- one of the `--for ...` modes below

### Match input

All match modes operate on the **ANSI-stripped post-checkpoint output stream**, treated as one continuous text buffer including newlines. Control sequences (CSI, OSC, etc.) are removed before matching.

This is not full terminal emulation. For rich TUIs, use `--for quiescence` followed by `snapshot`.

### Supported modes in v1

#### Sentinel mode

```bash
tpctl wait --pane %42 --after TOKEN --for sentinel --token abc123 --timeout-ms 5000
```

Sentinel mode searches for the structured form:

```text
__DONE__:<token>:<exit-code>
```

where:

- `__DONE__:` is the fixed prefix
- `<token>` is the literal value passed via `--token`
- `<exit-code>` is one or more decimal digits

On success, the response includes the full `matched` sentinel and the parsed `exit_code` as an integer.

Constraints on `--token` values in v1:

- must not contain `:`
- must not contain newline

This keeps parsing unambiguous.

#### Regex mode

```bash
tpctl wait --pane %42 --after TOKEN --for regex --pattern "READY" --timeout-ms 5000
```

Regex mode uses **RE2** (the Go `regexp` flavor). RE2 does not support backreferences or lookaround; this is intentional for bounded-time matching.

No implicit anchoring is applied. The pattern is evaluated against the full post-checkpoint text buffer. By default `.` does not match newline; use `(?s)` to enable dotall if needed.

#### Quiescence mode

```bash
tpctl wait --pane %42 --after TOKEN --for quiescence --ms 250 --timeout-ms 5000
```

Quiescence means the pane has stopped producing output. Any byte appended to the retained output stream after the supplied checkpoint counts as activity and resets the quiet timer. Control sequences count as activity — quiescence tracks stream-level activity, not rendered semantic change, so a busy TUI emitting only control output is not treated as quiet.

Let:

- `T_checkpoint` = time the checkpoint token was created
- `T_last` = timestamp of the most recent output byte appended after that checkpoint, if any

Quiescence is satisfied at time `now` iff:

```text
now - max(T_checkpoint, T_last_if_present) >= ms
```

In words:

- if no output has appeared after the checkpoint, the quiet window is measured from the checkpoint time
- if output has appeared after the checkpoint, the quiet window is measured from the most recent output byte
- if the pane is already quiet for at least `--ms` when the wait is registered, the wait succeeds immediately

On success, quiescence returns only `result: "quiescence"` and `next`. It does not return `matched`. The returned `next` corresponds to the stream position at the moment quiescence is declared satisfied, so a follow-up `read --after <next>` is guaranteed to see only strictly later output.

### Semantics

- `wait` matches against **all retained output after the checkpoint token**, including output that was already buffered by the controller at the moment `wait` was registered, as well as output that arrives later
- it does **not** match already-visible or already-buffered text that predates the checkpoint
- success always returns `next`
- success always returns `result`
- `matched` is returned only when relevant

Together with the send-ack guarantee of `text` and `key` (§9.4, §9.5), this makes the `snapshot → text → wait` pattern race-free: fast output produced between the send and the `wait` registration is not missed, because `wait` scans the retained stream from the token forward.

### Concurrency

Multiple `wait` calls against the same pane are fully supported. Each `wait` is a passive, read-only observer of the retained output stream; registering a `wait` does not acquire any exclusive lock on the pane.

Consequences:

- an agent may have any number of concurrent `wait`s outstanding on the same pane, with different checkpoint tokens, modes, and patterns
- `wait`s compose freely with concurrent `read` calls on the same pane
- each `wait` is evaluated independently and may succeed or time out on its own
- `text` and `key` are serialized by the single-writer controller loop, but they do not block or cancel registered `wait`s

### Success example: sentinel

```json
{
  "pane_id": "%42",
  "next": "r_000240",
  "result": "sentinel",
  "matched": "__DONE__:abc123:0",
  "exit_code": 0
}
```

### Success example: regex

```json
{
  "pane_id": "%42",
  "next": "r_000248",
  "result": "regex",
  "matched": "READY"
}
```

### Success example: quiescence

```json
{
  "pane_id": "%42",
  "next": "r_000252",
  "result": "quiescence"
}
```

### Failure example: timeout

```json
{
  "pane_id": "%42",
  "code": "TIMEOUT",
  "message": "wait timed out"
}
```

---

## 10. Architectural patterns

The implementation should explicitly follow these patterns:

- **Ports and Adapters**: keep the core independent from CLI, IPC, and tmux-specific mechanics.
- **Single-writer event loop**: one controller loop owns the tmux connection and all mutable pane state.
- **Explicit state machines**: make transport and wait lifecycles explicit instead of implicit.
- **Command-query separation**: keep mutating operations (`text`, `key`) distinct from query/sync operations (`list`, `snapshot`, `read`, `wait`).
- **Checkpointed stream abstraction**: model each pane as a visible screen plus an output stream with opaque checkpoints.
- **Facade over tmux**: isolate control-mode parsing, capture, send-keys, and resync logic behind one tmux adapter.

### Implementation note

Implement the tool as a small hexagonal system with a tmux facade adapter and a single controller event loop that owns pane state. Model pane output as a checkpointed stream. Keep command handlers thin, use explicit state transitions for transport and waits, and separate command-style operations from query-style operations. Avoid overengineering.

---

## 11. Controller lifecycle and IPC

`tpctl` is a single binary with two logical roles:

- a short-lived **CLI frontend** (one process per invocation)
- a long-lived **controller process** that owns tmux state and pane buffers

### 11.1 One controller per tmux server

There is exactly one controller per tmux server. Checkpoint tokens are valid only within the controller instance that issued them (see §4.3).

### 11.2 Default behavior: auto-spawn

On each invocation the CLI:

1. determines which tmux server it is targeting
2. derives the controller socket path for that server
3. attempts to connect to the controller
4. if no controller is running, starts one automatically
5. reconnects and proceeds with the command

### 11.3 Explicit control: `tpctl daemon`

A `tpctl daemon` subcommand is also provided for users who want to start, supervise, or debug the controller explicitly. It has the same effect as auto-spawn but runs in the foreground.

### 11.4 Socket location

Controller sockets live under a well-known per-user runtime path, for example:

```text
$XDG_RUNTIME_DIR/tpctl/
```

The socket name is derived from the target tmux server identity so that distinct tmux servers get distinct controllers.

### 11.5 Startup race

Two simultaneous CLI invocations must not both spawn a controller. Implementations use a lock file or equivalent coordination to serialize spawn attempts.

### 11.6 Reconnect flow

If the initial connect fails:

- attempt to spawn a controller
- wait briefly for readiness
- retry the connect
- if the connect still fails, emit a runtime failure on `stderr` (§7.3)

### 11.7 Controller restart

If the controller restarts, all previously issued checkpoint tokens are invalidated. The agent must call `snapshot` to obtain a new token. This matches the token lifetime rule in §4.3.

### 11.8 Output stream retention

The controller retains a bounded in-memory output stream per pane using a ring buffer.

Policy for v1:

- the retention budget is **1 MiB per pane**, measured in bytes of pane output
- when new output would exceed the budget, the oldest retained output for that pane is evicted
- any checkpoint token that points before the new retained start becomes invalid

If a command is invoked with a token that refers to evicted output, it must fail with `INVALID_AFTER`:

```json
{
  "pane_id": "%42",
  "code": "INVALID_AFTER",
  "message": "checkpoint token is no longer retained"
}
```

Rationale:

- bytes (not lines) are simpler and more predictable, avoid line-splitting edge cases, and work uniformly for shell output and TUI chatter
- a fixed per-pane cap keeps memory bounded and behavior easy to explain
- very high-volume panes may invalidate old tokens relatively quickly; this is an accepted trade-off for v1

Explicitly out of scope for v1:

- multi-dimensional policies (e.g. "N lines or M bytes")
- global LRU fairness across panes
- persistence across controller restarts
- configurable retention exposed in the public API

### 11.9 Pane lifecycle

Panes can disappear at any time (user closes them, their process exits, `kill-pane`, etc.). The controller handles this as follows:

- **Pending `wait` observes destruction** — the wait fails with `PANE_CLOSED` (see §7.5). The pane existed when the wait was registered but ceased to exist before the wait could complete.
- **New command for a missing pane** — `read`, `snapshot`, `wait`, `text`, and `key` fail with `PANE_NOT_FOUND` when the pane does not exist at dispatch time.
- **Retained stream on destruction** — the pane's retained output stream is dropped immediately when the pane is destroyed. Subsequent commands for that `%pane_id` fail with `PANE_NOT_FOUND`, not `INVALID_AFTER`.

Example `PANE_CLOSED` failure:

```json
{
  "pane_id": "%42",
  "code": "PANE_CLOSED",
  "message": "pane was destroyed while wait was pending"
}
```

#### tmux server restart

A tmux server restart is not a pane-level event. It is a controller-level event:

- the controller's tmux connection breaks
- the controller treats this as a runtime failure (§7.3) and may exit or attempt to reconnect
- on controller restart, all previously issued checkpoint tokens are invalidated (§4.3)
- `PANE_CLOSED` is **not** used for server-wide restart scenarios — it is reserved for pane-level destruction within an otherwise live controller

---

## 12. Suggested internal structure

A Go-oriented layout could look like:

```text
cmd/tpctl/main.go
internal/cli/
internal/ipc/
internal/controller/
internal/domain/
internal/tmuxctl/
internal/waiter/
internal/store/
```

### Responsibilities

- `cli/`: argument parsing, stdout/stderr behavior
- `ipc/`: controller connection, request/response transport
- `controller/`: event loop, request dispatch, checkpoint ownership
- `domain/`: request/response types, state enums, core abstractions
- `tmuxctl/`: tmux control mode, send/capture/subscription handling, resync
- `waiter/`: sentinel/regex/quiescence evaluation
- `store/`: in-memory pane state and output stream buffers

---

## 13. Internal state machines

These are internal, not part of the public API.

### 13.1 Transport state

Suggested states:

- `connecting`
- `live`
- `paused`
- `resyncing`
- `failed`

### 13.2 Wait lifecycle state

Suggested states:

- `pending`
- `matched`
- `timed_out`
- `failed`

These should be explicit in code rather than inferred ad hoc.

---

## 14. Internal tmux integration guidance

The tmux adapter should be responsible for:

- maintaining the control-mode connection
- capturing visible snapshots
- capturing scrollback when requested
- sending text and keys
- consuming pane output notifications
- maintaining output-stream ordering
- issuing/advancing checkpoint tokens
- handling pause/resume/resync behavior

The rest of the system should not depend on tmux protocol details.

Do not expose tmux mechanics such as:

- `refresh-client`
- `pause-after`
- control-mode frame syntax
- `capture-pane`
- subscriptions / notification names

These stay inside the tmux facade.

---

## 15. Explicit simplifications for v1

To keep v1 simple and correct:

- no pane-kind detection in the public API
- no delta API
- no prompt detection
- no implicit "read from now"
- no implicit "wait from now"
- no default timeout for `wait`
- no metadata-heavy `list`
- no success payload for `text` or `key`

---

## 16. Agent usage patterns

## 16.1 Bootstrap observation

```bash
tpctl snapshot --pane %42
```

Agent stores `next`.

## 16.2 Bootstrap with context

```bash
tpctl snapshot --pane %42 --history-lines 40
```

Agent gets:

- `scrollback_text`
- `text`
- `next`

## 16.3 Observe incrementally

```bash
tpctl read --pane %42 --after TOKEN
```

## 16.4 Send a command and wait for a sentinel

```bash
tpctl snapshot --pane %42
# store next = r_100

tpctl text --pane %42 "kubectl get pods; printf '__DONE__:abc123:%d\\n' $?" --enter

tpctl wait --pane %42 --after r_100 --for sentinel --token abc123 --timeout-ms 5000
```

## 16.5 Send keys to a TUI and wait for quiet

```bash
tpctl snapshot --pane %42
# store next = r_200

tpctl key --pane %42 Escape "/" "pods" Enter

tpctl wait --pane %42 --after r_200 --for quiescence --ms 250 --timeout-ms 3000
```

---

## 17. Acceptance criteria

An implementation is acceptable for v1 if it satisfies all of the following.

### Core commands

1. `list` returns only `%pane_id` strings.
2. `snapshot` returns visible screen text and a usable checkpoint token.
3. `snapshot --history-lines N` returns `scrollback_text` and `text` distinctly.
4. `snapshot.text` reflects the pane's rendered visible screen rows, not reconstructed logical shell lines.
5. `read --after TOKEN` never rereads pre-token output.
6. `read` may return empty `text` and still succeed.

### Checkpoint tokens

7. Checkpoint tokens are opaque, pane-scoped, and controller-lifetime scoped.
8. `read --after TOKEN` fails with `INVALID_AFTER` when the token is wrong for the pane, refers to evicted output, or was issued by a prior controller instance.
9. `wait --after TOKEN` fails with `INVALID_AFTER` under the same conditions.

### Stream retention

10. The retained output stream is bounded to 1 MiB per pane.
11. Eviction of retained output invalidates checkpoints that point before the new retained start.

### Wait contract

12. `wait` requires `--timeout-ms`.
13. `wait` supports exactly: `sentinel`, `regex`, `quiescence`.
14. `wait --after TOKEN` considers already-buffered post-token output at registration time, not just future arrivals.
15. `wait --after TOKEN` cannot miss fast output that occurs after the checkpoint.
16. `wait` always returns `next` on success.
17. Multiple concurrent `wait`s on the same pane are allowed and evaluated independently.

### Matching semantics

18. `wait --for regex` uses RE2.
19. Regex and sentinel matching operate on ANSI-stripped, `\n`-normalized post-checkpoint text.
20. Matching is performed against one continuous text buffer with no implicit anchoring.
21. `wait --for sentinel --token TOKEN` matches the structured form `__DONE__:<token>:<exit-code>`.
22. On sentinel success, the response includes both `matched` and `exit_code` (integer).
23. `wait --for quiescence` treats any appended byte after the checkpoint as activity.
24. `wait --for quiescence` may succeed immediately if already satisfied at registration time.

### Text representation

25. `snapshot.text`, `snapshot.scrollback_text`, and `read.text` are all: ANSI-stripped, `\n`-normalized, trailing-whitespace-trimmed per line, and trailing-blank-lines-trimmed.

### Input commands

26. `text` and `key` do not return until tmux has acknowledged processing the send command.
27. `text` and `key` emit no success payload.
28. `text` and `key` produce no stdout at all on success.
29. `key` uses tmux's `send-keys` key vocabulary; invalid tokens fail cleanly with a structured JSON error.
30. `key` tokens are treated as data, never as tmux command syntax.

### Controller model

31. There is exactly one controller per tmux server.
32. The CLI auto-spawns a controller on demand when none is running for the target server.
33. An explicit `tpctl daemon` subcommand is provided.

### Pane lifecycle

34. A pending `wait` whose pane is destroyed fails with `PANE_CLOSED`, distinct from `TIMEOUT` and `PANE_NOT_FOUND`.
35. When a pane is destroyed, its retained stream is dropped immediately; later commands for that `%pane_id` fail with `PANE_NOT_FOUND`, not `INVALID_AFTER`.

### Errors

36. Command-level failures emit structured JSON on `stdout` with a nonzero exit code.
37. Runtime or controller failures emit diagnostics on `stderr` with a nonzero exit code.
38. The canonical error codes `MISSING_AFTER`, `INVALID_AFTER`, `PANE_NOT_FOUND`, `PANE_CLOSED`, and `TIMEOUT` are used where applicable.

### Architecture

39. The implementation follows the architectural patterns in §10.

---

## 18. Future extensions (not in v1)

Possible later additions:

- `list --details`
- human-friendly pane location mapping
- optional `inspect`
- richer diagnostic commands
- prompt-aware helpers
- pane-selection helpers for humans

These are intentionally excluded from the v1 contract.
