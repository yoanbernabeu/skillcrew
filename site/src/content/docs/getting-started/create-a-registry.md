---
title: Create a registry
description: Create the Git repository that holds your organization's skills and governance, then publish it.
section: getting-started
order: 2
---

The registry is one Git repository per organization. It uses the Claude Code marketplace layout plus a `skillcrew.yaml` governance file, so it also works as a regular Claude Code marketplace. This page is for administrators.

## Step 1: Scaffold the registry

In an empty Git repository:

```console
$ mkdir acme-skills && cd acme-skills && git init
$ skillcrew registry init --org acme
Created a new Skillcrew registry.
  + .claude-plugin/marketplace.json
  + .github/workflows/validate.yml
  + AGENTS.md
  + CLAUDE.md
  + README.md
  + plugins/example/.claude-plugin/plugin.json
  + plugins/example/skills/hello-world/SKILL.md
  + skillcrew.yaml
Add skills with "skillcrew skill new <plugin:skill>", then commit and push.
```

You get a marketplace with one example plugin and skill, a `skillcrew.yaml`, a README for your developers, an `AGENTS.md` (imported by `CLAUDE.md`) that tells AI agents how to change the registry, and a GitHub Actions workflow that runs `skillcrew validate` on every pull request.

Hosting the registry on GitLab? Add `--ci gitlab` to get a `.gitlab-ci.yml` instead, which runs `skillcrew validate` on every merge request (`--ci none` writes no CI file). See [Validate in CI](/docs/guides/ci/).

Already have a Claude Code marketplace? Run the same command inside it: see [Convert a Claude Code marketplace](/docs/guides/convert-a-claude-code-marketplace/).

## Step 2: Add skills

```console
$ skillcrew skill new git:commit-message --status required \
    --description "Writes commit messages following the team convention. Use it whenever you create a git commit."
Created skill git:commit-message.
  + plugins/git/.claude-plugin/plugin.json
  + plugins/git/skills/commit-message/SKILL.md
  ~ .claude-plugin/marketplace.json
  ~ skillcrew.yaml
Status: required for the organization.
Next: write the instructions in plugins/git/skills/commit-message/SKILL.md, run "skillcrew validate", then commit.
```

`skill new` creates the skill folder and its `SKILL.md`, creates the plugin and lists it in `.claude-plugin/marketplace.json` when it is new, and sets the status in `skillcrew.yaml`. Then write the instructions in the `SKILL.md`, by hand or with your AI agent: the generated `AGENTS.md` gives it the rules of the registry.

Skill ids are `plugin:skill`, the notation Claude Code uses. Skill names use lowercase letters, digits and single hyphens. The `description` says what the skill does and when to use it: agents read it to decide when to load the skill. See [`skillcrew skill new`](/docs/reference/cli/#skillcrew-skill-new).

The resulting layout:

```text
.claude-plugin/marketplace.json     # lists the plugins
plugins/
  git/
    .claude-plugin/plugin.json
    skills/commit-message/SKILL.md  # skill id: git:commit-message
skillcrew.yaml
```

## Step 3: Set statuses and teams

`--status` sets a status for the whole organization; add `--team` to set it for one team:

```sh
skillcrew skill new symfony:new-project --status required --team backend \
  --description "Creates a new Symfony project following the team standards. Use it when the user wants to start a new Symfony application."
```

`skillcrew.yaml` then holds:

```yaml
org: acme
default: available                 # status of skills not listed below

skills:
  example:hello-world: recommended # plugin:skill, as in Claude Code
  git:commit-message: required

teams:
  backend:
    skills:
      symfony:new-project: required
```

Change statuses and teams later by editing `skillcrew.yaml`. Each skill is `required`, `recommended`, `available`, `deprecated` or `blocked`, for the whole organization and per team. See [Statuses](/docs/concepts/statuses/) and the [skillcrew.yaml reference](/docs/reference/skillcrew-yaml/).

## Step 4: Validate

```console
$ skillcrew validate
Registry acme is valid: 3 skills.
```

`validate` checks that every id exists under `plugins/`, that skill names do not collide, that team rules do not soften organization rules, and that pins exist. Errors make the command fail.

## Step 5: Publish

```sh
git add -A && git commit -m "Create the skills registry"
gh repo create acme/skills --private --source . --push
```

Any Git host works: GitHub, GitLab, a self-hosted server or Azure DevOps. Developers only need read access, with the Git credentials they already use.

> [!SECURITY]
> Skillcrew pushes the registry content to every developer machine automatically. Protect the default branch with required reviews before you invite developers. See [Security model](/docs/concepts/security-model/).

## Next steps

- Share the join command with your developers: `skillcrew init github.com/acme/skills --team backend`.
- Keep the generated CI workflow: [Validate in CI](/docs/guides/ci/).
