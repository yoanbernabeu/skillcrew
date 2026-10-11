# Guide

## Private registries and authentication

Skillcrew runs the system `git` and never stores credentials. Anything that works for `git clone` works for Skillcrew:

- **SSH**: `skillcrew init git@github.com:acme/skills.git` uses your SSH agent and keys.
- **HTTPS**: configure a credential helper, e.g. `gh auth setup-git` for GitHub or `glab auth setup-git` for GitLab, then `skillcrew init github.com/acme/skills`.

URLs with an embedded password (`https://user:token@…`) are refused, because the URL is stored in plain text. When `skillcrew init` fails with `could not read Username` or `Permission denied (publickey)`, Git has no credentials for the registry: set up the helper or SSH key above, check that `git clone <registry>` works, then run `init` again.

Syncs run non-interactively (hooks, timer): Git never prompts. A failed fetch keeps the last fetched registry: `skillcrew sync` prints `Using the last fetched registry (offline)` with the error, and `skillcrew doctor` reports it (`fetch` check).

## How updates happen

`skillcrew init` installs:

- a **session-start hook** in each detected agent (Claude Code, Codex, Copilot CLI and VS Code, Cursor, Gemini CLI), which runs `skillcrew sync --hook`: debounced (one fetch per minute at most), silent, never failing; the fetch gives up after 2.5 seconds, and after a failed fetch hooks stay offline for 10 minutes, so an agent never waits on the network;
- a **user-level timer** (launchd on macOS, systemd user units on Linux) running `skillcrew sync --quiet` every hour, for agents without hooks.

Each sync fetches the registry, resolves your skills, then installs, updates or removes them atomically. Skills keep working offline.

With no detected agent (or `--no-hooks`) and no timer (or `--no-timer`), nothing syncs automatically: `init` says so and `skillcrew doctor` reports `nothing triggers syncs`. Agents load skills when a session starts: open a new session after `init`.

## Changing a team skill: `skillcrew propose`

Team skills are the norm: if you edit one, the next sync backs your version up in `~/.skillcrew/backups/<skill>/` and restores the team version. To suggest your change:

```sh
skillcrew propose review                  # your local edits, or the latest backup of them
skillcrew propose my-skill --plugin tools # contribute one of your personal skills
```

Skillcrew pushes a branch `skillcrew/<skill>-<date>` to the registry and opens a pull request with `gh` or `glab` when available; otherwise it prints the branch to open the pull request from. You need push access to the registry.

## Troubleshooting

Start with `skillcrew doctor`: it checks git, the registry, the last sync and fetch, every agent hook, the timer, installed skills and duplicates.

| Symptom | Fix |
|---|---|
| `<agent> session-start hook missing` | Re-run `skillcrew init <registry>`; it is idempotent. |
| `timer fallback timer not installed` | On WSL2 without systemd, enable systemd or rely on hooks. |
| `triggers nothing triggers syncs` | No hook and no timer: re-run `skillcrew init <registry>` without `--no-hooks`/`--no-timer` once an agent is installed. |
| `fetch the last sync used the last fetched registry (offline)` | Run `git -C ~/.skillcrew/registry.git fetch` to see the error, then fix your credentials or network. |
| `sync last sync failed: …` | Read the error; `~/.skillcrew/sync.log` has the history. |
| A skill shows as `modified` | You edited a team skill; the next sync restores it (your version is backed up). |
| Skills appear twice in an agent | `doctor` lists directories present in both `~/.claude/skills` and `~/.agents/skills`; remove your duplicate. |
| New skills not visible | Some agents (Cursor, Copilot CLI, Gemini CLI) load skills when a session starts: open a new chat or run their reload command. |

Logs are in `~/.skillcrew/sync.log`.
