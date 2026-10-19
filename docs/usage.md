# Using lockfile

lockfile has two sides, in two OS accounts:

- **the console** (`lockfile console`), run by the human in the `lockfile` account. It alone
  holds the source credentials, the plugins, the fetched files and the audit log;
- **the agent side** (`lockfile plan|run|…` and `lockfile mcp`), run by the AI agent in the
  human's own account. It holds nothing and gets 1s and 0s.

## Setup

```sh
sh scripts/install.sh            # or a release archive; cosign is required
sudo lockfile install            # Linux: account lockfile, group lockfile-clients, /etc/lockfile/system.toml,
                                 # /run/lockfile (2750), root-owned /usr/local/bin/lockfile
lockfile doctor                  # from both accounts
```

`lockfile install` prints the root script before it runs it (`--print` only prints it,
`--yes` skips the question). It refuses a binary another account could replace (install a
release into `/usr/local/bin` first, or pass `--trust-binary` to pin this one's sha256).
After it: give the console account a password, log out and in again (the new group), and
open a separate session as `lockfile` (switch user; prefer Wayland) for the console.

`/proc` must hide other accounts' processes, or consoles refuse to start and plans are
refused with `proc_not_hidden` (`E_PROC_NOT_HIDDEN`); `lockfile install` does not change it:

```sh
sudo mount -o remount,hidepid=invisible,gid=<admin group> /proc
# /etc/fstab, to keep it after a reboot:
proc /proc proc defaults,hidepid=invisible,gid=<admin group> 0 0
```

Members of `gid=` still see every process (monitoring tools, admins).

From the console account, `lockfile doctor` also checks the work directory (mode 0700, parents
other accounts cannot traverse, not under a world-writable directory such as `/tmp`, not on a
network filesystem), every `local` source directory (not reachable by other accounts or by the
clients group) and the socket directory's owner (`lockfile:lockfile-clients`). It prints no
path from the configuration.

## Profiles (console account)

Profiles live in the lockfile account's `~/.config/lockfile/config.toml`; there is no per-project
config. `lockfile profile add` appends one, validated, without touching the rest of the file:

```sh
lockfile profile add docs --source s3 --bucket documents --region eu-west-1 --prefix invoices/ \
  --credentials vault --vault-path secret/data/lockfile/docs --production
lockfile profile add partner --source sftp --host sftp.example.org --user reader --auth key \
  --key ~/.ssh/partner_ed25519 --known-hosts ~/.ssh/known_hosts --base-dir /outgoing
lockfile profile add samples --source local --path /srv/samples
```

```toml
[profiles.docs]
source = "s3"                 # s3 | sftp | local
bucket = "documents"
region = "eu-west-1"
endpoint = ""                 # S3-compatible server; https:// (http:// only for localhost)
prefix = "invoices/"          # keys outside the prefix are refused
credentials = "vault"         # ask | keychain | vault (none: local, or an unencrypted sftp key)
vault_path = "secret/data/lockfile/docs"   # username = access key id, password = secret key
production = true             # the profile name is typed for every approval
max_object_bytes = "50MiB"    # larger objects are not fetched
max_plugins_per_handle = 16   # answers revealed about one fetched file, at most
handle_ttl = "30m"            # a fetched file is deleted after this
answer_quantum = "5s"         # results leave the console only on multiples of this
```

Credentials: S3 takes the access key id as `user` and the secret access key as the secret;
SFTP `auth = "password"` takes user + password; `auth = "key"` reads the private key (mode
0600 or stricter, no symlink) in the lockfile account and takes its passphrase as the secret.
On SFTP a key whose path below `base_dir` goes through a symbolic link on the server, or that
the server resolves outside `base_dir`, is not fetched (`fetch_failed`).
`lockfile forget --profile P` deletes a keychain secret.

## Plugins and marketplaces (console account)

```sh
lockfile plugin install pdf.parses@1.0.0        # exact versions only; signatures checked with cosign
lockfile plugin update pdf.parses@1.1.0         # shows the manifest diff; a changed question needs the name typed
lockfile plugin list | verify [NAME] | remove NAME
lockfile marketplace add acme --index https://plugins.acme.example/index.json \
  --identity-regexp '^https://github\.com/acme/lockfile-plugins/\.github/workflows/release\.yml@refs/tags/'
lockfile marketplace list | remove NAME
```

The official marketplace's signer is pinned in the binary. An unofficial marketplace needs
its name typed when added, is flagged on every approval screen and in every agent result,
and never runs in auto mode. Keyless signers only in v0.2 (`--public-key` is refused).

A running console sees an installed plugin or an added marketplace as a policy loosening:
it applies it only after `:review` in the console.

## The console

```sh
lockfile console --profile docs [--auto]
```

It checks the isolation (separate account and session), asks for the secret, connects, and
serves the profile's socket in `/run/lockfile`. Every plan shows an approval screen: the key as
the agent typed it, each plugin with its question and the meaning of 1 and 0, how many answers
the plan reveals, and a red line per unofficial plugin. `y` approves (the profile name on
production), `n <reason>` denies. Keys: `:review`, `:status`, `:mode`/Shift+Tab (auto mode, never
on production, never for unofficial plugins), `:quit`. Results show as `plan pl_…: answered` or
`refused <code>`; the answers, causes, sizes and hashes are in the audit:

```sh
lockfile audit [--since 24h] [--json]           # console account only
```

## The agent side

| Command | MCP tool | What |
|---|---|---|
| `lockfile status [--profile P]` | `lockfile_status` | the running consoles' state |
| `lockfile sources` | `lockfile_sources` | profiles of the running consoles (`busy`: waiting on the human) |
| `lockfile plugins [--profile P]` | `lockfile_plugins` | questions, meaning of 1 and 0 |
| `lockfile plan --key K --plugin X… [--intent T] [--run]` | `lockfile_plan` | ask the human to approve |
| `lockfile run PLAN_ID` | `lockfile_run` | run an approved plan once |
| `lockfile plan --handle H --plugin X… --run` | `lockfile_plan` + `lockfile_run` | follow-up on the same file |
| `lockfile answers HANDLE` | `lockfile_answers` | answers already given |
| `lockfile release HANDLE` | `lockfile_release` | delete the fetched file now |

Every command takes `--json` and `--profile P` (optional when one console runs). Exit codes
depend on the outcome only, never on an answer: 0 answered, 1 no console or error, 2 refused, 3
denied or no answer in time, 64 usage.

There is no listing or stat: the agent asks the user for the exact key.

## Wiring an agent

```sh
lockfile init claude|codex|cursor|gemini          # this project
lockfile init claude --user                       # every project of this account
lockfile doctor --fix                             # wires the installed agents not wired yet
```

Claude Code may call `lockfile_status`, `lockfile_sources` and `lockfile_plugins` without a
prompt; plans and runs always go through Claude Code's permission prompt and the console.

## Updates

```sh
lockfile update [--check] [--yes]
```

checks `checksums.txt` against the signature of lockfile's release workflow (cosign), then
the archive against it, then replaces the binary (with sudo from a pinned copy when root
owns it).
