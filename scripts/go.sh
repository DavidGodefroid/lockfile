#!/bin/sh
# Runs a Go command in the golang:1.26 image, so Go is not needed on the host.
#   scripts/go.sh go test ./...
#   scripts/go.sh --sandbox go test ./sandbox/...   (Landlock/seccomp reach the kernel)
#   scripts/go.sh --integration                      (S3 and SFTP sources against in-process servers)
set -eu
opts=""
case "${1:-}" in
--sandbox)
	opts="--security-opt seccomp=unconfined"
	shift
	;;
--integration)
	shift
	set -- go test -count=1 -tags integration ./test/integration/... "$@"
	;;
esac
# shellcheck disable=SC2086
docker run --rm $opts \
	-v "$PWD":/src -w /src \
	-v lockfile-gomod:/go/pkg/mod -v lockfile-gocache:/root/.cache/go-build \
	-e CGO_ENABLED=0 -e GOFLAGS="${GOFLAGS:-}" \
	golang:1.26 "$@"
