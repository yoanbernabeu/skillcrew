---
title: Statuses
description: Every skill is required, recommended, available, deprecated or blocked, and the status decides where it is installed.
section: concepts
order: 2
---

## The five statuses

| Status | Behavior |
|---|---|
| `required` | Installed automatically, kept up to date, restored if removed or edited. Cannot be disabled. |
| `recommended` | Installed by default; developers can `skillcrew disable` it. |
| `available` | Installed on demand with `skillcrew install`. |
| `deprecated` | Stays where it is already installed (or opted in), is never installed on new machines, can be disabled. Its `message` and `replaced_by` are shown by `skillcrew status` and `list`. |
| `blocked` | Never installed; removed from every machine at the next sync. |

Skills not listed in `skillcrew.yaml` get the `default` status, which is `available` unless you change it.

```yaml
org: acme
default: available

skills:
  security:review: required
  git:commit-msgs: recommended
  docs:pdf: available
  quality:old-linter:
    status: deprecated
    message: Replaced by quality:lint-rules.
    replaced_by: quality:lint-rules
  misc:risky: blocked
```

## What developers can change

| Status | `install` / `remove` | `disable` / `enable` |
|---|---|---|
| `required` | no | no |
| `recommended` | no | yes |
| `available` | yes | no |
| `deprecated` | no | yes; `enable` on a disabled deprecated skill reinstalls it |
| `blocked` | no | no |

The CLI explains refusals:

```console
$ skillcrew disable review
Error: security:review is required for you; it cannot be disabled or removed
$ skillcrew install risky
Error: misc:risky is blocked by your organization; it is never installed
```

## Deprecating a skill

Mark the skill `deprecated` with a `message` and a `replaced_by` id. Machines that have it keep it; new machines never get it; `skillcrew status` shows the notice:

```console
$ skillcrew status
...
SKILL        ID               STATUS      STATE  VERSION
commit-msgs  git:commit-msgs  deprecated  ok     7f396e1a9c2d
review       security:review  required    ok     7f396e1a9c2d
git:commit-msgs is deprecated: Replaced by git:conventional-commits. (replaced by git:conventional-commits)
```

## Blocking a skill: the kill switch

Set a skill to `blocked` and merge. At the next sync (the next agent session, or within the hour with the timer), it is removed from every machine:

```console
$ skillcrew sync
Synced registry ff91ec61ab73:
  - risky (removed: blocked by your organization)
```

`skillcrew validate` only warns about the content of a blocked skill, since it is never installed. You can block a broken skill without fixing it first.

## Team skills are the norm

Every skill installed from the registry, including an `available` skill you opted in to, is a team skill. If you edit one, the next sync backs up your version in `~/.skillcrew/backups/<skill>/` (last 5 versions) and restores the team version. `skillcrew status` shows such a skill as `modified` until then. To change a team skill, [propose the change](/docs/guides/propose-a-change/).

Statuses combine across the organization and your teams: see [Teams and resolution rules](/docs/concepts/teams-and-resolution-rules/).
