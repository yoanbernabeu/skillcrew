# Skillcrew

[![CI](https://github.com/yoanbernabeu/skillcrew/actions/workflows/ci.yml/badge.svg)](https://github.com/yoanbernabeu/skillcrew/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/yoanbernabeu/skillcrew)](https://github.com/yoanbernabeu/skillcrew/releases)
[![Go Report Card](https://goreportcard.com/badge/github.com/yoanbernabeu/skillcrew)](https://goreportcard.com/report/github.com/yoanbernabeu/skillcrew)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**One skill registry for your whole team, in every AI coding agent, always up to date.**

Skillcrew distributes [Agent Skills](https://agentskills.io) (`SKILL.md`) to every developer of an organization, whatever agent they use (Claude Code, Codex, GitHub Copilot, Cursor, Gemini CLI, and every agent that reads `~/.agents/skills`), and keeps them updated **with no action from developers**.

> **Status: early development.** The MVP works end to end; expect breaking changes before v1.

## Why

- **Version drift**: skills copied by hand diverge; fixes never reach everyone.
- **Multi-agent teams**: every agent reads skills from its own folder.
- **No governance**: no way to make sure everyone has the security review skill, or to pull a bad one.
- **Manual updates**: other tools need someone to remember to run `update`.

| | Skillcrew | Claude Code marketplace | Skill package managers |
|---|---|---|---|
| Works with every agent | ✓ | Claude Code only | ✓ |
| Updates with no action | ✓ (hooks + timer) | Opt-in per marketplace | Manual `update` |
| Required / blocked skills per team | ✓ | Organization-wide only | ✗ |
| Reads Claude Code marketplaces | ✓ | ✓ | Some |

## How it works

```
 Registry (one Git repo, Claude Code marketplace layout + skillcrew.yaml)
      │  git fetch (your existing Git credentials)
      ▼
 skillcrew sync ── started in the background when an agent session starts (hook) + hourly timer
      │
      ├─▶ ~/.agents/skills/<skill>    real copies  (Codex, Copilot, Cursor, Gemini CLI, …)
      └─▶ ~/.claude/skills/<skill>    symlinks     (Claude Code)
```

- **Zero action**: `skillcrew init` registers a session-start hook in each detected agent and a user-level timer (launchd / systemd). No daemon, no admin rights.
- **Governance**: each skill is `required`, `recommended`, `available`, `deprecated` or `blocked`, per organization and per team.
- **The team version is the norm**: local edits to a team skill are backed up, then the team version is restored. Change a team skill with `skillcrew propose`, which opens a pull request.
- **Your personal skills are yours**: Skillcrew only touches the directories it installed.
- **Safe by design**: atomic updates, nothing from the registry is ever executed by Skillcrew, and it keeps working offline with the last fetched registry. See [Security model](#security-model).

## Install

```sh
brew install yoanbernabeu/tap/skillcrew
# or
curl -fsSL https://raw.githubusercontent.com/yoanbernabeu/skillcrew/main/install.sh | sh
# or
go install github.com/yoanbernabeu/skillcrew/cmd/skillcrew@latest   # Go 1.26 or later
```

Requirements: macOS or Linux (Windows through WSL2) and `git`. `propose` uses `gh` (GitHub) or `glab` (GitLab) when installed.

Verify a release (optional): `checksums.txt` is signed keylessly with [Sigstore cosign](https://docs.sigstore.dev/) by the release workflow; `install.sh` checks it automatically when `cosign` is installed.

```sh
VERSION=v0.1.0
base=https://github.com/yoanbernabeu/skillcrew/releases/download/$VERSION
curl -fsSLO "$base/checksums.txt" -fsSLO "$base/checksums.txt.sigstore.json"
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/yoanbernabeu/skillcrew/.github/workflows/release.yml@refs/tags/$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
shasum -a 256 --ignore-missing -c checksums.txt   # then check your archive against it
```

## Quick start

### Administrators: create a registry

```sh
mkdir acme-skills && cd acme-skills && git init
skillcrew registry init --org acme        # or run it inside an existing Claude Code marketplace
git add -A && git commit -m "Create the skills registry"
gh repo create acme/skills --private --source . --push
```

`registry init` creates a marketplace with an example plugin and skill, a `skillcrew.yaml` and a CI workflow. Add skills under `plugins/<plugin>/skills/<skill>/SKILL.md`, list the plugins in `.claude-plugin/marketplace.json`, then set their status in `skillcrew.yaml`:

```yaml
org: acme
default: available                 # status of skills not listed below

skills:
  example:hello-world: recommended # plugin:skill, as in Claude Code
  # security:review: required
  # misc:risky: blocked            # removed from every machine at the next sync

teams:
  backend:
    skills:
      example:hello-world: required
```

`skillcrew validate` checks the registry: every id must exist under `plugins/`, skill names must not collide, pins must exist (and `ref` must be a tag), and plugin directories missing from `marketplace.json` are reported. The generated workflow runs it on every pull request. Converting an existing marketplace gives colliding skills an `install_as` alias, blocks skills that fail validation, and prints what is left to fix. See the [configuration reference](docs/configuration.md).

### Developers: join

```sh
skillcrew init github.com/acme/skills --team backend   # "skillcrew team list" shows the teams
```

That's it. Skills now follow the registry; start a new agent session to load them. `skillcrew status` shows what you have, `skillcrew list --available` what you can add.

## Commands

| Command | Purpose |
|---|---|
| `skillcrew init <registry>` | Join an organization: clone, install hooks and timer, first sync |
| `skillcrew team list` | The registry teams, and the ones you joined |
| `skillcrew team join <team>` / `leave` | Join or leave a team |
| `skillcrew sync` | Sync now (agents and the timer do it for you) |
| `skillcrew status` | Organization, teams, installed skills and their state |
| `skillcrew list [--available]` | Every registry skill and its status for you |
| `skillcrew install` / `remove <skill>` | Opt in to / out of an `available` skill |
| `skillcrew disable` / `enable <skill>` | Turn a `recommended` or `deprecated` skill off / on |
| `skillcrew propose <skill>` | Open a PR with your local changes (or `--plugin p` for a personal skill) |
| `skillcrew doctor` | Check hooks, timer, registry and installed skills |
| `skillcrew validate [dir]` | Validate a registry (CI) |
| `skillcrew registry init [dir]` | Create a registry or convert a Claude Code marketplace |
| `skillcrew uninstall` | Remove everything Skillcrew installed and restore the personal skills it replaced |

`<skill>` is a `plugin:skill` id or an installed name. `status`, `list`, `team list`, `sync`, `doctor` and `validate` accept `--json`.

## Supported agents

Tested on 2026-10-11 with real agents; agents change fast, and `skillcrew doctor` reports what it finds.

| Agent | Skills read from | Sync trigger | Tested |
|---|---|---|---|
| Claude Code | `~/.claude/skills` (symlinks) | `SessionStart` hook | ✓ skills and hook |
| OpenCode | `~/.agents/skills` | hourly timer | ✓ skills |
| Codex CLI | `~/.agents/skills` | `SessionStart` hook (approve it once with `/hooks`) | ✓ skills, hook not verified yet |
| Gemini CLI | `~/.agents/skills` | `SessionStart` hook | ✓ skills, hook not tested |
| GitHub Copilot CLI / VS Code | `~/.agents/skills` | `sessionStart` hook in `~/.copilot/hooks/` | not tested yet |
| Cursor | `~/.agents/skills` | `sessionStart` hook | not tested yet |
| Windsurf, Amp, Goose | `~/.agents/skills` | hourly timer | not tested yet |

A hook never makes an agent wait: it returns at once and, when the last sync is older than a minute, starts the sync in the background. Claude Code picks up new skills live; other agents at their next session.

## Security model

Skillcrew pushes content to every developer machine automatically, so the registry is your root of trust:

- **Protect the registry** like production code: required reviews on the default branch, and protected tags if you pin with `ref`.
- **Pins cannot be hijacked**: `sha` must be a full commit id and `ref` a tag; neither ever resolves to a branch. `skillcrew validate` checks both in CI.
- **Skillcrew executes nothing** from the registry: it copies files. Skills may bundle scripts that agents run when they use the skill, so review them in pull requests.
- **No secrets stored**: Git authentication goes through your existing credential helpers; URLs with embedded passwords are refused.
- **Kill switch**: set a skill to `blocked` and it is removed from every machine at the next sync.

Report vulnerabilities privately: see [SECURITY.md](SECURITY.md).

## FAQ

**What happens to my local edits to a team skill?** They are backed up in `~/.skillcrew/backups/<skill>/` (last 5 versions) and the team version is restored. `skillcrew propose <skill>` turns them into a pull request.

**I already have a personal skill with the same name.** It is backed up (and never pruned) and replaced by the team skill; `skillcrew uninstall` puts it back. Rename yours to keep both.

**Two registry skills have the same name.** `skillcrew validate` reports it; give one an `install_as` alias in `skillcrew.yaml`.

**Can a skill be pinned?** Yes: `ref` (a tag) or `sha` (a full commit id). Otherwise skills follow the registry branch.

**What about project-specific skills?** Commit them in the project (`.agents/skills/`, `.claude/skills/`): Git already distributes them. Skillcrew manages user-level skills.

**How do I uninstall?** `skillcrew uninstall` lists what it removes, then removes hooks, the timer, team skills and `~/.skillcrew`; personal skills that team skills replaced are restored when their name is free, and backups of your local changes are kept. Then `brew uninstall skillcrew` or delete the binary.

## Documentation

Full documentation: **[skillcrew.yoandev.co](https://skillcrew.yoandev.co/docs/)**

- [Getting started](https://skillcrew.yoandev.co/docs/getting-started/install/): install, create a registry, join as a developer
- [`skillcrew.yaml` reference](https://skillcrew.yoandev.co/docs/reference/skillcrew-yaml/) and [CLI reference](https://skillcrew.yoandev.co/docs/reference/cli/)
- [Guides](https://skillcrew.yoandev.co/docs/guides/private-registries/): private registries, pinning, proposals, CI
- [Troubleshooting](https://skillcrew.yoandev.co/docs/troubleshooting/doctor/)
- For AI agents: [llms.txt](https://skillcrew.yoandev.co/llms.txt)

## Contributing

Issues and pull requests are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md). The project follows strict test-driven development. Report vulnerabilities privately as described in [SECURITY.md](SECURITY.md).

## License

[MIT](LICENSE). Skillcrew is not affiliated with Anthropic, OpenAI, GitHub, Cursor or Google; product names are trademarks of their owners.
