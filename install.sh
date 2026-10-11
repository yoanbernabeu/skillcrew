#!/bin/sh
# Install skillcrew from GitHub releases:
#   curl -fsSL https://raw.githubusercontent.com/yoanbernabeu/skillcrew/main/install.sh | sh
#
# Environment:
#   SKILLCREW_VERSION  version to install, with or without a leading v (default: latest)
#   SKILLCREW_BIN_DIR  install directory (default: $HOME/.local/bin)
#
# The archive is checked against checksums.txt. When cosign is installed, the
# keyless signature of checksums.txt is verified too.
#
# Everything runs inside main(), called on the last line, so a truncated
# download never executes a partial script.
set -eu

REPO="yoanbernabeu/skillcrew"

say() {
  printf '%s\n' "$*"
}

fail() {
  printf 'skillcrew install: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | cut -d' ' -f1
  else
    shasum -a 256 "$1" | cut -d' ' -f1
  fi
}

# latest_version prints the latest release tag (e.g. v0.1.0) by following the
# redirect of /releases/latest, which is not subject to the API rate limit.
latest_version() {
  url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$REPO/releases/latest") ||
    fail "cannot resolve https://github.com/$REPO/releases/latest (set SKILLCREW_VERSION to pick a version)"
  tag=${url##*/}
  case "$tag" in
    v[0-9]*) printf '%s\n' "$tag" ;;
    *) fail "cannot determine the latest version (no release published yet?)" ;;
  esac
}

main() {
  need uname
  need curl
  need tar
  need install
  need mktemp
  if ! command -v sha256sum >/dev/null 2>&1 && ! command -v shasum >/dev/null 2>&1; then
    fail "required command not found: sha256sum or shasum"
  fi

  BIN_DIR="${SKILLCREW_BIN_DIR:-$HOME/.local/bin}"

  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    darwin | linux) ;;
    *) fail "unsupported OS: $os (use WSL2 on Windows)" ;;
  esac
  machine=$(uname -m)
  case "$machine" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) fail "unsupported architecture: $machine" ;;
  esac

  tag=${SKILLCREW_VERSION:-}
  if [ -z "$tag" ]; then
    tag=$(latest_version)
  fi
  version=${tag#v}
  [ -n "$version" ] || fail "invalid SKILLCREW_VERSION: $tag"
  tag="v$version"

  archive="skillcrew_${version}_${os}_${arch}.tar.gz"
  base="https://github.com/$REPO/releases/download/$tag"
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM

  say "Downloading skillcrew $tag ($os/$arch)..."
  curl -fsSL -o "$tmp/$archive" "$base/$archive" || fail "download failed: $base/$archive"
  curl -fsSL -o "$tmp/checksums.txt" "$base/checksums.txt" || fail "download failed: $base/checksums.txt"

  if command -v cosign >/dev/null 2>&1; then
    if curl -fsSL -o "$tmp/checksums.txt.sigstore.json" "$base/checksums.txt.sigstore.json" 2>/dev/null; then
      cosign verify-blob \
        --bundle "$tmp/checksums.txt.sigstore.json" \
        --certificate-identity "https://github.com/$REPO/.github/workflows/release.yml@refs/tags/$tag" \
        --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
        "$tmp/checksums.txt" >/dev/null ||
        fail "cosign could not verify checksums.txt (a recent cosign, v3 or later, is required)"
      say "Signature of checksums.txt verified with cosign."
    elif [ "${SKILLCREW_SKIP_SIGNATURE:-}" = 1 ]; then
      say "No signature found for $tag; skipped as requested (SKILLCREW_SKIP_SIGNATURE=1)."
    else
      fail "no signature found for $tag; refusing to install (set SKILLCREW_SKIP_SIGNATURE=1 to skip the check)"
    fi
  fi

  expected=$(grep " $archive\$" "$tmp/checksums.txt" | cut -d' ' -f1)
  actual=$(sha256 "$tmp/$archive")
  if [ -z "$expected" ] || [ "$expected" != "$actual" ]; then
    fail "checksum mismatch for $archive"
  fi

  tar -xzf "$tmp/$archive" -C "$tmp" skillcrew
  mkdir -p "$BIN_DIR"
  install -m 0755 "$tmp/skillcrew" "$BIN_DIR/skillcrew"
  say "skillcrew $version installed to $BIN_DIR/skillcrew"

  case ":${PATH:-}:" in
    *":$BIN_DIR:"*) ;;
    *)
      say ""
      say "$BIN_DIR is not on your PATH. Add this line to your shell profile:"
      say "  export PATH=\"$BIN_DIR:\$PATH\""
      ;;
  esac
}

main "$@"
