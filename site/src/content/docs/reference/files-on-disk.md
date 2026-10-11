---
title: Files on disk
description: Every file and directory Skillcrew reads or writes on a developer machine.
section: reference
order: 3
---

Skillcrew works in the user's home directory only. It needs no admin rights and runs no resident process.

## Skills

| Path | Content |
|---|---|
| `~/.agents/skills/<skill>` | Installed team skills (real copies), read by Codex, Copilot, Cursor, Gemini CLI and others |
| `~/.claude/skills/<skill>` | Symlinks to `~/.agents/skills/<skill>`, for Claude Code |
| `~/.agents/.skillcrew-staging` | Skills being prepared before they are swapped in (transient) |

Skillcrew only modifies the skill directories it installed, as recorded in its lockfile. Your personal skills next to them are left alone.

## State

| Path | Content |
|---|---|
| `~/.skillcrew/config.yaml` | Registry, branch, teams and your choices (see [config.yaml](/docs/reference/skillcrew-yaml/#skillcrewconfigyaml)) |
| `~/.skillcrew/registry.git` | Bare clone of the registry |
| `~/.skillcrew/cache/<tree id>` | Exported skills, one per installed version (Git tree id) |
| `~/.skillcrew/lock.json` | What Skillcrew installed: registry commit and content hash of each skill (ownership) |
| `~/.skillcrew/state.json` | Last sync result |
| `~/.skillcrew/sync.log` | Sync history |
| `~/.skillcrew/sync.lock` | Held while a sync or a command changing skills runs |
| `~/.skillcrew/backups/<skill>` | Backups of edited team skills (last 5) and replaced personal skills (restored by `skillcrew uninstall`) |

## Agent hooks

`skillcrew init` merges a session-start hook into each detected agent's configuration, without touching anything else in the file. `skillcrew uninstall` removes it.

| Agent | File | Hook |
|---|---|---|
| Claude Code | `~/.claude/settings.json` | `SessionStart` |
| Codex CLI | `~/.codex/hooks.json` | `SessionStart` |
| Copilot CLI / VS Code | `~/.copilot/hooks/skillcrew.json` | `sessionStart` |
| Cursor | `~/.cursor/hooks.json` | `sessionStart` |
| Gemini CLI | `~/.gemini/settings.json` | `SessionStart` |

The hook runs `skillcrew sync --hook` (`--hook=json` for Gemini CLI), with the absolute path of the binary that ran `init`. It returns immediately; when a sync is due, it starts one detached in the background (the hidden `skillcrew sync --background`), which writes its result to `~/.skillcrew/sync.log`.

Codex asks you to trust new hooks once: run `/hooks` in Codex and approve the Skillcrew hook.

## Fallback timer

| OS | Files |
|---|---|
| macOS | `~/Library/LaunchAgents/co.yoandev.skillcrew.sync.plist` (launchd LaunchAgent) |
| Linux | `~/.config/systemd/user/skillcrew-sync.service` and `skillcrew-sync.timer` (systemd user units) |

The timer runs `skillcrew sync --quiet` every hour by default. Change `interval` in `~/.skillcrew/config.yaml`, then re-run `skillcrew init <registry>` to apply it.

## Logs

Sync results and errors go to `~/.skillcrew/sync.log`. Hooks print nothing on standard output, because some agents inject hook output into the model context.
