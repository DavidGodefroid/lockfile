# lockfile security model

**Rule: the agent only ever sees 1s and 0s about a file.** The human approves every plan in a
console the agent cannot drive, in an account the agent cannot read.

## Boundaries

| Boundary | Enforced by |
|---|---|
| The agent cannot read the file, the credentials, the plugins or the audit | a separate OS account `lockfile` (home 0700); `lockfile doctor` checks from the agent's side that opening it fails |
| The agent cannot drive the console | lockkit's isolation checks: the console runs in its own login session (or a desktop terminal of its own account, Wayland only), refuses X11 on production, refuses relays |
| Only the clients group reaches the console | socket in `/run/lockfile` (2750, setgid group `lockfile-clients`), socket 0660, kernel peer credentials checked on every connection |
| The agent cannot replace the binary the console runs | `/usr/local/bin/lockfile` owned by root; `install` refuses a user-writable source unless its sha256 is pinned |
| A plugin cannot read anything but its input, nor talk to the network | Landlock (execute + read on its own binary only; TCP denied with ABI ≥ 4), seccomp (no sockets, ptrace, mount, bpf, keyctl, second execve, System V IPC, POSIX message queues), rlimits, empty environment, input as a read-only fd 3, stdout capped at 3 bytes, stderr discarded |
| A plugin answers only its signed question | the manifest's question/1/0 are signed with the binary; marketplace review; a changed question is a loosening the human types to accept |
| Plugins and releases are what their publisher built | cosign keyless signatures checked through lockkit `update` (index, manifest, binary, lockfile releases); no unsigned mode; a missing cosign refuses |
| The agent cannot widen the policy | no IPC method installs, trusts or loosens anything; `change.request` only queues text for the human |

## What the agent can see: the exhaustive list

Everything the console sends to the client (socket, CLI, MCP) is one of these, from closed
lists. `test/e2e` walks every response of every method against this list.

| Output | Why it is not file information |
|---|---|
| plugin answer `1` / `0` | the one allowed thing |
| `result: null` + `plugin_error` | absence of an answer; **residual**: it depends on the file (a plugin crashed on it); at most one symbol per plugin, capped by `max_plugins_per_handle` |
| `fetch_failed` | the plan could not start; **residual**: says "this key could not be fetched" (missing, too big, denied, network are not distinguished) |
| handle `f_…`, plan id `pl_…` | random, from `crypto/rand` |
| plugin name, version, `official`, `warning: unofficial_marketplace`, question, meaning of 1/0 | plugin metadata from the signed manifest |
| profile names and `production` flag (`sources`), `busy` | lockfile configuration; `busy` = the console is waiting for the human on another request |
| approval outcome (`approval_denied`, `approval_timeout`) | a human decision; the reason the human types is never forwarded |
| refusals `key_outside_prefix`, `too_many_plugins`, `unknown_profile`, `plugin_not_installed`, `plugin_tampered`, `plugin_yanked`, `sandbox_unavailable`, `handle_expired` (as `E_*` codes with a static hint) | computed from the request and the local install, before or without reading the file |
| `status` (console up, production, auto mode, security level, idle/session timers, plugin protocol, sandbox ok) | console state |

Never sent: size, media type, sha256, key listings, file or path names, page or byte counts,
library or OS error text, per-plugin timing, progress during a run (the MCP server's run
heartbeat is a constant text), stderr. The client also refuses to forward any console text it
does not recognise: an unknown error becomes `console error`.

## Side channels

- **Timing.** A run's response leaves the console only at the next multiple of
  `answer_quantum` (default 5 s) after the run started; a fetch failure after approval too.
  Residual: a plan that takes longer than one quantum shows it took longer (one more multiple).
- **The console window.** At desktop security level 1 the agent's account can read the console's
  window. It shows the agent's own request, the plugin metadata and the outcome codes; causes,
  sizes and hashes go to the audit only. Residual: the start-up lines show the profile's
  location (bucket and prefix, host and base directory, or path), and **a reason the human types
  with a denial (`n <reason>`) is echoed in the window** by lockkit: do not type file details there.
- **Exit codes** depend on the outcome only, never on an answer.
- **Plugin IPC.** System V shared memory, message queues and semaphores, and POSIX message
  queues, outlive the plugin and are reachable by every account in the same IPC namespace;
  Landlock does not cover them. seccomp refuses every one of these system calls (`EPERM`). The
  plugin stays in the host IPC namespace: an unprivileged IPC namespace needs a user namespace,
  which Ubuntu 24.04+ restricts through AppArmor by default and which would hand the plugin
  in-namespace capabilities; the seccomp denial is the control.

## Data at rest

- Fetched files: `work/<random>/` (0700) in the console account, deleted on `release`, on handle
  expiry, on console exit, and swept at start. Deletion is an unlink, not a secure erase
  (residual on SSDs and copy-on-write filesystems).
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
