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
