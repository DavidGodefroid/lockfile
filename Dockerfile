# lockfile in Docker: the separated setup baked into
# an image. The console runs as "lockfile" in one container; the agent's MCP
# server runs as "agent" in another; they share the socket directory through
# a named volume, and the kernel checks every peer as it does on a host.
# The plugin sandbox (Landlock, seccomp) runs under the seccomp profile
# shipped next to this file: never seccomp=unconfined. See docs/docker.md.
#
#   docker build -t lockfile .
#   docker build --target vault -t lockfile .   # with the vault CLI, for credentials = "vault"

# Base images are pinned by digest; update tag and digest together.
FROM docker.io/library/golang:1.26-alpine@sha256:27f2d3b83cfc5803c8078f8e730af0f7c431bf067afaec8f60a36fb763187cba AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X github.com/DavidGodefroid/lockfile/internal/product.Version=${VERSION}" -o /out/lockfile ./cmd/lockfile

# cosign checks every plugin, index and release signature; there is no
# unsigned mode. The binary is checked against the sums pinned here (from
# the release's cosign_checksums.txt; the version lockfile's releases use).
FROM docker.io/library/alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f AS cosign
ARG TARGETARCH
ARG COSIGN_VERSION=v3.1.3
ARG COSIGN_SHA256_amd64=4629c757b7618056f8ddd7e2625ae9fdd94c0372a65049520bc7d9df9efc7f71
ARG COSIGN_SHA256_arm64=c5d324e091826b0d7a78eb16fef316450b4eb9aaec045611c08ba06f5e73220a
RUN set -eu; arch=${TARGETARCH:-amd64}; \
    case $arch in amd64) sum=$COSIGN_SHA256_amd64 ;; arm64) sum=$COSIGN_SHA256_arm64 ;; *) echo "no cosign for $arch" >&2; exit 1 ;; esac; \
    wget -qO /tmp/cosign "https://github.com/sigstore/cosign/releases/download/${COSIGN_VERSION}/cosign-linux-${arch}"; \
    echo "$sum  /tmp/cosign" | sha256sum -c -; \
    install -D -m 0755 /tmp/cosign /out/cosign

# The HashiCorp Vault CLI, checked against the sums pinned here (update them
# with VAULT_VERSION from releases.hashicorp.com/vault/<version>/).
FROM docker.io/library/alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f AS vault-cli
ARG TARGETARCH
ARG VAULT_VERSION=2.2.0
ARG VAULT_SHA256_amd64=05d78b0ac842ca7c522fc297df91527cd43c15420c4c5c0a969d48a7b0ff7b6d
ARG VAULT_SHA256_arm64=9d9ae0ce520744fb095bdf150a6924f5583528fd385a2e92782f7a6d00cad53c
RUN set -eu; arch=${TARGETARCH:-amd64}; \
    case $arch in amd64) sum=$VAULT_SHA256_amd64 ;; arm64) sum=$VAULT_SHA256_arm64 ;; *) echo "no vault CLI for $arch" >&2; exit 1 ;; esac; \
    wget -qO /tmp/vault.zip "https://releases.hashicorp.com/vault/${VAULT_VERSION}/vault_${VAULT_VERSION}_linux_${arch}.zip"; \
    echo "$sum  /tmp/vault.zip" | sha256sum -c -; \
    unzip -q /tmp/vault.zip vault -d /out

FROM docker.io/library/alpine:3.22@sha256:2e0b81d60597f449baa56dc25d355367869d2dca4cd57e866f1e2b52af3eb55f AS console
# ALLOWED_UIDS: comma-separated uids that may reach the console besides the
# members of lockfile-clients, for an agent in a container of its own whose
# uid has no account here (docs/docker.md#agent-in-its-own-container).
ARG ALLOWED_UIDS=""
# ca-certificates: S3, the marketplaces and sigstore are reached over TLS.
# /etc/lockfile/container.toml declares a container of its own: /proc
# cannot have hidepid in Docker, and the console accepts that only in a
# private pid namespace that no other account shares (internal/procmount).
# /srv/lockfile holds the read-only mounts of local sources: closed to
# every account but the console's.
RUN case "$ALLOWED_UIDS" in *[!0-9,]*|,*|*,|*,,*) echo "ALLOWED_UIDS: comma-separated uids, got '$ALLOWED_UIDS'" >&2; exit 1;; esac \
 && for u in $(echo "$ALLOWED_UIDS" | tr ',' ' '); do \
      case "$u" in 0*|10031|65534) echo "ALLOWED_UIDS: $u is root, the console account, nobody (or has a leading zero)" >&2; exit 1;; esac; \
    done \
 && apk add --no-cache ca-certificates \
 && addgroup -S -g 10030 lockfile-clients \
 && adduser -D -u 10031 -s /sbin/nologin -g "lockfile console" lockfile \
 && adduser -D -u 10032 -G lockfile-clients -s /sbin/nologin -g "lockfile agent" agent \
 && chmod 0700 /home/lockfile \
 && install -d -m 0755 -o root -g root /etc/lockfile \
 && printf '%s\n' \
      '# lockfile: the console runs as service_user; members of client_group reach it.' \
      'service_user = "lockfile"' \
      'client_group = "lockfile-clients"' \
      'socket_dir   = "/run/lockfile"' \
      'x11          = "refuse"' > /etc/lockfile/system.toml \
 && if [ -n "$ALLOWED_UIDS" ]; then printf 'allowed_uids = [%s]\n' "$ALLOWED_UIDS" >> /etc/lockfile/system.toml; fi \
 && chmod 0644 /etc/lockfile/system.toml \
 && printf '%s\n' \
      '# lockfile: this image runs the console in a pid namespace of its own.' \
      'pid_namespace = "private"' > /etc/lockfile/container.toml \
 && chmod 0644 /etc/lockfile/container.toml \
 && install -d -m 2750 -o lockfile -g lockfile-clients /run/lockfile \
 && install -d -m 0700 -o lockfile -g lockfile /srv/lockfile
COPY --from=build --chown=root:root --chmod=0755 /out/lockfile /usr/local/bin/lockfile
COPY --from=cosign --chown=root:root --chmod=0755 /out/cosign /usr/local/bin/cosign

# Named volumes take these directories' owner and mode on first use.
VOLUME ["/run/lockfile", "/home/lockfile"]
WORKDIR /
USER agent
ENTRYPOINT ["lockfile"]
CMD ["help"]

# The console with the vault CLI (docs/docker.md, Credentials from Vault).
# Not the default: the CLI adds some 550 MB.
FROM console AS vault
COPY --from=vault-cli --chown=root:root --chmod=0755 /out/vault /usr/local/bin/vault

# The default image.
FROM console
