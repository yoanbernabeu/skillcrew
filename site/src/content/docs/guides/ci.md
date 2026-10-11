---
title: Validate in CI
description: Run skillcrew validate on every pull request so a broken registry never reaches developer machines.
section: guides
order: 5
---

`skillcrew validate` checks a registry: skills, governance, plugin layout and name collisions. Errors make it exit with a non-zero status, which fails the pull request.

## The generated workflow

`skillcrew registry init` writes `.github/workflows/validate.yml` in new registries:

```yaml
name: Validate skills

on:
  pull_request:
  push:
    branches: [main]

permissions:
  contents: read

jobs:
  validate:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
          fetch-depth: 0 # tags and history, to check pinned skills
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version: 'stable'
      - run: go run github.com/yoanbernabeu/skillcrew/cmd/skillcrew@latest validate
```

Make the `validate` job a required status check in your branch protection rules.

> [!NOTE]
> Keep `fetch-depth: 0`. In a shallow clone, `validate` cannot check that pinned tags and commits exist, and says so.

## What validate checks

- Every skill id in `skillcrew.yaml` exists under `plugins/`.
- Skill names follow the Agent Skills specification, and installed names do not collide (use `install_as`).
- Team rules do not soften organization rules, and `ref`, `sha` and `install_as` only appear at organization level.
- Pins: `sha` is a full commit id, `ref` is a tag; in a Git checkout, both exist and a `ref` is not only a branch.
- Every plugin listed in `marketplace.json` has its source directory. Directories under `plugins/` that hold skills but are not listed, and plugin features Skillcrew ignores, are reported as warnings.

```console
$ skillcrew validate
Registry acme is valid: 6 skills.
```

## Other CI systems

`validate` takes an optional directory and has a `--json` output for tooling:

```sh
skillcrew validate path/to/registry
skillcrew validate --json
```

Install the binary with the [install script](/docs/getting-started/install/#install-script) or run it with `go run`, as in the workflow above.
