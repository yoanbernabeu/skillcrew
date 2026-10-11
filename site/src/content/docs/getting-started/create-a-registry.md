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
  + README.md
  + plugins/example/.claude-plugin/plugin.json
  + plugins/example/skills/hello-world/SKILL.md
  + skillcrew.yaml
Edit skillcrew.yaml to set statuses and teams, then commit and push.
```

You get a marketplace with one example plugin and skill, a `skillcrew.yaml`, a README for your developers and a GitHub Actions workflow that runs `skillcrew validate` on every pull request.

Already have a Claude Code marketplace? Run the same command inside it: see [Convert a Claude Code marketplace](/docs/guides/convert-a-claude-code-marketplace/).

## Step 2: Add skills

A skill is a folder with a `SKILL.md` file, inside a plugin:

```text
.claude-plugin/marketplace.json     # lists the plugins
plugins/
  security/
    .claude-plugin/plugin.json
    skills/review/SKILL.md          # skill id: security:review
skillcrew.yaml
```

1. Create `plugins/<plugin>/skills/<name>/SKILL.md`. The frontmatter needs a `name` equal to the folder name (lowercase letters, digits and single hyphens) and a `description` that says what the skill does and when to use it.
2. List every new plugin in `.claude-plugin/marketplace.json`, with `"source": "./plugins/<plugin>"`.

Skill ids are `plugin:skill`, the notation Claude Code uses: the skill above is `security:review`.

## Step 3: Set statuses and teams

Edit `skillcrew.yaml`:

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

Each skill is `required`, `recommended`, `available`, `deprecated` or `blocked`, for the whole organization and per team. See [Statuses](/docs/concepts/statuses/) and the [skillcrew.yaml reference](/docs/reference/skillcrew-yaml/).

## Step 4: Validate

```console
$ skillcrew validate
Registry acme is valid: 6 skills.
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
