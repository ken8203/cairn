# cairn

Work often spans a chain of PRs across repos (an API change in one, its client in
another) plus steps that aren't PRs at all, such as a release or a feature flag. A
**trail** is that chain as an ordered checklist. Each agent session (a Claude Code or
Codex session, or a human in a plain terminal pane) works on its own trail, and sessions share
one only when asked to (`cairn use <name>`). `cairn` keeps trails, shows them, and
records the PRs each session touches.

## Install

```sh
make install                # builds ~/.local/bin/cairn
make install PREFIX=/usr/local
cairn setup
```

Or ask your agent to "set up cairn": `cairn skills get setup` tells it how. Install with
`make install` rather than `go install`: the command lives in `./cmd`, so `go install`
would name the binary `cmd`.

`cairn setup` installs cairn's plugin into Claude Code and Codex, and into herdr when
it's on `PATH`, then runs `cairn doctor` to check the install. Codex runs a plugin's hooks
only once you trust them: open Codex, run `/hooks`, and trust cairn's two.
`cairn setup --dry-run` shows what it would run.

## Trails

Trails are Markdown files in `~/.cairn/trails/`:

```markdown
# checkout-v2

- [ ] acme/api#120
- [ ] acme/web#45
- [ ] release api 1.4
- [ ] staging: enable CHECKOUT_V2

## Notes

- 2026-11-02 #120 waiting on review
```

A PR step counts as done once GitHub says merged or closed; any other step is ticked by
hand. Edit the file freely; cairn only rewrites the lines it changes.

## Use

You rarely start a trail yourself. When an agent opens or pushes a PR, cairn's hook
records it, and a session without a trail gets one, named after the terminal pane's
workspace or the PR's branch. A later session in the same repo and branch is told that
trail, and joins it with `cairn use <name>`.

```
cairn                  this session's trail, else this folder's, and the PRs it touched
cairn ls               every trail with its progress and next step
cairn use <name>       attach this session to a trail, creating it if needed
cairn add <step>...    append steps: owner/repo#n, a PR URL, or free text
cairn link <n> <pr>    turn free-text step n into that PR once it's open
cairn done <n>         tick (or untick) free-text step n
cairn note <text>      append a note
cairn panel            open or close the trail beside this pane
```

`cairn --help` lists every command.

## Panel

`cairn panel` keeps the pane's trail on screen beside it, in the way its terminal suits
best. Agents run it when you ask to see the trail; bind it to a key to toggle it
yourself:

| Host | Panel | Bind it |
| --- | --- | --- |
| Rex | a floating layer over the window | an `init.lua` action, below |
| tmux | a popup over the pane; <kbd>q</kbd> closes it | `bind-key T run-shell 'cairn panel'` |
| herdr | an overlay over the pane, from cairn's herdr plugin | the plugin's `cairn.panel` action, below |
| Herdr.app | a terminal in the same space and tab | a Herdr.app command that runs `cairn panel` |
| Orca | a terminal split off the pane | an Orca command that runs `cairn panel` |

In Rex's `init.lua`:

```lua
rex.action{ name = "cairn", title = "Toggle Cairn", run = function(ctx)
  local res = rex.exec{ os.getenv("HOME") .. "/.local/bin/cairn", "panel", "--lua", ctx.block_id }
  if not res.ok then error(res.stderr, 0) end
  loadstring(res.stdout)()
end }
rex.bind("ctrl+cmd+p", "cairn")
```

In herdr's `~/.config/herdr/config.toml`, once `cairn setup` has linked the plugin:

```toml
[[keys.command]]
key = "prefix+t"
type = "plugin_action"
command = "cairn.panel"
description = "toggle cairn"
```

In Ghostty or any other terminal without a pane API, run `cairn watch` in a split
yourself.

| Key | Action |
| --- | --- |
| <kbd>1</kbd>–<kbd>9</kbd> | open that row's PR in the browser gh uses, or tick that step |
| <kbd>e</kbd> | edit the trail in `$EDITOR` |
| <kbd>r</kbd> | refresh from GitHub now |
| <kbd>j</kbd>/<kbd>k</kbd>, arrows, mouse wheel | scroll a trail taller than the panel |
| <kbd>q</kbd> | close |

In Orca, cairn also keeps each worktree's card in sync with its trail: its PR, the
trail's other PRs as references, and a progress note.

## Claude Code statusline

`cairn statusline` prints the session's trail, its progress and next step. Append it to
whatever `statusLine.command` `~/.claude/settings.json` already has:

```json
"statusLine": { "type": "command", "command": "<existing command>; ~/.local/bin/cairn statusline" }
```

## Docs

- [Reference](docs/reference.md): how cairn behaves in full — setup and doctor,
  sessions and panes, `~/.cairn`, finding a trail by folder, what the hook records, the
  panel on each host, Orca sync, and the `cairn show --json` format.
- [Architecture](docs/architecture.md): how the code is laid out, for working on cairn.
  Build with `make build`, test with `make test`.
