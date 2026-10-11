---
title: Teams and resolution rules
description: How organization rules, team rules and a developer's own choices combine into one status per skill.
section: concepts
order: 3
---

## Three levels

1. **Organization**: the `skills` map of `skillcrew.yaml` applies to everyone.
2. **Team**: each entry under `teams` applies to its members. A developer can belong to several teams.
3. **Developer**: their own choices, stored in `~/.skillcrew/config.yaml` (disabled and opted-in skills).

```yaml
org: acme
default: available

skills:
  security:review: required
  docs:pdf: available

teams:
  backend:
    skills:
      data:db-migrations: required
  frontend:
    skills:
      docs:pdf: recommended
```

Here every developer gets `security:review`. Backend members also get `data:db-migrations`. Frontend members get `docs:pdf` by default and can disable it; everyone else can install it.

## Membership

Team membership is manual: each developer joins the teams they belong to.

```console
$ skillcrew team list
TEAM      MEMBER
backend   joined
frontend  -
$ skillcrew team join frontend
Joined team frontend.
Synced registry 7f396e1a9c2d:
  + pdf (installed)
$ skillcrew team leave frontend
Left team frontend.
Synced registry 7f396e1a9c2d:
  - pdf (removed)
```

`skillcrew init <registry> --team <team>` joins at setup time; the flag is repeatable.

## Resolution rules

1. The organization rule applies to everyone; team rules apply to team members.
2. A team can harden a status (`available` → `recommended` → `required`), never soften an organization `required`.
3. When teams disagree, a requirement wins over another team's block: if one of your teams requires a skill, you get it.
4. An organization `blocked` always wins.
5. Developers can only disable `recommended` and `deprecated` skills and install `available` ones.

`ref`, `sha` and `install_as` are allowed at organization level only.

## Conflicts are caught in review

`skillcrew validate` reports every rule a team cannot express, so the pull request fails before anything reaches a machine:

```console
$ skillcrew validate
error: teams.backend: data:db-migrations is blocked by the organization; a team cannot allow it
Error: the registry is invalid
```

The generated CI workflow runs `validate` on every pull request: see [Validate in CI](/docs/guides/ci/).
