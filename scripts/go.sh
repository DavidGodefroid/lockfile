#!/bin/sh
# Runs a Go command in the golang:1.26 image, so Go is not needed on the host.
#   scripts/go.sh go test ./...
#   scripts/go.sh --sandbox go test ./sandbox/...   (Landlock/seccomp reach the kernel)
set -eu
opts=""
if [ "${1:-}" = "--sandbox" ]; then
	opts="--security-opt seccomp=unconfined"
	shift
fi
# shellcheck disable=SC2086
exec docker run --rm $opts \
	-v "$PWD":/src -w /src \
	-v lockfile-gomod:/go/pkg/mod -v lockfile-gocache:/root/.cache/go-build \
	-e CGO_ENABLED=0 -e GOFLAGS="${GOFLAGS:-}" \
	golang:1.26 "$@"
