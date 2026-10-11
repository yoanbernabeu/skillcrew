---
title: Convert a Claude Code marketplace
description: Turn an existing Claude Code marketplace repository into a Skillcrew registry with one command.
section: guides
order: 4
---

A Skillcrew registry is a Claude Code marketplace plus a `skillcrew.yaml`. If your organization already has a marketplace, you only add the governance file; the repository keeps working in Claude Code.

## Run registry init inside the marketplace

```console
$ cd acme-marketplace
$ skillcrew registry init --org acme
Converted the Claude Code marketplace into a Skillcrew registry.
  + skillcrew.yaml
  ! plugin "remote" is hosted outside the registry and is ignored
  ! skills named "review" collide; install_as installs frontend:review as frontend-review, security:review as security-review
Edit skillcrew.yaml to set statuses and teams, then commit and push.
```

The generated `skillcrew.yaml`:

- lists every skill as `available`, so nothing is installed until you decide;
- gives colliding skills an `install_as` alias, `<plugin>-<name>`;
- blocks skills that fail validation;
- and the command prints what `skillcrew validate` finds, so you know what is left to fix.

```yaml
org: acme

default: available

skills:
  frontend:review:
    status: available
    install_as: frontend-review
  security:review:
    status: available
    install_as: security-review
```

Use `--force` to overwrite an existing `skillcrew.yaml`.

## What is not converted

Skillcrew distributes skills only:

- Plugins hosted outside the repository (`github`, `url`, `npm`… sources) are ignored with a warning. Copy their skills into the registry with a pull request if you need them.
- Commands, agents, hooks and MCP servers of your plugins are ignored, and custom `skills` paths in `plugin.json` or `marketplace.json` are not followed. `skillcrew validate` warns about such plugins.

## Then

1. Set real statuses: make your baseline `required` or `recommended`. See [Statuses](/docs/concepts/statuses/).
2. Run `skillcrew validate` until it passes.
3. Add the CI check: [Validate in CI](/docs/guides/ci/).
4. Commit, push, and share `skillcrew init <registry>` with your developers.
