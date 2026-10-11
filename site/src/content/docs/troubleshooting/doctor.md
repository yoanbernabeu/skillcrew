---
title: Diagnose with doctor
description: Run skillcrew doctor first, then use the table of symptoms and fixes.
section: troubleshooting
order: 1
---

## Run doctor

```console
$ skillcrew doctor
✓ git          git found
✓ registry     https://github.com/acme/skills.git (main @ 7f396e1a9c2d)
✓ sync         last sync on 2026-10-11 13:52
✓ claude-code  session-start hook installed
✓ codex        session-start hook installed
✓ copilot-cli  session-start hook installed
✓ cursor       session-start hook installed
✓ gemini-cli   session-start hook installed
✓ timer        fallback timer installed
✓ skills       3 team skills installed
✓ symlinks     team skills linked from /home/alice/.claude/skills
```

`doctor` checks git, the registry, the last sync and fetch, every agent hook, the timer, the sync triggers, installed skills, Claude Code symlinks and duplicates. `✓` is ok, `!` a warning, `✗` an error; the command exits with `doctor found problems` when a check fails. Add `--json` for a machine-readable report.

## Symptoms and fixes

| Symptom | Fix |
|---|---|
| `<agent> session-start hook missing` | Re-run `skillcrew init <registry>`; it is idempotent. |
| `timer fallback timer not installed` | On WSL2 without systemd, enable systemd or rely on hooks. |
| `triggers nothing triggers syncs` | No hook and no timer: re-run `skillcrew init <registry>` without `--no-hooks` / `--no-timer` once an agent is installed. |
| `fetch the last sync used the last fetched registry (offline)` | Run `git -C ~/.skillcrew/registry.git fetch` to see the error, then fix your credentials or network. |
| `sync last sync failed: …` | Read the error; `~/.skillcrew/sync.log` has the history. |
| A skill shows as `modified` | You edited a team skill; the next sync restores it (your version is backed up). |
| Skills appear twice in an agent | `doctor` lists directories present in both `~/.claude/skills` and `~/.agents/skills`; remove your duplicate. |
| New skills not visible | Claude Code reloads skills live; other agents load them when a session starts: open a new chat or run their reload command. The hook syncs in the background, so an update may land just after the session that triggered it. |
| Codex never triggers a sync | Run `/hooks` in Codex once and trust the Skillcrew hook. The hourly timer keeps Codex up to date either way. |

## Offline

A failed fetch never blocks you: the sync uses the last fetched registry and says so.

```console
$ skillcrew sync
Using the last fetched registry (offline), at 7f396e1a9c2d:
  ! offline: using the last fetched registry (git ls-remote origin -- refs/heads/main: fatal: Could not read from remote repository. …)
```

For credential problems, see [Private registries](/docs/guides/private-registries/#when-init-cannot-authenticate).

## Logs

Every sync appends to `~/.skillcrew/sync.log`. `skillcrew status --json` includes `last_error` when the last sync failed.

## Start over

```sh
skillcrew uninstall --yes
skillcrew init github.com/acme/skills --team backend
```

`uninstall` keeps the backups of your local changes and restores the personal skills that team skills replaced.
