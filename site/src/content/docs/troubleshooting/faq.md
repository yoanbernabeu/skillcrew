---
title: FAQ
description: Short answers about local edits, name collisions, pinning, project skills and uninstalling.
section: troubleshooting
order: 2
---

## What happens to my local edits to a team skill?

They are backed up in `~/.skillcrew/backups/<skill>/` (last 5 versions) and the team version is restored. `skillcrew propose <skill>` turns them into a pull request. See [Propose a change](/docs/guides/propose-a-change/).

## I already have a personal skill with the same name.

It is backed up (and never pruned) and replaced by the team skill; `skillcrew uninstall` puts it back. Rename yours to keep both.

## Two registry skills have the same name.

`skillcrew validate` reports it; give one an `install_as` alias in `skillcrew.yaml`. See [install_as](/docs/reference/skillcrew-yaml/#install_as).

## Can a skill be pinned?

Yes: `ref` (a tag) or `sha` (a full commit id). Otherwise skills follow the registry branch. See [Pin a skill](/docs/guides/pinning/).

## What about project-specific skills?

Commit them in the project (`.agents/skills/`, `.claude/skills/`): Git already distributes them. Skillcrew manages user-level skills.

## Do I need to run an update command?

No. Session-start hooks in your agents and an hourly timer run `skillcrew sync` for you. `skillcrew sync` exists for when you do not want to wait.

## Does Skillcrew send telemetry?

No. The open source project contains no telemetry code.

## Can I use several registries?

No. One organization has one registry. Skills from elsewhere are copied into the registry through a pull request.

## How do I uninstall?

`skillcrew uninstall` lists what it removes, then removes hooks, the timer, team skills and `~/.skillcrew`; personal skills that team skills replaced are restored when their name is free, and backups of your local changes are kept. Then `brew uninstall skillcrew` or delete the binary.
