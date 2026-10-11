# AGENTS.md

Guide for coding agents (and humans) changing Skillcrew. Read it before touching the code. User documentation lives in `site/` (published at https://skillcrew.yoandev.co); contribution rules are in [CONTRIBUTING.md](CONTRIBUTING.md).

## What Skillcrew is

A Go CLI that distributes Agent Skills (`SKILL.md`) from one Git repository (the *registry*: a Claude Code marketplace plus a `skillcrew.yaml` governance file) to every developer of an organization, in every AI coding agent, and keeps them updated with no action: session-start hooks in each agent and a user-level timer run `skillcrew sync`. Real copies go to `~/.agents/skills/<name>`, symlinks to `~/.claude/skills/<name>`.

## Layout

```
cmd/skillcrew/        entry point (version resolution, signals, App.Execute)
internal/cli/         Cobra commands; App holds every dependency (tests inject fakes)
internal/syncer/      one sync cycle: lock, debounce, fetch, resolve, apply, notices
internal/policy/      governance resolution (pure): org → teams → user prefs
internal/registry/    marketplace discovery, skillcrew.yaml parsing, validation
internal/snapshot/    registry read from git objects; skills cached by tree id
internal/install/     applies skills: staging, atomic swap, backups, symlinks, lock
internal/gitx/        the only place that runs git; all security hardening lives here
internal/propose/     local skill → branch + pull request on the registry
internal/agent/       session-start hook adapters (declarative entries)
internal/scheduler/   launchd / systemd user timer
internal/scaffold/    `skillcrew registry init`
internal/skill/       Agent Skills spec subset: frontmatter, names, hashing, CopyAs
internal/state/       config.yaml, lock.json, state.json
internal/fsx/         atomic writes, copies, file locks
internal/paths/       every path Skillcrew reads or writes
internal/testutil/    throwaway git registries; isolates tests from the user's git config
site/                 Astro website and documentation
```

Dependencies point down this list: `cli` → `syncer` → `policy`/`install`/`snapshot` → `registry`/`gitx`/`skill`. `policy` and `registry` do no I/O beyond the `fs.FS` they are given.

## Invariants (never break them)

Security (the registry is reviewed by pull request but treated as untrusted; any developer can push branches):

- **Exact refs only.** Fetch only the followed branch and the pinned tags, matched by exact name with `ls-remote` and fetched by object id. Never a wildcard refspec (case-insensitive filesystems turn a pushed `Main` into `main`), never git's "did you mean" resolution. Tags are immutable once fetched. Pins: `sha` is a full commit id, `ref` is a tag, neither ever resolves to a branch.
- **Never execute registry content.** Files are written from raw git blobs (`ls-tree` + `cat-file --batch`), never `git archive` or a checkout that applies attributes or filters. Every git call goes through `gitx` with hooks disabled, `ext::` blocked, inherited repository-local `GIT_*` variables removed, no terminal (new session).
- **Only touch what Skillcrew owns.** Ownership is the lockfile. Anything else is personal: backed up (never pruned) before a team skill replaces it. Local edits to team skills are backed up, then restored.
- **No secrets.** Git credentials come from the user's helpers; URLs with credentials are refused; every URL printed or logged goes through `gitx.Redact`; registry text is stripped of control characters before reaching a terminal or the log.
- **Never break an agent's startup.** `sync --hook` is silent, never fails, respects the debounce, the fetch timeout, the backoff after a failed fetch and the overall hook budget.

Consistency:

- A sync never leaves a half-written skill visible: stage in `~/.agents/.skillcrew-staging`, swap with renames, roll back on failure.
- A skill that cannot be prepared this time keeps its previous state (it is never removed because of a transient error).
- Commands that change the configuration hold the sync lock across read, edit, save and sync.

## Tests

- **Strict TDD**: write the failing test, watch it fail for the right reason, then implement. Bug fixes start with a regression test.
- **Never the real home**: `t.TempDir()` for every home, registry and work directory; functions take paths, never call `os.UserHomeDir()` under test; schedulers use an injected runner.
- Git in tests: build registries with `internal/testutil` (it isolates git from the user's global config). Security behavior is tested with real git and real attack shapes (case-variant refs, filters, hooks, symlinks).
- Trying the CLI by hand: `go build -o /tmp/skillcrew ./cmd/skillcrew`, then run it with `HOME=$(mktemp -d)` and `--no-timer`.

## Checks before every commit

```sh
gofmt -l .                       # prints nothing
go vet ./...
go test -race ./...
GOTOOLCHAIN=go1.27.2 go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0 run ./...
GOTOOLCHAIN=$(go env GOVERSION) go run golang.org/x/vuln/cmd/govulncheck@v1.9.0 ./...
sh -n install.sh                 # when install.sh changes
(cd site && npm ci && npm run build)   # when site/ changes
```

## Things that change outside this repo

AI agents move their skills directories and hook formats often. Before changing an adapter in `internal/agent`, the paths in `internal/paths`, or the agent tables in the docs, check the agent's current official documentation and note the date in the commit message. `skillcrew doctor` must keep reporting what it finds on a real machine.

## Conventions

- Everything in English: code, comments, docs, commit messages, issues.
- Small, focused packages; no new dependency without a clear reason (standard library first).
- Errors say what happened and what to do next.
- Commits: imperative subject, one logical change, `Fixes #<issue>`, `Co-Authored-By` for contributors who shaped or verified the issue.
- User-visible behavior changes update the documentation in `site/` and the README in the same commit.
