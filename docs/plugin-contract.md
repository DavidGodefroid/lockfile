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

- **No parameters.** With one bit of output, a parameter is the way to aim that bit at content
  ("does page 2 contain X?"). A variant is another plugin with its own question and review.
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
| stdin | `/dev/null` |
| Environment | empty except `LOCKFILE_PLUGIN_PROTOCOL=1`; cwd `/` |
| Filesystem | Landlock: execute and read its own binary only |
| Network | none (seccomp denies sockets; Landlock denies TCP with ABI ≥ 4) |
| Other syscalls denied | `ptrace`, `process_vm_*`, `mount`, `bpf`, `keyctl`, `perf_event_open`, a second `execve`; System V IPC (`shm*`, `msg*`, `sem*`) and POSIX message queues (`mq_*`) |
| Limits | CPU seconds, address space, no core, file size 0, few processes, wall-clock timeout then SIGKILL of the group |
| stderr | drained and discarded; only its byte count is audited |

## Answer

Write exactly `1` or `0` on stdout (one trailing `\n` tolerated) **and** exit with status 0.

Anything else is a failure: other or more bytes, nothing, a non-zero exit, a signal (including
`SIGSYS` from seccomp), the timeout, an rlimit. Every failure reaches the agent as one code,
`plugin_error` with `result: null`, which is **not** a 0. The precise cause goes to the audit
only. Exit-code-only answers are not accepted: a panic or `log.Fatal` must never read as an
answer.

The marketplace's `sdk/` module gives the fd-3 reader, `sdk.Answer(bool)`, `sdk.Fail()` and a
test harness that runs a plugin under the same sandbox on synthetic fixtures.

## Publishing

Official plugins go through the
[lockfile-marketplace](https://github.com/DavidGodefroid/lockfile-marketplace) pipeline: PR
with synthetic fixtures for both answers, CI (vet, staticcheck, govulncheck, contract tests
under the sandbox, fuzzing, an import check), a maintainer's review against `REVIEW.md`, then
a tag `<name>/vX.Y.Z` whose release workflow builds, hashes and signs (cosign keyless) the
binaries and the manifest, and a signed `index.json`.

An unofficial marketplace publishes the same index format, signed keyless by its own CI
identity; its users add it with `lockfile marketplace add`.
