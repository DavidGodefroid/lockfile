# A demo image of the separated setup: the console account `lockfile` and
# an agent account `agent` in one container. For documentation and trying
# lockfile out, not for production (one container is one kernel, one
# boundary). The plugin sandbox needs Landlock and seccomp from the host
# kernel; run with:
#
#   docker build -t lockfile-demo .
#   docker run --rm -it --security-opt seccomp=unconfined lockfile-demo
#
# then, in the container: `su - lockfile` (console side) and `su - agent`
# (agent side) in two terminals (docker exec).
FROM docker.io/library/golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/lockfile ./cmd/lockfile

FROM docker.io/library/debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates sudo \
	&& rm -rf /var/lib/apt/lists/* \
	&& useradd --create-home --shell /bin/bash agent
COPY --from=build /out/lockfile /usr/local/bin/lockfile
# The same root script `lockfile install` prints, for the account agent.
RUN lockfile install --client agent --yes \
	&& mkdir -p /run/lockfile && chown lockfile:lockfile-clients /run/lockfile && chmod 2750 /run/lockfile
CMD ["bash"]
