# Security Policy

## Supported versions

Skillcrew is in early development. Only the **latest release** receives security fixes. Upgrade before reporting if you can.

## Reporting a vulnerability

Do **not** open a public issue, discussion or pull request.

Report privately, either:

- through GitHub private vulnerability reporting: **Security** tab of [yoanbernabeu/skillcrew](https://github.com/yoanbernabeu/skillcrew) → **Report a vulnerability** (preferred), or
- by email to **contact@yoandev.co**.

Please include:

- the Skillcrew version (`skillcrew --version`) and OS,
- a description of the issue and its impact,
- steps or a minimal registry that reproduces it,
- any suggested fix.

## What to expect

- Acknowledgement within **3 business days**.
- A first assessment within **7 days**.
- A fix or mitigation plan as soon as possible, depending on severity. You will be kept informed.
- Credit in the release notes and advisory, unless you prefer to stay anonymous.

Please give us a reasonable time to ship a fix before any public disclosure.

## Scope

Skillcrew runs on every developer machine of an organization and writes into agent configuration directories, so its threat model matters.

**Registry content is untrusted.** Changes to a registry are reviewed through pull requests, but Skillcrew must stay safe even if a malicious or careless skill is merged. In scope, among others:

- **Path traversal**: a skill name, `install_as` alias, plugin path or file inside a skill that writes outside the intended skills directories.
- **Symlinks**: symlinks shipped in a registry or a skill that make Skillcrew read, overwrite or delete files outside its directories, and races around symlink creation in `~/.claude/skills`.
- **Code execution**: anything that makes Skillcrew execute content from the registry or a skill (Skillcrew must never execute registry content).
- **Command injection** in the session-start hooks or the launchd / systemd timers Skillcrew installs (binary path, arguments, quoting).
- **Credential leaks**: Git tokens or passwords appearing in logs, error messages, `--json` output, lockfiles or state files.
- **Destructive behavior**: Skillcrew deleting or overwriting personal skills, agent settings or files it did not install, beyond the documented backup-and-restore behavior.
- **Integrity**: bypassing the registry commit and content hash checks.

Out of scope:

- What a skill instructs an AI agent to do once installed: review skills in your registry like any other code.
- Vulnerabilities in the AI agents themselves, Git, or the hosting platform of your registry.
- Attacks that require write access to the developer's home directory or to the registry's protected branch.
