# Configuration reference

Skillcrew reads two files: `skillcrew.yaml` at the root of the registry (governance, written by administrators) and `~/.skillcrew/config.yaml` on each machine (written by `skillcrew` commands).

## Registry layout

A registry is a [Claude Code marketplace](https://code.claude.com/docs/en/plugins/marketplace-reference) plus a `skillcrew.yaml`:

```
.claude-plugin/marketplace.json     # lists the plugins
plugins/
  security/
    skills/review/SKILL.md          # skill id: security:review
skillcrew.yaml
```

- Only plugins stored in the repository (`"source": "./plugins/x"` or a bare name under `metadata.pluginRoot`) are distributed. Plugins hosted elsewhere (`github`, `url`, `npm`… sources) are ignored with a warning.
- A plugin's skills are `skills/<name>/SKILL.md`; a plugin with a `SKILL.md` at its root and no `skills/` directory is a single skill. Skillcrew distributes nothing else: custom `skills` paths declared in `plugin.json` or `marketplace.json`, commands, agents, hooks and MCP servers are ignored, and `skillcrew validate` warns about such plugins.
- `skillcrew validate` fails when a listed plugin's source directory does not exist, and warns about directories under `plugins/` (or `metadata.pluginRoot`) that hold skills but are not listed.
- Skill names must follow the [Agent Skills specification](https://agentskills.io/specification): lowercase letters, digits and single hyphens, matching the directory name.

## `skillcrew.yaml`

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

### Skill ids

`plugin:skill`, the notation Claude Code uses. A skill is installed under its bare name (`review`); two installed skills with the same bare name collide, and `install_as` gives one of them another name (its `SKILL.md` `name` is rewritten accordingly). `skillcrew registry init` adds `<plugin>-<name>` aliases when it converts a marketplace with colliding names.

### Statuses

| Status | Behavior |
|---|---|
| `required` | Installed automatically, kept up to date, restored if removed or edited. Cannot be disabled. |
| `recommended` | Installed by default; developers can `skillcrew disable` it. |
| `available` | Installed on demand with `skillcrew install`. |
| `deprecated` | Stays where it is already installed (or opted in), is never installed on new machines, can be disabled; `message` and `replaced_by` are shown by `skillcrew status` and `list`. `skillcrew enable` on a disabled deprecated skill reinstalls it (it is added to `installed`). |
| `blocked` | Never installed; removed from every machine at the next sync. `skillcrew validate` only warns about the content of a blocked skill, since it is never installed. |

### Resolution rules

1. The organization rule applies to everyone; team rules apply to team members.
2. A team can harden a status (`available` → `recommended` → `required`), never soften an organization `required`.
3. When teams disagree, a requirement wins over another team's block: if one of your teams requires a skill, you get it.
4. An organization `blocked` always wins.
5. Developers can only disable `recommended` and `deprecated` skills and install `available` ones.

`ref`, `sha` and `install_as` are allowed at organization level only. `skillcrew validate` reports every violation.

### Pins

Without a pin, a skill follows the branch developers track (the registry default branch). `sha` must be a full commit id (40 or 64 lowercase hexadecimal characters); `ref` must be a tag name valid for `git check-ref-format`, without `refs/tags/`. Pins never resolve to a branch, so nobody can override a pin by pushing a branch with the same name. Protect your tags. Skillcrew treats tags as immutable: once fetched, a tag keeps its first commit. Manual syncs and the hourly timer report a tag moved upstream; agent hooks do not check, to spare a network round trip at every session start.

In a git checkout, `skillcrew validate` also checks that the tag or commit exists and that a `ref` is not only a branch. In a shallow clone it cannot, and says so: the generated CI workflow checks out the full history (`fetch-depth: 0`).

## `~/.skillcrew/config.yaml`

Written by `skillcrew init`, `team`, `install`/`remove` and `enable`/`disable`; you rarely edit it by hand.

```yaml
registry: git@github.com:acme/skills.git
ref: main                 # branch to follow
teams: [backend]
disabled: [git:commit-messages]   # recommended or deprecated skills turned off
installed: [docs:pdf]             # available skills turned on, deprecated skills re-enabled
debounce: 60s             # minimum delay between two fetches (default 60s)
interval: 1h              # fallback timer period (default 1h; re-run "skillcrew init" to apply)
```

## Files on each machine

| Path | Content |
|---|---|
| `~/.agents/skills/<skill>` | Installed team skills (real copies) |
| `~/.agents/.skillcrew-staging` | Skills being prepared before they are swapped in (transient) |
| `~/.claude/skills/<skill>` | Symlinks for Claude Code |
| `~/.skillcrew/registry.git` | Bare clone of the registry |
| `~/.skillcrew/cache/<tree id>` | Exported skills, one per installed version (git tree id) |
| `~/.skillcrew/lock.json` | What Skillcrew installed (ownership) |
| `~/.skillcrew/state.json`, `sync.log` | Last sync result and history |
| `~/.skillcrew/sync.lock` | Held while a sync or a command changing skills runs |
| `~/.skillcrew/backups/<skill>` | Backups of edited team skills (last 5) and replaced personal skills (restored by `skillcrew uninstall`) |
