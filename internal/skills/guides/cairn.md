# cairn

cairn keeps a **trail** for each agent session: an ordered checklist of the PRs and
other steps (a release, a feature flag, a rollout) that one piece of work needs, often
across repos. Use it so you, the user, and later sessions know where the work stands.

## What happens on its own

cairn's plugin hook watches your shell commands. When you:

- open a PR (`gh pr create`), or push a branch that has one (`git push`), the PR is
  recorded for this session and joins its trail, ahead of the first unfinished
  free-text step. A session without a trail gets one, named after the terminal tab
  you named, else the PR's branch.
- run `gh pr checkout|comment|review|edit|merge|ready|close|reopen|update-branch`, that
  PR is remembered as touched, but does not join the trail.

`cd dir && …`, `git -C dir` and `gh -R owner/repo` are understood. A `cd` inside `if`, a
loop or a function, or a `pushd`, makes the directory unknown and nothing after it is
recorded, so keep `cd dir && gh …` flat. Commands run any other way (a script that calls
gh, an API call) are not seen: add those PRs yourself.

## Commands

```
cairn                  this session's trail and the PRs it touched
cairn ls               every trail with its progress and next step
cairn use <name>       attach this session to a trail, creating it if needed
cairn add <step>...    append steps: owner/repo#n, a PR URL, or free text
cairn link <n> <pr>    turn free-text step n into that PR once it's open
cairn done <n>         tick (or untick) free-text step n
cairn note <text>      append a dated note
```

Step numbers are the row numbers `cairn` prints.

## What to do

- When a session starts, cairn may say this folder's branch is on a trail. If you're
  continuing that work, run `cairn use <name>` to join it; otherwise leave it alone.
  Until you join, `cairn` shows that trail but `add`, `done`, `note` and `link` refuse.
- At the start of multi-PR or multi-step work, `cairn use <short-name>` (or join the
  trail the user names), then `cairn add` the steps you already know, in order.
- Add steps that aren't PRs as free text: `cairn add "release api 5.7" "enable FLAG on
  staging"`. Tick them with `cairn done <n>` once they're actually done.
- Planned a PR before opening it? Add it as text, then `cairn link <n> <pr>` once it
  exists.
- Use `cairn note` for what the next session needs: who is reviewing, what is blocked.
- Run `cairn` to check where things stand before reporting progress.
- When the user asks to see the trail, run `cairn panel`; when you need it as data, run
  `cairn show --json`. To inspect another pane without changing sessions, use
  `cairn show --pane <key> --json`; add `--dir` only for that target's folder.
  Herdr.app shorthand `hc-t1` resolves on the machine where cairn runs.
- In Orca, each worktree's card shows its trail's PRs and a `cairn · ` note on its own;
  don't set them with `orca worktree set` yourself.

Don't create a trail for one-off work with a single PR; the hook records it anyway.
<!-- full -->

## Details

- A PR step counts as done once GitHub says merged or closed; free-text steps only when
  ticked.
- Touching a PR that is already on another session's trail never moves this session to
  that trail. `cairn use <name>` is the only way to share one.
- A PR whose base branch is another step's head is shown as stacked.
- `cairn add` refuses a PR GitHub doesn't know.
- Trails are Markdown files in `~/.cairn/trails/` (`$CAIRN_HOME/trails/`); the user may
  edit them with `cairn edit`.
- A folder finds its trail by a PR on it whose head is the folder's branch, or by a
  session that recorded a PR from that worktree and branch.
- Sessions are keyed by `$CODEX_THREAD_ID`, else `$CLAUDE_CODE_SESSION_ID`, else the
  terminal pane, so commands you run act on your own session.

- Herdr.app's native floating panel follows terminal focus and retains its target when clicked.
  It needs current cairn on remote machines too. Nested tmux/herdr panes have only
  folder fallback in the outer panel. Escape or `q` closes it; `r` refreshes the view.
  PR links are clickable; edit and tick steps through the CLI. Run `cairn skills get setup` for installation.
