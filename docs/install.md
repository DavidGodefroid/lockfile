# Installing lockfile

lockfile runs in Docker, on Linux (the console needs a kernel with Landlock
and seccomp, which sandbox the plugins; on Windows, WSL 2 with Docker
Desktop). The console runs in one container, your agent's commands in others;
they share only the socket volume. One `lockfile` command, the launcher, runs
each of them in the signed release image. Full guide:
[docker.md](docker.md).

## Homebrew

```sh
brew trust davidgodefroid/tap
brew install davidgodefroid/tap/lox davidgodefroid/tap/lockfile
```

Homebrew asks you to trust a third-party tap once. The `lockfile` formula is
Linux only; it installs the same launcher and seccomp profile as
`scripts/install.sh`, pinned to the same image digests. Each release tag bumps
that formula ([homebrew-tap](https://github.com/DavidGodefroid/homebrew-tap)).
`lox` is [lockhub](https://github.com/DavidGodefroid/lockhub)'s CLI. The whole
Lock Family instead: `brew install davidgodefroid/tap/lockfamily`.

Then let
[`lox add lockfile`](https://github.com/DavidGodefroid/lockhub/blob/main/docs/lox-add.md)
ask for your file store (S3, SFTP or a local directory), set up its lock and
start its console, and keep `lox run` open in a terminal of its own:

```sh
cd your-project            # with lea: its projects directory
lox add lockfile           # asks about the file store, sets up the lock, starts its console
lox run                    # in its own terminal, outside any agent: serves the decisions
```

If a lea container already runs for the project, `lox add` says `lea gets lock
NAME at its next start: lea stop, then lea`: lea mounts the locks' sockets
when its container starts, so `lea stop`, then `lea` brings the new lock in.

## The install script

`scripts/install.sh` installs the `lockfile` launcher and the seccomp profile,
after checking with cosign that the release and its image
`ghcr.io/davidgodefroid/lockfile:X.Y.Z` come from lockfile's release workflow
run on that tag; the launcher runs every command in that image, by its digest.
[docker.md](docker.md) covers what runs where, the volumes, the seccomp
profile, the `-vault` image (`LOCKFILE_VAULT=1`), your own build,
`ALLOWED_UIDS` and migrating from a host install. The base images are pinned
by digest, cosign and the Vault CLI by sha256; the image has no `sudo`.

## First plan, step by step

With lockfile's own installer and [cosign](https://docs.sigstore.dev/cosign/system_config/installation/):

```sh
# 1. The launcher and the seccomp profile the console runs under (cosign checks the
#    release and its image; never seccomp=unconfined).
V=vX.Y.Z
curl -fsSLO "https://raw.githubusercontent.com/DavidGodefroid/lockfile/$V/scripts/install.sh"
less install.sh && LOCKFILE_VERSION=$V sh install.sh

# 2. From the workspace: the lock's profile, in .lockfile/ (lockhub reviews it
#    and copies it, read-only, to the console), then the lock and its hub
#    (lockhub's lox CLI), then the console's own questions (policy review).
lockfile profile add --profile samples --source local --path /srv/lockfile/samples --prefix invoices/
lox add lockfile samples --profile samples
lox run             # in a terminal of its own: starts the hub and the lock, serves decisions
lox attach samples  # the policy review (detach: Ctrl-P Ctrl-Q)

# 3. The plugins, in the home of the running console (signatures checked
#    with cosign, in the image); the console applies them after :review.
lockfile plugin install file.non-empty@1.0.0
lockfile plugin install format.is-pdf@1.0.0
lox attach samples  # :review (detach: Ctrl-P Ctrl-Q)

# 4. Your agent: wire it (from the project root), then ask. The plan waits
#    for your answer in lox run, then its ticket runs.
lockfile init codex   # or claude, cursor, gemini
lockfile plan --key invoices/2026/10/abc123.pdf --plugin file.non-empty --plugin format.is-pdf --json
lockfile run TICKET --json
```

The console runs hub-attached only: lockhub's `lox` starts it with its hub
flags, and `lockfile console` on its own refuses to start. `lox up`, which the
older docs use, is deprecated in lockhub in favour of `lox run`.

```json
{"handle":"f_8Kq2xR","answers":[
 {"plugin":"file.non-empty","version":"1.0.0","official":true,"result":1},
 {"plugin":"format.is-pdf","version":"1.0.0","official":true,"result":0}]}
```

The file is not empty and does not start like a PDF. `result: null` with
`"error": "plugin_error"` means the plugin gave no answer: it is **not** a 0.

## Upgrade

`lockfile update` (same checks as the install script), then restart the
console; the volumes keep its state.

## Build

```sh
CGO_ENABLED=0 go build ./cmd/lockfile
```

Requires Go 1.26 or later. Your own image: `docker build -t lockfile .`, then
`LOCKFILE_IMAGE=lockfile lockfile version`.

## Status

Console, agent CLI, MCP server and agent wiring on
[lockkit](https://github.com/DavidGodefroid/lockkit), deployed with Docker; hub
mode (plans decided on [lockhub](https://github.com/DavidGodefroid/lockhub), in
`lox run`, `lox approve` or another facade) since v0.4.0. The console needs a
Linux kernel with Landlock and seccomp (they sandbox the plugins).

Roadmap, not in this release: an onboarding wizard of lockfile's own
(`setup`), a macOS console, public-key trust roots for unofficial
marketplaces.
