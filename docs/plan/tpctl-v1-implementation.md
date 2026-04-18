# tpctl v1 Implementation Plan

Companion to [`docs/specs/tpctl-v1.md`](../specs/tpctl-v1.md). Defines how we
incrementally build the v1 implementation so a fresh session can pick up where
the last one left off.

## Working principles

- **Vertical slices.** Each slice ends on a runnable binary with a
  demonstrable, user-visible change. No slice lands that only moves internals.
- **TDD where practical.** Write failing tests first for pure logic
  (`domain` types, `waiter` matchers, text normalizer, `store` ring buffer).
  For adapters and I/O, lead with a fake; promote to real-tmux e2e once the
  contract is stable.
- **Small commits, short-lived branches.** One slice = one branch off the
  development branch → PR → squash merge. Commits within a slice follow
  `test:` → `feat:` → `refactor:` when that ordering helps.
- **Defer infrastructure until it pays for itself.** Keep the controller
  logic in-process until the command contract is stable (slice 14), then
  split into a daemon + IPC. Avoids rewriting IPC as the contract shifts.

## Testing layers

- **Unit** — `domain`, `waiter` matchers, text normalizer, `store` ring buffer.
  Pure, fast, drive these with TDD.
- **Component** — controller + fake `tmuxctl` port. Covers wait concurrency,
  retention, pane lifecycle.
- **E2E** — real tmux on a disposable socket, invoked by the test. Gated to
  environments with tmux available.

## Conventions

- **Go module path:** `github.com/hsperker/tmux-pane-control`.
- **Go version:** target the latest stable Go release at the time slice 1
  lands; record it in `go.mod` and treat that as the project floor.
- **Slice is "done" when:** its tests pass on trunk and its manual
  validation command succeeds. Tick the checkbox in the same PR that
  lands the slice.
- **Commit style:** imperative subject, optional prefix
  (`test:`, `feat:`, `refactor:`, `plan:`, `docs:`, `spec:`).
- **Branches:** one short-lived branch per slice, named
  `slice-NN-<short-name>` (e.g. `slice-01-scaffold`), cut from the trunk
  branch the session is working on, PR back, squash-merge. Automation
  harnesses may prefix branch names; the `slice-NN-<short-name>` suffix is
  what matters.
- **Never push directly to `main`.**
- **Keep this plan in sync.** Every slice PR also ticks its checkbox below
  and updates the "Current status" section. The plan is the source of truth
  for progress; `git log` is secondary.

## Slice sequence

Tick a checkbox when the slice has landed on trunk. Validation column is
what a human can run by hand after the slice lands, in addition to the
green test suite.

- [ ] **Slice 1 — scaffold.** Go module + `tpctl` binary with `--help`.
  Tests first: binary smoke test. Validation: `./tpctl --help`.
- [ ] **Slice 2 — domain types.** `domain` package: response types, error
  codes, JSON shapes. Tests first: table-driven marshal/unmarshal tests
  against spec §9 examples. Validation: `go test ./...`.
- [ ] **Slice 3 — `list` handler.** `tmuxctl` port (interface) + fake +
  `list` handler. Tests first: handler test using the fake.
  Validation: `go test ./...`.
- [ ] **Slice 4 — real `list`.** Real tmux adapter for `list-panes` + CLI
  wiring for `list`. Tests first: e2e against real tmux.
  Validation: `./tpctl list` inside a live tmux.
- [ ] **Slice 5 — `snapshot`.** Visible screen only; no history, no token
  yet. Tests first: handler + adapter tests.
  Validation: `./tpctl snapshot --pane %N`.
- [ ] **Slice 6 — text normalization.** §8.3 as a pure package.
  Tests first: golden tests. Validation: unit only.
- [ ] **Slice 7 — `read --after`.** Ring-buffer `store` + opaque checkpoint
  tokens + `read` command. Tests first: store + handler tests with a fake
  stream. Validation: `snapshot` then `read` shows the delta.
- [ ] **Slice 8 — controller loop.** Event loop owns the store + subscribes
  to tmux output. Tests first: controller tests with fake `tmuxctl`.
  Validation: live pane output flows through `read`.
- [ ] **Slice 9 — `text` + `key`.** Send-ack semantics (§9.4, §9.5).
  Tests first: fake-ack tests + integration.
  Validation: `snapshot → text → read` sees echoed output.
- [ ] **Slice 10 — sentinel matcher.** `waiter` package: pure sentinel
  matcher. Tests first: table-driven matcher tests. Validation: unit only.
- [ ] **Slice 11 — `wait` command.** Timeout plumbing + sentinel mode
  wired end-to-end. Tests first: handler tests.
  Validation: `wait --for sentinel` end-to-end.
- [ ] **Slice 12 — quiescence mode.** Tests first: matcher + timer tests.
  Validation: `wait --for quiescence`.
- [ ] **Slice 13 — regex mode.** RE2. Tests first: matcher tests.
  Validation: `wait --for regex`.
- [ ] **Slice 14 — daemon split.** Controller/CLI split: daemon + Unix
  socket + auto-spawn + `tpctl daemon`. Tests first: IPC + startup-race
  tests. Validation: same CLI, now cross-process.
- [ ] **Slice 15 — pane lifecycle.** `PANE_CLOSED`, `PANE_NOT_FOUND`,
  retained-stream drop on destruction. Tests first: controller tests.
  Validation: pane close surfaces correct codes.
- [ ] **Slice 16 — history + multi-server.** Scrollback history; tmux
  socket identity (`-S` / `-L` / `$TMUX`); multi-server.
  Tests first: adapter tests.
  Validation: `snapshot --history-lines N`; multi-socket.
- [ ] **Slice 17 — acceptance suite.** Map spec §17 acceptance criteria to
  an acceptance test suite; close residual gaps.
  Tests first: acceptance suite. Validation: all 39 criteria green.

## Current status

- Spec frozen at `docs/specs/tpctl-v1.md`.
- Plan captured (this file). No implementation code exists yet.
- **Next action:** start slice 1.

## How a fresh session should pick up

1. Read `docs/specs/tpctl-v1.md` for the normative contract.
2. Read this file. The first unchecked slice above is the one to start.
3. Cross-check the checkboxes against reality: run `go test ./...` and the
   validation commands of the most recently ticked slices. If a ticked
   slice fails its check, the plan has drifted — fix the plan (or the
   code) before starting new work.
4. Cut a `slice-NN-<short-name>` branch and proceed.

## Retrospective: what a first end-to-end implementation taught us

A complete end-to-end implementation of v1 was built out on a
separate branch, walking through the 17 slices in order. That work
surfaced fifteen spec-refinement opportunities — a mix of normative
tightenings, clarifications, and one implementation note. The full
list and resulting spec changes are captured in the PR that updated
`docs/specs/tpctl-v1.md`; the highest-impact ones:

- **§7.5 precedence was ambiguous and the implementation got it
  wrong.** The "prefer PANE_NOT_FOUND over INVALID_AFTER" phrasing let
  the validation checks run in the wrong order; only an acceptance-
  suite audit caught it. The spec now mandates a concrete dispatch
  ordering: pane-existence validation precedes token-to-pane
  validation.
- **§9.6 match-input CR handling was unspecified.** Regex users
  writing `(?m)^ready$` against `"ready\r\n"` silently miss matches
  because `\r` sits between the content and the `\n` anchor. The spec
  now requires `\r` stripping before match.
- **§14 accidentally prescribed control mode.** A simpler
  `pipe-pane` + `list-panes` poll adapter also satisfies the
  observable contract. §14's normative bullet no longer names a
  specific mechanism; a non-normative note enumerates the trade-offs.
- **§11.2 didn't require daemon lifetime decoupling.** Auto-spawn
  works in an interactive shell but fails under agent harnesses that
  reap process trees; the spec now mandates lifetime decoupling with
  `setsid`-style detachment as the Unix example.
- **§11.9 restart semantics were too permissive.** "May exit or
  reconnect" left observable behavior undefined in reconnect mode;
  the spec now requires an atomic reset of tmux-derived state and
  explicit failure of pending waits as runtime errors (not
  `PANE_CLOSED`, not `TIMEOUT`).

## Known implementation deviations from the tightened spec

Some spec tightenings have not yet been reflected in the
implementation. These are tracked as follow-up work:

- **§11.9 tmux server restart.** The controller does not currently
  detect tmux connection loss; pre-restart tokens remain "valid"
  after a tmux restart. Needs either exit-on-failure or
  atomic-state-reset reconnect.
- **§6.2 global flag position.** The CLI dispatcher rejects global
  flags that precede the subcommand: `tpctl --tmux-socket X list`
  fails with "unknown command --tmux-socket", and only the
  post-subcommand form `tpctl list --tmux-socket X` works. Fix is
  a small change in `internal/cli/app.go` to strip recognized
  global flags from `args` before the subcommand dispatch switch.
  This finding surfaced from the live real-tmux smoke test (see
  below), not from the test suite, which exclusively uses the
  post-subcommand form.

### Resolved since the first retrospective

- **§9.6 CR handling.** Wait handler now uses
  `textnorm.StripANSIAndCR` for both sentinel and regex modes.
  Regression test: `TestWait_Regex_MultilineAnchorAgainstCRLF`.
  Verified live: `(?m)^READY-MARKER$` now matches against real
  tmux CRLF output.
- **§11.2 daemon lifetime regression test.** New
  `cmd/tpctl/daemon_lifetime_test.go` starts a bash subprocess
  that auto-spawns the daemon, records the daemon PID via
  `/proc`, SIGKILLs the parent's process group, and asserts the
  same daemon PID is still alive and responsive. Linux-only.

## Real-tmux smoke test summary

Beyond the automated acceptance suite, a manual end-to-end pass
against a disposable tmux 3.4 server exercised all 15 scenarios
below. Every scenario behaved as the spec requires:

1. `list` — returns `{"panes": ["%0"]}`
2. `snapshot` — visible text + opaque base64 token
3. `snapshot --history-lines 5` — scrollback + visible distinctly
4. `snapshot → text → wait --for sentinel` (exit 0) — parses
   `exit_code: 0` and full `matched` literal
5. Same pattern with exit 42 — parses `exit_code: 42` correctly
6. `snapshot → text → read --after` — returns only the delta
7. `wait --for regex` with `(?m)^READY-MARKER$` against
   CRLF-terminated output — matches (validates the §9.6 CR-strip
   fix end-to-end)
8. `wait --for quiescence` — returns immediately when already idle
9. `key C-c` — sends the keystroke, exits 0 with no stdout
10. `PANE_NOT_FOUND` for `%999`
11. `MISSING_AFTER` for `read` without `--after`
12. `INVALID_AFTER` for a garbage token
13. `TIMEOUT` for a sentinel that never appears
14. §7.5 precedence — `PANE_NOT_FOUND` wins over `INVALID_AFTER`
    when both could apply
15. Daemon auto-spawn was transparent; `tpctl daemon` was never
    invoked manually yet every subsequent call connected.

The only divergence from spec-as-written was §6.2 global flag
position, which the smoke test caught and which is now tracked
above as a known deviation.

## Lessons learned

- **Audit the acceptance suite aggressively.** The §7.5 precedence
  bug shipped through every earlier slice; only the post-slice-17
  audit caught it. Write the acceptance suite last but treat the
  audit as a first-class step, not a rubber stamp.
- **Pure functions for normalization paid off.** The `textnorm`
  package's hand-rolled ANSI state machine had a single bug (charset
  designators were miscounted as two-byte) caught by one golden
  test. Having the normalizer as a standalone package with
  table-driven tests made extension trivial.
- **Go's `flag` package doesn't do interleaved parsing.** Every
  pane-taking command needed a small `reorderArgs` helper because
  the spec's own examples interleave flags and positionals. Worth
  building into the scaffold from slice 1 of any future Go-based
  implementation; the spec now mandates interleaving in §6.1.
- **The single-writer controller loop was the right abstraction.**
  Serializing all pane mutations through one goroutine avoided a
  whole class of data-race tests. The hexagonal split made the fake
  tmux adapter trivial to write, which in turn made controller-level
  component tests fast and deterministic.
- **`pipe-pane` + poll is a valid alternative to control mode.**
  Control mode is richer but its frame grammar and subscription
  semantics are non-trivial. The polling-with-pipe-pane approach
  traded sub-millisecond latency for a much smaller adapter surface
  and satisfied every observable requirement in §4, §9.6, and §11.9.
