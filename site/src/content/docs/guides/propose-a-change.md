---
title: Propose a change
description: Turn your local edits to a team skill, or one of your personal skills, into a pull request on the registry.
section: guides
order: 3
---

Team skills are the norm: if you edit one, the next sync backs up your version in `~/.skillcrew/backups/<skill>/` and restores the team version. `skillcrew propose` turns your edits into a pull request, so they reach everyone after review.

## Improve a team skill

Edit the skill where it is installed (`~/.agents/skills/<skill>`), then propose it:

```sh
skillcrew propose review --title "Check for SSRF in the review skill"
```

`propose` takes your local edits, or the latest backup of them if a sync already restored the team version. It does not matter whether you were quick enough.

## Contribute a personal skill

Add one of your own skills to a registry plugin:

```sh
skillcrew propose my-skill --plugin tools
```

The plugin must exist in the registry.

## What happens

1. Skillcrew pushes a branch `skillcrew/<skill>-<date>` to the registry.
2. It opens a pull request with `gh` (GitHub) or `glab` (GitLab) when one is installed. `--title` sets the pull request title.
3. Otherwise, it prints the branch to open the pull request from:

```console
$ skillcrew propose review
Branch skillcrew/review-20261011-115245 pushed. Open the pull request manually: …
```

You need push access to the registry. The pull request then goes through the usual review and the `skillcrew validate` check.

## Flags

| Flag | Effect |
|---|---|
| `--plugin <plugin>` | Registry plugin to add a personal skill to |
| `--title <title>` | Pull request title |

`<skill>` is a `plugin:skill` id or an installed name, as shown by `skillcrew list`.
