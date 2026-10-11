---
title: skillcrew.yaml
description: Full reference of the governance file at the root of the registry, and of the per-machine config.yaml.
section: reference
order: 2
---

Skillcrew reads two files: `skillcrew.yaml` at the root of the registry (governance, written by administrators) and `~/.skillcrew/config.yaml` on each machine (written by `skillcrew` commands).

## Complete example

```yaml
org: acme                     # required
default: available            # status of skills not listed below (default: available)

skills:
  security:review: required   # short form: just the status
  git:commit-messages:
    status: recommended
  quality:old-linter:
    status: deprecated
    message: Replaced by quality:lint-rules.
    replaced_by: quality:lint-rules
  docs:pdf:
    status: available
    sha: 3f2a9c1e5b7d4a8f9e0c1b2a3d4e5f6a7b8c9d0e   # pin to a full commit id
  tools:format:
    status: required
    ref: format-v2            # pin to a tag
  frontend:review:
    status: recommended
    install_as: frontend-review   # alias: avoids a collision with security:review
  misc:risky: blocked

teams:
  backend:
    skills:
      data:db-migrations: required
```

## Top-level keys

| Key | Required | Description |
|---|---|---|
| `org` | yes | Organization name |
| `default` | no | Status of skills not listed under `skills` (default: `available`) |
| `skills` | no | Organization rules, keyed by skill id |
| `teams` | no | Team rules: `teams.<team>.skills`, keyed by skill id |

## Skill ids

Keys under `skills` are `plugin:skill` ids, the notation Claude Code uses: `security:review` is `plugins/security/skills/review/SKILL.md`. Every id must exist in the registry.

## Skill entries

An entry is either a status (short form) or a map:

| Field | Level | Description |
|---|---|---|
| `status` | organization, team | `required`, `recommended`, `available`, `deprecated` or `blocked` |
| `message` | organization | Deprecation notice shown by `status` and `list` |
| `replaced_by` | organization | Id of the replacement skill, shown with the notice |
| `sha` | organization | Pin to a full commit id (40 or 64 lowercase hexadecimal characters) |
| `ref` | organization | Pin to a tag, without `refs/tags/` |
| `install_as` | organization | Name to install the skill under, to avoid a collision |

`ref`, `sha` and `install_as` are allowed at organization level only. `skillcrew validate` reports every violation.

## Statuses

| Status | Behavior |
|---|---|
| `required` | Installed automatically, kept up to date, restored if removed or edited. Cannot be disabled. |
| `recommended` | Installed by default; developers can `skillcrew disable` it. |
| `available` | Installed on demand with `skillcrew install`. |
| `deprecated` | Stays where it is already installed (or opted in), is never installed on new machines, can be disabled. `skillcrew enable` on a disabled deprecated skill reinstalls it. |
| `blocked` | Never installed; removed from every machine at the next sync. `validate` only warns about its content. |

See [Statuses](/docs/concepts/statuses/) and [Teams and resolution rules](/docs/concepts/teams-and-resolution-rules/).

## install_as

A skill is installed under its bare name (`review`). Two installed skills with the same bare name collide; `install_as` gives one of them another name, and its `SKILL.md` `name` is rewritten accordingly. `skillcrew registry init` adds `<plugin>-<name>` aliases when it converts a marketplace with colliding names.

## Pins

Without a pin, a skill follows the branch developers track. `sha` must be a full commit id; `ref` must be a tag name valid for `git check-ref-format`. Pins never resolve to a branch. See [Pin a skill](/docs/guides/pinning/).

## Registry layout rules

- Only plugins stored in the repository (`"source": "./plugins/x"` or a bare name under `metadata.pluginRoot`) are distributed. Plugins hosted elsewhere are ignored with a warning.
- A plugin's skills are `skills/<name>/SKILL.md`; a plugin with a `SKILL.md` at its root and no `skills/` directory is a single skill.
- `skillcrew validate` fails when a listed plugin's source directory does not exist, and warns about directories under `plugins/` (or `metadata.pluginRoot`) that hold skills but are not listed.
- Skill names follow the [Agent Skills specification](https://agentskills.io/specification): lowercase letters, digits and single hyphens, matching the directory name.

## ~/.skillcrew/config.yaml

Written by `skillcrew init`, `team`, `install` / `remove` and `enable` / `disable`; you rarely edit it by hand.

```yaml
registry: git@github.com:acme/skills.git
ref: main                 # branch to follow
teams: [backend]
disabled: [git:commit-messages]   # recommended or deprecated skills turned off
installed: [docs:pdf]             # available skills turned on, deprecated skills re-enabled
debounce: 60s             # minimum delay between two fetches (default 60s)
interval: 1h              # fallback timer period (default 1h; re-run "skillcrew init" to apply)
```
