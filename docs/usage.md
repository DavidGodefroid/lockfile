# Using lockfile

lockfile has two sides, in two OS accounts:

- **the console** (`lockfile console`), run by the human in the `lockfile` account. It alone
  holds the source credentials, the plugins, the fetched files and the audit log;
- **the agent side** (`lockfile plan|run|…` and `lockfile mcp`), run by the AI agent in the
  human's own account. It holds nothing and gets 1s and 0s.

## Setup

lockfile runs in Docker, through the `lockfile` launcher: it runs each command below in the
release image, with the account and the volumes it needs ([docker.md](docker.md#what-runs-where)):

| Commands | Runs as |
|---|---|
| console account: `profile`, `plugin`, `marketplace`, `audit`, `forget`, `doctor` | `lockfile`, on the `lockfile-home` volume |
| the console | `lockfile`, confined, under the shipped seccomp profile ([Run the console](docker.md#run-the-console)); `local` sources mounted with `LOCKFILE_DOCKER_ARGS` |
| `init`, `doctor --fix` | you, with your home and the project |
| agent side: `status`, `sources`, `plugins`, `plan`, `run`, `answers`, `release`, `mcp` | `agent`, with the `lockfile-run` volume only |

`lockfile version` prints the launcher's version and the image's; `lockfile update [vX.Y.Z]`
replaces the launcher and the seccomp profile with a release's (cosign signatures and checksum
checked) and pulls its image.

The console refuses to run plugins without Landlock and seccomp (the container runs under the
shipped `seccomp.json`), and when other accounts could read the plugins' processes in `/proc`:
plans are then refused with `proc_not_hidden` (`E_PROC_NOT_HIDDEN`). In Docker the image's
container declaration stands in for `hidepid=invisible`; see
[docker.md](docker.md#what-docker-does-and-does-not-separate) for its conditions.

From the console account, `lockfile doctor` also checks the sandbox, `/proc`, the work
directory (mode 0700, parents other accounts cannot traverse, not under a world-writable
directory such as `/tmp`, not on a network filesystem), every `local` source directory (not
reachable by other accounts or by the clients group: mount it under `/srv/lockfile`) and the
socket directory's owner (`lockfile:lockfile-clients`). It prints no path from the
configuration.

## Profiles (console account)

Profiles live in the lockfile account's `~/.config/lockfile/config.toml`; there is no per-project
config. `lockfile profile add` appends one, validated, without touching the rest of the file:

```sh
lockfile profile add docs --source s3 --bucket documents --region eu-west-1 --prefix invoices/ \
  --credentials vault --vault-path secret/data/lockfile/docs --production
lockfile profile add partner --source sftp --host sftp.example.org --user reader --auth key \
  --key ~/.ssh/partner_ed25519 --known-hosts ~/.ssh/known_hosts --base-dir /outgoing
lockfile profile add samples --source local --path /srv/lockfile/samples   # mounted read-only, docker.md
```

```toml
[profiles.docs]
source = "s3"                 # s3 | sftp | local
bucket = "documents"
region = "eu-west-1"
endpoint = ""                 # S3-compatible server; https:// (http:// only for localhost)
prefix = "invoices/"          # keys outside the prefix are refused
credentials = "vault"         # ask | vault (none: local, or an unencrypted sftp key); keychain: not in Docker
vault_path = "secret/data/lockfile/docs"   # username = access key id, password = secret key
production = true             # the profile name is typed for every approval
max_object_bytes = "50MiB"    # larger objects are not fetched
max_plugins_per_handle = 16   # answers revealed about one fetched file, at most
handle_ttl = "30m"            # a fetched file is deleted after this
answer_quantum = "5s"         # results leave the console only on multiples of this
```

Top-level `work_dir` (absolute, default under the state directory) and `max_workspace_bytes`
(default `1GiB`, all fetched files together) are part of the approved policy: the console
uses them, and `handle_ttl`, only once `:review` has accepted them. Moving `work_dir`, raising
`max_workspace_bytes` or lengthening `handle_ttl` is a loosening; a fetched file keeps the TTL
it was fetched with.

Credentials: S3 takes the access key id as `user` and the secret access key as the secret;
SFTP `auth = "password"` takes user + password; `auth = "key"` reads the private key (mode
0600 or stricter, no symlink) in the lockfile account and takes its passphrase as the secret.
A leading `~/` in `key` and `known_hosts` is the lockfile account's home.
On SFTP a key whose path below `base_dir` goes through a symbolic link on the server, or that
the server resolves outside `base_dir`, is not fetched (`fetch_failed`).
The container has no OS keychain: use `ask` (typed in the console at each start) or `vault`
(the image's `vault` target, [docker.md](docker.md#credentials-from-vault)).

## Plugins and marketplaces (console account)

```sh
lockfile plugin install pdf.parses@1.0.0        # exact versions only; signatures checked with cosign
lockfile plugin update pdf.parses@1.1.0         # shows the manifest diff; a changed question needs the name typed
lockfile plugin list | verify [NAME] | remove NAME
lockfile marketplace add acme --index https://plugins.acme.example/index.json \
  --identity-regexp '^https://github\.com/acme/lockfile-plugins/\.github/workflows/release\.yml@refs/tags/'
lockfile marketplace list | remove NAME
```

In a separated setup every `plugin` and `marketplace` command, `list` and `verify` included,
refuses to run outside the lockfile account (from another account the store looks empty).

A version its marketplace withdraws (yanked) refuses to run (`plugin_yanked`). The yanked
status comes from the marketplace's signed index: `plugin install`, `plugin update` and
`plugin verify` read it and record it in `plugins.lock`; run `lockfile plugin verify`
regularly, and `plugin update` to a version that is not yanked.

The official marketplace's signer is pinned in the binary. An unofficial marketplace's
`--identity-regexp` must start with `^https://`; `marketplace add` warns when it has an
unescaped `.` or does not pin `HOST/OWNER/REPO/` (other signers would match). It needs
its name typed when added (in a terminal: a typed name is never read from a pipe, nor the
plugin name `plugin update` asks for), is flagged on every approval screen and in every agent result,
and never runs in auto mode. Keyless signers only (`--public-key` is refused).

A running console sees an installed plugin or an added marketplace as a policy loosening:
it applies it only after `:review` in the console.

## The console

```sh
lockfile console --profile docs [--auto | --hub URL [--hub-plain-http] --hub-key-file FILE --instance NAME]
```

Run in its container ([docker.md](docker.md#run-the-console)), it checks the isolation (the
console account, its own terminal), the sandbox and `/proc`, asks for the secret, connects,
and serves the profile's socket in `/run/lockfile` (the `lockfile-run` volume). Every plan shows an approval screen: the key as
the agent typed it, each plugin with its question and the meaning of 1 and 0, how many answers
the plan reveals, and a red line per unofficial plugin. `y` approves (the profile name on
production), `n <reason>` denies. Keys: `:review`, `:status`, `:mode`/Shift+Tab (auto mode, never
on production, never for unofficial plugins), `:quit`. Results show as `plan pl_…: answered` or
`refused <code>`; the answers, causes, sizes and hashes are in the audit:

```sh
lockfile audit [--since 24h] [--json]           # console account only
```

**Hub mode.** With `--hub URL --hub-key-file FILE --instance NAME` (the three go together, never with
`--auto`; `lock up` writes them), plans are decided on lockhub, never on this terminal; the
policy and `:review` stay here. `file.plan` fetches the object at once, hashes it, and asks
the hub with a signed ticket (an object that cannot be fetched is not told to the agent then:
the plan goes to the hub, the screen says so, and an approval answers `fetch_failed`; the plan
answer waits for the profile's `answer_quantum`): the screen masked (personal data in the key), the plan hash over
the content's sha256, the key or handle, the plugins and their pins, under the policy in force.
The agent hears `pending` with the ticket and runs it once decided (`lockfile run TICKET`,
`lockfile_run`). The run checks the decision (pinned hub key, this instance, the ticket, still
usable, nonce once), the policy and the plugins, fetches again, and refuses other bytes
`stale` before any plugin. A ticket runs once, for the agent that asked (its lea token). The
decision is audited with the ticket, the approver, the facade and the hub's signature.
`lockfile hub-key --instance NAME` prints the instance key for `lock up` to pin (made once in
the console account's home, `0600`).
`lock up` also passes `--hub-plain-http` for its `http://lockhub:8443` on the hub's own network:
without it an `http://` hub URL other than loopback is refused, and with it the URL must still
be a service name without a dot or a private address.

## The agent side

| Command | MCP tool | What |
|---|---|---|
| `lockfile status [--profile P]` | `lockfile_status` | the running consoles' state (`busy`: see below) |
| `lockfile sources` | `lockfile_sources` | profiles of the running consoles (`busy`: see below) |
| `lockfile plugins [--profile P]` | `lockfile_plugins` | questions, meaning of 1 and 0 |
| `lockfile plan --key K --plugin X… [--intent T] [--run]` | `lockfile_plan` | ask the human to approve |
| `lockfile run PLAN_ID` (hub mode: `TICKET`) | `lockfile_run` | run an approved plan once (hub mode: the ticket of a pending plan, once decided) |
| `lockfile plan --handle H --plugin X… --run` | `lockfile_plan` + `lockfile_run` | follow-up on the same file |
| `lockfile answers HANDLE` | `lockfile_answers` | answers already given |
| `lockfile release HANDLE` | `lockfile_release` | delete the fetched file now (answers at the next `answer_quantum`) |

Every command takes `--json` and `--profile P` (optional when one console runs). Exit codes
depend on the outcome only, never on an answer: 0 answered, 1 no console running or another
error, 2 refused (an `E_*` refusal; `unknown_profile` is 2 whether the client or the console finds
it), 3 denied or no answer in time, 64 usage. `status` exits 1 when a profile it reports on has
no console.

A console serves one request at a time. `busy` means it did not answer the probe within 2 s
because it is serving another one: a plan waiting for the human, or a run until its
`answer_quantum`. A console whose profile name is too long for a plain socket name cannot be
listed while it is busy; passing its profile with `--profile` still reaches it.

Output is capped in text and in JSON alike: a value (a text line) is cut at 512 characters, and
output above 64 KiB is not printed (`output too large`, exit 1).

There is no listing or stat: the agent asks the user for the exact key.

## Wiring an agent

`lockfile init claude|codex|cursor|gemini`, run from the project root, writes the skill or
rules file and `lockfile mcp` as the MCP command: the launcher starts the MCP server in a
throwaway container with the socket volume only ([docker.md](docker.md#wire-the-agent)). For
every project instead: `claude mcp add --scope user lockfile -- lockfile mcp`. Claude Code
may call `lockfile_status`, `lockfile_sources` and `lockfile_plugins` without a prompt; plans
and runs always go through Claude Code's permission prompt and the console.

## Updates

`lockfile update` (or `lockfile update vX.Y.Z`) replaces the launcher and the seccomp profile,
then pulls the release's image; restart the console, the volumes keep its state. The agent's
containers start from the new image on their next run. With your own build, rebuild it and
keep `LOCKFILE_IMAGE` pointing at it.

A marketplace index older than the last one verified for that marketplace is refused (a
replayed index could lift a yank); the last `generated` time of each marketplace is kept next
to `plugins.lock`.
