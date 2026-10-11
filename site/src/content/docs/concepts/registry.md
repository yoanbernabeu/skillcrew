---
title: The registry
description: One Git repository per organization holds every skill and the rules that decide who gets which.
section: concepts
order: 1
---

## One repository, one source of truth

An organization has exactly one registry: a Git repository with the [Claude Code marketplace](https://code.claude.com/docs/en/plugins/marketplace-reference) layout and a `skillcrew.yaml` file at its root.

```text
.claude-plugin/marketplace.json     # lists the plugins
plugins/
  security/
    skills/review/SKILL.md          # skill id: security:review
  frontend/
    skills/review/SKILL.md          # skill id: frontend:review
skillcrew.yaml                      # governance: statuses, teams, pins
```

Because the layout is a Claude Code marketplace, the same repository still works as a marketplace in Claude Code. Skills from elsewhere enter the registry through a pull request, like any other change.

## What is distributed

- Only plugins stored in the repository are distributed: `"source": "./plugins/x"`, or a bare name under `metadata.pluginRoot`. Plugins hosted elsewhere (`github`, `url`, `npm` sources…) are ignored with a warning.
- A plugin's skills are `skills/<name>/SKILL.md`. A plugin with a `SKILL.md` at its root and no `skills/` directory is a single skill.
- Nothing else: custom `skills` paths declared in `plugin.json` or `marketplace.json`, commands, agents, hooks and MCP servers are ignored, and `skillcrew validate` warns about such plugins.
- Skill names follow the [Agent Skills specification](https://agentskills.io/specification): lowercase letters, digits and single hyphens, matching the directory name.

## Skill ids and installed names

A skill id is `plugin:skill`, the notation Claude Code uses. On developer machines, a skill is installed under its bare name: `security:review` becomes `~/.agents/skills/review`.

Two skills with the same bare name collide. `skillcrew validate` reports it, and you give one of them another name with `install_as` in `skillcrew.yaml`; its `SKILL.md` `name` is rewritten accordingly.

## From merge to machines

1. A pull request changes the registry; CI runs `skillcrew validate`; a reviewer merges it.
2. On each developer machine, the next agent session start (hook) or the hourly timer runs `skillcrew sync`. The hook returns immediately and starts the sync in the background, so the agent never waits.
3. The sync fetches the registry, resolves the skills this developer should have, then installs, updates or removes them atomically: each skill is prepared in a staging directory and swapped in, so no agent ever reads a half-written skill.

```text
 Registry (one Git repo, Claude Code marketplace layout + skillcrew.yaml)
      │  git fetch (your existing Git credentials)
      ▼
 skillcrew sync ── runs when any agent session starts (hook) + hourly timer
      │
      ├─▶ ~/.agents/skills/<skill>    real copies  (Codex, Copilot, Cursor, Gemini CLI, …)
      └─▶ ~/.claude/skills/<skill>    symlinks     (Claude Code)
```

Syncs are debounced (one per minute by default), so several agents starting at once cost a single fetch. If the registry cannot be fetched, the sync uses the last fetched registry: skills keep working offline.

Claude Code reloads skills live, so it picks up an update during the session that triggered it. Other agents load skills when a session starts: they see the update at their next session.

## Which agents read what

| Agent | Skills read from | Sync trigger | Tested |
|---|---|---|---|
| Claude Code | `~/.claude/skills` (symlinks) | `SessionStart` hook | Verified: skills and hook |
| Codex CLI | `~/.agents/skills` | `SessionStart` hook | Skills listed; hook not verified yet |
| GitHub Copilot CLI / VS Code | `~/.agents/skills` | `sessionStart` hook in `~/.copilot/hooks/`, read by Copilot CLI and VS Code local agent sessions | Not tested |
| Cursor | `~/.agents/skills` | `sessionStart` hook | Not tested |
| Gemini CLI | `~/.agents/skills` | `SessionStart` hook | Skills listed; hook not tested |
| OpenCode | `~/.agents/skills` | hourly timer | Verified: skills (timer only) |
| Windsurf, Amp, Goose | `~/.agents/skills` | hourly timer | Not tested |

Last checked on 2026-10-11. Agents change fast; `skillcrew doctor` reports what it finds on a machine.

> [!NOTE]
> Codex asks you to trust new hooks once: run `/hooks` in Codex and approve the Skillcrew hook. In a real-world test, Codex 0.162 listed the skills but did not run the hook command, so the hourly timer may be what keeps Codex up to date.

## Project skills are out of scope

Skills that belong to one project are committed in that project (`.agents/skills/`, `.claude/skills/`): Git already distributes them. Skillcrew manages user-level skills only.
