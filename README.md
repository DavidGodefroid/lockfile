# lockfile

**Your AI asks about the file. It never reads it.**

lockfile lets an AI agent ask yes/no questions about a file it must never
read: is this invoice empty, a PDF, does it parse, does it have a blank page,
is it truncated? A console run by a human in a separate OS account fetches
the file from S3, SFTP or a local directory, runs signed, sandboxed plugins
on it, and returns only each plugin's answer: `1`, `0`, or no answer. No byte
of the file, its name, size, type or path ever reaches the agent, and every
plan is approved by the human first.

Plugins come from a signed marketplace
([lockfile-marketplace](https://github.com/DavidGodefroid/lockfile-marketplace)):
each answers one fixed question, written in its signed manifest.

```
agent account (CLI / MCP)            lockfile account (console, human terminal)
  lockfile plan/run/answers   ──UDS──▶  approve ─▶ fetch ─▶ workspace (0700)
           ▲                    JSON-RPC                       │ fd 3, read-only
           │                                                    ▼
           └────── 1 / 0 per plugin ◀── strict check ◀── sandboxed plugin process
```

## Quick start (Linux)

```sh
# 1. The binary (checks the release signature with cosign).
curl -fsSL https://raw.githubusercontent.com/DavidGodefroid/lockfile/main/scripts/install.sh -o install.sh
sh install.sh

# 2. The separated setup: console account `lockfile`, your account in `lockfile-clients`.
sudo lockfile install
lockfile doctor

# 3. In the lockfile account's own session (switch user, Wayland):
lockfile profile add samples --source local --path /srv/samples --prefix invoices/
lockfile plugin install file.non-empty@1.0.0
lockfile plugin install format.is-pdf@1.0.0
lockfile console --profile samples

# 4. In your account: wire your agent, then ask.
lockfile init claude
lockfile plugins --json
lockfile plan --key invoices/2026/10/abc123.pdf --plugin file.non-empty --plugin format.is-pdf --run --json
```

```json
{"handle":"f_8Kq2xR","answers":[
 {"plugin":"file.non-empty","version":"1.0.0","official":true,"result":1},
 {"plugin":"format.is-pdf","version":"1.0.0","official":true,"result":0}]}
```

`result: null` with `"error": "plugin_error"` means the plugin gave no
answer: it is not a 0.

## Documentation

- [Usage](docs/usage.md): every command, profiles, the agent flow, MCP.
- [Security model](docs/security-model.md): what the agent can see (the exhaustive list), the
  boundaries, the residual risks.
- [Plugin contract](docs/plugin-contract.md): for plugin authors.

## Status

v0.2: console, agent CLI, MCP server and agent wiring on
[lockkit](https://github.com/DavidGodefroid/lockkit). The console needs Linux
(Landlock and seccomp sandbox the plugins); macOS gets the agent side only.
Not yet: an onboarding wizard (`setup`), a macOS console, public-key trust roots for
unofficial marketplaces.

## License

Apache-2.0, see [LICENSE](LICENSE).
