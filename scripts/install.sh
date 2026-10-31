#!/bin/sh
# Installs the lockfile launcher of a release on Linux or macOS (WSL 2 on
# Windows). lockfile runs in Docker: the launcher, installed as `lockfile`,
# runs every command in the release's image (scripts/lockfile,
# docs/docker.md). Besides it, only the seccomp profile of the console's
# container lands on the host.
#
# Download it from the tag you install, read it, then run it with that tag:
#
#   V=vX.Y.Z
#   curl -fsSLO "https://raw.githubusercontent.com/DavidGodefroid/lockfile/$V/scripts/install.sh"
#   less install.sh
#   LOCKFILE_VERSION=$V sh install.sh
#
# Environment:
#   LOCKFILE_VERSION      tag to install (default: the latest release), e.g. v0.5.2
#   LOCKFILE_INSTALL_DIR  launcher directory (default: /usr/local/bin, via sudo if needed)
#   LOCKFILE_SHARE_DIR    seccomp profile directory (default: /usr/local/share/lockfile)
#   LOCKFILE_ALLOW_DOWNGRADE=1  replace an installed lockfile with an older release
#
# cosign is required: the keyless signatures of checksums.txt and of the
# release's images are checked against lockfile's release workflow run on
# that exact tag, then the archive against checksums.txt. Without cosign
# nothing is installed. The launcher runs the images by their verified
# digests. `lockfile update` later replaces it the same way.
set -eu

repo="DavidGodefroid/lockfile"
dir="${LOCKFILE_INSTALL_DIR:-/usr/local/bin}"
share="${LOCKFILE_SHARE_DIR:-/usr/local/share/lockfile}"
registry="ghcr.io/davidgodefroid/lockfile"

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
# An exact vX.Y.Z: the tag goes into the signer identity below.
case "$version" in
'' | *[!0-9.]* | .* | *. | *..*) die "invalid release tag $tag" ;;
esac
[ "$(printf '%s' "$version" | tr -cd . | wc -c | tr -d ' ')" = 2 ] || die "invalid release tag $tag"

# The signature must come from the release workflow run on this very tag:
# a validly signed older release cannot be passed off as this one.
identity="^https://github\\.com/DavidGodefroid/lockfile/\\.github/workflows/release\\.yml@refs/tags/$(printf '%s' "$tag" | sed 's/\./\\./g')\$"

# Refuse to replace an installed lockfile with an older release.
older() { # older A B: A < B (X.Y.Z)
	[ "$1" != "$2" ] && [ "$(printf '%s\n%s\n' "$1" "$2" | sort -t. -k1,1n -k2,2n -k3,3n | head -n 1)" = "$1" ]
}
if [ -x "$dir/lockfile" ] && [ "${LOCKFILE_ALLOW_DOWNGRADE:-}" != 1 ]; then
	# A launcher holds its version; the binary of a former host install
	# prints it.
	if grep -q '^launcher_version=' "$dir/lockfile" 2>/dev/null; then
		current="$(sed -n 's/^launcher_version=//p' "$dir/lockfile" | sed -n 1p)"
	else
		current="$("$dir/lockfile" version 2>/dev/null | awk '{ print $2 }')"
	fi
	current="${current#v}"
	case "$current" in
	*[!0-9.]* | '') ;;
	*) if older "$version" "$current"; then
		die "$dir/lockfile is v$current, newer than $tag; set LOCKFILE_ALLOW_DOWNGRADE=1 to downgrade"
	fi ;;
	esac
fi

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

tar -xzf "$tmp/$archive" -C "$tmp" scripts/lockfile seccomp.json 2>/dev/null ||
	die "$archive holds no launcher (scripts/lockfile, seccomp.json): $tag predates the Docker launcher"

# The launcher runs the images of its own version, by the digests whose
# cosign signatures verify (the release workflow run on this tag, as for
# checksums.txt).
verified_digest() {
	cosign verify --certificate-identity-regexp "$identity" \
		--certificate-oidc-issuer https://token.actions.githubusercontent.com \
		"$registry:$1" 2>/dev/null |
		sed -n 's/.*"docker-manifest-digest":"\(sha256:[0-9a-f]\{64\}\)".*/\1/p' | sed -n 1p
}
digest="$(verified_digest "$version")"
vdigest="$(verified_digest "$version-vault")"
[ -n "$digest" ] && [ -n "$vdigest" ] && [ "$digest" != "$vdigest" ] ||
	die "cosign: the signature of $registry:$version (and -vault) does not verify, or the image is not published yet; nothing installed"
say "cosign: image $registry:$version verified ($digest)"
sed -e "s/^launcher_version=dev\$/launcher_version=$version/" \
	-e "s/^image_digest=\$/image_digest=$digest/" \
	-e "s/^vault_image_digest=\$/vault_image_digest=$vdigest/" \
	-e "s|^seccomp_profile=\$|seccomp_profile=$share/seccomp.json|" \
	"$tmp/scripts/lockfile" >"$tmp/launcher"
grep -qx "launcher_version=$version" "$tmp/launcher" || die "the launcher of $tag does not take its version"

# Root-owned in system directories, so that the agent's account can replace
# neither the launcher nor the seccomp profile. It replaces the binary of a
# former host install.
if { [ -w "$dir" ] || { [ ! -e "$dir" ] && mkdir -p "$dir" 2>/dev/null; }; } &&
	{ [ -w "$share" ] || { [ ! -e "$share" ] && mkdir -p "$share" 2>/dev/null; }; }; then
	install -m 0644 "$tmp/seccomp.json" "$share/seccomp.json"
	install -m 0755 "$tmp/launcher" "$dir/lockfile"
else
	command -v sudo >/dev/null 2>&1 || die "$dir or $share is not writable and sudo is missing; set LOCKFILE_INSTALL_DIR and LOCKFILE_SHARE_DIR"
	say "installing to $dir and $share with sudo"
	sudo mkdir -p "$dir" "$share"
	sudo install -m 0644 "$tmp/seccomp.json" "$share/seccomp.json"
	sudo install -m 0755 "$tmp/launcher" "$dir/lockfile"
fi

say "installed the lockfile launcher $tag to $dir/lockfile (image $registry@$digest, seccomp profile $share/seccomp.json)"
case ":$PATH:" in
*":$dir:"*) ;;
*) say "$dir is not on your PATH" ;;
esac
command -v docker >/dev/null 2>&1 || say "docker is not installed: the launcher needs it (docs/docker.md)"
# A former host install (sudo lockfile install) left its system file behind.
if [ -e /etc/lockfile/system.toml ]; then
	say "a former host install remains (/etc/lockfile/system.toml): see docs/docker.md#migrating"
fi
