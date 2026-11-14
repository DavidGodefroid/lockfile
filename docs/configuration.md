# Configuring lockfile

Profiles live in the workspace's `.lockfile/config.toml`, which lockhub
reviews and copies, read-only, to the console's `~/.config/lockfile` when it
starts the lock. `lockfile profile add --profile NAME --source local|s3|sftp
...` appends one, validated, and never rewrites one; on a terminal it asks for
what the flags did not give, and `--non-interactive --json` answers in one
line of JSON ([usage.md](usage.md#profiles-console-account)). No file ever
holds a secret, and `profile add` never takes one.

```toml
[profiles.docs]
source = "s3"                 # s3 | sftp | local
bucket = "documents"
region = "eu-west-1"
endpoint = ""                 # S3-compatible server; https:// (http:// only to host.docker.internal or localhost, never production)
prefix = "invoices/"          # keys outside the prefix are refused
credentials = "vault"         # ask | vault (none: local, or an unencrypted sftp key); no keychain in Docker
vault_path = "secret/data/lockfile/docs"   # username = access key id, password = secret key
production = true             # tickets flagged production; the console's widenings need the name typed
# unconfined = true           # serve agents that do not run confined (never on production)
max_object_bytes = "50MiB"    # larger objects are not fetched
max_plugins_per_handle = 16   # answers revealed about one fetched file, at most
handle_ttl = "30m"            # a fetched file is deleted after this
answer_quantum = "5s"         # results leave the console only on multiples of this
tickets_per_window = 30       # hub tickets a human decides, per ticket_window at most
ticket_window = "5m"
max_pending = 30              # hub tickets pending a decision at once, at most
```

**Ticket limits** (lockkit v0.15.0). The console bounds the tickets it opens
on lockhub, so that an agent cannot flood you with questions: at most
`tickets_per_window` tickets a human decides per `ticket_window` (default 30
per 5 minutes) and `max_pending` pending at once (default 30). A ticket an
auto rule of `lox run` decides leaves the `tickets_per_window` count once its
decision is read; a safety cap of 300 tickets per window still counts every
one. A plan over a limit is not submitted: the console prints `not submitted:
…` with the limit, the count and when the next ticket is accepted, and the
agent's plan is refused. Lowering a limit applies at once; more tickets, a
shorter window or more pending tickets is a loosening, reviewed at `:review`
(the typed profile name on production). The status screen shows the limits
in force (`tickets: 30 per 5m0s · 30 pending`). Negative values are refused;
0 or unset is the default.

SFTP profiles take `host`, `port`, `user`, `auth = "password"` or
`auth = "key"` with `key` (a private key in the `lockfile` account's home
volume, mode 0600 or stricter), `known_hosts` and `base_dir`; local profiles
take `path`, a read-only mount under `/srv/lockfile` in the console's
container. See [usage.md](usage.md#profiles-console-account).

`production = true` (v0.8.0): the console's own widenings, the policy review
at start and a loosening at `:review`, take the profile name typed instead of
`y`; its tickets are flagged production on every lockhub facade.

**Confined agents** (v0.10.0, lockkit v0.20.0). The console serves only an
agent that runs confined: its lockfile client reports, in its `hello`, that
it runs in a container with no host runtime socket (Docker's), no Lock
Family state and a read-only `.lockfamily/`, as under lea. Any other agent,
or a client older than v0.10.0 that sends no report (`old_client`), is
refused at `hello` ("this agent does not run confined (…); run it inside
lea, or ask the human to set unconfined = true on this non-production
profile"), audited `refused-unconfined` with its reasons. `unconfined = true`
on a profile lets it in: the console then protects against mistakes, not
against a compromised agent, and says so in red at start and at every such
connection; every audit record of the connection carries
`"unconfined":true` and its hub tickets `flags.unconfined`. Setting it is a
loosening, reviewed at `:review`; a production profile with it does not
start ("unconfined is never allowed on a production profile"), and a managed
`[family] require_confined_agent = true` refuses unconfined agents whatever
the profile says. The client and the console must be of the same version:
an older console refuses a v0.10.0 client's `hello`.

## Managed configuration

A company can bound lockfile on a machine with the Lock Family's
`managed.toml` (schema and deployment:
[lockfamily docs/managed.md](https://github.com/DavidGodefroid/lockfamily/blob/main/docs/managed.md)):
`/etc/lockfamily/managed.toml` on Linux, `/Library/Application
Support/lockfamily/managed.toml` on macOS, owned by root and writable by root
only (`lox run` mounts it into the console's container). lockfile reads its
`[lockfile]` section; a user may tighten what it allows, never loosen it, and
no human can approve beyond it.

```toml
version = 1
organisation = "Acme"

[lockfile.bounds]
source = ["s3", "sftp"]
hosts = ["*.s3.corp.example", "files.corp.example"]
max_object_bytes = "10MiB"
marketplaces = ["official"]

[lockfile.defaults]
handle_ttl = "10m"

[lockfile.profiles.erp]
source = "sftp"
host = "files.corp.example"
user = "files"
auth = "key"
key = "/home/lockfile/.ssh/erp"
known_hosts = "/home/lockfile/.ssh/known_hosts"
```

Precedence: hard-coded floor > managed bounds and profiles > your config >
managed defaults > built-in defaults. The merged result is checked against
the bounds at the console's start and at every policy poll (2 s): beyond
them, a profile is refused with no prompt (`blocked by company policy:
lockfile.bounds.max_object_bytes = 10485760 (profile dev:
limits.max_object_bytes = 52428800)`, audited `refused-managed`), and a
change made while the console runs is refused, never pending. The console
prints `Managed by <organisation> (sha256:…)` at start; the approved policy
and every audit record carry the file's fingerprint (`managed_sha256`).

**`[lockfile.bounds]`**, every key optional:

| Key | Type | Looser is |
|---|---|---|
| `production` | `true` | a profile without `production = true` |
| `source` | allowed list (`["s3", "sftp", "local"]`) | another source kind |
| `hosts` | allowed host patterns (`*` matches any part, case-insensitive) | an s3 `endpoint` host, the AWS host of a `region` (`s3.<region>.amazonaws.com`) or an sftp `host` matching none |
| `buckets` | allowed list of s3 bucket names | another bucket |
| `credentials` | allowed list (`ask`, `keychain`, `vault`, `none`) | another credentials mode (a local source's is `none`) |
| `credentials_ttl` | duration ceiling | longer, or unset (the whole session) |
| `idle_timeout`, `max_session`, `handle_ttl` | duration ceiling | longer |
| `max_object_bytes` | size ceiling | larger |
| `max_objects_per_plan`, `max_plugins_per_handle` | ceiling | larger |
| `max_workspace_bytes` | size ceiling (the file-level key) | larger |
| `answer_quantum` | duration floor | shorter |
| `marketplaces` | allowed list of marketplace names (`official` is lockfile's own) | a `[[marketplaces]]` entry not in the list |

`[family] vault = { required = true }` acts as `credentials = ["vault"]`,
cited as `family.vault.required`. A refusal names the managed key; a bound
on a list (`source`, `hosts`, `buckets`, `credentials`) reads `blocked by
company policy: lockfile.bounds.source does not allow local (profile disk:
source = local)`. There is no plugin allow list: plugins come only from the
marketplaces, which `marketplaces` bounds.

**`[lockfile.defaults]`** takes the profile keys plus `max_workspace_bytes`:
each fills a key your config leaves unset, before the built-in defaults. A
default beyond the bounds is a load error.

**`[lockfile.profiles.<name>]`** takes exactly the profile keys and is served
as written, with no config of your own. A profile of yours with a managed
name is refused, naming it (rename yours). A managed profile beyond the
bounds is a load error.

Commands:

- `lockfile managed show` prints the file's path, organisation, fingerprint
  and signature, lockfile's bounds and defaults and the managed profile
  names; `no managed configuration` without one (exit 0); a refused file
  (not root's, invalid, bad signature) prints lockkit's error, exit 3.
- `lockfile managed check [--profile NAME]` loads your config and the
  managed file as the console does and prints one `blocked by company
  policy: …` line per violation (exit 3), else `N profile(s) within the
  company policy (Managed by …)` (exit 0). It opens no connection and reads
  no credential; `lox run` runs it before it starts the lock.
- `lockfile doctor` has a `managed` line: the file in force, `no managed
  configuration`, or why it is refused.
- A console refused by the managed file exits 3.
