---
title: Private registries
description: Use a private registry over SSH or HTTPS with the Git credentials developers already have.
section: guides
order: 1
---

Skillcrew runs the system `git` and never stores credentials. Anything that works for `git clone` works for Skillcrew.

## SSH

```sh
skillcrew init git@github.com:acme/skills.git
```

Git uses your SSH agent and keys.

## HTTPS

Configure a credential helper once, then use an HTTPS URL or the `host/owner/repo` shorthand:

```sh
gh auth setup-git          # GitHub
glab auth setup-git        # GitLab
skillcrew init github.com/acme/skills
```

Any other credential helper works the same way, including on self-hosted Git servers and Azure DevOps.

> [!SECURITY]
> URLs with an embedded password (`https://user:token@…`) are refused, because the URL is stored in plain text in `~/.skillcrew/config.yaml`. Use a credential helper instead.

## When init cannot authenticate

If `skillcrew init` fails with `could not read Username` or `Permission denied (publickey)`, Git has no credentials for the registry:

1. Set up the SSH key or credential helper above.
2. Check that `git clone <registry>` works in a terminal.
3. Run `skillcrew init` again.

## Background syncs never prompt

Hooks and the timer run non-interactively: Git never prompts for a password. A failed fetch keeps the last fetched registry, so skills keep working:

```console
$ skillcrew sync
Using the last fetched registry (offline), at 7f396e1a9c2d:
  ! offline: using the last fetched registry (git ls-remote origin -- refs/heads/main: fatal: Could not read from remote repository. …)
```

`skillcrew doctor` reports it under the `fetch` check. Run `git -C ~/.skillcrew/registry.git fetch` to see the full error, then fix your credentials or network.

Hooks never wait for the network: they start the sync in the background. After a failed fetch, background syncs skip the network for 10 minutes and use the last fetched registry, so session starts on a dead network do not pile up fetches.
