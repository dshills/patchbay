# deckctl contract

Phase 2 adds `internal/client` for Unix HTTP transport and extends `internal/cli`.
The transport depends only on the standard library and `pkg/protocol`; it does
not load configuration or import runtime services. These decisions were recorded
before implementation.

Prism review refinement: add read-only `GET /v1/actions/{name}` metadata lookup,
sharing the same effective action metadata as list discovery. This avoids copying
every action schema when encoding arguments for one invocation. Parameter writes
continue to validate against the active definition under the daemon transaction
lock; a schema change between discovery and writing may reject the request and
never triggers an automatic retry. Mutations use fresh HTTP connections to also
exclude the standard transport's safe zero-byte replay on stale pooled sockets.

## Commands and options

```text
deckctl status
deckctl project list|current|use <id>
deckctl context show|set <project|mode|values> <value>
deckctl action list|run <name> [--arg name=value ...] [--confirm] [--async] [--timeout 30s]
deckctl workflow list|run <name> [--confirm] [--async] [--timeout 30s]
deckctl job list|show <id>|cancel <id>
deckctl param list|get <name>|set <name> <value>
deckctl config validate [--config path]
deckctl config reload
```

Global options can appear before or after the command: `--socket` (default
`~/.deckd/deckd.sock`), `--json`, `--request-timeout` (default 5s per HTTP request),
and `--max-response-bytes` (default 128 MiB, configurable from 1 KiB to 1 GiB).
`--help` and `--version` require no daemon. Socket paths expand `~/` and resolve
relative to the current directory. `--` ends option parsing. Boolean flags may
use `=true` or `=false`; duplicate options are rejected except repeated `--arg`.
Run-only options are rejected for other commands. `--config` is only for offline
validation; reload always uses the connected daemon's configuration file.

Action arguments and parameter writes use discovered metadata to encode integer,
float, boolean, enum, and string values. Integers stay signed 64-bit values;
booleans must be `true` or `false`; floats must be finite. Strings and enum values
are literal command-line strings. The daemon remains authoritative for schemas,
bounds, permissions, current context, and configuration races. No schema failure
causes an automatic retry. Workflows accept no top-level arguments.

Context `project` uses the dedicated project-selection route; `mode` patches only
the mode; `values` takes a JSON object of strings and replaces the entire map
atomically. There is no client read/modify/write of individual context keys.
An empty project or mode clears it. `project current --json` returns
`{"project":"id"}` (empty string if no project is active).

## Execution and cancellation

Run commands submit asynchronously. By default they poll the job every 200 ms,
then return the final job snapshot. `--async` returns the admission response
immediately. `--timeout` is the daemon job timeout, rounded up to milliseconds;
it can only shorten a configured action timeout. Per-request timeouts limit
individual HTTP exchanges, not the complete job duration.

Ctrl-C/SIGTERM during a wait sends DELETE for that job using a fresh context with
a two-second total acknowledgement budget. The CLI exits 130 after cancellation
is requested, including when completion races with it. A failed acknowledgement
reports the job ID and explains that its outcome is unknown. An interrupted or
failed admission may already have executed work; the CLI never retries it and
asks the operator to inspect jobs when no ID was received. Other transport
failures while waiting leave the job running and report its ID.

## Output and exit codes

Human output shows project/context state, provider health, action input metadata,
parameter units, and job/workflow outcomes with bounded stdout/stderr and
truncation markers. Successful inspection of a failed job exits zero; a run that
waits for a failed job reports failure. `job cancel` returns the API's current
snapshot and succeeds when the cancellation request is accepted.

`--json` emits one JSON document to stdout: the corresponding API object/list,
the admitted `{"job_id":"..."}`, or the final job snapshot for a waiting run.
Errors without a snapshot use `{"error":{"code":"...","message":"..."}}`,
with `job_id` when known. Diagnostics and retry hints go to stderr. Help remains
text; version supports JSON. Raw transport errors and response bodies are not
copied into diagnostics.

| Exit | Meaning |
| ---: | --- |
| 0 | Success or successful resource inspection/cancellation request |
| 1 | Invalid configuration, internal error, or output failure |
| 2 | Invalid usage, value/schema error, or unknown resource |
| 3 | Transport/response error, unavailable provider, full queue, or daemon shutdown |
| 4 | Confirmation required or permission denied |
| 5 | Action/workflow execution failed |
| 124 | Job deadline or individual HTTP request timeout |
| 130 | Cancellation or interruption |

Responses are size-bounded and JSON numbers retain precision. Redirects are
rejected and transport never falls back to TCP or an HTTP proxy. Large job lists
can exceed the client response limit; reduce history/output retention, inspect a
single job, or explicitly increase the bounded client limit.
