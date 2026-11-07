# How lockfile works

Two sides in two OS accounts. Only the console touches the file.

```
agent account (CLI / MCP)            lockfile account (console)
  lockfile plan/run/answers   ──UDS──▶  approve ─▶ fetch ─▶ workspace (0700)
           ▲                    JSON-RPC                       │ fd 3, read-only
           │                                                    ▼
           └────── 1 / 0 per plugin ◀── strict check ◀── sandboxed plugin process
```

1. The agent picks its questions from `lockfile plugins` (each lists its
   question and what 1 and 0 mean) and submits a plan with the exact key it
   was given: `lockfile plan --key K --plugin X…` (or the `lockfile_plan` MCP
   tool). There is no listing and no stat.
2. The console validates the request alone, without touching the source:
   profile, key under the profile's `prefix`, every plugin installed and
   verified, the plugin count within `max_plugins_per_handle`. It draws the
   approval screen and sends it to
   [lockhub](https://github.com/DavidGodefroid/lockhub) as a ticket, which
   waits for your answer in `lox run`:

   ```
   ╭─ SAMPLES · file plan · 2 answers ─────
   │ requested by uid 1000 (alice) · pid 48211 · exe /usr/local/bin/lockfile
   │ agent: check the invoice reported as blank
   │
   │ key    invoices/2026/10/abc123.pdf
   │
   │ file.non-empty@1.0.0  (official)
   │   ? Does the file contain at least one byte?   1 = size > 0   0 = the file is empty
   │ format.is-pdf@1.0.0  (official)
   │   ? Does the file start like a PDF?   1 = `%PDF-` appears within the first 1024 bytes   0 = no PDF header
   │
   │ this plan reveals at most 2 answers about the file
   ╰─ pending: ticket 01J…
   ```

3. On approval the console fetches the file into a fresh `0700` workspace,
   runs each plugin in its own sandbox, checks that it wrote exactly `1` or
   `0` and exited 0, and returns the answers together at the next multiple of
   `answer_quantum`, with a handle (`f_…`) for follow-up questions on the
   same file.

| Command | MCP tool | What |
|---|---|---|
| `lockfile status` | `lockfile_status` | the running consoles' state |
| `lockfile sources` | `lockfile_sources` | profiles of the running consoles (`busy`: waiting on the human) |
| `lockfile plugins` | `lockfile_plugins` | questions, meaning of 1 and 0 |
| `lockfile plan --key K --plugin X… [--run]` | `lockfile_plan` | ask the human to approve |
| `lockfile run PLAN_ID` | `lockfile_run` | run an approved plan once |
| `lockfile plan --handle H --plugin X… --run` | `lockfile_plan` + `lockfile_run` | follow-up on the same file |
| `lockfile answers HANDLE` | `lockfile_answers` | answers already given |
| `lockfile release HANDLE` | `lockfile_release` | delete the fetched file now |

Every agent command and `lockfile mcp` also take `--instance NAME`, to reach
one lock among several of this type that lea mounts (without it:
`$LOCKFILE_INSTANCE`, else the one lock in `$LEA_LOCKFILE_INSTANCES`).
`lockfile console` and `lockfile hub-key` take an `--instance` too, but that
one is the name lockhub gives the lock.

Exit codes depend on the outcome only, never on an answer: 0 answered, 1 no
console or error, 2 refused, 3 denied or no answer in time, 64 usage. Full
walkthrough: [usage.md](usage.md).
