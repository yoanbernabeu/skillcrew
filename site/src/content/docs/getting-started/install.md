---
title: Install
description: Install the skillcrew binary on macOS or Linux with Homebrew, the install script or Go.
section: getting-started
order: 1
---

Skillcrew is a single static binary. Every developer installs it once; administrators use the same binary to create and validate the registry.

## Requirements

- macOS or Linux. On Windows, use WSL2: Skillcrew then serves the agents that run inside WSL, not native Windows apps.
- `git`. Skillcrew runs the system `git`, so it reuses the credentials you already have.
- Optional: `gh` (GitHub) or `glab` (GitLab), used by `skillcrew propose` to open pull requests.

## Homebrew

```sh
brew install yoanbernabeu/tap/skillcrew
```

## Install script

```sh
curl -fsSL https://raw.githubusercontent.com/yoanbernabeu/skillcrew/main/install.sh | sh
```

The script downloads the release archive for your OS and architecture, checks it against `checksums.txt`, and installs the binary in `~/.local/bin`. When `cosign` is installed, it also verifies the signature of `checksums.txt` and refuses to install if no signature is found.

| Variable | Effect |
|---|---|
| `SKILLCREW_VERSION` | Version to install, with or without a leading `v` (default: latest) |
| `SKILLCREW_BIN_DIR` | Install directory (default: `$HOME/.local/bin`) |
| `SKILLCREW_SKIP_SIGNATURE` | Set to `1` to install a release that has no signature while `cosign` is installed |

If the install directory is not on your `PATH`, the script prints the line to add to your shell profile.

## Go

```sh
go install github.com/yoanbernabeu/skillcrew/cmd/skillcrew@latest
```

This needs Go 1.26 or later.

## Check the installation

```console
$ skillcrew --version
```

`skillcrew --help` lists every command; each command has its own `--help`.

## Verify a release

Releases are built by GitHub Actions. `checksums.txt` is signed keylessly with [Sigstore cosign](https://docs.sigstore.dev/), so you can check that an archive comes from the release workflow of the repository:

```sh
VERSION=v0.1.0
base=https://github.com/yoanbernabeu/skillcrew/releases/download/$VERSION
curl -fsSLO "$base/checksums.txt" -fsSLO "$base/checksums.txt.sigstore.json"
cosign verify-blob --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/yoanbernabeu/skillcrew/.github/workflows/release.yml@refs/tags/$VERSION" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
shasum -a 256 --ignore-missing -c checksums.txt
```

The last command checks the archives you downloaded next to `checksums.txt`.

> [!NOTE]
> Skillcrew is in early development. The MVP works end to end; expect breaking changes before v1.

## Next steps

- Administrators: [create a registry](/docs/getting-started/create-a-registry/).
- Developers: [join your organization](/docs/getting-started/join-as-a-developer/).
- To remove Skillcrew later, run `skillcrew uninstall` first, then `brew uninstall skillcrew` or delete the binary. See [the CLI reference](/docs/reference/cli/#skillcrew-uninstall).
