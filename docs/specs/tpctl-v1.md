# tpctl v1 Specification

## 1. Purpose

`tpctl` is a small CLI for agent-driven interaction with tmux panes.

It is designed for coding agents such as Codex that need to:

- discover tmux panes
- capture the current visible pane state (optionally with scrollback)
- continue reading pane output incrementally without races
- send text and keys to a pane with a clear send-ack guarantee
- wait for output conditions without polling heuristics, using sentinel, regex, or quiescence modes

The design is intentionally simple, tmux-specific, and optimized for machine consumption. A short-lived CLI frontend talks to an auto-spawned controller process that owns tmux state and per-pane output stream buffers behind opaque checkpoint tokens (see §19 for a quick reference).

---

## 2. Design goals

- Keep the API small and explicit.
- Use tmux-native terminology where it helps clarity.
- Avoid leaky exposure of tmux control-mode mechanics.
- Avoid race-prone "read from now" semantics — every observation is anchored to an explicit checkpoint token.
- Favor stable, compact, agent-friendly JSON with uniform text normalization.
- Give agents deterministic, testable guarantees (send-ack ordering, lock-free concurrent waits, structured sentinels with parsed exit codes).
- Keep the implementation modular without overengineering: one controller per tmux server, hexagonal internals, bounded in-memory state.

---

## 3. Non-goals for v1

The following are explicitly out of scope for v1:

- prompt detection
- semantic screen diffs / delta APIs
- pane-kind inference in the public API
- plugin system
- remote tmux servers
- multi-user / cross-host coordination
- durable persistence or database storage (retained output is in-memory only; see §11.8)
- rich terminal semantics beyond what is required for snapshots and incremental reads
- preserving raw ANSI escape sequences in JSON output (all text fields are stripped — see §8.3)
- exposing regex submatches or capture groups in `wait --for regex` responses
- human-friendly pane location (session:window.pane) in the public API; `%pane_id` is the sole identity (see §5)
- configurable retention budgets exposed via the public API

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
- **carry enough state for quiescence** — for any still-valid token, implementations must be able to recover the checkpoint reference time used by §9.6 quiescence. This may be encoded in the token itself or stored in controller-side state associated with that token.

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

### 6.1 Argument parsing

Implementations must accept flags and positional arguments in interleaved order. A flag appearing after a positional argument must still be parsed as a flag, and a positional argument appearing between flags must still be delivered to the command handler. The `--` sentinel ends flag parsing; every argument after `--` is treated as positional, even if it begins with `-`.

The following invocations are all equivalent (all pass `"hello"` as the text payload and the `Enter` flag):

```bash
tpctl text --pane %42 "hello" --enter
tpctl text --pane %42 --enter "hello"
```

The `--` sentinel lets callers pass literal payloads or key tokens that would otherwise be parsed as flags:

```bash
tpctl text --pane %42 -- "--starts-with-dashes"
tpctl key  --pane %42 -- -l
```

In the `key` example, `-l` is sent as a literal key token to tmux; it is not parsed as a CLI flag.

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
3. Pane-existence validation must run before token-to-pane validation. If the target pane does not exist at dispatch time, the command fails with `PANE_NOT_FOUND`, even if the supplied token is malformed, expired, controller-instance-mismatched, or references a different pane.

Note: CLI argument parsing (including detection of a missing required `--after`, which surfaces as `MISSING_AFTER`) precedes pane-existence validation. `MISSING_AFTER` is therefore always reported before any pane lookup is attempted.

Implementations **may** emit additional error codes for other command-level failures (for example, regex compile failure, invalid argument combinations, controller unavailable, pane closed mid-operation), but must preserve the standard JSON error shape defined in §7.2 and §8.

Rule of thumb:

- canonical cases **must** use the canonical names above
- non-canonical failures **may** use implementation-defined codes

### 7.6 Error payload shape

- `code` and `message` are always present on command-level failures
- `pane_id` is present when the command targeted a specific pane **and** that pane was identified
- `pane_id` is omitted when the command is not pane-scoped (e.g. `list`), when pane resolution failed before a specific pane identity was established, or when the error concerns global arguments or controller/server selection

---

## 8. JSON response design

### 8.1 General rules

- compact JSON by default
- no `status` field
- no `op` field
- no `schema` field
- no redundant wrappers such as `data`
- omit fields that are **not applicable** to the command result; fields that are part of the command's primary payload remain present even when empty (e.g. `panes: []`, `text: ""`, or `scrollback_text: ""` when history was explicitly requested)
- use short but readable field names
- checkpoint tokens (`next`, `--after`) are always JSON strings; they are opaque and must not be parsed by callers

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

- `panes` is always present, even when the list is empty (`{"panes": []}`)
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

Rules:

- default when omitted: no history; `scrollback_text` is not included
- `N = 0` is valid and means "history mode requested, zero lines"; `scrollback_text: ""` is included
- v1 does not impose a spec-level maximum on `N`; implementations may cap it, and tmux's own scrollback buffer is the practical ceiling
- if the pane's retained scrollback is shorter than the requested `N` lines, `scrollback_text` contains all available scrollback and the response still succeeds. Implementations must not pad missing lines with blanks and must not treat short scrollback as an error.

### Field presence

- `text` is always present; if the pane is empty, `text: ""`
- `scrollback_text` is present **iff** `--history-lines` was supplied; when requested, it is always present, even as `scrollback_text: ""` if no scrollback was available

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
- **exactly one** positional text argument

Passing zero or more than one positional argument is a command-level error.

### Optional arguments

- `--enter`

### Argument rules

- the text payload is sent **literally**, including embedded newlines if present
- an empty payload is valid: `tpctl text --pane %42 "" --enter` means "press Enter after sending nothing"
- `--enter` appends an `Enter` key press after the literal payload has been sent; it is **not** equivalent to appending `\n` to the payload
- `--enter` does not rewrite, trim, or normalize the supplied text — a trailing newline in the payload plus `--enter` results in `\n` followed by `Enter`
- there is no `--no-enter` flag; absence of `--enter` is the opposite

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

### Validation scope

`tpctl key` delegates key-token interpretation to tmux's `send-keys` behavior and does not maintain an exhaustive token whitelist. As a result, whether a token is rejected is tmux-version-dependent; modern tmux versions may accept unknown names permissively, for example by treating them as literal character sequences rather than producing an error. When tmux rejects a token sequence, implementations must surface that as a structured command-level error. When tmux accepts a token sequence, `tpctl key` succeeds and no validation error is raised.

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

All wait match modes operate on the post-checkpoint output stream after the following transformations are applied, in order:

1. ANSI / ESC-introduced control sequences are removed.
2. Carriage returns are removed: `\r\n` is normalized to `\n`, and stray `\r` is dropped.

Newlines (`\n`) are preserved. Match input is treated as one continuous text buffer including newlines. Text-field normalizations such as trailing-whitespace trimming and trailing-blank-line trimming do **not** apply to match input; those apply only to JSON text fields such as `snapshot.text`, `snapshot.scrollback_text`, and `read.text`.

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
- `<exit-code>` matches `[0-9]+` — unsigned decimal digits only, leading zeros allowed

On success, the response includes the full `matched` sentinel and the parsed `exit_code` as an integer (so `__DONE__:abc123:007` yields `exit_code: 7`). v1 does not enforce an upper bound on `exit_code` at the matcher level; shell exit codes are conventionally 0–255, and values beyond that are a caller concern. A signed form such as `__DONE__:abc123:-1` is not a sentinel match.

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

On success, `matched` is the whole match (group 0 equivalent). v1 does not expose submatches or capture groups in the response; callers that need structured extraction can re-run a regex client-side against `matched` or use sentinel mode.

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

On success, quiescence returns only `result: "quiescence"` and `next`. It does not return `matched`. The general `next` guarantee in the Semantics subsection below applies.

### Semantics

- `wait` matches against **all retained output after the checkpoint token**, including output that was already buffered by the controller at the moment `wait` was registered, as well as output that arrives later
- it does **not** match already-visible or already-buffered text that predates the checkpoint
- success always returns `next`
- success always returns `result`

#### Field presence on success

On success, `wait` always includes `pane_id`, `next`, and `result`. Additional fields are mode-specific and are omitted entirely when not applicable.

- sentinel
    - `matched`: present; full matched sentinel literal `__DONE__:<token>:<exit-code>`
    - `exit_code`: present; integer
- regex
    - `matched`: present; whole match (group 0 equivalent)
    - `exit_code`: absent
- quiescence
    - `matched`: absent
    - `exit_code`: absent

Fields marked absent are omitted from the JSON output entirely, not emitted as `null`.

#### `next` on success

On success, `wait` returns a `next` token that corresponds to the controller's current observed stream head at the evaluation step that satisfied the wait condition. A follow-up `read --after <next>` is guaranteed to return only output appended strictly later than that stream position.

`next` is not defined as the byte immediately following a regex or sentinel match. Implementations may return a stream position that includes additional output observed in the same evaluation step. Callers must not derive "post-match tail" semantics from `next`; callers that need exact post-match interpretation must use `matched` and their own parsing.

Together with the send-ack guarantee of `text` and `key` (§9.4, §9.5), this makes the `snapshot → text → wait` pattern race-free: fast output produced between the send and the `wait` registration is not missed, because `wait` scans the retained stream from the token forward.

### Concurrency

Multiple `wait` calls against the same pane are fully supported. Each `wait` is a passive, read-only observer of the retained output stream; registering a `wait` does not acquire any exclusive lock on the pane.

Consequences:

- an agent may have any number of concurrent `wait`s outstanding on the same pane, with different checkpoint tokens, modes, and patterns
- `wait`s compose freely with concurrent `read` calls on the same pane
- each `wait` is evaluated independently and may succeed or time out on its own
- a single output append may satisfy zero, one, or many pending `wait`s on the same pane; there is no ordering guarantee between their responses
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

#### Daemon lifetime

An auto-spawned controller must outlive the CLI invocation that spawned it. Implementations must decouple the controller process lifetime from the spawning CLI so that the controller is not terminated merely because the CLI exits, loses its controlling terminal, or is reaped as part of the CLI's process tree by an automation harness. On Unix, this typically means starting the controller in a new session/process group and closing or redirecting inherited standard streams and any controlling terminal handles. A controller that is expected to persist but is routinely terminated when its spawning CLI exits is not conforming.

The same lifetime requirement applies when the controller is started via `tpctl daemon` in background-style usage (§11.3).

### 11.3 Explicit control: `tpctl daemon`

A `tpctl daemon` subcommand is also provided for users who want to start, supervise, or debug the controller explicitly. It has the same effect as auto-spawn but runs in the foreground.

### 11.4 tmux server identity and socket location

The canonical identity of a tmux server is its **resolved socket path** (what tmux itself uses to key a server). This is resolved in the following precedence order:

1. an explicit `--tmux-socket PATH` or `--tmux-socket-name NAME` flag on the `tpctl` invocation (mirroring tmux's `-S` and `-L`)
2. the `$TMUX` environment variable, if set, by extracting the socket path from its leading component
3. the tmux default socket path (typically `/tmp/tmux-$UID/default`)

Passing both `--tmux-socket` and `--tmux-socket-name` in the same invocation is a command-level error — they are alternative ways to identify the same server.

The controller socket path is derived by hashing the resolved tmux socket path and placing it under the per-user runtime directory:

```text
$XDG_RUNTIME_DIR/tpctl/<hash-of-resolved-tmux-socket-path>.sock
```

Hashing is used to keep the path short, filesystem-safe, and stable across custom tmux socket paths. Distinct tmux servers therefore get distinct controllers automatically, and `tpctl` can target multiple tmux servers from the same user session via the flags above.

#### Fallback when `$XDG_RUNTIME_DIR` is unset

When `$XDG_RUNTIME_DIR` is unset (common on macOS and in minimal Linux environments), implementations must fall back to `$TMPDIR/tpctl-$UID/`, or `/tmp/tpctl-$UID/` if `$TMPDIR` is also unset. The runtime directory must be created with mode `0700`. Implementations must ensure that the controller socket is accessible only to the current user; creating the socket with mode `0600` is the preferred mechanism where supported.

#### Hash choice

The hash function and encoding used for deriving the controller socket name are implementation-defined. They must be deterministic, produce path-safe output (hex or base64url are typical choices), and be long enough that collisions between distinct tmux socket paths are practically negligible — at least 8 bytes of a cryptographic hash is sufficient. Cross-implementation socket-name interoperability is not required because each implementation controls both ends of the derivation.

### 11.5 Startup race

When auto-spawning a controller, implementations must coordinate concurrent CLI invocations so that at most one controller becomes active for a given tmux server identity. Other concurrent invocations must wait briefly and retry the controller socket connection rather than starting independent controllers. Callers either connect successfully after a bounded retry path or receive a runtime failure on `stderr` (§7.3).

The specific coordination mechanism is implementation-defined. `flock` on a per-server lock file, an atomic `bind()` race on the socket path, or a pidfile with advisory locking are all acceptable — the spec mandates only the observable guarantee, not the primitive.

#### Coordination order

Whatever coordination primitive is used, implementations must re-check controller liveness after acquiring the coordination token and before deciding to spawn. The required sequence is:

1. try to connect to the controller socket
2. if that fails, acquire the coordination token
3. try to connect again
4. if step 3 still fails, spawn the controller
5. keep the coordination token until the new controller is reachable or startup has definitively failed

Step 3 exists to close a TOCTOU race: a concurrent invocation may have won the coordination race between step 1 and step 2, so the second connect attempt may succeed without spawning.

*Non-normative:* a readiness poll window on the order of 1–5 seconds is reasonable for interactive use.

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
- retained stream positions are interpreted as a half-open interval `[start, end)`. A token remains valid when its offset equals the current retained `start`. Only tokens whose offset is strictly less than the retained `start` are evicted; subsequent `read` or `wait` calls using such tokens fail with `INVALID_AFTER`.

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

A tmux server restart is not a pane-level event. It is a controller-level event.

The controller must detect loss of its tmux server connection. Detection mechanisms are implementation-defined, but the observable behavior must follow one of two modes:

- **Exit mode.** The controller terminates. A later CLI invocation may auto-spawn a fresh controller. All previously issued checkpoint tokens are invalidated by the controller-restart rule (§4.3).
- **Reconnect mode.** The controller must perform an atomic reset of its tmux-derived state: it invalidates all previously issued checkpoint tokens, fails all pending waits as runtime failures, clears retained pane/output state derived from the old tmux server, and re-initializes pane tracking from the new tmux server state. After that reset, every pre-restart token must fail with `INVALID_AFTER`, or `PANE_NOT_FOUND` if the target pane does not exist in the re-initialized state.

Pending waits that were registered before the tmux server restart must fail as runtime/controller failures (§7.3), not as `PANE_CLOSED`, and not as `TIMEOUT`. `PANE_CLOSED` is reserved for per-pane destruction on an otherwise live tmux server.

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

- maintaining the tmux integration channel(s) needed for observation, input, and lifecycle tracking
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

*Non-normative implementation note.* The tmux adapter's observation/subscription mechanism is implementation-defined. Control mode (`tmux -C`) with subscriptions is the richest option and supports low-latency event handling. Simpler approaches, such as `pipe-pane` for pane output combined with periodic `list-panes` polling for lifecycle changes, may also satisfy the observable contract if they preserve output ordering, checkpoint semantics, pane-lifecycle correctness, and restart behavior.

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
29. `key` uses tmux's `send-keys` key vocabulary. When tmux rejects a key token sequence, the failure is surfaced as a structured command-level error. Because modern tmux versions may accept unknown names permissively (see §9.5 "Validation scope"), not every misspelled token is guaranteed to trigger an error.
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

---

## 19. Agent quick reference

This section is a condensed reference for agents consuming `tpctl`. It is informative, not normative; the binding behavior is defined in §4–§17.

### Mental model

A pane is a **visible screen** plus an **append-only output stream**. You observe the stream through **opaque checkpoint tokens** (JSON strings). Every read or wait is anchored to a token; there is no "read from now."

### Command table

| Command | Purpose | Returns |
|---|---|---|
| `tpctl list` | enumerate panes | `{"panes": ["%42", ...]}` |
| `tpctl snapshot --pane %N [--history-lines K]` | bootstrap observation; get `text` and a token | `pane_id`, `next`, `text`, optional `scrollback_text` |
| `tpctl read --pane %N --after TOKEN` | incremental read | `pane_id`, `next`, `text` (may be `""`) |
| `tpctl text --pane %N "..." [--enter]` | send literal text | empty stdout |
| `tpctl key --pane %N <key> [<key>...]` | send tmux keys (`Enter`, `C-c`, `Escape`, …) | empty stdout |
| `tpctl wait --pane %N --after TOKEN --for <mode> ... --timeout-ms T` | wait for output condition | `pane_id`, `next`, `result`, plus `matched` / `exit_code` when relevant |
| `tpctl daemon` | explicit controller startup | runs in foreground |

### The race-free idiom

```bash
SNAP=$(tpctl snapshot --pane %42)          # remember next from here
TOKEN=$(echo "$SNAP" | jq -r .next)

tpctl text --pane %42 "make test; printf '__DONE__:run1:%d\n' $?" --enter

tpctl wait --pane %42 --after "$TOKEN" \
  --for sentinel --token run1 --timeout-ms 30000
```

Why this works:

- `text` does not return until tmux has acknowledged the send (§9.4)
- `wait --after TOKEN` scans **all retained output after TOKEN**, including output already buffered by the time `wait` registers (§9.6)
- the sentinel response parses the exit code into `exit_code` (§9.6)

### Wait modes at a glance

- `--for sentinel --token TOKEN` — match `__DONE__:TOKEN:<exit-code>`; response includes `exit_code`
- `--for regex --pattern PATTERN` — RE2 against ANSI-stripped post-checkpoint text; response includes `matched` (whole match, no submatches)
- `--for quiescence --ms MS` — output has been idle for at least `MS` ms; succeeds immediately if already quiet

### Text representation

All text fields (`snapshot.text`, `snapshot.scrollback_text`, `read.text`) and all wait-match inputs are:

- ANSI-stripped
- `\n`-normalized (no `\r\n`)
- trailing-whitespace-trimmed per line
- trailing-blank-lines-trimmed

### Error handling cheat sheet

| Code | When | Recovery |
|---|---|---|
| `MISSING_AFTER` | `read`/`wait` called without `--after` | call `snapshot` to get a token |
| `INVALID_AFTER` | token wrong pane, evicted (>1 MiB ago), or from old controller | call `snapshot` again |
| `PANE_NOT_FOUND` | pane doesn't exist at dispatch | call `list` |
| `PANE_CLOSED` | pane vanished during your `wait` | choose a different pane |
| `TIMEOUT` | `wait` hit `--timeout-ms` | retry with a longer timeout or a different mode |

Command-level errors are JSON on `stdout`; runtime/controller failures are diagnostics on `stderr`. Both use nonzero exit codes. Implementations may emit additional codes — branch on the canonical ones above.

### Rules worth memorizing

- `%pane_id` is the only identity; tokens are pane-scoped and controller-lifetime scoped
- `text` takes **exactly one** positional argument; embedded newlines are sent literally; `--enter` appends an `Enter` key press (not `\n`)
- concurrent `wait`s on the same pane are supported and independent
- retained output is 1 MiB per pane; if you lag that far behind, expect `INVALID_AFTER`
- controller auto-spawns on first use; one per tmux server, keyed on the resolved tmux socket path
