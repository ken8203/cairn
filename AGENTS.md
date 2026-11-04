# AGENTS.md

cairn is a Go CLI, shipped with a Claude Code and Codex plugin, that keeps a **trail** (an
ordered checklist of PRs and free-text steps) for each agent session and records the PRs
a session touches through agent hooks.

## Commands

```sh
make build                               # ./cairn
make test                                # go test ./...
go test ./internal/trail -run TestAdd    # one package, one test
```

Go 1.27, with `encoding/json/v2` imported as `json`. When writing Go, follow the
`use-modern-go` skill in `.claude/skills/use-modern-go/`.

## Guardrails

- `hooks/hooks.json` stays byte-for-byte as it is: Codex trusts hooks by its hash, and
  `TestHooksFileUnchanged` pins it. New hook behavior goes into `cairn hook`.
- Package imports follow the dependency rules in
  [docs/architecture.md](docs/architecture.md#package-dependencies); `cmd` passes
  behavior across as functions.
- A behavior change lands with its docs in the same change: the `help` const in
  `cmd/cairn/main.go` for commands, `internal/skills/guides/` for what agents using cairn
  are told, [docs/reference.md](docs/reference.md) for the behavior, and the README when
  it changes installing or everyday use.
- Examples use fictional repos (`acme/api`, `acme/web`), in the terse, plain English of
  the existing docs.

## Where to read

- [docs/architecture.md](docs/architecture.md) — before adding a package, background
  work or a terminal host, and before writing tests (how they isolate state, `gh` and
  host CLIs).
- [docs/reference.md](docs/reference.md) — before changing what a command, the hook,
  the panel, setup/doctor, the Orca sync or the `--json` output does; it is the spec.
- [README.md](README.md) — the user's view: installing, everyday commands, binding the
  panel.
