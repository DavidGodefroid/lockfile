# lockfile security model

**Rule: the agent only ever sees 1s and 0s about a file.** The human approves every plan in a
console the agent cannot drive, in an account the agent cannot read.

## Deployment

lockfile is deployed with Docker ([docker.md](docker.md)): the console in one container as the
account `lockfile`, the agent's commands in others as `agent` or an `ALLOWED_UIDS` uid; they
share only the socket volume. Other deployment modes exist in the code but are no longer
documented.

## Preconditions

- **Landlock and seccomp.** The console container runs under the shipped `seccomp.json`
  (Docker's default profile, which allows the sandbox's system calls), never
  `seccomp=unconfined`. Without Landlock or seccomp the console refuses to run plugins and
  `lockfile doctor` fails: there is no unsandboxed run.
- **No other account sees the plugins' processes.** While a plugin runs, its `/proc` entries
  (command line, thread names and count, memory counters) are written by the plugin itself and
  are readable by every account that shares its pid namespace or an ancestor of it: the agent
  could poll them and read file content out of them. seccomp cannot stop a process from writing
  its own entries. On a host this needs `/proc` mounted with `hidepid=invisible`, which Docker
  cannot do. The image declares a container of its own (`/etc/lockfile/container.toml`, owned
  by root, not writable by others), and the console accepts a plain `/proc` only if, at start,
  when a plan arrives and just before it runs, that declaration holds, its pid namespace is not the host's and
  `/proc` is that namespace's, no listed process runs as an account other than root and
  `lockfile`, and the console runs under a seccomp filter (refusing `seccomp=unconfined`). Anything else is refused with `proc_not_hidden`, and `lockfile doctor` fails
  naming the condition. Residual: the host's pid namespace lists every container's processes,
  so the host's accounts read them unless the host's own `/proc` has `hidepid=invisible`;
  whoever has Docker on the host is outside the boundary anyway (below). Never `--pid=host` or
  `--pid=container:…` for the console, nor `--pid=container:<console>` for an agent.

## Boundaries

| Boundary | Enforced by |
|---|---|
| The agent cannot read the file, the credentials, the plugins or the audit | they are in the console's container and its home volume (`lockfile-home`, home 0700, account `lockfile`); the agent's containers mount only the socket volume; local sources are mounted read-only under `/srv/lockfile` (0700, `lockfile`) in the console's container only |
| The agent cannot drive the console | the console's terminal is the one you started `lockfile console` in (the launcher's `docker run -it`); lockkit's isolation checks run inside the container (no relay, the terminal belongs to the console account, X11 refused by `system.toml`). Residual: whoever runs `docker` commands can `attach` or `exec` into it ([docker.md](docker.md#what-docker-does-and-does-not-separate)) |
| Only the clients group reaches the console | socket in the `lockfile-run` volume at `/run/lockfile` (2750, setgid group `lockfile-clients`), socket 0660, kernel peer credentials checked on every connection: a peer that is neither `lockfile`, nor in `lockfile-clients`, nor in `allowed_uids` (`ALLOWED_UIDS` at build time) is refused |
| The agent cannot replace the binary the console runs | `/usr/local/bin/lockfile`, `/usr/local/bin/cosign` and `/etc/lockfile/*` owned by root in the image, which has no `sudo`; base images pinned by digest, cosign and the Vault CLI by sha256 |
| A plugin cannot read anything but its input, nor talk to the network | Landlock (execute + read on its own binary only; TCP denied with ABI ≥ 4), seccomp (no sockets, ptrace, mount, bpf, keyctl, second execve, System V IPC, POSIX message queues, signals to other processes including through a descriptor owner, shared anonymous memory), rlimits (CPU, data and address space, a task cap relative to the account's tasks), empty environment, input as a read-only fd 3, stdout capped at 3 bytes, stderr discarded |
| A plugin answers only its signed question | the manifest's question/1/0 are signed with the binary; marketplace review; a changed question is a loosening the human types to accept; a plan runs only the version and hashes it was approved with, checked on the opened binary (an update in between answers `plugin_tampered`) |
| A withdrawn version does not run | the yanked flag of the signed index, recorded in `plugins.lock` by `plugin install`/`update`/`verify`; the store refuses to open a yanked version (`plugin_yanked`); `lockfile doctor` reports the recorded ones. Residual: a version yanked upstream keeps running until one of those commands reads the index |
| Plugins and releases are what their publisher built | cosign keyless signatures checked through lockkit `update` (index, manifest, binary, lockfile releases); no unsigned mode; a missing cosign refuses |
| The agent cannot widen the policy | no IPC method installs, trusts or loosens anything; `change.request` only queues text for the human |

## What the agent can see: the exhaustive list

Everything the console sends to the client (socket, CLI, MCP) is one of these, from closed
lists. `test/e2e` walks every response of every method against this list, and the CLI and MCP
tests check their text, JSON, structured content and progress output against the same
whitelist (`internal/testenv`): any key, value or word outside it fails.

| Output | Why it is not file information |
|---|---|
| plugin answer `1` / `0` | the one allowed thing |
| `result: null` + `plugin_error` | absence of an answer; **residual**: it depends on the file (a plugin crashed on it); at most one symbol per plugin, capped by `max_plugins_per_handle` |
| `fetch_failed` | the plan could not start; **residual**: says "this key could not be fetched" (missing, too big, denied, network are not distinguished) |
| handle `f_…`, plan id `pl_…` | random, from `crypto/rand` |
| plugin name, version, `official`, `warning: unofficial_marketplace`, question, meaning of 1/0 | plugin metadata from the signed manifest |
| profile names and `production` flag (`sources`), `busy` | lockfile configuration; `busy` = the console did not answer the probe in time because it serves another request (a plan waiting for the human, or a run until its quantum) |
| hub mode | a plan's ticket carries a masked screen and a plan hash over the content's sha256, key or handle, plugins and pins; a decision runs only verified under the pinned hub key, for this instance, matching the ticket, the policy unchanged, once (nonce), for the requesting agent's token; a fetch failure at plan time is told only after an approval and the plan answer is quantized, so planning is no existence or size oracle; the instance key lives in the console account's home; **residual**: the content is fetched twice (plan, run): a change in between is refused `stale`, which tells that the file changed; an expired ticket is asked again as a new ticket for the same plan (re-checked: policy fingerprint, which must be unchanged, and the installed plugins; the content's sha256 is kept from the plan and checked when the run fetches again), whose changes name the plan inputs that moved, never their values; a console restarted without `--hub` withdraws its pending hub plans and refuses their tickets `withdrawn` before lockfile acts |
| `stale` (`E_STALE`) | the plan was approved on a content hash (hub mode) and the fetched bytes differ; **residual**: says "this file changed since the approval", never how; no plugin ran on the new bytes, and the spool is released |
| approval outcome (`approval_denied`, `approval_timeout`) | a human decision; the reason the human types is never forwarded |
| refusals `key_outside_prefix`, `too_many_plugins`, `unknown_profile`, `plugin_not_installed`, `plugin_tampered`, `plugin_yanked`, `sandbox_unavailable`, `proc_not_hidden`, `handle_expired` (as `E_*` codes with a static hint) | computed from the request and the local install, before or without reading the file |
| `status` (console up, production, auto mode, security level, idle/session timers, plugin protocol, sandbox ok, approved `answer_quantum`) | console state and configuration |

Never sent: size, media type, sha256, key listings, file or path names, page or byte counts,
library or OS error text, per-plugin timing, progress during a run (the MCP server's run
heartbeat is a constant text, paced by the client on the quantum, never by the console), stderr. The client also refuses to forward any console text it
does not recognise: an unknown error becomes `console error`, and so does a successful result
with a value outside its shape (plan id, handle, plugin name, version, marketplace, manifest
text with control characters, sandbox state, a status for another profile).

## Side channels

- **Timing.** A run's response leaves the console only at the next multiple of
  `answer_quantum` (default 5 s) after the run started; a fetch failure after approval too, and
  `release` (deleting a larger file takes longer), refusal included.
  Residual: a plan that takes longer than one quantum shows it took longer (one more multiple).
- **The console window.** At desktop security level 1 the agent's account can read the console's
  window. It shows the agent's own request, the plugin metadata and the outcome codes; causes,
  sizes and hashes go to the audit only. Error text bound for the window or the audit is scrubbed
of URLs, paths, hosts, key ids and of the profile's own bucket, endpoint, host, user, base
directory and paths; a few lockkit messages are not yet passed through lockfile's scrubber
(they carry configuration, never file data). Residual: the start-up lines show the profile's
  location (bucket and prefix, host and base directory, or path), and **a reason the human types
  with a denial (`n <reason>`) is echoed in the window** by lockkit: do not type file details there.
- **Exit codes** depend on the outcome only, never on an answer.
- **Plugin IPC.** System V shared memory, message queues and semaphores, and POSIX message
  queues, outlive the plugin and are reachable by every account in the same IPC namespace;
  Landlock does not cover them. seccomp refuses every one of these system calls (`EPERM`). The
  plugin stays in the host IPC namespace: an unprivileged IPC namespace needs a user namespace,
  which Ubuntu 24.04+ restricts through AppArmor by default and which would hand the plugin
  in-namespace capabilities; the seccomp denial is the control.

## What the human approves

The approval screen shows the key the console will fetch: percent-decoded once and cleaned
(`invoices/./%61.pdf` is shown as `key    invoices/a.pdf`), followed by the text the agent
typed on a `typed` line whenever the two differ.

## Release and marketplace signers

The keyless identities lockfile trusts are "the `release.yml` workflow of this repository, run
on a tag": the lockfile repository for the launcher, its seccomp profile and its images
(`lockfile update`, `scripts/install.sh`, bound to the exact `vX.Y.Z` tag) and the marketplace repository for its index and plugins. Whoever can
push such a tag can therefore have an arbitrary commit, with a `release.yml` of their choice,
signed under a trusted identity. Both repositories **must** keep these settings:

- a tag ruleset restricting creation, update and deletion of `v*` (lockfile) and `*/v*`
  (marketplace plugin tags) to maintainers, with no bypass for other roles;
- a protected `main` (reviewed pull requests only).

lockfile's `release.yml` also refuses to build or sign a tag whose commit is not on `main`
(the `on-main` job). That check lives in the tagged commit's own workflow, so it complements
the tag ruleset and does not replace it.

## Data at rest

- Fetched files: `work/<random>/` (0700) in the console account, deleted on `release`, on handle
  expiry, on console exit, and swept at start. Deletion is an unlink, not a secure erase
  (residual on SSDs and copy-on-write filesystems). The directory (`work_dir`), the total cap
  (`max_workspace_bytes`) and the TTL (`handle_ttl`, fixed per handle at fetch) are read from
  the approved policy only, never from an unreviewed config edit. A fetch reserves its share of
  the cap before streaming (concurrent fetches never exceed it together), and at most 64 files
  are held or being fetched at once.
- The S3 SDK keeps the secret access key in the console's memory for the session; core dumps
  are disabled.
- The audit log (`~lockfile/.local/state/lockfile/audit.log`, 0600) is the one place file
  metadata exists: key, size, sha256, fetch cause, each plugin's precise outcome, stderr byte
  count. Never file bytes. `lockfile audit` reads it, in the console account only.

## Residual risks

- A plugin whose code does not match its question (lies, or answers a content question under a
  structural name): mitigated by review, build from reviewed source, signing, and the
  unofficial-marketplace warnings; not prevented.
- Many plugins over one file combine bits: bounded by `max_plugins_per_handle` and by the
  per-plan approval that states how many answers the plan reveals.
- A question can itself be about content ("is the amount above 10 000?"): the marketplace's
  review rule refuses such questions; an unofficial marketplace may not.
- `fetch_failed` and `plugin_error` each carry one symbol that depends on the file.
- Plugin resource caps are availability limits, not isolation: the task cap is `RLIMIT_NPROC`
  set to the account's current task count (measured from the kernel) plus 512, so it is shared
  with whatever else the account starts meanwhile, and root ignores it (the console never runs
  as root). Memory is bounded by `RLIMIT_DATA` and `RLIMIT_AS` (limit + 1 GiB).
