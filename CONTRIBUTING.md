# Contributing to Skillcrew

Skillcrew takes contributions through **issues, not pull requests**. Pull requests are limited to the maintainer: changes are implemented by coding agents from issues that are ready, then reviewed and merged by the maintainer. This keeps the bar high and the review load sane, and it is how the project is meant to be maintained.

For security vulnerabilities, do **not** open a public issue: see [SECURITY.md](SECURITY.md).

## How to contribute

The most valuable contributions are the ones an agent cannot make:

- **Report a bug** with a reproduction and the output of `skillcrew doctor`.
- **Propose a feature** with the real use case behind it, the expected behavior and how we will know it works.
- **Shape a design**: comment on issues labelled `needs-design` with constraints, edge cases, prior art.
- **Verify a change**: try what an issue labelled `needs-verification` describes on your machine and agents, and report.
- **Report agent changes**: agents move skills directories and hook formats often; tell us when one breaks.

Contributors who substantially shape or verify an issue are credited as `Co-Authored-By` on the commit that implements it.

## Issue lifecycle

| Label | Meaning |
|---|---|
| `triage` | New; the maintainer reads it, asks questions or closes it with an explanation. |
| `needs-design` | Accepted; the behavior, constraints and acceptance criteria are being settled. |
| `ready` | Specified well enough for an agent to implement it test-first. |
| `in-progress` | Being implemented. |
| `needs-verification` | Implemented; waiting for someone to confirm it works in real conditions. |

An issue is closed when the change is merged and verified, or when it will not be done.

## Working on the code (maintainer and agents)

[AGENTS.md](AGENTS.md) is the guide for anyone, human or agent, changing the code: layout, invariants, test rules and the checks to run. In short:

- Go **1.26** or later (the toolchain pinned in `go.mod` is downloaded automatically) and `git`.
- **Strict TDD**: a failing test first, then the smallest change that makes it pass.
- Tests never touch the real home directory; try the CLI by hand only with a throwaway `HOME` and `--no-timer`.
- Before pushing: `gofmt -l .`, `go vet ./...`, `go test -race ./...`, golangci-lint v2, and `sh -n install.sh` when it changes.

## Commits

- Imperative mood, concise subject: `Add Cursor hook adapter`, `Fix backup rotation on rename`.
- One logical change per commit; explain the *why* in the body when it is not obvious.
- Reference the issue (`Fixes #123`) and credit contributors with `Co-Authored-By`.

## Code of conduct

This project follows the [Contributor Covenant](CODE_OF_CONDUCT.md).
