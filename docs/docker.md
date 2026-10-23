# Running lockfile in Docker

Docker is how lockfile is deployed: nothing on your machine but Docker and a
Linux kernel with Landlock. Other deployment modes exist in the code but are
no longer documented.

The image holds the separated setup in two kinds of containers that share
only the socket volume:

- the console runs as `lockfile` (uid 10001), the only account that holds
  the source credentials, the plugins, the fetched files and the audit log;
- the agent's MCP server and CLI run as `agent` (uid 10002), a member of
  `lockfile-clients` (gid 10000), each in a throwaway container;
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
  Docker version. Every console container runs with
  `--security-opt seccomp=/path/to/seccomp.json`. **Never
  `seccomp=unconfined`**: it would lift every restriction Docker puts on the
  console itself, and the sandbox does not need it.

## Build

From a clone:

```sh
docker build -t lockfile .
```

or without cloning (then fetch the profile too):

```sh
docker build -t lockfile https://github.com/DavidGodefroid/lockfile.git
curl -fsSLO https://raw.githubusercontent.com/DavidGodefroid/lockfile/main/seccomp.json
```

`--build-arg VERSION=v0.3.0` sets the version that `lockfile version` prints.
The base images are pinned by digest, and cosign (and the Vault CLI, below)
by sha256.

## Set up the console

The setup commands run as the console account in throwaway containers, on
the `lockfile-home` volume. Docker creates the volumes on first use, with the
image's owner and mode.

### Profiles

`lockfile profile add` appends a validated profile to the console's
`~/.config/lockfile/config.toml` (see [usage.md](usage.md#profiles-console-account)
for every key):

```sh
docker run --rm --user lockfile -v lockfile-home:/home/lockfile lockfile \
  profile add docs --source s3 --bucket documents --region eu-west-1 --prefix invoices/ \
  --credentials vault --vault-path secret/data/lockfile/docs --production
```

Inside a container, a few things differ from a host:

- **`local` sources.** The path is the console container's, not your
  machine's. Mount the directory read-only under `/srv/lockfile` (the image
  makes that directory `0700`, owned by `lockfile`, so no other account in
  the container can reach what is mounted below it) and give that path to the
  profile:

  ```sh
  docker run --rm --user lockfile -v lockfile-home:/home/lockfile lockfile \
    profile add samples --source local --path /srv/lockfile/samples
  ```

  and add `-v /data/samples:/srv/lockfile/samples:ro` to the console's
  `docker run` below. The files must be readable by uid 10001.
- **`localhost` is the container itself.** A server on your machine is
  `host.docker.internal` (on Linux add
  `--add-host=host.docker.internal:host-gateway` to the console's
  `docker run`); a server in another container is reached by its name on a
  shared network (`--network NAME`).
- **No keychain.** `credentials = "keychain"` needs an OS keychain, which the
  container does not have: use `credentials = "ask"` (typed in the console at
  each start), or `"vault"` with the `vault` image below.
- **SFTP keys** (`auth = "key"`) and `known_hosts` live in `lockfile-home`,
  owned by `lockfile`, the key mode 0600:

  ```sh
  docker run --rm --user lockfile -v lockfile-home:/home/lockfile -v "$PWD/keys:/keys:ro" \
    --entrypoint sh lockfile -c 'install -d -m 0700 ~/.ssh && install -m 0600 /keys/partner_ed25519 ~/.ssh/'
  ```

  then `--key ~/.ssh/partner_ed25519` is `/home/lockfile/.ssh/partner_ed25519`.

### Plugins and marketplaces

They are installed as the console account, in its home volume. cosign is in
the image; signatures are checked as on a host:

```sh
docker run --rm --user lockfile -v lockfile-home:/home/lockfile lockfile plugin install file.non-empty@1.0.0
docker run --rm --user lockfile -v lockfile-home:/home/lockfile lockfile plugin list
docker run -it --rm --user lockfile -v lockfile-home:/home/lockfile lockfile \
  marketplace add acme --index https://plugins.acme.example/index.json --identity-regexp '^https://github\.com/acme/'
```

(`marketplace add` asks you to type the name: `-it`.)

### Check

```sh
docker run --rm --user lockfile --security-opt seccomp="$PWD/seccomp.json" \
  -v lockfile-run:/run/lockfile -v lockfile-home:/home/lockfile lockfile doctor
```

`sandbox ok` means Landlock and seccomp work under the profile; `proc ok
container: …` is explained in [What Docker does and does not
separate](#what-docker-does-and-does-not-separate).

## Run the console

In a terminal you keep in view:

```sh
docker run -it --rm --name lockfile-console --user lockfile \
  --security-opt seccomp="$PWD/seccomp.json" \
  -v lockfile-run:/run/lockfile \
  -v lockfile-home:/home/lockfile \
  -v /data/samples:/srv/lockfile/samples:ro \
  lockfile console --profile samples
```

Add `--auto` after `console` as on a host. The console refuses to run any
plugin, with a message naming the reason, when Landlock or seccomp is
unavailable or when the container is not private (below): there is no
unsandboxed run.

## Credentials from Vault

`credentials = "vault"` runs the `vault` CLI in the console's container. The
default image leaves it out (it adds some 550 MB); the `vault` target has it:

```sh
docker build --target vault -t lockfile .
```

The console reads `VAULT_ADDR` (and `VAULT_CACERT`, `VAULT_NAMESPACE`) from
its environment: add `-e VAULT_ADDR=https://vault.example.com` to its
`docker run`. Log in once as the console account; the token stays in
`lockfile-home`, out of the agents' reach:

```sh
docker run -it --rm --user lockfile -e VAULT_ADDR=https://vault.example.com \
  -v lockfile-home:/home/lockfile --entrypoint vault lockfile login -method=oidc
```

## Wire the agent

The agent starts the MCP server in a throwaway container, as `agent`, with
only the socket volume:

```sh
claude mcp add --scope user lockfile -- docker run -i --rm -v lockfile-run:/run/lockfile lockfile mcp
```

or for one project, in `.mcp.json`:

```json
{
  "mcpServers": {
    "lockfile": {
      "type": "stdio",
      "command": "docker",
      "args": ["run", "-i", "--rm", "-v", "lockfile-run:/run/lockfile", "lockfile", "mcp"]
    }
  }
}
```

`lockfile init claude` (or `codex`, `cursor`, `gemini`) also writes the
agent's skill or rules file; run it from the project root, as you, then
replace the `lockfile` MCP entry it wrote (`lockfile mcp`) by the one above:

```sh
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp -v "$PWD:$PWD" -w "$PWD" lockfile init claude
```

Codex, Gemini CLI and Cursor take the same `command` and `args` in their MCP
configuration (keep their tool timeout long: the console waits for your
approval).

The agent's commands run the same way:

```sh
docker run --rm -v lockfile-run:/run/lockfile lockfile sources
docker run --rm -v lockfile-run:/run/lockfile lockfile plan --key invoices/a.pdf --plugin file.non-empty --run --json
```

## Agent in its own container

When the agent itself runs in a container without the Docker socket, it runs
the `lockfile` binary inside its own container as a client of the console
container; the two share only the socket volume. The console checks every
peer by uid against its own container's accounts, where the agent's uid has
none: name it at build time.

```sh
docker build -t lockfile --build-arg ALLOWED_UIDS=1000 .
```

`ALLOWED_UIDS` is a comma-separated list of uids, written to `allowed_uids`
in the image's `/etc/lockfile/system.toml`: the uid the kernel reports for
the agent's process, as the console sees it (the agent container's uid when
neither container remaps user namespaces). Any other peer, neither
`lockfile`'s nor in `lockfile-clients` nor listed, is refused.

The agent's image needs what a client host has:

- the `lockfile` binary, the same version as the console's;
- `/etc/lockfile/system.toml`, owned by root, mode 0644, with the image's
  `service_user`, `client_group` and `socket_dir`;
- an account `lockfile` with uid 10001: the client refuses a socket served by
  any uid but `service_user`'s;
- a group `lockfile-clients` with gid 10000 that holds the agent's account:
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
at start and before every plan:

- that declaration is a regular file owned by root that others cannot write;
- the console is in a pid namespace other than the host's, and `/proc` is
  that namespace's;
- no process listed in `/proc` runs as an account other than root and
  `lockfile`.

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
An agent that runs shell commands in your account usually can: the MCP
server above is itself a `docker run`. The console cannot see this (its
terminal belongs to the container), so treat this setup as keeping the
sources' credentials and the files out of the agent's reach and the approval
in your terminal, not as stopping an agent determined to go around it. For
that boundary, run the agent itself in a container with no access to the
Docker socket and no shared pid namespace ([Agent in its own
container](#agent-in-its-own-container)).
