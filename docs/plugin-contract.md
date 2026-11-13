# Plugin contract

A lockfile plugin answers **one yes/no question about one file**, fixed in its signed
manifest. The only thing that leaves it is `1` or `0`.

## Manifest (`plugin.toml`, signed with the binary)

```toml
name = "pdf.has-blank-page"
version = "1.0.0"
protocol = 1
question = "Does the PDF have at least one page that paints nothing visible?"
one  = "at least one page has no painting operator, or only full-page white fills"
zero = "every page paints something"
accepts = ["application/pdf"]
limits = { cpu_s = 20, memory = "512MiB", timeout_s = 30 }
```

- **No value chosen by the agent.** With one bit of output, a parameter the agent chooses is the
  way to aim that bit at content ("does the file contain `<guessed national number>`?"), or to
  read a file bit by bit ("is bit k 1?"). A variant is another plugin with its own question and
  review. The only values a plugin takes are **typed by the human** (protocol 2, below): the
  agent binds each input to a placeholder, never to a value of its own.
- **Inputs (protocol 2).** A `protocol = 2` manifest may declare at most 4 inputs, each named in
  the question as `${name}`, and nothing else in `${…}`:

  ```toml
  protocol = 2
  question = "Does the extracted text of the PDF contain ${needle}?"
  inputs = [{ name = "needle", kind = "text", max_bytes = 256 }]
  detail = true
  ```

  `name` is `[a-z0-9_]{1,32}`, `kind` one of `text`, `email`, `digits` (a closed list),
  `max_bytes` 1..1024. `inputs` or `detail` on a protocol-1 manifest is refused; every other
  unknown key still is. The agent plans with `"inputs": {"pdf.contains.needle": "${email}"}`;
  a literal (`"alice@example.org"`) is refused `E_LITERAL_INPUT`. The human types the value of
  `${email}` in `lox run` when approving (no echo); it travels to the lock sealed in the
  decision, to a one-use key of the ticket, and the console holds it in memory for the rest of
  its session, so the same `${email}` is replayed on other files without a new prompt. A value
  that does not fit its input's `kind` or `max_bytes` refuses the run `E_INPUT_INVALID`, never
  echoed. Each (plugin, distinct placeholder) pair counts as one answer in
  `max_plugins_per_handle`.
- **Detail (protocol 2).** With `detail = true` the plugin may write a human-only detail on fd 7
  (`found on pages 4512, 12307`). The agent still gets only `1`/`0`.
- **A value hard-coded in a plugin** (`contains "John Doe"?`) is a parameter in disguise and is
  refused at review; class detectors ("contains an IBAN-shaped string") stay allowed, and a
  content search is accepted only with an `inputs` slot.
- The question is about format, structure, integrity or rendering, **never about what the
  content means or says** ("is the addressee a woman?" and "is the amount above 10 000?" are
  refused at review).
- `accepts` documents the inputs; the console does not sniff the type, the plugin checks its
  own input.
- Changing `question`, `one` or `zero` needs a new major version; users type the plugin name to
  accept such an update.

## Runtime

| What | Value |
|---|---|
| Input | the file as an inherited **read-only fd 3**; no path |
| Typed values (protocol 2, with `inputs`) | **read-only fd 6**: a memfd sealed against writes, growth and shrinking, holding the canonical JSON of the plugin's inputs (`{"needle":"..."}`, keys sorted); never argv, env or a path |
| Detail (protocol 2, with `detail = true`) | **write fd 7**, a pipe, given on every run of such a plugin (a plugin without `detail` has no fd 7, and its Go runtime may use that number): the first 4 KiB are kept, the rest drained unread; printed escaped on the console window under `shown here only; the agent got 1/0`, **only when clear results are on** (Ctrl-O or `:results`, which lockkit refuses at desktop security levels 1 and 2), otherwise discarded; never to the agent, the audit or the hub |
| stdin | `/dev/null` |
| Environment | empty except `LOCKFILE_PLUGIN_PROTOCOL=<the manifest's protocol>`; cwd `/` |
| Filesystem | Landlock: execute and read its own binary only |
| Network | none (seccomp denies sockets; Landlock denies TCP with ABI ≥ 4) |
| Other syscalls denied | `ptrace`, `process_vm_*`, `mount`, `bpf`, `keyctl`, `perf_event_open`, `execveat` and `memfd_create` (only its own binary executes, by Landlock); System V IPC (`shm*`, `msg*`, `sem*`) and POSIX message queues (`mq_*`); signals or limits aimed at any process but itself (`kill`, `tgkill`, `prlimit64`, …, and a descriptor owner set with `fcntl` `F_SETOWN`/`F_SETOWN_EX`/`F_SETSIG` or `ioctl` `FIOSETOWN`/`SIOCSPGRP`); child processes (threads only); file metadata of any kind (`chmod`, `chown`, xattrs, times, `truncate`, inode flags through `ioctl` `FS_IOC_SETFLAGS`/`FS_IOC_FSSETXATTR` or `file_setattr`); other processes' memory layout (`move_pages`, `migrate_pages`, `get_robust_list`); `mmap` of shared anonymous memory or with `MAP_GROWSDOWN`. Denied calls fail with `EPERM` |
| Limits | CPU seconds; private writable memory (`RLIMIT_DATA` = `memory`) and address space (`RLIMIT_AS` = `memory` + 1 GiB, room for the Go runtime's reservations); no core; file size 0; threads: at most 512 tasks more than the console's account runs when the plugin starts (`RLIMIT_NPROC`, measured from the kernel's count; past it thread creation fails with `EAGAIN`); wall-clock timeout then SIGKILL of the group |
| stderr | drained and discarded; only its byte count is audited |

## Answer

Write exactly `1` or `0` on stdout (one trailing `\n` tolerated) **and** exit with status 0.

Anything else is a failure: other or more bytes, nothing, a non-zero exit, a signal (including
`SIGSYS` from seccomp), the timeout, an rlimit. Every failure reaches the agent as one code,
`plugin_error` with `result: null`, which is **not** a 0. The precise cause goes to the audit
only. Exit-code-only answers are not accepted: a panic or `log.Fatal` must never read as an
answer.

The marketplace's `sdk/` module gives the fd-3 reader, `sdk.Answer(bool)`, `sdk.Fail()` and a
test harness that runs a plugin under the same sandbox on synthetic fixtures (`sdk.Inputs()`,
the fd-6 reader, and `sdk.Detail(string)`, the capped fd-7 writer, come with protocol 2).

## Publishing

Official plugins go through the
[lockfile-marketplace](https://github.com/DavidGodefroid/lockfile-marketplace) pipeline: PR
with synthetic fixtures for both answers, CI (vet, staticcheck, govulncheck, contract tests
under the sandbox, fuzzing, an import check), a maintainer's review against `REVIEW.md`, then
a tag `<name>/vX.Y.Z` whose release workflow builds, hashes and signs (cosign keyless) the
binaries and the manifest, and a signed `index.json`.

An unofficial marketplace publishes the same index format, signed keyless by its own CI
identity; its users add it with `lockfile marketplace add`. Give them an identity pattern
that escapes every dot and pins the repository path, e.g.
`^https://github\.com/acme/lockfile-plugins/\.github/workflows/release\.yml@refs/tags/`:
`marketplace add` warns about an unescaped `.` or a path that other repositories also
match (`^https://github\.com/acme` matches `acme-evil` too).
