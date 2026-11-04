# Setting up cairn

## Install

```sh
go install github.com/ken8203/cairn/cmd/cairn@latest   # needs Go 1.27
cairn setup
```

The binary lands in `$(go env GOPATH)/bin`. From a checkout of
github.com/ken8203/cairn, `make install` builds `~/.local/bin/cairn` instead.

`cairn setup` is safe to run again at any time. It prints one line per step:

```
ok|done|skip|manual <step>: <detail>
```

- `ok`: already in place. `done`: setup just did it. `skip`: not applicable (an agent
  that isn't installed) or a dry run.
- `manual`: something only a person (or you, with their go-ahead) can do; the detail
  is the exact action.

Then it runs `cairn doctor`. `cairn setup --dry-run` prints what it would do and
changes nothing.

What it does:

1. Checks that `cairn` on `PATH` is this binary.
2. Checks that `gh` is installed and logged in; cairn reads PRs through it.
3. With Claude Code: `claude plugin marketplace add ken8203/cairn` and
   `claude plugin install cairn@cairn`, each only when missing; enables the plugin if
   it is disabled.
4. With Codex: `codex plugin marketplace add ken8203/cairn` and
   `codex plugin add cairn@cairn`, each only when missing. Codex runs a plugin's hooks
   only once the user trusts them, so the user has to open Codex and approve cairn's
   two hooks.
5. With herdr: writes cairn's herdr plugin to `${CAIRN_HOME:-~/.cairn}/herdr-plugin/`
   and runs `herdr plugin link` on it when missing or rewritten. The `manual herdr-keys`
   line is the keybinding the user adds to `~/.config/herdr/config.toml` to toggle the
   panel; pass it on, since setup never edits that file.

Agents load a plugin's hooks only when a session starts. After installing or enabling the
plugin in an agent, setup prints `manual claude-restart` or `manual codex-restart`: tell
the user to restart running sessions of that agent, this one included.

It never edits agent settings or dotfiles itself.

## Doctor

`cairn doctor` checks the same things without changing any, as
`ok|warn|fail <check>: <detail>` lines (`--json` for an array of
`{check,status,detail,fix}`), and exits 1 when a check fails. Act on each `fix`.
Codex hooks that aren't trusted or are disabled are a `warn`, not a `fail`: only the
user can trust them, so pass the fix on to them.
<!-- full -->

## Troubleshooting

- **PRs aren't recorded.** Run `cairn doctor`. The plugin must be installed and enabled
  in the agent, and in Codex its hooks trusted. Hooks load when a session starts, so
  restart the agent after installing: a `warn session` line saying cairn's hook hasn't
  run means this session started without them.
- **`path` fails.** Add the directory doctor names to `PATH` in the shell profile, or
  install into a directory already on it: `GOBIN=<dir> go install …`.
- **`gh` fails.** Install the GitHub CLI and run `gh auth login`.
- **Codex hooks are untrusted or disabled.** Open Codex, run `/hooks`, and trust or
  enable cairn's SessionStart and PostToolUse hooks.
- **A command wasn't seen.** The hook reads the Bash commands the agent runs; it can't
  see PRs opened by scripts or through the API. `cairn add <pr>` adds one by hand.
