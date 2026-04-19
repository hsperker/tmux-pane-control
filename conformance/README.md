# tpctl v1 conformance kit

An executable version of the tpctl v1 spec: a language-agnostic test
suite that drives any candidate implementation through its CLI and
asserts the spec-mandated behavior.

**What it is.** A single Go module (`conformance/`) containing a test
harness and ~55 scenarios organized by spec section. The tests are
written in Go but the *binary under test can be anything* — the kit
invokes it as an external process and observes argv, stdout, stderr,
exit code, and JSON shapes. No imports from any reference
implementation; response types are redefined from spec §9 examples,
tokens are treated as opaque strings.

**What it is for.**

- A second implementer gets `docs/specs/tpctl-v1.md`,
  `docs/plan/tpctl-v1-implementation.md`, and this directory.
- They build their own `tpctl` (in any language).
- They run `go test ./conformance/scenarios -args --binary=/path/to/their/tpctl`.
- A green run means the implementation is spec-conformant.

## Running the kit

**Prerequisites.**

- A Go 1.24 toolchain (to run the kit; your implementation can be in
  any language).
- `tmux` 3.x on `PATH`.
- `bash` (the harness starts each scenario's pane as `bash -i` so
  commands typed via `tpctl text` are actually executed).
- The `tpctl` binary under test, already built.

**Command.**

```bash
cd conformance
go test ./scenarios/... -parallel=4 -args --binary=/absolute/path/to/tpctl
```

`-v` to see each scenario's name. `-run TestC21` to run one.

**Parallelism.** Scenarios that are CPU- and IO-independent call
`t.Parallel()`, so `-parallel=N` controls how many run concurrently.
`-parallel=4` is the recommended target — enough to get meaningful
wall-clock speedup without starving shell subprocesses that drive
pane output. Going higher is usually fine but may introduce
timing-sensitive flakes on constrained hardware. Lower (`-parallel=1`)
works but is slower. A handful of timing-sensitive scenarios opt
out of parallelism explicitly; those run serially regardless of the
flag.

**Optional flags.**

- `--binary PATH` — the only required flag; points at the tpctl-
  compatible binary under test.
- `--tmux PATH` — override the tmux binary (default `tmux`). Useful
  for a multi-version matrix.

If `--binary` is not set, tests skip rather than fail, so a plain
`go test ./conformance/...` in the reference repo is a no-op.

## Failure diagnostics

When a scenario fails, the harness auto-dumps a failure-context
summary to the test log before teardown:

- the last ~6 `tpctl` invocations (argv, exit code, truncated
  stdout, truncated stderr);
- a `tmux capture-pane -p` of every pane the scenario touched.

This surfaces most "why didn't it work?" questions in the test
output itself — no need to re-run with extra logging. Scenarios
that pass don't pay this cost.

The harness also performs a per-scenario zombie check after
teardown (polls up to 3s for the daemon to self-exit) and a
suite-level process count in `TestMain` that fails the suite if
`tpctl daemon` processes leaked beyond the start count.

## What the kit covers

### Spec §17 acceptance criteria (C01–C39)

Every numbered criterion has at least one scenario. Subtests are
named `TestC<NN>_<summary>` so a failure maps straight back to the
spec item.

| File | Covers |
|---|---|
| `scenarios/core_test.go` | C01 (list), C02–C04 (snapshot), C05–C06 (read), C07–C09 (tokens) |
| `scenarios/retention_test.go` | C10–C11 (1 MiB retention, eviction invalidates tokens) |
| `scenarios/wait_test.go` | C12–C17 (wait contract), C18–C24 (matching semantics) |
| `scenarios/io_test.go` | C25 (text representation), C26–C30 (text, key, send-ack) |
| `scenarios/lifecycle_test.go` | C31–C33 (controller model), C34–C35 (pane lifecycle) |
| `scenarios/errors_test.go` | C36–C38 (error conventions), C39 (external consistency) |
| `scenarios/multi_server_test.go` | §11.4 socket resolution + cross-server isolation |
| `scenarios/workflows_test.go` | realistic agent idioms (sentinel build, TUI drive, tail) |
| `scenarios/main_test.go` | `TestMain` suite hooks (zombie check) |

C39 (architectural patterns) is structural — the kit cannot inspect
internal code organization. The scenario instead asserts the
observable consequences (CLI surface, JSON shapes, opaque tokens)
hold consistently across the rest of the suite.

### Post-§17 spec tightenings

The spec gained several normative additions after the §17 list was
frozen (during the first-implementation retrospective). The
conformance kit covers these in `scenarios/extensions_test.go`:

| Spec section | Scenario | What it tests |
|---|---|---|
| §6.1 | `TestExt_ArgParsingInterleaved` | flags and positionals interleave |
| §6.1 | `TestExt_DoubleDashSentinel` | `--` ends flag parsing |
| §6.2 | `TestExt_GlobalFlagsBeforeSubcommand` | `tpctl --tmux-socket X list` works |
| §6.2 | `TestExt_GlobalFlagsEqualsFormBeforeSubcommand` | `--flag=value` form |
| §9.2 | `TestExt_ScrollbackShorterThanRequested` | no padding, no error on short history |
| §9.6 | `TestExt_MatchInputStripsCRForMultilineAnchor` | `(?m)^ready$` matches CRLF output |
| §11.2 | `TestExt_DaemonSurvivesSpawnerKill` | setsid/stdio decoupling |
| §11.4 | `TestExt_DaemonSocketUnderXDG` | XDG-derived socket path |
| §11.5 | `TestExt_ConcurrentSpawnRaceIsSerialized` | TOCTOU-safe auto-spawn |
| §11.9 | `TestExt_TmuxServerLossRaisesRuntimeError` | runtime error surfaces after tmux dies |

### What the kit does NOT cover

- **§8.2 field ordering in JSON** — the spec does not mandate an
  ordering, so the kit's decoders don't check one.
- **§11.4 hash function / fallback path specifics** — the spec
  explicitly says these are implementation-defined. The kit checks
  that the socket lands under `$XDG_RUNTIME_DIR/tpctl/` when that
  variable is set; it does not re-derive the hash.
- **§14 internal tmux integration** — control mode vs `pipe-pane`
  is implementation-defined. Both approaches must satisfy the
  observable contract; the kit only observes the contract.
- **§10 architectural patterns** — see C39 above.

## Adding a scenario

Scenarios are plain Go tests against the `harness` package. Minimal
template:

```go
func TestExt_MyNewScenario(t *testing.T) {
    e := harness.NewEnv(t)
    pane := e.FirstPane(t)
    snap := e.Snapshot(t, pane)

    // … exercise the CLI …
    var r harness.ReadResponse
    e.Run("read", "--pane", pane, "--after", snap.Next).MustJSON(t, &r)

    // … assert …
}
```

`harness.Env` provides:

- `Run(args ...string) Result` — run `tpctl` with the fixture's
  `--tmux-socket` prepended. Returns `{Code, Stdout, Stderr}`.
- `RunArgs(args ...string) Result` — run `tpctl` with **no** auto-
  injection. Use when you're probing flag-position behavior.
- `Result.MustJSON(t, &v)` — assert exit 0, decode stdout into `v`.
- `Result.MustError(t) ErrorResponse` — assert nonzero exit, decode
  a §7.2 error response.
- `Result.MustMuted(t)` — assert exit 0 and empty stdout (for `text`
  and `key`).
- `Snapshot(t, pane, ...)`, `List(t)`, `FirstPane(t)`,
  `NewPane(t)`, `KillPane(t, pane)` — pane-level helpers.
- `Tmux(args...)` — run tmux directly against the fixture socket,
  useful for setting up or tearing down state the kit can't (or
  shouldn't) drive through tpctl.

Each scenario gets its own fresh tmux server and XDG runtime dir,
so scenarios are isolated and can run in parallel (but the kit does
not currently call `t.Parallel()`; each scenario is already quick).

## Running against multiple tmux versions

The `--tmux` flag lets you point at a non-default tmux binary, so
a CI matrix can run:

```yaml
- name: Conformance against tmux 3.4
  run: go test ./conformance/scenarios -args --binary=./tpctl --tmux=/usr/local/bin/tmux-3.4

- name: Conformance against tmux 3.5
  run: go test ./conformance/scenarios -args --binary=./tpctl --tmux=/usr/local/bin/tmux-3.5
```

This catches spec-level behavior that depends on tmux version — for
example, C29 (key-token rejection) is softer on tmux 3.x+ because
that range is permissive about unknown names.

## Design notes

- **Single entry point.** Everything runs under `go test`; no
  custom runner, no YAML DSL, no config files. A scenario is just
  a Go test function that asserts on JSON.
- **Per-scenario tmux.** Each scenario starts its own disposable
  tmux server rather than sharing one. Slightly slower, but
  eliminates cross-scenario interference and makes failures
  reproducible in isolation.
- **Treat tokens as opaque.** The harness never decodes, compares,
  or pattern-matches tokens. Any implementation's token format is
  fine as long as the round-trip contract holds.
- **JSON shapes redefined locally.** The harness carries its own
  `ListResponse`, `SnapshotResponse`, `ReadResponse`,
  `WaitResponse`, `ErrorResponse` types. If an implementation
  matches these in wire form, it conforms — regardless of how its
  internal types are named.
- **Timeouts are generous.** Quiescence/timing assertions use
  timeouts that are 2–5x the detection threshold of the reference
  implementation, so slower but conformant implementations still
  pass on modest hardware.

## Using the kit from another repo

The kit is a standalone Go module at
`github.com/hsperker/tmux-pane-control/conformance`. Another
implementer has two ways to consume it:

- **`go get`** and drive it from their own test file:

  ```bash
  go get github.com/hsperker/tmux-pane-control/conformance/scenarios
  ```

  then write a Go test that imports and runs the scenario
  functions against their binary. Less common but possible.

- **Copy in tree** — `git subtree add` or `cp -r` the
  `conformance/` directory into your repo, then run it as
  `go test ./conformance/scenarios/ -parallel=4 -args --binary=/path/to/your/tpctl`.
  Simplest for one-off validation.

Either way, the only dependency is Go stdlib plus `tmux` on PATH.

## Current status

- Every §17 criterion has a named scenario.
- Post-§17 tightenings (§6.1, §6.2, §9.2 short-scrollback, §9.6
  CR-strip, §11.2 lifetime, §11.4 socket path, §11.5 coordination,
  §11.9 restart) each have a scenario.
- Multi-server (§11.4 resolution branches, cross-server isolation)
  and realistic agent workflow scenarios are covered.
- Total: ~55 scenarios; full run ≤65s at `-parallel=4` on a modern
  laptop.
- Verified 10/10 consecutive green runs at `-parallel=4` against the
  reference implementation on branch
  `claude/review-tpctl-v1-docs-k35ZW`.
