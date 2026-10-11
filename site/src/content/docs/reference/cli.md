---
title: CLI
description: Every skillcrew command, its arguments, flags and JSON output.
section: reference
order: 1
---

```text
skillcrew <command> [arguments] [flags]
```

`<skill>` is a `plugin:skill` id or an installed name, as shown by `skillcrew list`. Every command has `-h, --help`. `skillcrew --version` (or `-v`, or `skillcrew version`) prints the version.

## Overview

| Command | Purpose |
|---|---|
| `skillcrew init <registry>` | Join an organization: clone, install hooks and timer, first sync |
| `skillcrew team list` | The registry teams, and the ones you joined |
| `skillcrew team join <team>` / `leave <team>` | Join or leave a team |
| `skillcrew sync` | Sync now (agents and the timer do it for you) |
| `skillcrew status` | Organization, teams, installed skills and their state |
| `skillcrew list [--available]` | Every registry skill and its status for you |
| `skillcrew install <skill>` / `remove <skill>` | Opt in to / out of an `available` skill |
| `skillcrew disable <skill>` / `enable <skill>` | Turn a `recommended` or `deprecated` skill off / on |
| `skillcrew propose <skill>` | Open a pull request with your local changes, or contribute a personal skill |
| `skillcrew doctor` | Check hooks, timer, registry and installed skills |
| `skillcrew validate [dir]` | Validate a registry (CI) |
| `skillcrew registry init [dir]` | Create a registry or convert a Claude Code marketplace |
| `skillcrew uninstall` | Remove everything Skillcrew installed |
| `skillcrew completion <shell>` | Shell completion script for bash, zsh, fish or powershell |

`status`, `list`, `team list`, `sync`, `doctor` and `validate` accept `--json`.

## Developer commands

### skillcrew init

```text
skillcrew init <registry> [flags]
```

Join an organization from its registry: any Git URL, a local path, or the `host/owner/repo` shorthand (which becomes `https://host/owner/repo.git`). Clones the registry, installs a session-start hook in each detected agent and the hourly fallback timer, then runs a first sync. Idempotent: run it again to repair hooks or the timer.

| Flag | Effect |
|---|---|
| `--team <team>` | Team to join (repeatable) |
| `--ref <branch>` | Registry branch to follow (default: the registry's default branch) |
| `--no-hooks` | Do not install agent session-start hooks |
| `--no-timer` | Do not install the fallback timer |

```sh
skillcrew init github.com/acme/skills --team backend
```

### skillcrew team

```text
skillcrew team list [--json]
skillcrew team join <team>
skillcrew team leave <team>
```

`list` shows the registry teams and the ones you joined. `join` and `leave` change your membership and sync right away.

```console
$ skillcrew team list --json
[
  {
    "name": "backend",
    "joined": true
  },
  {
    "name": "frontend",
    "joined": false
  }
]
```

### skillcrew sync

```text
skillcrew sync [flags]
```

Fetch the registry and update skills now. Agents and the timer run it for you.

| Flag | Effect |
|---|---|
| `--json` | Print JSON |
| `--quiet` | Print nothing and respect the debounce window (used by the fallback timer) |
| `--hook[=text\|json]` | Run from an agent hook: returns at once, prints nothing (or `{}` with `--hook=json`), and starts a sync in the background when the last one is older than the debounce window |

```console
$ skillcrew sync
Skills are up to date (registry 7f396e1a9c2d).
$ skillcrew sync --json
{
  "commit": "7f396e1a9c2d4b0e8a1f5c6d3e2b9a8f7c6d5e4f",
  "installed": [],
  "notices": [],
  "removed": [],
  "skipped": false,
  "updated": []
}
```

### skillcrew status

```text
skillcrew status [--json]
```

Your organization, registry and commit, teams, last sync, and the installed skills with their status, state and version. Deprecation notices follow the table.

The `STATE` column is `ok`, `modified` (you edited it; the next sync restores it), `missing`, `pending` (not installed yet), `disabled`, or `-`.

```console
$ skillcrew status --json
{
  "org": "acme",
  "registry": "https://github.com/acme/skills.git",
  "ref": "main",
  "commit": "7f396e1a9c2d4b0e8a1f5c6d3e2b9a8f7c6d5e4f",
  "teams": [
    "backend"
  ],
  "last_sync": "2026-10-11T13:52:38.837477+02:00",
  "skills": [
    {
      "id": "security:review",
      "name": "review",
      "status": "required",
      "wanted": true,
      "state": "ok",
      "commit": "7f396e1a9c2d4b0e8a1f5c6d3e2b9a8f7c6d5e4f"
    }
  ]
}
```

`last_error` and `notices` appear when there are any. A skill entry can also carry `deprecated` (the notice text).

### skillcrew list

```text
skillcrew list [--available] [--json]
```

Every registry skill and its status for you. `--available` only shows the skills you can install. The JSON output is an array of the same skill entries as `status`.

```console
$ skillcrew list
SKILL          ID                   STATUS       STATE  VERSION
db-migrations  data:db-migrations   required     ok     7f396e1a9c2d
pdf            docs:pdf             available    -      -
hello-world    example:hello-world  available    -      -
commit-msgs    git:commit-msgs      recommended  ok     7f396e1a9c2d
risky          misc:risky           blocked      -      -
review         security:review      required     ok     7f396e1a9c2d
```

### skillcrew install / remove

```text
skillcrew install <skill>
skillcrew remove <skill>
```

Install an `available` skill on demand, or remove one you installed. Both sync right away. To turn off a `recommended` or `deprecated` skill, use `disable`.

### skillcrew disable / enable

```text
skillcrew disable <skill>
skillcrew enable <skill>
```

Turn off a `recommended` or `deprecated` skill: it is removed and stays off. `enable` turns it back on; a deprecated skill is reinstalled. `required` skills cannot be disabled.

```console
$ skillcrew disable commit-msgs
Disabled git:commit-msgs.
Synced registry 7f396e1a9c2d:
  - commit-msgs (removed: disabled)
```

### skillcrew propose

```text
skillcrew propose <skill> [flags]
```

Propose your local changes to a team skill (or their backup, if a sync already restored the team version), or contribute one of your personal skills with `--plugin`. Pushes a branch `skillcrew/<skill>-<date>` and opens a pull request with `gh` or `glab` when available. See [Propose a change](/docs/guides/propose-a-change/).

| Flag | Effect |
|---|---|
| `--plugin <plugin>` | Registry plugin to add a personal skill to |
| `--title <title>` | Pull request title |

### skillcrew doctor

```text
skillcrew doctor [--json]
```

Checks git, the configuration, the registry, the last sync and fetch, every agent hook, the timer, the sync triggers, installed skills, Claude Code symlinks and duplicates. Each line is `✓` (ok), `!` (warning) or `✗` (error); the command exits with an error when a check fails. See [Diagnose with doctor](/docs/troubleshooting/doctor/).

```console
$ skillcrew doctor --json
[
  {
    "name": "git",
    "level": "ok",
    "detail": "git found"
  }
]
```

`level` is `ok`, `warning` or `error`.

### skillcrew uninstall

```text
skillcrew uninstall [-y|--yes]
```

Lists what it removes, asks for confirmation, then removes the team skills, the agent hooks, the fallback timer and `~/.skillcrew`. Personal skills that team skills replaced are restored when their name is free; backups of your local changes to team skills are kept.

```console
$ skillcrew uninstall
This removes:
  - 2 team skills: commit-msgs, review
  - the session-start hooks of claude-code, codex, copilot-cli, cursor, gemini-cli
  - the registry clone, configuration and state in /home/alice/.skillcrew
Personal skills replaced by team skills are restored when their name is free.
Uninstall Skillcrew? [y/N]
```

`--yes` skips the confirmation. Then remove the binary (`brew uninstall skillcrew`, or delete it).

## Administrator commands

### skillcrew registry init

```text
skillcrew registry init [dir] [flags]
```

In a Claude Code marketplace repository, generate `skillcrew.yaml` listing every skill as `available` (colliding names get an `install_as` alias, invalid skills are blocked) and report what `skillcrew validate` finds. Anywhere else, create a new registry with an example skill and a CI workflow.

| Flag | Effect |
|---|---|
| `--org <name>` | Organization name |
| `--owner <name>` | Marketplace owner name (new registries) |
| `--force` | Overwrite an existing `skillcrew.yaml` |

### skillcrew validate

```text
skillcrew validate [dir] [--json]
```

Check a registry (the current directory by default): skills, governance, plugin layout and name collisions. In a Git checkout, pinned tags (`ref`) and commits (`sha`) must also exist, and a `ref` must be a tag, never only a branch. Errors make the command fail.

The JSON output is an array of findings, empty when the registry is clean:

```json
[
  {
    "severity": "error",
    "message": "teams.backend: data:db-migrations is blocked by the organization; a team cannot allow it",
    "skill": "data:db-migrations"
  }
]
```

## Shell completion

```sh
source <(skillcrew completion zsh)   # current zsh session
```

`skillcrew completion <shell> --help` explains how to load the script permanently for each shell.
