---
title: Security model
description: The registry is your root of trust; Skillcrew copies files, executes nothing from it and stores no secrets.
section: concepts
order: 4
---

Skillcrew pushes content to every developer machine automatically. Its security rests on one principle: the registry, reviewed through pull requests, is the only source of truth.

## Protect the registry

Treat the registry like production code:

- Require reviews on the default branch.
- Protect tags if you pin skills with `ref`.
- Keep the generated CI workflow, which runs `skillcrew validate` on every pull request.

> [!SECURITY]
> Anyone who can push to the registry's default branch can change the skills of the whole organization. Branch protection is the control that matters most.

## Pins cannot be hijacked

`sha` must be a full commit id and `ref` must be a tag; neither ever resolves to a branch. Nobody can override a pin by pushing a branch with the same name. `skillcrew validate` checks that pinned tags and commits exist in CI. See [Pin a skill](/docs/guides/pinning/).

## Skillcrew executes nothing from the registry

Skillcrew copies files. It never runs anything shipped by the registry or a skill at install or update time.

Skills may bundle scripts that agents run when they use the skill. Review them in pull requests like any other code.

## No secrets stored

Git authentication goes through your existing SSH keys and credential helpers. Registry URLs with an embedded password (`https://user:token@…`) are refused, because the URL would be stored in plain text:

```console
$ skillcrew init https://user:token@github.com/acme/skills
Error: the registry URL embeds credentials; remove them and use a Git credential helper (e.g. gh auth setup-git)
```

## Updates are atomic and reversible

- Each skill is prepared in a staging directory, then swapped in: no agent ever reads a partially written skill.
- A lockfile records the registry commit and content hash of every installed skill, and Skillcrew only modifies directories it installed.
- Local edits and colliding personal skills are backed up before being replaced.
- `skillcrew uninstall` removes hooks, the timer, team skills and `~/.skillcrew`, and restores personal skills that team skills replaced.

## Kill switch

Set a skill to `blocked` and it is removed from every machine at the next sync. See [Statuses](/docs/concepts/statuses/#blocking-a-skill-the-kill-switch).

## Release integrity

Release checksums are signed keylessly with Sigstore cosign by the release workflow, and the install script verifies them when `cosign` is installed. See [Install](/docs/getting-started/install/#verify-a-release).

## Reporting a vulnerability

Do not open a public issue. Report privately through GitHub private vulnerability reporting (**Security** tab of [yoanbernabeu/skillcrew](https://github.com/yoanbernabeu/skillcrew), **Report a vulnerability**) or by email to contact@yoandev.co. Include the Skillcrew version (`skillcrew --version`), your OS, the impact and steps to reproduce.

You get an acknowledgement within 3 business days and a first assessment within 7 days. Only the latest release receives security fixes.

### In scope

Registry content is untrusted: Skillcrew must stay safe even if a malicious or careless skill is merged. In scope, among others:

- **Path traversal**: a skill name, `install_as` alias, plugin path or file inside a skill that writes outside the intended skills directories.
- **Symlinks** shipped in a registry or a skill that make Skillcrew read, overwrite or delete files outside its directories, and races around symlink creation in `~/.claude/skills`.
- **Code execution**: anything that makes Skillcrew execute content from the registry or a skill.
- **Command injection** in the session-start hooks or the launchd / systemd timers Skillcrew installs.
- **Credential leaks**: Git tokens or passwords in logs, error messages, `--json` output, lockfiles or state files.
- **Destructive behavior**: deleting or overwriting personal skills, agent settings or files Skillcrew did not install, beyond the documented backup-and-restore behavior.
- **Integrity**: bypassing the registry commit and content hash checks.

### Out of scope

- What a skill instructs an AI agent to do once installed: review skills in your registry like any other code.
- Vulnerabilities in the AI agents themselves, Git, or the hosting platform of your registry.
- Attacks that require write access to the developer's home directory or to the registry's protected branch.

The full policy is in [SECURITY.md](https://github.com/yoanbernabeu/skillcrew/blob/main/SECURITY.md).
