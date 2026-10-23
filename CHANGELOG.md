# Changelog

## Unreleased

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
  that lists no process of an account other than root and the console's; checked at start and
  before every plan, refused otherwise (`proc_not_hidden`, the failed condition named).
  `lockfile doctor` reports it.
- Documentation is Docker-only: `docs/docker.md` (new), the README quick start,
  `docs/usage.md` and `docs/security-model.md`. Other deployment modes (`lockfile install`, the
  session and desktop shapes) stay in the code but are no longer documented.
- CI: a `docker` job builds the image, runs the sandbox tests in a container under
  `seccomp.json`, and runs `test/docker` (console and agent containers; sandbox active; peer
  refused; `ALLOWED_UIDS`; no Landlock and `--pid=host` refused).

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
