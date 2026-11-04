# cairn reference

How cairn behaves, in full. The [README](../README.md) covers installing and everyday
use; this picks up where it stops.

- [Setup and doctor](#setup-and-doctor)
- [Trails](#trails)
- [Sessions](#sessions)
- [State](#state)
- [Folders](#folders)
- [Recording](#recording)
- [Panel](#panel)
- [Orca workspaces](#orca-workspaces)
- [JSON](#json)
- [Statusline](#statusline)

## Setup and doctor

`cairn setup` checks that `cairn` on `PATH` is the binary just built and that `gh` is
logged in, then installs cairn's plugin into Claude Code and Codex, whichever are on
`PATH`, through their own plugin CLIs:

```sh
claude plugin marketplace add ken8203/cairn && claude plugin install cairn@cairn
codex plugin marketplace add ken8203/cairn && codex plugin add cairn@cairn
```

With herdr on `PATH`, setup also writes cairn's herdr plugin and links it (see
[Panel](#panel)). Setup prints one `ok|done|skip|manual <step>: <detail>` line per step,
runs only what's missing, leaves user settings and layouts alone, and ends with `cairn doctor`.
`cairn setup --dry-run` shows what it would run. Agents load a plugin's hooks only when a
session starts, so after installing or enabling the plugin in an agent setup prints
`manual <agent>-restart: restart running <Agent> sessions to load cairn's hooks`.

`cairn doctor` prints `ok|warn|fail <check>: <detail>` lines, or with `--json` an array of
`{check,status,detail,fix}`: whether `cairn` on `PATH` is this binary, `gh` is logged in,
`cairn@cairn` is installed and enabled in Claude Code and Codex, and both its hooks are
trusted in `$CODEX_HOME/config.toml`, and, with herdr installed, cairn's herdr plugin
linked to this binary (a `warn` otherwise), then the terminal host, pane and agent
session. Untrusted or disabled Codex hooks are a `warn`, since setup can't trust them for
you. In an agent session that never ran cairn's `SessionStart` hook (no `hooked/<session>`
marker, see [State](#state)), as one started before the plugin was installed, the
`session` check is a `warn` whose fix is to restart that session. Last, an `orca` line
counts the Orca worktrees on trails, or says Orca isn't installed.

With `herdr-app` on `PATH`, setup also installs the [native panel](#herdrapp-panel).
Doctor adds a read-only `herdr-mod` check for missing, stale or edited assets; current
assets do not imply that the floating panel is open.

`cairn skills get` prints guides built into the binary, so an agent reads the one that
matches the cairn it runs. The plugin's `cairn` skill only points the agent there.

## Trails

A step that starts with `owner/repo#n` is a PR step: it counts as done once GitHub says
merged or closed. Any other step is free text, ticked by hand. Notes are dated
`YYYY-MM-DD` (older ones `MM-DD`). Edit the file freely; cairn only rewrites the lines it
changes.

`cairn add` refuses a PR GitHub doesn't know. A step written before its PR exists
becomes that PR with `cairn link <n> <pr>`, which also drops any other entry for it. A
PR whose base branch is another step's head is shown as stacked, with a hint once that
base has merged. Every row is numbered; the numbers are what `done` and `link` take. A
PR's ref is a link to it (OSC 8), blue and underlined.

Writes take a lock and replace the trail atomically, so sessions adding steps at the same
time don't lose one.

## Sessions

A session is keyed by `$CODEX_THREAD_ID`, else `$CLAUDE_CODE_SESSION_ID`, else the pane
it runs in (a human in a plain pane). Codex comes first because a Codex launched from
Claude Code inherits Claude's session id.

cairn knows the panes of these terminals, checked innermost first, since herdr usually
runs inside another terminal or tmux, tmux inside a terminal app, and either may run in
Herdr.app. When they nest, only the innermost pane is recorded, so its panel is the one
that follows the session:

| Host | Pane key | Tab | Label |
| --- | --- | --- | --- |
| herdr | `herdr-<session>.<id>.<terminal>` from `$HERDR_SESSION` (else `default`), `$HERDR_PANE_ID` and the pane's terminal id in `herdr pane get` | the label of the pane's tab in `herdr tab get`, unless still the number herdr gave it | the workspace's label |
| tmux | `tmux-<pid>.<n>` from the server pid in `$TMUX` and `$TMUX_PANE` (`%3` is `tmux-<pid>.3`) | the window's name once renamed, which turns off its `automatic-rename` | the tmux session's name |
| Herdr.app | `hc-<address>.<core-id>` from `$HC_TERMINAL_ID` and `hc server status --json`'s `core.id`, titles and labels from the `hc` CLI | the app tab in the same `where` label, unless still its number | the app space in `hc get`'s `where` label |
| Orca | `orca-<handle>` from `$ORCA_TERMINAL_HANDLE` | the worktree's directory name | the same |
| Rex | `$REX_BLOCK` without its `block:` prefix | the label of the window holding the block, unless still Rex's `Window <last 8 of its id>` | the Rex session's label |

Titles come from each host's CLI and are blank when it can't answer, as for a herdr
session whose name has characters other than letters, digits, `.`, `_` and `-`, or a
tmux server started with `-L`.

In Ghostty or any other terminal there is no pane: an agent session works as anywhere
else, but a human has no session of their own.

Herdr.app keys name a logical terminal: its permanent address on a permanent core.
Shell recovery keeps its last agent trail. Replacing the core gives it a different key.
Older `hc-<address>.<scope>` keys remain readable when passed explicitly. After an
upgrade, sessions re-register under the new key on their next cairn command or hook;
there is no prefix or timestamp migration. The private `HC_SCOPE` is not read.

Other hosts' pane keys name one pane process: a restored or reused pane starts fresh.
Run `cairn use <name>` there to rejoin a trail.

A session that touches a PR already on another trail does not join it. When a session
without a trail opens or pushes its own PR, cairn starts a trail for it, named after the
pane's tab when you named it, else the PR's branch, else the host's label (both above),
else `trail-<key>`, with `-2`, `-3`, … added if that name is taken. A tab holds one piece
of work, while a label such as a Rex session's is shared by every tab in it.

## State

All of cairn's state lives in `${CAIRN_HOME:-~/.cairn}`:

```
~/.cairn/
  trails/<name>.md                     the trails
  sessions/<key>.json                  each session's trail, PRs and places
  panes/<pane-key>                     the last agent session that ran cairn in a pane
  hooked/<session>                     empty: this agent session ran cairn's SessionStart hook
  cache/prs/<owner>/<repo>/<n>.json    PR state from GitHub
  herdr-plugin/herdr-plugin.toml       cairn's herdr plugin, written by setup
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
untouched for 30 days are removed whenever another is written, and so are hook markers.
PR state is cached for 60 seconds, shared by every cairn process. A merged PR is not
asked about again, and a closed one, which may be reopened, after an hour; `r` in the
panel asks about them all.

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
switches to a new session once it has run cairn there; `<pane>` is a pane key (see
[Sessions](#sessions)), or a Rex block. Without one it watches the pane it runs in. When
the pane's session has no trail, it shows the trail of `--dir`, else of the folder it
runs in. `cairn watch --from-env` takes the pane and folder from `$CAIRN_PANE` and
`$CAIRN_DIR`, for herdr's overlay.

`cairn panel [pane]` toggles that view for a pane, the current one without a key, in the
way its host suits best; `cairn panel --close` only closes it. Agents run it when you ask
to see the trail, and a host's own keybinding can pass the focused pane's key. The
README's [Panel](../README.md#panel) section shows how to bind it.

| Host | Panel |
| --- | --- |
| Rex | a floating layer over the window, labelled `cairn` (`rex do - -b <block>` with a Lua script cairn carries) |
| tmux | a popup, 80% by 50%, over the pane |
| herdr | an overlay pane over the pane, labelled `cairn`, from cairn's herdr plugin; without the plugin, a pane split off to the right, 35% wide, named `cairn` in the same tab |
| Herdr.app | a native `cairn` mod in saved Floating/Dock mode; otherwise a terminal named `cairn-<address>` from `hc run` |
| Orca | a terminal split off the pane, titled `cairn` |

Rex starts an action's programs without the terminal's environment, where `rex` itself
can't be found, so its action toggles the layer in the Lua `cairn panel --lua [block]`
prints rather than through `cairn panel`.

For herdr, `cairn setup` writes a plugin, `herdr-plugin.toml` in
`${CAIRN_HOME:-~/.cairn}/herdr-plugin/`, whose commands use the absolute path of the cairn
that wrote it: a `panel` action, "Toggle cairn", runs `cairn panel`, and a `panel` pane,
an overlay titled `cairn`, runs `cairn watch --from-env`. Setup rewrites the file when it
differs, as after cairn moved, and runs `herdr plugin link <dir>` when herdr doesn't have
it from there or it was rewritten. Linked plugins are global to the user, across herdr
sessions. `cairn panel` then opens the overlay with `herdr plugin pane open --plugin cairn
--entrypoint panel --placement overlay --env CAIRN_PANE=<key> --env CAIRN_DIR=<dir>`; run
as the action, it takes the focused pane's folder from `$HERDR_PLUGIN_CONTEXT_JSON`.

In a terminal without a pane API, `cairn panel` says so and exits 0. Each host CLI call
times out after 10 seconds, and a failure is one line on stderr with exit 1.

The TUI panel opens on the cached PR states and asks GitHub for the stale ones behind them,
fits itself to its pane as it's resized, and refreshes every 60 seconds.

### Herdr.app panel

Setup embeds and installs `main.js`, `dock.js`, `mod.json` and `binary.js` into
`<app-home>/mods/cairn/`. The app home is `$HERDR_APP_HOME`, else
`$XDG_CONFIG_HOME/herdr-app`, else `~/.config/herdr-app`. `binary.js` contains this
cairn executable's absolute path. `.cairn-owned.json` records generated file hashes:
setup updates owned files, preserves edits and unknown content with a manual conflict,
and writes nothing on a dry run. Doctor reads these files without changing them.
Move conflicting files aside yourself before reinstalling; keep your edits in a separate
mod if you want to maintain a variant.

The mod installs with `place: "none"`. First open defaults to Dock unless Float was
explicitly saved. Float uses a 920 × 420 window with Herdr's frame, border and shadow.
**Dock** switches to a right sidebar that shares space with the terminal; **Float** hides the sidebar and returns to the window.
The sidebar starts at 400 px, has an invisible draggable divider and places PR titles below the
state/CI/ref row. Herdr persists resized dimensions in its native layout state.
Its frame matches the default terminal pane: 10 px corners, a 1 px border that uses
the accent color while focused, and an 8 px gap beside the terminal.

The first Dock in a layout without a Cairn dock creates a personal copy with a block
beside its active-tab content and a Cairn toolbar beside the native tabs block.
The tabs slot becomes a row, retaining its ID, geometry, paint and window-drag setting;
its original content and props stay in a flexible child beside the fixed-width button.
No tabs fork or extra mod is installed. An older marked dock without a toolbar is
upgraded through a new copy; layouts containing both reuse them.
It checks actual files through `readTextFile`, using an
available `<layout>-cairn` name (then `-2`, `-3`, ...), skipping names with existing
layout files or saved layout state, and creates the complete copy
through `writeTextFile` with `expected: null` for exclusive creation. Only after that
write succeeds does it queue `layout.switch`. A write failure or creation race leaves
the floating view and previous mode intact with a visible error. Layout names must
contain only letters, digits, hyphens and underscores; changing layouts during
preparation cancels the switch. It preserves the original
layout, Spaces, tab bar, status and Files/Changes panels. Later switches reuse the dock
by its `content.mod: "cairn"` and `content.props.cairnMode: "dock"` marker, regardless
of its node ID. Layouts without one unambiguous active-tab block inside a container,
or with duplicate IDs, are refused with a visible error; use Float or another layout.
Adding a toolbar also requires exactly one visible native `content.mod: "tabs"` block.
Custom or ambiguous tab bars are left untouched and offer Float instead.
This customization happens on Dock, not during setup. Setup never edits layouts or keys.
The mod does not write `.state/` files.

The selected mode is saved with Herdr's `shared` API under `cairn.mode`; `cairn.target`
stores the source terminal, source tab metadata, presentation and open version between copies. These runtime choices are
separate from installed assets and do not create setup ownership conflicts.

The Cairn toolbar button, `cairn panel` and the `mod.open:cairn` keybinding toggle the
saved mode. The button remains when the dock is hidden or Float is selected, without
fetching data, polling or taking focus. Its highlight follows visible dock ancestry
and stack selection, or a live floating view. Each toolbar mount requests a fresh
visibility acknowledgement so persisted shared values cannot light it after restart.
In dock mode, a short-lived overlay routes the toggle to the installed sidebar and closes itself,
without taking keyboard focus or querying cairn. `cairn panel --close` closes Cairn's
`mod-cairn` overlay and hides marked Cairn docks in the current layout, idempotently.
It reads the current layout's public JSON for node IDs and sends `layout.hide`; it
never writes layout/state files. It leaves other panels and overlays alone.

```sh
herdr-app do mod.open --name cairn  # toggle the saved mode
cairn panel --close               # close Cairn in both modes
```

Merge `"ctrl-shift-t":"mod.open:cairn"` into a suitable `keys.json` section, such as
`global`, keeping other bindings. The × button closes the current presentation.
Escape or `q` closes and `r` refreshes only while Cairn has keyboard focus. Sidebar
mounts keep terminal focus; click the panel to focus it. PR links are clickable;
editing and ticking steps stay in the CLI.

Without mod files or a reachable app, `cairn panel` falls back to the `hc` TUI panel.
A failed native command or unreadable/malformed layout during close is reported as an
error. In the native panel, `cairn panel [key]` uses the app's selected terminal at
opening, even with an explicit key. The key still selects the host/fallback; the native
mod does not bind or mutate that target's session.

A fresh open captures the selected terminal (including a `terminal.show` replacement),
just as Rex Watch fixes its pane key. Changing focus or tabs does not change this source.
Dock/Float conversions and visible view restoration retain it; closing and reopening
selects the newly focused terminal. Reloading the panel preserves its source.
Hidden docks neither query nor adopt incidental focus.
With no terminal focus on a fresh open, Cairn shows an empty source and asks you to focus
a terminal and reopen. Only an active presentation or mode handoff restores a saved source;
a closed target is not a fallback for an unrelated tab.

The header shows the source pane's tab name, including native unnamed labels such as
`2`, prefixed by the machine for remote targets. Tab moves and renames update live.
A terminal without a known source tab uses `Terminal`; it never borrows the current tab's label. Hover over
it to see the terminal address. A missing source shows an unavailable status, without
adopting another terminal or displaying cached trail data.

Both modes poll every three seconds and resolve the latest agent session through the
same terminal key. Folder fallback uses that terminal's live cwd; an unknown cwd disables
folder fallback instead of reusing an old folder. Each copy runs one request at a time
and discards replies from an older open, folder or unavailable source. All trail, PR
and CI logic remains in `cairn show --pane ... --json`;
GitHub cache expiry and background refresh work as usual. PR links open only validated
GitHub HTTPS URLs. The native panel is read-only; use the CLI to edit trails.

For remote terminals, the mod runs `cairn` through Herdr's `exec` on that machine,
using its local terminal address and core ID. Install current cairn, `hc` and authenticated
`gh` there; the remote `cairn` must be on `PATH`. It reads that machine's cairn state,
never the local machine's. Missing cwd means no folder fallback. When tmux or herdr TUI
runs inside Herdr.app, cairn records only the inner pane; the native panel can then
find a trail only through the terminal's folder, unless that outer terminal has its
own mapping. It does not write inner/outer pane links.

## Orca workspaces

cairn keeps the card of each Orca worktree whose folder is on a trail (see
[Folders](#folders)) in sync with it, through the `orca` CLI, whichever terminal changed
the trail:

- the trail's PR in the worktree's repo whose head is its branch becomes the card's PR,
  under reviews (`orca worktree set --worktree path:<dir> --pr <n> --json`);
- the trail's other PRs, in any repo, are added as references when the card lacks them
  (`orca reference add <url>... --worktree path:<dir> --json`);
- the note becomes `cairn · checkout-v2 2/4 ▶ api#121`, or `cairn · checkout-v2 ✓ all
  done` (`orca worktree set --worktree path:<dir> --comment <text> --json`).

cairn writes a note only when it is empty or starts with `cairn · `: a note you wrote
stays as it is. It never removes a PR or reference that left the trail, and calls
`worktree set` only when something changed.

The sync runs detached after `use`, `add`, `link`, `done`, `note`, a recorded PR and a
background refresh of PR states, one at a time, so a burst of commands runs it once or
twice. It looks for `orca` on `PATH`, then in `/Applications/Orca.app` and
`~/Applications/Orca.app`, skips everything when `orca status` says the runtime isn't
reachable, gives each call five seconds, and drops its errors.

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

`cairn show --pane <key> [--json] [--dir <path>]` reads that pane instead of the
caller's agent or pane. It never registers a pane pointer or binds a session. Full
stored keys and Rex `block:` keys work; `hc-t1` resolves a live terminal's permanent
core ID by asking the local `hc` CLI, without using the caller's HC environment.
Run this shorthand on the terminal's own machine. Missing or invalid `--pane` values
fail. No data succeeds with `{"trail":null,"prs":[]}`. Only an explicit `--dir` enables
folder fallback here: the mod's cwd must not become the target's folder.

Fields may be added; existing ones keep their meaning.

## Statusline

`cairn statusline` prints the session's trail, its progress and next step (or, without a
trail, the open PRs the session touched). It answers from the cache at once and refreshes
stale PRs in the background, one refresh per session at a time. Run without the agent's
session id, it follows the pane to the session last run there, as the panel does; without
a trail either way, it shows the trail of its cwd's folder.
