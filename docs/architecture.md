# cairn architecture

How the code is laid out, for working on cairn itself. What cairn does is in the
[README](../README.md) and the [reference](reference.md).

## Layout

```
cmd/cairn/main.go    the cairn subcommands, wired together
internal/trail       trail files and their locked edits
internal/github      PR state: one batched GraphQL request, the 60-second cache, CI
internal/session     agent and pane ids, session files, pane pointers, auto-created trails, folder lookup
internal/hook        agent hook payloads, and what a Bash command touched to record
internal/host        the terminal cairn runs in: pane keys, labels, pane titles, the panel
internal/orca        Orca worktree cards kept in sync with their trails
internal/setup       setup through the agents' plugin CLIs, herdr's plugin, and doctor's checks
internal/skills      the guides `cairn skills` prints, embedded
internal/ui          show, ls, the statusline and the panel
```

The plugin lives at the repo root: `.claude-plugin/` and `.agents/plugins/` hold the
manifests and marketplaces, `hooks/hooks.json` the two hooks, and `skills/cairn` the
skill.

## Package dependencies

`cmd` wires the rest together (`newApp`) and hands the host's label and title lookups and
the GitHub query in as functions. `trail`, `github`, `host`, `orca`, `hook`, `setup` and
`skills` import no other cairn package; `session` uses `trail` and `host`; `ui` uses
`trail`, `github` and `session`. A package that needs another's behavior takes it as a
function from `cmd`.

## The hook

Codex trusts a hook by the hash of its definition, so `hooks/hooks.json` never changes:
`TestHooksFileUnchanged` pins it, and new behavior goes into `cairn hook`.

`cairn hook` returns at once and never fails the agent's command. It parses the Bash
command with `mvdan.cc/sh` (`internal/hook`) and hands the GitHub lookup to a detached
`cairn record`.

## Background work

Work that shouldn't hold up a command runs detached through `spawn` in
`cmd/cairn/main.go`: `cairn record` from the hook, `__refresh` for stale PRs and `__orca`
for the Orca sync. `spawn` runs the `cairn` beside the running binary, not the one on
`PATH`. A lock in the cache dir (`TryLock`) keeps one refresh per session, and one Orca
sync, running at a time.

State is shared by every cairn process (hooks, panels, agents), so every write to a
trail or session file takes a lock and replaces the file atomically (`trail.Lock`,
`trail.WriteAtomic`).

## Hosts

`internal/host` has one file per terminal, each a `host` value: `rex.go` (with
`rex.lua`, embedded), `herdr.go`, `hc.go`, `tmux.go` and `orca.go`; `host.go`
dispatches. The `hosts` slice is in detection order, innermost first, and a stored pane
key finds its host by prefix. Rex's keys have no prefix, so Rex stays last as the
fallback. Every host CLI call, a panel's or a lookup's (pane keys, labels, titles), goes
through `cli`, with its 10-second timeout: the hook and the statusline must not hang on
a host that stopped answering.

## Agent guides

`cairn skills get <topic>` prints `internal/skills/guides/<topic>.md`, embedded in the
binary, so an agent always reads the guide for the cairn it runs. Text after
`<!-- full -->` is shown only with `--full`. `skills/cairn/SKILL.md` only points the
agent at `cairn skills get`.

## Tests

Tests never touch real terminals, `gh` or `~/.cairn`:

- `testApp` in `cmd/cairn/main_test.go` gives an app on a temp home, with the host env
  vars (`REX_BLOCK`, `TMUX_PANE`, `HERDR_PANE_ID`, `HC_TERMINAL_ID`,
  `ORCA_TERMINAL_HANDLE`, `CODEX_THREAD_ID`, `CLAUDE_CODE_SESSION_ID`) cleared and a GitHub that knows only the
  PRs it's given.
- Tests that run the binary build it once (`build` in `cmd/cairn/hook_test.go`). `hookEnv`
  puts it beside a stub `cairn` that logs what it was asked to record, which is what
  `spawn` runs.
- Host CLIs (`orca`, `herdr`, `rex`, …) are faked as scripts on a temp `PATH`.
