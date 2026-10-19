#!/bin/sh
# Installs a released lockfile binary on Linux or macOS, without Go.
#
#   curl -fsSL https://raw.githubusercontent.com/DavidGodefroid/lockfile/main/scripts/install.sh -o install.sh
#   less install.sh && sh install.sh
#
# Environment:
#   LOCKFILE_VERSION      tag to install (default: the latest release), e.g. v0.2.0
#   LOCKFILE_INSTALL_DIR  target directory (default: /usr/local/bin, via sudo if needed)
#
# cosign is required: the keyless signature of checksums.txt is checked
# against lockfile's release workflow, then the archive against
# checksums.txt. Without cosign nothing is installed. This script only
# places the binary: `sudo lockfile install` sets up the separated console
# afterwards (Linux).
set -eu

repo="DavidGodefroid/lockfile"
dir="${LOCKFILE_INSTALL_DIR:-/usr/local/bin}"
identity='^https://github\.com/DavidGodefroid/lockfile/\.github/workflows/release\.yml@refs/tags/v[0-9]'

say() { printf 'lockfile: %s\n' "$*" >&2; }
die() {
	say "$*"
	exit 1
}

command -v cosign >/dev/null 2>&1 ||
	die "cosign is required to check the release signature: https://docs.sigstore.dev/cosign/system_config/installation/"

if command -v curl >/dev/null 2>&1; then
	fetch() { curl -fsSL -o "$2" "$1"; }
	latest() { curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/$repo/releases/latest"; }
elif command -v wget >/dev/null 2>&1; then
	fetch() { wget -q -O "$2" "$1"; }
	latest() { wget -q -S --spider "https://github.com/$repo/releases/latest" 2>&1 | sed -n 's/^ *[Ll]ocation: *//p' | tail -n 1 | tr -d '\r'; }
else
	die "curl or wget is required"
fi

case "$(uname -s)" in
Linux) os=linux ;;
Darwin) os=darwin ;;
*) die "unsupported OS $(uname -s): Linux and macOS only" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
aarch64 | arm64) arch=arm64 ;;
*) die "unsupported architecture $(uname -m): amd64 and arm64 only" ;;
esac

tag="${LOCKFILE_VERSION:-}"
if [ -z "$tag" ]; then
	tag="$(latest)"
	tag="${tag##*/}"
	case "$tag" in
	v[0-9]*) ;;
	*) die "no published release found; set LOCKFILE_VERSION" ;;
	esac
fi
case "$tag" in v*) ;; *) tag="v$tag" ;; esac
version="${tag#v}"

archive="lockfile_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$repo/releases/download/$tag"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT INT TERM

say "downloading $archive ($tag)"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || die "download failed: checksums.txt"
fetch "$base/checksums.txt.sigstore.json" "$tmp/checksums.txt.sigstore.json" || die "download failed: checksums.txt.sigstore.json"
cosign verify-blob --bundle "$tmp/checksums.txt.sigstore.json" \
	--certificate-identity-regexp "$identity" \
	--certificate-oidc-issuer https://token.actions.githubusercontent.com \
	"$tmp/checksums.txt" >/dev/null 2>&1 || die "cosign: checksums.txt is not signed by lockfile's release workflow; nothing installed"
say "cosign: checksums.txt signature verified"

fetch "$base/$archive" "$tmp/$archive" || die "download failed: $base/$archive"
want="$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")"
[ -n "$want" ] || die "$archive is not listed in checksums.txt"
if command -v sha256sum >/dev/null 2>&1; then
	got="$(sha256sum "$tmp/$archive" | awk '{ print $1 }')"
else
	got="$(shasum -a 256 "$tmp/$archive" | awk '{ print $1 }')"
fi
[ "$want" = "$got" ] || die "checksum mismatch for $archive; nothing installed"

tar -xzf "$tmp/$archive" -C "$tmp" lockfile

# Root-owned in a system directory: `lockfile install` copies only a binary
# that the agent's account cannot replace.
if [ -w "$dir" ] || { [ ! -e "$dir" ] && mkdir -p "$dir" 2>/dev/null; }; then
	install -m 0755 "$tmp/lockfile" "$dir/lockfile"
else
	command -v sudo >/dev/null 2>&1 || die "$dir is not writable and sudo is missing; set LOCKFILE_INSTALL_DIR"
	say "installing to $dir with sudo"
	sudo mkdir -p "$dir"
	sudo install -m 0755 "$tmp/lockfile" "$dir/lockfile"
fi

say "installed $("$dir/lockfile" version 2>/dev/null || echo "$tag") to $dir/lockfile"
case ":$PATH:" in
*":$dir:"*) ;;
*) say "$dir is not on your PATH" ;;
esac
