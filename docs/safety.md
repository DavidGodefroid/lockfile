# lockfile's safety model, in short

The console is the enforcement point. Skills, rules and MCP tool descriptions
only guide the agent; the console's checks are the guarantee. The threat
model in full: [security-model.md](security-model.md).

- **OS separation.** The console runs as the `lockfile` account in a
  container of its own; the agent's commands run as `agent` (group
  `lockfile-clients`) in others, which share only the socket volume. The
  socket in `/run/lockfile` checks the kernel peer credentials of every
  connection and refuses any other account (`--allowed-uids` or
  `ALLOWED_UIDS` names an agent in its own container). Whoever runs `docker`
  on the host stays outside this boundary: see
  [docker.md](docker.md#what-docker-does-and-does-not-separate).
- **Human approval.** Every plan is a ticket you answer on
  [lockhub](https://github.com/DavidGodefroid/lockhub), in `lox run` (or `lox
  approve`); the console's own decisions (its policy) stay at its terminal.
  No socket method approves, installs or loosens anything. An unofficial
  plugin is never auto-approved.
- **A closed output list.** Everything the console sends the agent is a
  plugin answer, an absence of answer, a random handle or plan id, signed
  plugin metadata, profile names, an approval outcome, a refusal code or the
  console state. Never size, media type, sha256, key listings, names, page or
  byte counts, library or OS error text, per-plugin timing or stderr. The
  end-to-end tests walk every response of every method against this list.
- **Sandboxed plugins.** Landlock (execute and read its own binary only; TCP
  denied with ABI ≥ 4), seccomp (no sockets, `ptrace`, `mount`, `bpf`,
  `keyctl`, `execveat`, `memfd_create`, System V IPC, POSIX message queues,
  signals to other processes), rlimits and a wall-clock timeout, an empty
  environment, the file as a read-only fd 3, stdout capped at 3 bytes,
  stderr discarded.
- **Signed everything.** Plugin binaries, manifests, the marketplace index
  and lockfile's releases are checked against keyless cosign signatures. A
  missing cosign refuses; there is no unsigned mode.
- **Bounded leakage.** `max_plugins_per_handle` (16) caps the answers about
  one fetched file, and each approval states how many a plan reveals.
  Results are released on `answer_quantum` (5 s) multiples.
- **Audit for the human only.** Key, size, sha256, fetch cause and each
  plugin's precise outcome go to an audit log only the console account can
  read (`lockfile audit`). Never file bytes.
- **Fetched files** live in a random `0700` workspace, deleted on `release`,
  after `handle_ttl` (30 min), on console exit, and swept at start.

Residual risks: a plugin whose code does not match its question is mitigated
by review and signing, not prevented; `plugin_error` and `fetch_failed` each
carry one symbol that depends on the file; deletion is an unlink, not a
secure erase.

> [!IMPORTANT]
> "The agent only ever sees 1s and 0s" is the design goal and what the wire
> carries. Plugins' `/proc` entries are a channel to any account sharing
> their pid namespace: the console refuses to run plugins unless no other
> account shares it (in Docker: never `--pid=host`), and older Landlock ABIs
> do not block `fcntl` signals (seccomp does).
