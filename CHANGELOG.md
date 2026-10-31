# Changelog

## Unreleased

The console is hub-attached only (DavidGodefroid/lockkit#110).

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
  there. Builds on lockkit's hub-only console (pseudo-version until its release).

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
