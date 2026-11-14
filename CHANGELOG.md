# Changelog

## Unreleased

## v0.10.0 (2026-11-14)

Builds on lockkit v0.20.0 (from v0.19.0).

### Breaking

- Agents that do not run confined are refused by default (lockkit v0.20.0,
  DavidGodefroid/lockkit#133): the console refuses at `hello`, with `CodeUnconfined` (-32009),
  an agent whose client reports it does not run confined (outside a container, with a host
  runtime socket, Lock Family state or a writable `.lockfamily/`) or sends no report (a client
  older than this release), and audits it `refused-unconfined`. The new profile key
  `unconfined = true` lets such an agent in on a non-production profile (a loosening, reviewed at
  `:review`; red banner; `"unconfined":true` on its audit records, `flags.unconfined` on its hub
  tickets); a production profile with it does not start. Ship the client and the console of
  the same version: an older console refuses the new `hello`
  ([configuration.md](docs/configuration.md#configuring-lockfile), DavidGodefroid/lockfile#83).

### Fixed

- `lockfile sources` and profile resolution sent their console probe's `hello` without a
  confinement report, which a lockkit v0.20.0 console refuses as `old_client`: it now sends the
  client's report (DavidGodefroid/lockfile#83).

## v0.9.1 (2026-11-13)

### Fixed

- A Go plugin whose manifest memory was between about 80 and 176 MiB (128 MiB, say) died at
  start (exit 2): the sandbox's address-space limit (`RLIMIT_AS` = memory + 1 GiB, since v0.3.0)
  fell in a range where the Go runtime cannot reserve its heap. The address-space headroom is
  now 4 GiB; the memory a plugin can use is still bounded by `RLIMIT_DATA` = `memory`, and the
  main-thread stack, which `RLIMIT_DATA` does not count, is now fixed at 8 MiB
  (DavidGodefroid/lockfile#76).

## v0.9.0 (2026-11-13)

Builds on lockkit v0.19.0 (from v0.14.1).

### Breaking

- lockkit v0.18.0 (DavidGodefroid/lockkit#120): the status answer no longer carries
  `auto_approve`, so `status` and `sources` no longer print `auto`: lockhub decides. A client
  that reads `auto_approve` gets false.
- lockkit v0.18.0 (DavidGodefroid/lockkit#120): a config value that embeds a bearer token, an
  `Authorization:` header or a URL with userinfo (`https://TOKEN@host`) is refused as a secret
  ("looks like it embeds a secret (a password or a token)").
- With a managed configuration (`/etc/lockfamily/managed.toml`), a profile beyond its
  `[lockfile.bounds]`, or a profile of yours named as a managed one, is refused and the console
  exits 3 (see Added). Without the file, nothing changes.
- Plain `http://` S3 endpoints are refused on a production profile, to `localhost` as well,
  which was accepted before (DavidGodefroid/lockfile#62).

### Behaviour

- **Hub ticket limits**: the console opens at most 30 tickets a human decides per 5 minutes
  (was 10) and 30 pending ones (was 20); a ticket `lox run`'s auto mode decides leaves that
  count, and a safety cap of 300 tickets per window counts every one. The profile's
  `tickets_per_window`, `ticket_window` and `max_pending` set those limits; more tickets, a
  shorter window or more pending tickets is a loosening, reviewed like the others (the typed
  profile name on production), a lowering applies at once. The status screen shows the limits
  in force (DavidGodefroid/lockkit#150).
- lockkit v0.19.0: the hub screen's masking of object keys catches more phone formats and
  e-mail addresses with an apostrophe.
- **Launcher**: it mounts the machine's managed directory (`/etc/lockfamily`,
  `/Library/Application Support/lockfamily` on macOS) read-only on `/etc/lockfamily` in its
  containers, as `lox run` does in its locks: `lockfile managed show`, `managed check`, `doctor`,
  `profile`, `init` and the other commands run from the host see the company's managed
  configuration. No directory: no mount; a file, a dangling link or a `:` in the path stops the
  launcher (DavidGodefroid/lockfile#69).

### Added

- **Managed configuration**: lockfile enforces the `[lockfile]` section of the company's
  `managed.toml` (lockfamily#31): `[lockfile.profiles.<name>]` served as written (a profile of
  yours with that name is refused), `[lockfile.defaults]` for what your config leaves unset, and
  `[lockfile.bounds]` you may tighten, never loosen: production, source, hosts, buckets,
  credentials, timers, limits, `max_workspace_bytes` and marketplaces; `[family] vault.required`
  allows only Vault credentials. The console refuses a policy beyond them at start and at every
  poll, with no prompt, citing the managed key (`blocked by company policy: …`), and exits 3.
  New `lockfile managed show` and `lockfile managed check [--profile NAME]` (the check `lox run`
  runs before it starts the lock), and a `managed` line in `lockfile doctor`. See
  docs/configuration.md#managed-configuration (DavidGodefroid/lockfile#63).
- **Placeholders typed by the human** (plugin protocol 2): a plugin may declare up to 4 `inputs`
  (`text`, `email` or `digits`, at most 1024 bytes) named in its question as `${name}`. The
  agent binds them to placeholders (`--input 'pdf.contains.needle=${email}'`, MCP `inputs`); a
  literal is refused `E_LITERAL_INPUT`. The human types the value in `lox run` at approval (no
  echo, sealed in the decision to a one-use key of the ticket, never auto-approved); the
  console holds it in memory for its session and replays it on other files without a new
  prompt (`held_inputs` in the plan answer, `reused` and the replay count on the screen). A
  value that does not fit is refused `E_INPUT_INVALID`, never echoed. The plugin reads its
  values on a sealed read-only memfd, fd 6; each (plugin, placeholder) pair counts as one
  answer. The value never reaches the agent, the audit (which names the placeholders), the hub
  or the disk (DavidGodefroid/lockfile#9).
- **Human-only detail**: a protocol-2 plugin with `detail = true` may write up to 4 KiB on fd 7
  (`found on pages 4512, 12307`), printed escaped on the console window only while clear
  results are on (Ctrl-O, level 0); never to the agent, the audit or the hub.
- Plugins get `LOCKFILE_PLUGIN_PROTOCOL` set to their manifest's protocol; `status` reports
  plugin protocol 2.
- **Public-key trust roots** for unofficial marketplaces (DavidGodefroid/lockfile#68, lockkit
  v0.18.0's key mode): `marketplace add NAME --index URL --public-key cosign.pub` trusts a
  cosign public key (ECDSA P-256/P-384, Ed25519, RSA >= 2048) instead of a keyless identity,
  never both. The index, manifests and binaries are checked with `cosign verify-blob --key`, a
  Rekor entry required. `public_key` in the config holds the key inline (the base64 body of the
  PEM, on one line), so the config's approval covers it; it is checked at load, never quoted in
  an error, and shown as its SHA-256 fingerprint (`public_key_sha256` in JSON). A changed key
  is a loosening on `:review`.
- **Local S3 store under `lox run`**: an S3 `endpoint` may be plain `http://` to
  `host.docker.internal` (the host, as `lox run` maps it), as it was to `localhost`, so a MinIO
  or LocalStack without TLS on your machine works from the console's container; `lox run`'s
  allowlist reaches it. The policy review and the status screen show `WARNING the connection to
  the S3 endpoint HOST:PORT is NOT encrypted`, and `lockfile doctor` and `profile add` (both run
  by `lox add`) warn too. An endpoint on `localhost` or a loopback address is flagged the same
  way: under `lox run` it is the lock's own container, use `host.docker.internal`
  (DavidGodefroid/lockfile#62).

### Fixed

- **Several lockfile locks, one MCP server**: when lea mounts several lockfile locks and neither
  `--instance` nor `LOCKFILE_INSTANCE` chooses one, `lockfile mcp` (the one server `lockfile
  init` writes) and the agent commands no longer exit with `several lockfile locks`: they reach
  every lock, list the profiles of all of them, and route each call to the lock that serves its
  profile. `lockfile mcp` also starts when the instance cannot be resolved, and every tool,
  `lockfile_status` first, says why, instead of a closed connection
  (DavidGodefroid/lockfile#60).
- **Official marketplace unreachable**: a failed download of the official index or of its
  signature (an HTTP error such as 404, a host that does not answer) now says that the
  official marketplace is unreachable or not published and gives its URL, instead of a bare
  `HTTP 404`. A daily `official-index` workflow checks that the index answers and verifies
  against the pinned identity (lockfile#61).
- Tests: the thread-cap sandbox test no longer starves plugins started by other test binaries
  under the same uid (DavidGodefroid/lockfile#72).

## v0.8.1 (2026-11-07)

Builds on lockkit v0.14.1 (from v0.13.0). No behavior change: container labels, files
(`up.json`, `.lockfamily/`) and flags stay the same.

- **`lox run` replaces `lox up`** (deprecated since lockhub v0.10.0) in every user- and
  agent-facing text: the docs, the agent assets the binary wires (skill, `AGENTS.md`
  instructions, Cursor rule, MCP instructions), the launcher's help and errors, the Go
  messages (`console`, `doctor`, `profile add`) and lockkit's shared hints (`StartHint`,
  `ErrNoHub`). A wired agent now asks the user to run `lox run`
  (DavidGodefroid/lockfile#57).

## v0.8.0 (2026-11-06)

Builds on lockkit v0.13.0 (from v0.11.0); lockhub as v0.6.0. No lockfile code change; what
the console's users will see:

- **Production profiles: the profile name typed instead of `y`** for every widening the
  console decides itself: the policy review at start and a loosening at `:review` (`Type the
  profile name "prod" to apply them:`; turning `production` off is such a loosening) and
  `:unmask` (`Type the profile name "prod" to allow it:`). Outside production they still take
  `y`. When the start-up review was approved with the name, the second `Type the profile name
  "P" to continue:` prompt is no longer asked (DavidGodefroid/lockkit#8).
- **Idle timeout**: only the human's input and the backend's methods count as activity. An
  agent polling `status` (or `hello`, `pii.list`, `pii.add`, `change.request`) no longer keeps
  an unattended console open until `max_session` (DavidGodefroid/lockkit#10).
- **`pii.add` audited with the peer**: the `tightened` record (and any `failed`/`unsaved`
  record of that change) carries `peer_uid`, `peer_pid`, `peer_user` and the `pattern` added,
  so an agent's addition is told apart from a tightening made in the files
  (DavidGodefroid/lockkit#6).
- A policy change of the credentials mode or Vault path ends the console session, a
  tightening as well as an approved loosening, instead of keeping the old setting live
  (DavidGodefroid/lockkit#7).
- From lockkit v0.12.1: a plain restart of an approved, unchanged production profile no longer
  asks its name again; the console records the question it waits for next to its socket, so
  `lox attach` and lea show it.

## v0.7.0 (2026-11-01)

- Each release tag bumps the `lockfile` formula of the
  [Homebrew tap](https://github.com/DavidGodefroid/homebrew-tap)
  (`brew install davidgodefroid/tap/lockfile`): the release workflow's last job
  dispatches the bump once the release is signed
  (DavidGodefroid/lockfamily#8). It is skipped while the
  `TAP_DISPATCH_TOKEN` secret is not set.
- `--instance NAME`: reach one lock among several of this type (lea mounts each at
  /run/lockfile/NAME). Every agent-side command and `mcp` take it; it defaults to
  `LOCKFILE_INSTANCE`, then to the one lock listed in `LEA_LOCKFILE_INSTANCES`.
- `credentials = "vault"`: the console asks the Vault token at its start (masked), under
  `lox up` too.

## v0.6.0

The console is hub-attached only (DavidGodefroid/lockkit#110): `lox up` starts it, the
launcher reaches it through `.lockfamily/up.json` (lockhub#78), and the launcher verbs of
lockkit v0.10.0 (DavidGodefroid/lockkit#111). Builds on lockkit v0.10.0; lockhub v0.3.0 or
later.

- **The launcher verbs of lockkit v0.10.0** (DavidGodefroid/lockkit#111), the same in every
  lock, which `lox init` drives ([docs/usage.md](docs/usage.md#profiles-console-account)):
  - `lockfile profile add [--non-interactive] [--json] [--project DIR] [--profile NAME]
    [--production] [--credentials ask|vault [--vault-path P]] --source local|s3|sftp <source
    flags>`. Breaking: the profile name is `--profile NAME` (`default` without it), no longer a
    positional argument, which is refused, as any positional argument is; `profile add` is the
    only `profile` command. On a terminal it asks for what the flags did not give (the
    prompts on stderr); with `--non-interactive`, or without a terminal, a missing value is a
    usage error naming its flag. `--credentials` takes `ask` or `vault` (`keychain`, `none`
    and anything else are refused; the interactive form still offers `none` for an SFTP key
    without a passphrase); a `local` source takes no `--credentials`. Its arguments go
    through `verbs.Parse`: a flag named like a secret, a value that embeds a password and a
    control character are refused and never quoted. A profile already in the file is never
    rewritten (`exists`). `--json` prints one line, `verbs.Profile` or `verbs.Error` (codes
    `usage`, `secret`, `exists`, `failed`). `--project DIR` writes that workspace's
    `.lockfile/config.toml` (the launcher passes it on as an absolute path).
  - `lockfile doctor [--fix [--yes]] [--json]`: `--fix` asks before wiring each agent unless
    `--yes`, and refuses without a terminal and without `--yes`; `--yes` without `--fix` is a
    usage error. `--json` prints one `verbs.Doctor` (`ok`, `fixed`, `checks` with `fix`)
    instead of the array of findings, or a `verbs.Error` when doctor could not check.
  - `lockfile init <agent>`: the agent is required (no default `claude`).
- lockkit v0.10.0.

- **The profile and the plugins reach the console `lox up` runs** (#44). `lockfile profile add`
  and `lockfile marketplace add|remove|list` edit the workspace's `.lockfile/config.toml`, as
  you (the launcher mounts only `.lockfile/`, made `0700`; the file `0600`), where `lox up`
  reads the lock's profile, reviews it and copies it read-only to the console's
  `~/.config/lockfile`; a `.lockfile` or `config.toml` that is a link, someone else's, or that
  others may write is refused, as `lox up` would. The workspace is the one the agent commands
  reach (`.lockfamily/up.json`), else the nearest `.lockfamily/`, else the current directory.
  `lockfile plugin`, `audit`, `forget` and `doctor` run with the home of the console `lox up`
  runs (`lockfamily-<hash>_<instance>-home`) and its profile copy, read-only, once Docker shows
  both are lox up's (compose's labels), with `--instance NAME` for one of several; without
  `lox up`, `plugin`, `audit` and `forget` exit 2 and say why. The fixed `lockfile-home`
  volume is no longer used by them ([docs/docker.md](docs/docker.md#the-console-of-lox-run)).
  The quick start installs the plugins after `lox up`.
- The agent commands no longer hang when the current directory was removed: they exit 2.
- **The agent commands reach the console `lox up` runs** (lockhub#78). The launcher's `mcp`,
  `status`, `sources`, `plugins`, `plan`, `run`, `answers`, `release` and `doctor` mount the
  socket volume of the workspace's lockfile lock that `lox up` names in `.lockfamily/up.json`,
  read-only, once Docker confirms it is lox up's (the checks of lea; no container lox up did not
  start may mount it read-write), and no longer the fixed `lockfile-run`: without `lox up` they
  exit 2 and say to run it (`doctor` runs without a socket and says why). With several lockfile
  locks, `--instance NAME` picks one (in `.mcp.json`: `"args": ["mcp", "--instance", "NAME"]`).
  `.mcp.json` is unchanged ([docs/docker.md](docs/docker.md#the-socket-of-lox-runs-lock)).
- `lockfile console` is launched by `lox up` only: `--hub`, `--hub-key-file` and `--instance`
  are required, and without them (or with only some) it refuses before the config, the
  plugin store or the terminal is touched ("the console is launched by `lox up` only", exit
  64). `--auto` and auto mode (`:mode`, Shift+Tab) are gone: plans are decided on lockhub,
  the console's own decisions (its policy) stay at its terminal. The standalone start that
  withdrew the plans hub mode had left pending is gone too.
- A run by plan id answers the fixed text `in hub mode, run the ticket file.plan answered`
  (`rpc.MsgRunTicket`), now in the closed list with the pending answer (ticket, lock,
  instance) (docs/security-model.md).
- The quick start (README, docs, site) goes through `lox add lockfile <instance>`, `lox up`,
  `lox attach` and `lox approve`; the profile and plugin setup steps are unchanged.
- Tests: every test console (`internal/testenv/consoletest`, the CLI's, the MCP server's,
  `test/e2e`, `test/docker`) runs attached to a fake lockhub (hubtest) and approves its plans
  there. Builds on lockkit's hub-only console.

lockkit v0.9.1 (from v0.9.0): a console is started by `lox up` only, and the agent's hints say so.

- The no-console text (`lockfile_sources`, `lockfile status` and `lockfile_status` with no
  console running, a call without a profile when none runs) ends with lockkit's `client.StartHint`, "the console is started by
  `lox up` only: ask the user to run `lox up`", no longer `lockfile console --profile NAME`.
- The `unknown_profile` hint no longer names `lockfile console --profile P`, which refuses to
  start. It covers both causes: "if profile P exists, its console is not running: ask the user
  to run `lox up`; otherwise use a profile listed by lockfile sources, or ask the user to set up
  profile P and declare it (lox add lockfile INSTANCE --profile P), then to run `lox up`" (P
  only when it is a valid profile name, else NAME).
- `clientcmd.StartCommand` is lockkit's, `lox up`, and takes no profile.
- The agent instructions `lockfile init` writes (skill, AGENTS.md, the Claude Code note) and the
  MCP instructions say plans are decided on lockhub (`lox approve`, lea's deck, a paired phone),
  answer `pending` with a ticket and run by that ticket, no longer that they are approved in the
  lockfile console. They, `lockfile doctor`'s config check and the security model (docs, site)
  say `lox up` where they said to start or use the `lockfile console` terminal.
- The quick start (README, docs/docker.md, site) declares the lock with its profile, `lox add
  lockfile samples --profile samples` (without `--profile`, `lox up` serves `default`), and
  `lockfile profile add` prints `lox add lockfile INSTANCE --profile P`.

## v0.5.3

lockkit v0.8.1: the hub URL is `http(s)://host[:port]` only, and lockfile prints lockkit's
hub errors as they are (no typed value in them).

- lockkit v0.8.1 (from v0.6.0). The `--hub` URL is `http(s)://host[:port]` only: a path
  (`https://h/lockhub`, which never reached a working hub: lockhub serves `/v1/` at its
  root), query, fragment, IPv6 zone, user or password, a host that is not a DNS name or an
  IP address, or a port outside 1-65535 is refused naming `--hub` and the allowed form,
  never the URL. The console's start line names the hub in that form, and a hub that
  cannot be reached is named by scheme and host, no longer by the whole request URL.
- lockkit's hub errors are printed as they are: lockkit v0.8.1 quotes no typed `--hub`
  URL, `--instance`, `--require-device` class or `hub-key` flag, so lockfile no longer
  replaces those values with `<value>` (the workaround of v0.5.2 is gone). The no-echo
  tests cover a token in the hub URL's userinfo, path, query, fragment, port and zone
  without it. One case remains, by design: a hub host that is a valid DNS name
  (`http://some_name`) is shown as the hub, in the plain-http refusal and on the console's
  start line, since a host cannot be told apart from a token.
- The hub screen's personal data (`email`, `phone`, `iban`, `card`) is also found right
  after a JSON `\n`, `\t` or `\u00XX` escape, read as the blank or delimiter it stands for
  (lockkit v0.8.0): a key such as `{"msg":"tel:\t0475123456"}` is now masked. lockfile
  implements none of lockkit v0.7.0's console product hooks and turns on none of v0.8.0's
  new detectors, so the console is otherwise unchanged.

## v0.5.2

Errors never quote a typed config or CLI value (a token pasted in the wrong key, a URL
carrying one).

- Errors no longer quote a value typed in the config or on the command line: a pasted
  token or a URL with one (userinfo, path, query) never reaches the terminal, a log or an
  agent. A download error names the marketplace and the file (`marketplace acme: index:
  download: HTTP 404`, `... connection refused`), not the URL, and keeps only the kind of a
  transport error (its host and address are dropped); an invalid plugin name or version,
  marketplace name (`--marketplace`, `marketplace remove`), `--since`, source kind, size, or
  manifest name, version or media type, `--allowed-uids`, an unknown command or agent
  (`lockfile init`), or a profile name (`profile add`, `forget --profile`, `console
  --profile`) is refused naming the key and what is allowed. A plugin or marketplace name
  is quoted only once it is checked to be one, and the `unknown_profile` hint names the
  profile only when it is a valid profile name. Errors of lockkit v0.6.0 that quote a flag
  value (`--hub URL`, `--instance`, `--require-device`, `hub-key --instance`) are printed
  with that value replaced by `<value>`, and lockkit's `invalid profile name %q` and
  `unknown agent %q` are not reached (lockfile checks first).
- A marketplace `index_url` with a query or fragment is refused, as one with userinfo was:
  a token there would be shown by `marketplace add` and `marketplace list` (and the
  signature is fetched at `index_url` + `.sigstore.json`).

## v0.5.1

lea's agents reach the release image's console: `console --allowed-uids` (lea#226).

- The `ALLOWED_UIDS` build argument refuses root (0), leading zeros, the console's uid
  (10031) and 65534 (nobody), as lockmq's does (it took `0`, which served root peers).
- `lockfile doctor` as a uid with no account here (an agent in a container of its own)
  warns that a console serves it only when started with `--allowed-uids`, instead of failing.
- `lockfile console --allowed-uids UID,...` (lea#226): these uids may connect besides the
  members of `lockfile-clients`, as `allowed_uids` of the image's `system.toml` (the
  `ALLOWED_UIDS` build argument) does, for that console only, with the release image
  unchanged: an agent in a container of its own whose uid has no account in the console's
  image (lea's agents run as the host user's uid). Set only by whoever starts the console
  (the human: the launcher passes it on, `lock up` of lockhub v0.2.4 a lock's reviewed
  `allowed_uids`); root, leading zeros, 65534 (nobody) and the console's own uid are refused; every isolation check sees the uids (a session or
  terminal of one is the agent's), and the console prints them at start.

## v0.5.0

Hub mode on lockkit v0.6.0: expired tickets are approved again, reveal from a local
facade, and a console restarted without the hub withdraws its hub plans.

- Hub mode, renewals (#31): a plan whose object could not be fetched (`unfetched`, no
  `sha256` input) is renewed with `sha256` `unchecked` too, never shown checked and
  unchanged. A run whose expired ticket cannot be asked again prints and audits the cause
  (`reason`, lockkit's `hubclient.AnswerCode`: e.g. `policy_changed`, `recheck_failed`,
  `not_renewable`; the agent still hears the outcome only); a renewal stored but not sent
  to the hub yet says so. `docs/usage.md` lists `unfetched` among the plan inputs. New end-to-end tests: an unfetched plan's renewal, a
  renewal whose content changes before its approval (`stale`), a policy tightened on disk
  (`policy_changed`).
- `lockfile console`'s usage and help list `--hub-plain-http` (the flag `lock up` passes for
  its `http://lockhub:8443`); `docs/usage.md` says when it is needed.
- Site: the plugin sandbox hardening the site still called "coming in v0.2.1" shipped in
  v0.3.0 (System V IPC, POSIX message queues and signals to other processes denied; `/proc`
  hidden from other accounts, or a private pid namespace); the security page says so, the FAQ
  no longer lists it.
- Docs and site: no stale version claims left (the `v0.2.x` pre-release banners and footers,
  `plugin_yanked` "not produced yet" — it is since v0.3.0 —, "keyless signers only in v0.2",
  "lockkit v0.2.1" in the public-key refusal messages); the README status mentions hub mode.
  The seccomp lists name `execveat` and `memfd_create` in place of a "second `execve`" that no
  filter denies (Landlock lets only the plugin's own binary execute).
- Builds on lockkit v0.6.0 (was v0.3.1). Hub mode: a ticket lockkit reports expired
  (`ticket.ErrExpired`: an approval the console can no longer use) is no longer answered
  `approval_timeout`:
  the run asks it again (`console.Hub.RenewResp`) and the agent hears `pending` with a new
  ticket that renews the old one. Plans record their plan-hash inputs
  (`Binding.Inputs`: `profile`, `key`, `handle`, `sha256`, `plugins`, `pins`, `unfetched`)
  and the console recomputes them for a renewal (`SetRecompute`, the run's recheck: policy in
  force and installed plugins; the content's sha256 is `ticket.UncheckedInput`, named
  `unchecked` on the renewal and never one click, with the stored plan hash: the run checks it
  when it fetches again), so the human sees which moved. The recompute is given to the hub
  before any request (`console.Options.OnHub`) and reads the policy in force as the console
  saved it: a renewal asked from the hub right after a start is checked too. Renewals by the agent are audited
  (`renew`); reveals and renewals asked from the hub are audited by lockkit. lockfile's
  tickets are `read_pii`, which lockkit never ages (no TTL): the path is reached by a decision
  the console's clock refuses as expired.
- Reveal (Ctrl-O on a local facade) and `--facade-keys-file` come with `lockfile console`'s hub
  flags; a console restarted without `--hub` withdraws its pending hub plans and refuses
  their tickets `withdrawn` (tested end to end).

## v0.4.0

Hub mode: plans can be decided on lockhub instead of the console's own prompt.

### Hub mode

- `lockfile console --hub URL --hub-key-file FILE --instance NAME`: plans are decided on
  lockhub. `file.plan` fetches and hashes the content at once, stores the plan (content
  sha256, key or handle, plugins, pins, policy) and pushes a signed ticket with a masked
  screen; the agent hears `pending` and runs the ticket once decided (`file.run` with
  `ticket`, `lockfile run TICKET`, `lockfile_run`). The run verifies the decision, the
  policy and the plugins, fetches again and refuses changed bytes `stale` before any
  plugin. Decisions are audited with the ticket, approver, facade and hub signature.
  `lockfile hub-key --instance NAME` prints the instance key to pin. Plan and run carry the
  agent's lea name and the hash of its token (`requester`; the raw token stays in the
  agent's process, lockkit#71).
- Builds on lockkit v0.3.1 (was v0.2.1). An `http://` hub other than a
  loopback address needs `--hub-plain-http` and must be a service name without a dot or a
  private address; redirects from the hub are refused and the hub state file must be private
  (lockkit#72).

### Plans bound to a content hash (groundwork for hub mode)

- A plan can carry the sha256 of the bytes it was approved on. The run spools the object
  into the console's workspace and hashes the bytes it writes; when they differ the spool
  is released before any plugin runs and the plan is refused `stale` (`E_STALE`, retry
  rewrite: plan again). The plugins read only the spool, so the bytes they answer on are
  the hashed ones. Hub mode (above) sets it.

## v0.3.0

Docker-only: every command runs in the image through the `lockfile` launcher, the image
has its own account ids, and the host install code is gone. Upgrade from v0.2.x with
`scripts/install.sh` (it installs the launcher), then run `lockfile console` once: the
launcher moves the existing volumes to the new ids (docs/docker.md, Migrating).

### lockfile's own account ids

- The image's accounts move from locksql's ids to lockfile's own: group
  `lockfile-clients` 10000 → 10030, console `lockfile` 10001 → 10031, `agent`
  10002 → 10032, so that each Lock has its own.
- The launcher runs `lockfile migrate-ids` before `console`, `doctor`, `profile`,
  `plugin`, `marketplace`, `audit` and `forget`: in a throwaway container, as root with
  `CAP_CHOWN` and `CAP_DAC_READ_SEARCH` only, under the seccomp profile, with no network
  and a read-only root, it gives the files of `lockfile-run` and `lockfile-home` that
  hold an old id the new one, and leaves the others alone (idempotent). It refuses any
  account but root and any argument, and stays for one release.

**Breaking, migration:** volumes of a previous release are migrated by the launcher.
Without it, run `migrate-ids` once as docs/docker.md shows. An agent container that
reached the console with gid 10000 now needs gid 10030.
### The host install code is gone

- `cmd/lockfile/install.go`, the binary's host self-update and `internal/separate` are
  removed: the image refused them since the Docker-only launcher. `lockfile install` and
  `lockfile update` in the image still print the hint to the launcher (`scripts/install.sh`,
  `lockfile update` on the host, docs/docker.md).
- `lockfile doctor`'s separation checks no longer tell you to run `sudo lockfile install`:
  they point to the launcher and its image.

### lockkit v0.2.1

- lockfile now builds on lockkit v0.2.1 (was v0.1.0), the version locksql and lockhttp use.
  No API change affects lockfile: it does not show clear results, read Vault with a held
  token or use `pii`, so its behaviour is unchanged.

### One `lockfile` command, backed by Docker

Every lockfile command now runs in Docker, through one host command: the `lockfile`
launcher.

- `scripts/lockfile`, a POSIX shell launcher, runs each command in
  `ghcr.io/davidgodefroid/lockfile` with the account and volumes it needs: `console` as
  `lockfile`, one container per profile (`lockfile-console-<n>`, `LOCKFILE_CONSOLE_NAME`),
  under the shipped seccomp profile (it refuses to start without it), with no capability,
  `no-new-privileges`, a read-only root and a small `/tmp`, 512 processes, 4 GB, no log
  driver, `host.docker.internal` mapped, `VAULT_ADDR`/`VAULT_NAMESPACE` passed and
  `VAULT_CACERT`'s file mounted read-only; `profile`, `plugin`, `marketplace`, `audit`,
  `forget` and `doctor` as `lockfile` on `lockfile-home`; `init` and `doctor --fix` as you,
  with your home and the project; the agent's commands, `mcp` included, as `agent` with
  `lockfile-run` only. It never mounts the Docker socket, refuses paths with `:`, and
  splits `LOCKFILE_DOCKER_ARGS` without globbing (`local` sources are mounted with it).
- `scripts/install.sh` installs the launcher and `seccomp.json`
  (`/usr/local/share/lockfile`, `LOCKFILE_SHARE_DIR`), root-owned, after the cosign checks of
  `checksums.txt` and of the images against the release workflow run on that exact tag; the
  launcher runs the image by its verified digest (the two digests must differ). A newer
  installed launcher is not downgraded without `LOCKFILE_ALLOW_DOWNGRADE=1`.
- `lockfile version` prints the launcher's and the image's versions; `lockfile update
  [vX.Y.Z]` (an exact tag only) replaces the launcher and the profile the same way, then
  pulls the image.
- The release workflow builds the image (and its `-vault` variant) for amd64 and arm64 after
  the "tag is on main" guard, publishes it to ghcr.io and signs it with cosign. The archives
  ship the launcher and `seccomp.json`.
- The binary refuses `install` and `update` ("lockfile runs in Docker"). `lockfile init`
  writes `lockfile mcp`, which now works as is.
- CI drives the launcher against the built image, and `test/docker` runs the console as the
  launcher confines it.

**Breaking, migration:** `install.sh` installs the launcher in place of the binary.
Replace the `docker run … lockfile …` commands and `docker run … lockfile mcp` agent entries
of the former guide with `lockfile …` (the old ones keep working). A host install (`sudo
lockfile install`) leaves the `lockfile` account and group, `/etc/lockfile/`, the socket
directory and the console account's home behind: see
[docs/docker.md](docs/docker.md#migrating).

### Docker image

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
  that lists no process of an account other than root and the console's, with a seccomp filter
  on the console (`seccomp=unconfined` is refused); checked at start, when a plan arrives and
  just before it runs (the sandbox too), refused otherwise (`proc_not_hidden`, the failed
  condition named).
  `lockfile doctor` reports it.
- Documentation is Docker-only: `docs/docker.md` (new), the README quick start,
  `docs/usage.md` and `docs/security-model.md`. Other deployment modes (`lockfile install`, the
  session and desktop shapes) stay in the code but are no longer documented.
- CI: a `docker` job builds the image, runs the sandbox tests in a container under
  `seccomp.json`, and runs `test/docker` (console and agent containers; sandbox active; peer
  refused; `ALLOWED_UIDS`; no Landlock, `seccomp=unconfined`, `--pid=host` and an agent in the
  console's pid namespace refused).

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
