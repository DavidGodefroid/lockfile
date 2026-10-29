# Changelog

## Unreleased

### The host install code is gone

- `cmd/lockfile/install.go`, the binary's host self-update and `internal/separate` are
  removed: the image refused them since the Docker-only launcher. `lockfile install` and
  `lockfile update` in the image still print the hint to the launcher (`scripts/install.sh`,
  `lockfile update` on the host, docs/docker.md).
- `lockfile doctor`'s separation checks no longer tell you to run `sudo lockfile install`:
  they point to the launcher and its image.

### lockkit v0.2.1

- lockfile now builds on lockkit v0.2.1 (was v0.1.0), the version locksql and lockhttp use.
  No API change affects lockfile: it does not show clear results, read Vault with a held
  token or use `pii`, so its behaviour is unchanged.

### One `lockfile` command, backed by Docker

Every lockfile command now runs in Docker, through one host command: the `lockfile`
launcher.

- `scripts/lockfile`, a POSIX shell launcher, runs each command in
  `ghcr.io/davidgodefroid/lockfile` with the account and volumes it needs: `console` as
  `lockfile`, one container per profile (`lockfile-console-<n>`, `LOCKFILE_CONSOLE_NAME`),
  under the shipped seccomp profile (it refuses to start without it), with no capability,
  `no-new-privileges`, a read-only root and a small `/tmp`, 512 processes, 4 GB, no log
  driver, `host.docker.internal` mapped, `VAULT_ADDR`/`VAULT_NAMESPACE` passed and
  `VAULT_CACERT`'s file mounted read-only; `profile`, `plugin`, `marketplace`, `audit`,
  `forget` and `doctor` as `lockfile` on `lockfile-home`; `init` and `doctor --fix` as you,
  with your home and the project; the agent's commands, `mcp` included, as `agent` with
  `lockfile-run` only. It never mounts the Docker socket, refuses paths with `:`, and
  splits `LOCKFILE_DOCKER_ARGS` without globbing (`local` sources are mounted with it).
- `scripts/install.sh` installs the launcher and `seccomp.json`
  (`/usr/local/share/lockfile`, `LOCKFILE_SHARE_DIR`), root-owned, after the cosign checks of
  `checksums.txt` and of the images against the release workflow run on that exact tag; the
  launcher runs the image by its verified digest (the two digests must differ). A newer
  installed launcher is not downgraded without `LOCKFILE_ALLOW_DOWNGRADE=1`.
- `lockfile version` prints the launcher's and the image's versions; `lockfile update
  [vX.Y.Z]` (an exact tag only) replaces the launcher and the profile the same way, then
  pulls the image.
- The release workflow builds the image (and its `-vault` variant) for amd64 and arm64 after
  the "tag is on main" guard, publishes it to ghcr.io and signs it with cosign. The archives
  ship the launcher and `seccomp.json`.
- The binary refuses `install` and `update` ("lockfile runs in Docker"). `lockfile init`
  writes `lockfile mcp`, which now works as is.
- CI drives the launcher against the built image, and `test/docker` runs the console as the
  launcher confines it.

**Breaking, migration:** `install.sh` installs the launcher in place of the binary.
Replace the `docker run … lockfile …` commands and `docker run … lockfile mcp` agent entries
of the former guide with `lockfile …` (the old ones keep working). A host install (`sudo
lockfile install`) leaves the `lockfile` account and group, `/etc/lockfile/`, the socket
directory and the console account's home behind: see
[docs/docker.md](docs/docker.md#migrating).

### Docker image

Docker is the deployment of lockfile, at parity with locksql (#10).

- `Dockerfile`: a console image replaces the single-container demo. Static build with
  `-X …product.Version=${VERSION}`; accounts `lockfile` (10001) and `agent` (10002), group
  `lockfile-clients` (10000); root-owned binary, cosign and `/etc/lockfile/system.toml`
  (`x11 = "refuse"`, `allowed_uids` from the `ALLOWED_UIDS` build argument); `/run/lockfile`
  (2750) and the console's home as volumes; `/srv/lockfile` (0700) for read-only `local`
  sources; a `vault` target with the Vault CLI. Base images pinned by digest, cosign and the
  Vault CLI by sha256; no `sudo`, no shell by default.
- `seccomp.json`: the seccomp profile every console container runs under (Docker's default,
  which allows the sandbox's system calls). `seccomp=unconfined` is no longer needed nor
  documented.
- `/proc` in a container: Docker cannot mount it with `hidepid=invisible`. Where the image's
  root-owned `/etc/lockfile/container.toml` declares a container of its own, the console
  accepts a plain `/proc` only in a pid namespace other than the host's, whose `/proc` it is,
  that lists no process of an account other than root and the console's, with a seccomp filter
  on the console (`seccomp=unconfined` is refused); checked at start, when a plan arrives and
  just before it runs (the sandbox too), refused otherwise (`proc_not_hidden`, the failed
  condition named).
  `lockfile doctor` reports it.
- Documentation is Docker-only: `docs/docker.md` (new), the README quick start,
  `docs/usage.md` and `docs/security-model.md`. Other deployment modes (`lockfile install`, the
  session and desktop shapes) stay in the code but are no longer documented.
- CI: a `docker` job builds the image, runs the sandbox tests in a container under
  `seccomp.json`, and runs `test/docker` (console and agent containers; sandbox active; peer
  refused; `ALLOWED_UIDS`; no Landlock, `seccomp=unconfined`, `--pid=host` and an agent in the
  console's pid namespace refused).

## v0.2.0

The console, the agent side and the human commands, on lockkit v0.1.0.

- `lockfile console --profile P`: lockkit's console with lockfile's backend: plans validated
  from the request and the local install only, approval screens without file data, one run per
  approved plan (same peer, 10 minutes, unchanged policy), results released on the answer
  quantum, audit of every fetch and plugin outcome.
- Agent commands `status`, `sources`, `plugins`, `plan [--run]`, `run`, `answers`, `release`
  and the `lockfile mcp` server (seven tools); nothing outside the closed list of the security
  model reaches the agent, checked end to end over the socket.
- `lockfile init` wires Claude Code, Codex, Cursor and Gemini CLI.
- Human commands: `install` (separated setup, Linux), `profile add`, `plugin
  install|update|remove|list|verify`, `marketplace add|remove|list`, `doctor [--fix]`, `audit`,
  `forget`, `update`.
- Plugin and release signatures checked with lockkit's `update` (cosign keyless).
- Releases: GoReleaser, signed `checksums.txt`, `scripts/install.sh` (requires cosign).

Not in this release: the `setup` wizard, a macOS console, public-key trust roots for
unofficial marketplaces, the website.

## v0.1.1, v0.1.0

Core: sources (S3, SFTP, local), workspace, plugin sandbox (Landlock, seccomp), plugin store,
marketplace client, answer runner.
