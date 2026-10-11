---
title: Pin a skill
description: Hold a skill at a tag or a commit while the rest of the registry moves on.
section: guides
order: 2
---

Without a pin, a skill follows the branch developers track (the registry default branch, or the branch given to `skillcrew init --ref`). A pin freezes one skill at a known version.

## Pin to a tag

```yaml
skills:
  tools:format:
    status: required
    ref: format-v2            # a tag, without refs/tags/
```

`ref` must be a tag name valid for `git check-ref-format`, written without `refs/tags/`.

## Pin to a commit

```yaml
skills:
  docs:pdf:
    status: available
    sha: 3f2a9c1e5b7d4a8f9e0c1b2a3d4e5f6a7b8c9d0e   # a full commit id
```

`sha` must be a full commit id: 40 or 64 lowercase hexadecimal characters. Abbreviated ids are rejected.

## Rules

- Pins are allowed at organization level only, not in team rules.
- Pins never resolve to a branch. Nobody can override a pin by pushing a branch with the same name as a tag.
- In a Git checkout, `skillcrew validate` checks that the tag or commit exists and that a `ref` is not only a branch.

> [!WARNING]
> In a shallow clone, `validate` cannot check pins, and says so. The generated CI workflow checks out the full history (`fetch-depth: 0`) for this reason.

> [!SECURITY]
> Protect your tags. A pin to a tag is only as stable as the tag: if anyone can move or delete it, they can change the pinned skill.

## Unpin

Remove `ref` or `sha`. The skill follows the branch again at the next sync.
