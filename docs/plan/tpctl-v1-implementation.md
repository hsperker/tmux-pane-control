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
