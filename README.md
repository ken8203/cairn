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

`cairn setup` checks that `cairn` on `PATH` is the binary just built and that `gh` is
logged in, then installs cairn's plugin into Claude Code and Codex, whichever are on
`PATH`, through their own plugin CLIs:

```sh
claude plugin marketplace add ken8203/cairn && claude plugin install cairn@cairn
codex plugin marketplace add ken8203/cairn && codex plugin add cairn@cairn
```

Codex runs a plugin's hooks only once you trust them: open Codex, run `/hooks`, and
trust cairn's two. Setup prints one `ok|done|skip|manual <step>: <detail>` line per step,
runs only what's missing, never edits a config file itself, and ends with `cairn doctor`.
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

A step that starts with `owner/repo#n` is a PR step: it counts as done once GitHub says
merged or closed. Any other step is free text, ticked by hand. Notes are dated
`YYYY-MM-DD` (older ones `MM-DD`). Edit the file freely; cairn only rewrites the lines it
changes.

## Sessions

A session is keyed by `$CODEX_THREAD_ID`, else `$CLAUDE_CODE_SESSION_ID`, else the pane
it runs in (a human in a plain pane). Codex comes first because a Codex launched from
Claude Code inherits Claude's session id.

cairn knows the panes of these terminals, checked innermost first, since herdr usually
runs inside another terminal or tmux, tmux inside a terminal app, and either may run in
Herdr.app. When they nest, only the innermost pane is recorded, so its panel is the one
that follows the session:

| Host | Pane key | Names a new trail |
| --- | --- | --- |
| herdr | `herdr-<session>.<id>.<terminal>` from `$HERDR_SESSION` (else `default`), `$HERDR_PANE_ID` and the pane's terminal id in `herdr pane get` | the workspace's label |
| tmux | `tmux-<pid>.<n>` from the server pid in `$TMUX` and `$TMUX_PANE` (`%3` is `tmux-<pid>.3`) | the tmux session's name |
| Herdr.app | `hc-<address>.<scope>` from `$HC_TERMINAL_ID` and `$HC_SCOPE`, titles and labels from the `hc` CLI | the app space in `hc get`'s `where` label |
| Orca | `orca-<handle>` from `$ORCA_TERMINAL_HANDLE` | the worktree's directory name |
| Rex | `$REX_BLOCK` without its `block:` prefix | the Rex session's label |

Titles come from each host's CLI and are blank when it can't answer, as for a herdr
session whose name has characters other than letters, digits, `.`, `_` and `-`, or a
tmux server started with `-L`.

In Ghostty or any other terminal there is no pane: an agent session works as anywhere
else, but a human has no session of their own.

A pane key names one pane process, never reused: a pane a host restores after a restart,
or a new pane that gets an old one's number, starts fresh in cairn. Run
`cairn use <name>` there to rejoin a trail.

A session that touches a PR already on another trail does not join it. When a session
without a trail opens or pushes its own PR, cairn starts a trail for it, named after the
pane's host as above, else the PR's branch, else `trail-<key>`, with `-2`, `-3`, … added
if that name is taken.

## State

All of cairn's state lives in `${CAIRN_HOME:-~/.cairn}`:

```
~/.cairn/
  trails/<name>.md                     the trails
  sessions/<key>.json                  each session's trail, PRs and places
  panes/<pane-key>                     the last agent session that ran cairn in a pane
  cache/prs/<owner>/<repo>/<n>.json    PR state from GitHub
```

A session's file is

```json
{"trail": "checkout-v2", "prs": ["https://github.com/acme/api/pull/120"],
 "places": [{"dir": "/home/me/api", "repo": "acme/api", "branch": "checkout-v2"}],
 "updated": "2026-11-03T10:00:00+08:00"}
```

with `trail` `null` until the session is on one. `places` are where the session recorded
its PRs from: the git toplevel (a worktree's own), the `owner/repo` of its `origin`
remote, and the branch. Session files are written under a lock and replaced atomically,
so a hook's record and an agent's `cairn use` don't lose each other's change.

A pane's file holds one session id; it is what that pane's panel shows. Pointers
untouched for 30 days are removed whenever another is written. PR state is cached for 60
seconds, shared by every cairn process.

## Folders

The unit of work is a repo, branch and worktree, not a terminal, so a folder finds its
trail whichever host or agent session opens it. The trail of a folder is:

1. a trail with a PR step in the folder's `origin` repo whose head branch is the
   folder's branch (from the PR cache; a PR not fetched yet doesn't match), else
2. the trail of a session that recorded a PR from the same git toplevel and branch,

the most recently changed trail when several match. Outside a git repo, or on a
detached HEAD, a folder has none. Each git call gets two seconds.

Views show, in order, the session's own trail, that of the session the pane last ran,
then the folder's: `cairn` and `cairn show` (the cwd, or `--dir <path>`), the
statusline (its cwd), `cairn watch` (`--dir`, else its cwd) and `cairn panel`, which
hands the caller's cwd to the watch it starts. A trail found by folder says so,
`found by folder: api on checkout-v2 — cairn use checkout-v2 to join`, and `cairn` shows
it even outside any session or pane. The folder never binds a session: `add`, `done`,
`note` and `link` still need the session's own trail, and their error names the
folder's. When a session starts without a trail in a folder that has one, the agent is
told that trail and to `cairn use` it if it is continuing that work.

## Commands

```
cairn [show] [--json] [--dir <path>]   this session's trail, else this folder's, and the PRs it touched
cairn ls                   every trail with its progress and next step
cairn use <name>           attach this session to a trail, creating it if needed
cairn add <step>...        append steps: owner/repo#n, a PR URL, or free text
cairn link <n> <pr>        turn free-text step n into that PR once it's open
cairn done <n>             tick (or untick) free-text step n
cairn note <text>          append a note
cairn edit [name]          open a trail in $EDITOR
cairn watch [pane] [--dir <path>]   keep a pane's view on screen (the panel)
cairn panel [--close] [pane]   open or close a pane's panel in the terminal it runs in
cairn statusline           Claude Code statusline segment
cairn setup [--dry-run]    install cairn's plugin in Claude Code and Codex, then run doctor
cairn doctor [--json]      check the install without changing it; exit 1 on a failure
cairn skills list          the guides for agents built into cairn
cairn skills get <topic> [--full]   print a guide: cairn (using it), setup
cairn hook                 handle a Claude Code or Codex hook payload on stdin
cairn record [--own] [--session id] [-C dir] [-R repo] [pr]   remember a PR (the hook runs this)
```

`cairn doctor` prints `ok|warn|fail <check>: <detail>` lines, or with `--json` an array of
`{check,status,detail,fix}`: whether `cairn` on `PATH` is this binary, `gh` is logged in,
`cairn@cairn` is installed and enabled in Claude Code and Codex, and both its hooks are
trusted in `$CODEX_HOME/config.toml`, then the terminal host, pane and agent session.
Untrusted or disabled Codex hooks are a `warn`, since setup can't trust them for you.

`cairn skills get` prints guides built into the binary, so an agent reads the one that
matches the cairn it runs. The plugin's `cairn` skill only points the agent there.

`cairn add` refuses a PR GitHub doesn't know. A step written before its PR exists
becomes that PR with `cairn link <n> <pr>`, which also drops any other entry for it. A
PR whose base branch is another step's head is shown as stacked, with a hint once that
base has merged. Every row is numbered; the numbers are what `done` and `link` take.

Writes take a lock and replace the trail atomically, so sessions adding steps at the same
time don't lose one.

## Recording

The plugin runs `cairn hook` on two of the agent's hook events. After each Bash command
the agent runs, it records the PR for the session when the command:

- opens a PR (`gh pr create`), taking the last PR URL it printed,
- pushes a branch that has one (`git push`), or
- runs `gh pr checkout|co|comment|review|edit|merge|ready|close|reopen|update-branch`.

The command is parsed as shell, so each command in `cd api && git push; gh pr merge 3`
counts, and `cd`, `git -C <dir>` and `gh -R <repo>` are followed from the session's
directory. A `cd` inside `if`, a loop or a function, or a `pushd`, leaves the directory
unknown, and nothing after it is recorded. Commands hidden in a script or a variable are
not seen; `cairn add` them.

PRs the agent authors (create, push) join the session's trail ahead of its first
unfinished free-text step and behind the PRs already there, so they keep the order they
were opened in; a trail is started if the session has none. The hook keys the session by
the id the agent hands it, and asks GitHub from a detached `cairn record`, so it returns
at once and never fails the agent's command.

Each record also stores the place it was recorded from (see [State](#state)): the dir a
push, a `gh pr` command or `gh pr create` ran in.

When a session starts on a trail, as when it resumes, the hook tells the agent the
trail's name, progress and next step; a session without one, in a folder that has one,
hears of that trail instead. Agents without hooks get no automatic recording.

## Panel

`cairn watch <pane>` keeps the trail of the session last run in a pane on screen, and
switches to a new session once it has run cairn there; `<pane>` is a pane key as above,
or a Rex block. Without one it watches the pane it runs in. When the pane's session has
no trail, it shows the trail of `--dir`, else of the folder it runs in.

`cairn panel [pane]` toggles that view for a pane, the current one without a key, in the
way its host suits best; `cairn panel --close` only closes it. Agents run it when you ask
to see the trail, and a host's own keybinding can pass the focused pane's key.

| Host | Panel | Bind it |
| --- | --- | --- |
| Rex | a floating layer over the window, labelled `cairn` (`rex do - -b <block>` with a Lua script cairn carries) | an `init.lua` action, below |
| tmux | a popup, 80% by 50%, over the pane; <kbd>q</kbd> closes it | `bind-key T run-shell 'cairn panel'` |
| herdr | a pane split off to the right, 35% wide, named `cairn` in the same tab | a herdr keybinding or command that runs `cairn panel` |
| Herdr.app | a terminal named `cairn-<address>` in the same space and tab, from `hc run` | a Herdr.app command that runs `cairn panel` |
| Orca | a terminal split off the pane, titled `cairn` | an Orca command that runs `cairn panel` |

In Rex's `init.lua`:

```lua
rex.action{ name = "cairn", title = "Toggle Cairn", run = function(ctx)
  rex.exec{ os.getenv("HOME") .. "/.local/bin/cairn", "panel", ctx.block_id }
end }
rex.bind("ctrl+cmd+p", "cairn")
```

In Ghostty or any other terminal without a pane API, `cairn panel` says so and exits 0:
run `cairn watch` in a split yourself. Each host CLI call times out after 10 seconds, and
a failure is one line on stderr with exit 1.

| Key | Action |
| --- | --- |
| <kbd>1</kbd>–<kbd>9</kbd> | open that row's PR in the browser gh uses, or tick that step |
| <kbd>e</kbd> | edit the trail in `$EDITOR` |
| <kbd>r</kbd> | refresh from GitHub now |
| <kbd>j</kbd>/<kbd>k</kbd>, arrows, mouse wheel | scroll a trail taller than the panel |
| <kbd>q</kbd> | close |

The panel fits itself to its pane as it's resized and refreshes every 60 seconds.

## JSON

`cairn show --json` (or `cairn --json`) prints this session's trail as one JSON object,
for panels and tools. Like the statusline, it answers from the cache at once, refreshes
stale PRs in the background, and run without the agent's session id, follows the pane to
the session last run there, then the folder.

```json
{
  "trail": {
    "name": "checkout-v2",
    "done": 1,
    "total": 3,
    "steps": [
      {"n": 1, "kind": "pr", "done": true, "ref": "acme/api#120",
       "url": "https://github.com/acme/api/pull/120", "title": "Add the checkout API",
       "state": "merged", "ci": "pass"},
      {"n": 2, "kind": "pr", "done": false, "ref": "acme/api#121",
       "url": "https://github.com/acme/api/pull/121", "title": "Retry failed checkouts",
       "state": "open", "ci": "pending", "stacked_on": "acme/api#120"},
      {"n": 3, "kind": "text", "text": "release api 1.4", "done": false}
    ],
    "notes": ["2026-11-02 #120 waiting on review"]
  },
  "via": "session",
  "prs": [
    {"ref": "acme/docs#7", "url": "https://github.com/acme/docs/pull/7",
     "title": "Document the checkout flow", "state": "draft", "ci": "fail"}
  ]
}
```

- `trail` is `null` for a session without one. `done` counts finished steps; `n` is the
  number `cairn done` and `cairn link` take.
- `via` is how the trail was found: `session` (its own), `pane` (the session the pane
  last ran) or `dir` (the folder, `--dir` or the cwd; see [Folders](#folders)). It is
  left out when `trail` is `null`.
- A step's `kind` is `pr` or `text`. A text step has `text`; a PR step has `ref`
  (`owner/repo#n`), `url`, `title`, `state` and `ci`, and `stacked_on`, the `ref` of the
  step whose head is its base, when it is stacked.
- `state` is `open`, `draft`, `merged`, `closed`, or `unknown` before GitHub has
  answered, when `title` is left out; `ci` is `none`, `pending`, `pass` or `fail`.
- `prs` are the PRs the session touched that aren't on its trail, each like a PR step's
  PR, and `[]` when there are none.

Fields may be added; existing ones keep their meaning.

## Claude Code statusline

`cairn statusline` prints the session's trail, its progress and next step (or, without a
trail, the open PRs the session touched). Append it to whatever `statusLine.command`
`~/.claude/settings.json` already has:

```json
"statusLine": { "type": "command", "command": "<existing command>; ~/.local/bin/cairn statusline" }
```

It answers from the cache at once and refreshes stale PRs in the background, one
refresh per session at a time. Run without the agent's session id, it follows the pane to
the session last run there, as the panel does; without a trail either way, it shows the
trail of its cwd's folder.

## Development

```sh
make build                  # ./cairn
make test
```

```
cmd/main.go          the cairn subcommands, wired together
internal/trail       trail files and their locked edits
internal/github      PR state: one batched GraphQL request, the 60-second cache, CI
internal/session     agent and pane ids, session files, pane pointers, auto-created trails, folder lookup
internal/hook        agent hook payloads, and what a Bash command touched to record
internal/host        the terminal cairn runs in: pane keys, labels, pane titles, the panel
internal/setup       setup through the agents' plugin CLIs, and doctor's checks
internal/skills      the guides `cairn skills` prints, embedded
internal/ui          show, ls, the statusline and the panel
```

The plugin lives at the repo root: `.claude-plugin/` and `.agents/plugins/` hold the
manifests and marketplaces, `hooks/hooks.json` the two hooks, and `skills/cairn` the
skill. Codex trusts a hook by the hash of its definition, so `hooks/hooks.json` never
changes; a test pins it, and new behavior goes into `cairn hook`.

`cmd` wires the rest together and hands the host's label and title lookups in as
functions; `trail`, `github`, `host`, `hook`, `setup` and `skills` import no other
cairn package, `session` uses `trail` and `host`, and `ui` uses `trail`, `github` and
`session`.
