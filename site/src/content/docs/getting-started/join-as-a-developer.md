---
title: Join as a developer
description: Run one command to get your organization's and your teams' skills in every agent, kept up to date.
section: getting-started
order: 3
---

Joining takes one command. After it, skills follow the registry with no action from you.

## Join your organization

```console
$ skillcrew init github.com/acme/skills --team backend
Cloning https://github.com/acme/skills.git...
Joined organization acme (registry https://github.com/acme/skills.git, branch main).
  ✓ claude-code: session-start hook installed
  ✓ codex: session-start hook installed
  ✓ copilot-cli: session-start hook installed
  ✓ cursor: session-start hook installed
  ✓ gemini-cli: session-start hook installed
  ✓ fallback timer installed (every 1h0m0s)
Synced registry 7f396e1a9c2d:
  + db-migrations (installed)
  + commit-msgs (installed)
  + review (installed)
Start a new agent session to load the skills. "skillcrew list --available" shows the skills you can add.
```

The registry can be any Git URL (`git@github.com:acme/skills.git`), a local path, or the `host/owner/repo` shorthand, which becomes an HTTPS URL. Private registries work with your existing Git credentials: see [Private registries](/docs/guides/private-registries/).

Not sure which team to join? Skip `--team`, then list them:

```console
$ skillcrew team list
TEAM      MEMBER
backend   joined
frontend  -
$ skillcrew team join frontend
```

`init` is idempotent: run it again to repair hooks or the timer.

> [!NOTE]
> Open a new agent session after `init`. Claude Code reloads skills live; other agents see new and updated skills at their next session.

## What init sets up

- A clone of the registry in `~/.skillcrew/registry.git`.
- A **session-start hook** in each agent it detects (Claude Code, Codex, Copilot CLI and VS Code, Cursor, Gemini CLI). The hook runs `skillcrew sync --hook`, which returns immediately and prints nothing. When the last sync is older than the debounce window (60 seconds by default), it starts a sync in the background, so an agent never waits on the network.
- A **user-level timer** (launchd on macOS, systemd user units on Linux) that runs `skillcrew sync --quiet` every hour, for agents without hooks.
- The skills themselves: real copies in `~/.agents/skills/<skill>`, and symlinks in `~/.claude/skills/<skill>` for Claude Code.

No daemon, no admin rights. Flags: `--no-hooks`, `--no-timer`, `--ref <branch>` (branch to follow instead of the default branch). See [the CLI reference](/docs/reference/cli/#skillcrew-init).

## Check what you have

```console
$ skillcrew status
Organization  acme
Registry      https://github.com/acme/skills.git (main @ 7f396e1a9c2d)
Teams         backend
Last sync     2026-10-11 13:52

SKILL          ID                  STATUS       STATE  VERSION
db-migrations  data:db-migrations  required     ok     7f396e1a9c2d
commit-msgs    git:commit-msgs     recommended  ok     7f396e1a9c2d
review         security:review     required     ok     7f396e1a9c2d
```

## Choose your optional skills

```console
$ skillcrew list --available
SKILL        ID                   STATUS     STATE  VERSION
pdf          docs:pdf             available  -      -
hello-world  example:hello-world  available  -      -
$ skillcrew install pdf
Installed docs:pdf.
Synced registry 7f396e1a9c2d:
  + pdf (installed)
```

- `skillcrew install` / `skillcrew remove` add or remove an `available` skill.
- `skillcrew disable` / `skillcrew enable` turn a `recommended` or `deprecated` skill off or back on.
- `required` skills cannot be turned off, and `blocked` skills cannot be installed.

Your choices are stored in `~/.skillcrew/config.yaml` and respected by every sync.

## Your own skills

Skillcrew only touches the directories it installed. Personal skills in `~/.agents/skills` or `~/.claude/skills` stay yours, unless one has the same name as a team skill: it is then backed up, replaced by the team skill, and restored by `skillcrew uninstall` when the name is free.

If you edit a team skill, the next sync backs up your version and restores the team one. To share your change, [propose it](/docs/guides/propose-a-change/).
