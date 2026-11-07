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
endpoint = ""                 # S3-compatible server; https:// (http:// only for localhost)
prefix = "invoices/"          # keys outside the prefix are refused
credentials = "vault"         # ask | vault (none: local, or an unencrypted sftp key); no keychain in Docker
vault_path = "secret/data/lockfile/docs"   # username = access key id, password = secret key
production = true             # tickets flagged production; the console's widenings need the name typed
max_object_bytes = "50MiB"    # larger objects are not fetched
max_plugins_per_handle = 16   # answers revealed about one fetched file, at most
handle_ttl = "30m"            # a fetched file is deleted after this
answer_quantum = "5s"         # results leave the console only on multiples of this
```

SFTP profiles take `host`, `port`, `user`, `auth = "password"` or
`auth = "key"` with `key` (a private key in the `lockfile` account's home
volume, mode 0600 or stricter), `known_hosts` and `base_dir`; local profiles
take `path`, a read-only mount under `/srv/lockfile` in the console's
container. See [usage.md](usage.md#profiles-console-account).

`production = true` (v0.8.0): the console's own widenings, the policy review
at start and a loosening at `:review`, take the profile name typed instead of
`y`; its tickets are flagged production on every lockhub facade.
