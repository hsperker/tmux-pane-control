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

## Slice sequence

Each row is one branch / PR. Validation is what a human can run by hand after
the slice lands, in addition to the green test suite.

| # | Slice | Tests first | Manual validation |
|---|---|---|---|
| 1 | Go module scaffold + `tpctl` binary with `--help` | binary smoke test | `./tpctl --help` |
| 2 | `domain` package: response types, error codes, JSON shapes | table-driven marshal/unmarshal tests vs spec §9 examples | `go test ./...` |
| 3 | `tmuxctl` port (interface) + fake + `list` handler | handler test using fake | `go test` |
| 4 | Real tmux adapter for `list-panes` + CLI wiring for `list` | e2e against real tmux | `./tpctl list` inside a live tmux |
| 5 | `snapshot` (visible screen only; no history, no token yet) | handler + adapter tests | `./tpctl snapshot --pane %N` |
| 6 | Text normalization (§8.3) as a pure package | golden tests | unit only |
| 7 | Ring-buffer `store` + opaque checkpoint tokens + `read --after` | store + handler tests with fake stream | `snapshot` then `read` shows delta |
| 8 | Controller event loop owning the store + tmux output subscription | controller tests with fake `tmuxctl` | live pane output flows through `read` |
| 9 | `text` + `key` with send-ack | fake-ack tests + integration | `snapshot → text → read` sees echoed output |
| 10 | `waiter` package: sentinel matcher (pure) | table-driven matcher tests | unit only |
| 11 | `wait` command + timeout plumbing | handler tests | `wait --for sentinel` end-to-end |
| 12 | Quiescence mode | matcher + timer tests | `wait --for quiescence` |
| 13 | Regex mode (RE2) | matcher tests | `wait --for regex` |
| 14 | Controller/CLI split: daemon + Unix socket + auto-spawn + `tpctl daemon` | IPC + startup-race tests | same CLI, now cross-process |
| 15 | Pane lifecycle: `PANE_CLOSED`, `PANE_NOT_FOUND`, retained-stream drop | controller tests | pane close surfaces correct codes |
| 16 | Scrollback history; tmux socket identity (`-S` / `-L` / `$TMUX`); multi-server | adapter tests | `snapshot --history-lines N`; multi-socket |
| 17 | Map §17 acceptance criteria → acceptance test suite; close residual gaps | acceptance suite | all 39 criteria green |

## Branch strategy

- Development branch tracked by the harness: `claude/review-tpctl-specs-OSjfY`.
- Each slice: a short-lived branch `claude/slice-NN-<short-name>` cut from the
  development branch, PR back into it, squash-merge.
- Never push directly to `main`.

## Current status

- Spec frozen at `docs/specs/tpctl-v1.md`.
- No implementation code exists yet.
- **Next action:** start slice 1 (Go module scaffold + `tpctl --help`).

## How a fresh session should pick up

1. Read `docs/specs/tpctl-v1.md` for the normative contract.
2. Read this file for the plan and current status.
3. Run `git log --oneline` to see which slices have landed.
4. Resume at the first slice that is not yet merged.
