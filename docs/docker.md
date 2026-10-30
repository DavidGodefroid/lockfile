# Running lockfile in Docker

lockfile runs in Docker, with nothing on your machine but Docker, the
`lockfile` launcher and the seccomp profile of the console's container. The
launcher is a short shell script ([`scripts/lockfile`](../scripts/lockfile)):
every command you type, `lockfile console`, `lockfile plugin install`,
`lockfile mcp` and the others, runs in the lockfile image, never in a binary
of its own on your machine.

The image holds the separated setup in two kinds of containers that share
only the socket volume:

- the console runs as `lockfile` (uid 10031), the only account that holds
  the source credentials, the plugins, the fetched files and the audit log;
- the agent's MCP server and CLI run as `agent` (uid 10032), a member of
  `lockfile-clients` (gid 10030), each in a throwaway container;
- `/etc/lockfile/system.toml` (`x11 = "refuse"`), `/etc/lockfile/container.toml`
  and the binaries (`lockfile`, `cosign`) are owned by root; there is no
  `sudo` and the image starts no shell;
- the socket directory `/run/lockfile` (mode 2750,
  `lockfile:lockfile-clients`) is a named volume, `lockfile-run`, shared by
  the console container and the agent's containers. The kernel checks every
  peer on the socket as on a host;
- the console account's home is a named volume, `lockfile-home`: the config
  and profiles, the plugin store and `plugins.lock`, the approved policies,
  the audit log, the fetched files (`work_dir`), SFTP keys and `known_hosts`,
  cosign's trust root cache.

## Requirements

- Docker on Linux, on a kernel with Landlock (5.13 or later, `landlock` in
  `/sys/kernel/security/lsm`; most distributions enable it). Docker Desktop
  runs containers in a VM whose kernel may lack it: `lockfile doctor` tells.
- The seccomp profile [`seccomp.json`](../seccomp.json), next to the
  `Dockerfile`. It is Docker's default profile (moby/profiles
  `seccomp/v0.2.4`), which already allows what the plugin sandbox calls
  (Landlock's three system calls, `seccomp` with filters, `prctl`,
  `close_range`, `prlimit64`): shipping it pins that allowance whatever your
  Docker version. `install.sh` installs it as
  `/usr/local/share/lockfile/seccomp.json`, and the launcher runs every
  console container with `--security-opt seccomp=` that file, and refuses to
  start one without it. **Never `seccomp=unconfined`**: it would lift every
  restriction Docker puts on the console itself, and the sandbox does not
  need it.

## Install

Download the install script from the release you install, read it, then
run it with that release:

```sh
V=vX.Y.Z
curl -fsSLO "https://raw.githubusercontent.com/DavidGodefroid/lockfile/$V/scripts/install.sh"
less install.sh
LOCKFILE_VERSION=$V sh install.sh
```

cosign is required. The script checks the signatures of `checksums.txt` and
of the release's images (`ghcr.io/davidgodefroid/lockfile:X.Y.Z` and its
`-vault` variant) against lockfile's release workflow run on that exact tag,
and the archive against `checksums.txt`. It then installs the launcher as
`/usr/local/bin/lockfile` and the seccomp profile as
`/usr/local/share/lockfile/seccomp.json` (with `sudo` when needed;
`LOCKFILE_INSTALL_DIR` and `LOCKFILE_SHARE_DIR` choose other directories),
root-owned, and writes the verified image digests into the launcher: it runs
the image by digest, never by a tag that could be moved. Docker pulls it on
first use. On Windows, install it in WSL 2 with Docker Desktop's WSL
integration (whose kernel must have Landlock: `lockfile doctor` tells).

```sh
lockfile version    # the launcher's version and its image's
lockfile update     # the latest release: a new launcher and profile, then docker pull
lockfile update vX.Y.Z
```

`lockfile update` checks the signatures and the checksum as `install.sh`
does.

### What runs where

| Command | Runs as | Volumes, mounts |
|---|---|---|
| `console` | `lockfile`, in a container of its own per profile (`lockfile-console-<n>`; `LOCKFILE_CONSOLE_NAME` names it), under the seccomp profile, with no capability, `no-new-privileges`, a read-only root with a 64 MB `/tmp`, at most 512 processes and 4 GB of memory, no log driver, `--add-host=host.docker.internal:host-gateway` | `lockfile-run`, `lockfile-home` |
| `profile`, `plugin`, `marketplace`, `audit`, `forget` | `lockfile` | `lockfile-home` |
| `doctor` | `lockfile`, under the seccomp profile | `lockfile-run`, `lockfile-home` |
| `init`, `doctor --fix` | you (your uid and gid): they write your agents' files | your home, and the project (the nearest directory up holding `.lockfile/`, or else `.git`), each at its own path |
| everything else: `mcp`, `status`, `sources`, `plugins`, `plan`, `run`, `answers`, `release`, `help` | `agent` | `lockfile-run` only |

The launcher reads:

- `LOCKFILE_IMAGE`: the image to run instead of the release's, for example
  your own build (below);
- `LOCKFILE_VAULT=1`: the image with the `vault` CLI
  ([Credentials from Vault](#credentials-from-vault));
- `LOCKFILE_DOCKER_ARGS`: more `docker run` arguments, split on spaces and
  never globbed, for example the read-only mount of a `local` source:
  `LOCKFILE_DOCKER_ARGS="-v /data/samples:/srv/lockfile/samples:ro" lockfile console --profile samples`;
- `VAULT_ADDR`, `VAULT_NAMESPACE`: passed to the console; `VAULT_CACERT`
  too, with its file mounted read-only at its own path.

It never mounts the Docker socket, and refuses a path holding `:` (docker
`-v` separates on it).

### Your own build

From a clone, or without cloning:

```sh
docker build -t lockfile .
docker build -t lockfile https://github.com/DavidGodefroid/lockfile.git
```

then `LOCKFILE_IMAGE=lockfile lockfile ...`. The launcher of a clone
(`sh scripts/lockfile ...`) runs the image tagged `lockfile` by default, with
the clone's `seccomp.json`. `--build-arg VERSION=vX.Y.Z` sets the version
that `lockfile version` prints. The base images are pinned by digest, and
cosign (and the Vault CLI, below) by sha256.

### Migrating

From the former Docker guide: its `docker run ... lockfile ...` commands keep
working, but the launcher replaces them; the `lockfile-run` and
`lockfile-home` volumes are the same. An agent entry that runs
`docker run -i --rm -v lockfile-run:/run/lockfile lockfile mcp` keeps
working; `lockfile init` writes `lockfile mcp`, which now goes through the
launcher.

From a release before 0.3: the image's accounts moved from locksql's ids
(10000-10002) to lockfile's own, `lockfile-clients` 10030, `lockfile` 10031
and `agent` 10032. The launcher gives the files of `lockfile-run` and
`lockfile-home` the new ids before the console, `doctor` and the commands on
the home volume, once (it changes only the old ids): in a throwaway container
of the image, as root with `CAP_CHOWN` and `CAP_DAC_READ_SEARCH` only and no
network. Without the launcher, run the same once before the console:

```sh
docker run --rm --security-opt seccomp=/usr/local/share/lockfile/seccomp.json \
  --cap-drop=ALL --cap-add=CHOWN --cap-add=DAC_READ_SEARCH \
  --security-opt no-new-privileges --read-only --network none --user 0:0 \
  -v lockfile-run:/run/lockfile -v lockfile-home:/home/lockfile \
  ghcr.io/davidgodefroid/lockfile migrate-ids
```

An agent in a container of its own that reached the console through the gid
10000 now needs gid 10030; `ALLOWED_UIDS` is unchanged.

From a host install (a release binary, then `sudo lockfile install`):
`install.sh` replaces the binary with the launcher, and the image refuses
`lockfile install` and `lockfile update`. What the host install created stays
until you remove it: the `lockfile` account and the `lockfile-clients` group,
`/etc/lockfile/`, the socket directory (`/run/lockfile`,
`/etc/tmpfiles.d/lockfile.conf`), and the console account's home (profiles,
plugins, approved policies, audit log): copy what you keep into the
`lockfile-home` volume.

## Set up the console

The launcher runs the setup commands as the console account in throwaway
containers, on the `lockfile-home` volume. Docker creates the volumes on
first use, with the image's owner and mode.

### Profiles

`lockfile profile add` appends a validated profile to the console's
`~/.config/lockfile/config.toml` (see [usage.md](usage.md#profiles-console-account)
for every key):

```sh
lockfile profile add docs --source s3 --bucket documents --region eu-west-1 --prefix invoices/ \
  --credentials vault --vault-path secret/data/lockfile/docs --production
```

Inside a container, a few things differ from a host:

- **`local` sources.** The path is the console container's, not your
  machine's. Mount the directory read-only under `/srv/lockfile` (the image
  makes that directory `0700`, owned by `lockfile`, so no other account in
  the container can reach what is mounted below it) and give that path to the
  profile:

  ```sh
  lockfile profile add samples --source local --path /srv/lockfile/samples
  ```

  and start the console with
  `LOCKFILE_DOCKER_ARGS="-v /data/samples:/srv/lockfile/samples:ro"` (below).
  The files must be readable by uid 10031.
- **`localhost` is the container itself.** A server on your machine is
  `host.docker.internal`, which the launcher maps to your machine; a server
  in another container is reached by its name on a shared network
  (`LOCKFILE_DOCKER_ARGS="--network NAME" lockfile console ...`).
- **No keychain.** `credentials = "keychain"` needs an OS keychain, which the
  container does not have: use `credentials = "ask"` (typed in the console at
  each start), or `"vault"` with the `vault` image below.
- **SFTP keys** (`auth = "key"`) and `known_hosts` live in `lockfile-home`,
  owned by `lockfile`, the key mode 0600:

  ```sh
  docker run --rm --user lockfile -v lockfile-home:/home/lockfile -v "$PWD/keys:/keys:ro" \
    --entrypoint sh ghcr.io/davidgodefroid/lockfile:X.Y.Z -c 'install -d -m 0700 ~/.ssh && install -m 0600 /keys/partner_ed25519 ~/.ssh/'
  ```

  then `--key ~/.ssh/partner_ed25519` is `/home/lockfile/.ssh/partner_ed25519`.

### Plugins and marketplaces

They are installed as the console account, in its home volume. cosign is in
the image; signatures are checked as on a host:

```sh
lockfile plugin install file.non-empty@1.0.0
lockfile plugin list
lockfile marketplace add acme --index https://plugins.acme.example/index.json --identity-regexp '^https://github\.com/acme/'
```

(`marketplace add` asks you to type the name.)

### Check

```sh
lockfile doctor
```

`sandbox ok` means Landlock and seccomp work under the profile; `proc ok
container: …` is explained in [What Docker does and does not
separate](#what-docker-does-and-does-not-separate).

## Run the console

In a terminal you keep in view:

```sh
LOCKFILE_DOCKER_ARGS="-v /data/samples:/srv/lockfile/samples:ro" lockfile console --profile samples
```

Add `--auto` after `console` as on a host. The launcher takes from the
container what the console never needs: every capability, privilege gain
(the plugin sandbox sets `no_new_privs` itself), a writable root; and bounds
its processes and memory (a plugin may take up to 2 GB). The console refuses
to run any plugin, with a message naming the reason, when Landlock or
seccomp is unavailable, when it runs without a seccomp filter
(`seccomp=unconfined`), or when the container is not private (below): there
is no unsandboxed run. Sandbox and container are checked at start, when a
plan arrives and again just before it runs.

## Credentials from Vault

`credentials = "vault"` runs the `vault` CLI in the console's container. The
default image leaves it out (it adds some 550 MB); the release's `-vault`
image has it (`ghcr.io/davidgodefroid/lockfile:X.Y.Z-vault`), which
`LOCKFILE_VAULT=1` selects. For your own build, `docker build --target vault
-t lockfile:vault .` and `LOCKFILE_IMAGE=lockfile:vault`.

The console reads `VAULT_ADDR` and `VAULT_NAMESPACE` from its environment,
which the launcher passes on, and `VAULT_CACERT`, whose file the launcher
mounts read-only at the same path:

```sh
LOCKFILE_VAULT=1 VAULT_ADDR=https://vault.example.com lockfile console --profile docs
```

Log in once as the console account; the token stays in `lockfile-home`, out
of the agents' reach:

```sh
docker run -it --rm --user lockfile -e VAULT_ADDR=https://vault.example.com \
  -v lockfile-home:/home/lockfile --entrypoint vault \
  ghcr.io/davidgodefroid/lockfile:X.Y.Z-vault login -method=oidc
```

## Wire the agent

From the project root, as you:

```sh
lockfile init claude
```

writes the agent's skill or rules file and `lockfile mcp` as its MCP
command (`.mcp.json` for Claude Code; or `codex`, `cursor`, `gemini`; `--user`
for the whole account). `lockfile mcp` is the launcher: it starts the MCP
server in a throwaway container, as `agent`, with only the socket volume.

```json
{
  "mcpServers": {
    "lockfile": {
      "type": "stdio",
      "command": "lockfile",
      "args": ["mcp"]
    }
  }
}
```

For every project instead, `claude mcp add --scope user lockfile -- lockfile
mcp`. Codex, Gemini CLI and Cursor take the same `command` and `args` (keep
their tool timeout long: the console waits for your approval).

The agent's commands run the same way: `lockfile sources`,
`lockfile plan --key invoices/a.pdf --plugin file.non-empty --run --json`.

## Agent in its own container

When the agent itself runs in a container without the Docker socket, the
launcher cannot start the MCP server: the agent runs the `lockfile` binary
inside its own container as a client of the console
container; the two share only the socket volume. The console checks every
peer by uid against its own container's accounts, where the agent's uid has
none: name it when you start the console, with the release image as it is:

```sh
lockfile console --profile samples --allowed-uids 1000
```

or at build time, in an image of your own:

```sh
docker build -t lockfile --build-arg ALLOWED_UIDS=1000 .
LOCKFILE_IMAGE=lockfile lockfile console --profile samples
```

`--allowed-uids` and `ALLOWED_UIDS` are comma-separated lists of uids: the
build writes them to `allowed_uids` in the image's `/etc/lockfile/system.toml`,
the flag adds them to it for that console only (the console prints them at
start). Both refuse root (0), the console's own uid (10031) and 65534
(nobody, the kernel's overflow uid). Only whoever starts the console sets
them: the human, through the launcher (which passes the flag on) or
through `lock up` (lockhub v0.2.4 or later: a lock's `allowed_uids` in the
lockfamily.yaml lock up reviews with you); no client can. `lockfile doctor`
in the agent's container cannot see a running console's flags: for a uid
with no account it warns instead of failing. It is the uid the kernel reports for
the agent's process, as the console sees it (the agent container's uid when
neither container remaps user namespaces). Any other peer, neither
`lockfile`'s nor in `lockfile-clients` nor listed, is refused.

The agent's image needs what a client host has:

- the `lockfile` binary, not the launcher: the one in the release archive
  (`lockfile_X.Y.Z_linux_ARCH.tar.gz`, checked as `install.sh` checks it),
  the same version as the console's image;
- `/etc/lockfile/system.toml`, owned by root, mode 0644, with the image's
  `service_user`, `client_group` and `socket_dir`;
- an account `lockfile` with uid 10031: the client refuses a socket served by
  any uid but `service_user`'s;
- a group `lockfile-clients` with gid 10030 that holds the agent's account:
  the socket directory is mode 2750 and the sockets 0660, group
  `lockfile-clients`.

Start it with `-v lockfile-run:/run/lockfile`; `lockfile init claude` then
needs no change, and `lockfile status` checks the link.

## What Docker does and does not separate

Inside Docker the boundary is the one of a host: the agent's containers hold
no credential, no plugin and no fetched file, reach the console only through
the socket, and a peer that is neither the console account, nor in
`lockfile-clients`, nor in `ALLOWED_UIDS` is refused. The plugins run in the
console's container under Landlock, seccomp and rlimits, which Docker's
seccomp profile (the shipped one) lets the console install.

**`/proc` and pid namespaces.** On a host, lockfile requires `/proc` mounted
with `hidepid=invisible`: otherwise any account can read a running plugin's
`/proc/<pid>` entries (command line, thread names, memory counters), which
the plugin writes from inside its sandbox, a covert channel to the agent.
Docker cannot mount `/proc` with that option. The image declares instead
(`/etc/lockfile/container.toml`, root-owned) that the console runs in a
container of its own, and the console then accepts a plain `/proc` only when,
at start, when a plan arrives and just before it runs:

- that declaration is a regular file owned by root that others cannot write;
- the console is in a pid namespace other than the host's, and `/proc` is
  that namespace's;
- no process listed in `/proc` runs as an account other than root and
  `lockfile`;
- the console runs under a seccomp filter (never `seccomp=unconfined`).

Otherwise it refuses, naming the condition. So **never start the console
with `--pid=host`** (the host's processes, your agent's among them, would
share its `/proc`; the console refuses), **nor with
`--pid=container:…`**, nor start an agent container with
`--pid=container:lockfile-console` (the agent would list the plugins; the
console refuses once it sees the agent's process). What remains is the host's
own pid namespace, which lists every container's processes: the host's
accounts read them unless the host's `/proc` has `hidepid=invisible`. In the
setup above that is your own account, which has Docker anyway (next
paragraph).

**Your own account** is another matter. Whoever can run `docker` commands can
`docker attach` to the console and type an approval, `docker exec` into it
and read its memory or the fetched files, or open the `lockfile-home` volume.
An agent that runs shell commands in your account usually can: the launcher
it runs for the MCP server is itself a `docker run`. The console cannot see this (its
terminal belongs to the container), so treat this setup as keeping the
sources' credentials and the files out of the agent's reach and the approval
in your terminal, not as stopping an agent determined to go around it. For
that boundary, run the agent itself in a container with no access to the
Docker socket and no shared pid namespace ([Agent in its own
container](#agent-in-its-own-container)).
