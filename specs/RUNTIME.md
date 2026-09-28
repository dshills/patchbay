# Phase 1 runtime decisions

This document records the Phase 1 implementation decisions, initially written
before implementation and refined during validation.

- `internal/runtime` coordinates immutable configuration generations, context,
  parameters, permissions, and prepared invocations. It owns the transaction lock
  for state mutation/admission/reload. Core packages never import API handlers.
- The job manager owns one goroutine per running job, bounded by concurrency.
  Pending work is a removable bounded queue; cancelling queued work frees its
  slot immediately. Workflows execute within their parent job. Expansion is
  limited to 1024 action/workflow nodes, in addition to the 32-level depth limit.
- Exec, Git, and open providers are stateless. Prepared jobs pin their commands,
  environment, policy, context, and output budget; no mutable provider resource
  needs retirement on reload. Phase 5 SCPI owns per-device serialization and
  health; device changes require restart and actions retain that shared manager.
- Reload supports definitions, security, defaults, parameters, bindings, worker
  concurrency, queue/history/output limits, and HTTP body limits. Socket path,
  state path/flush interval, shutdown grace, subscriber capacity, and SCPI device definitions require a
  restart. Changing them rejects the whole candidate and preserves active state.
- Process cancellation kills the dedicated process group immediately and reaps
  its child. Pipe draining has a 250 ms bound. Commands that deliberately detach
  into a different session are outside this process-group guarantee; exec is not
  an operating-system sandbox. No shell is inserted by the daemon.
- Socket startup requires a user-owned private parent directory, an adjacent
  exclusive lock, and a socket mode of 0600. Stale sockets are removed only after
  connection refusal, ownership/type checks, and a final identity check. The lock
  file remains after shutdown to preserve lock identity across daemon instances.
- The listener is acquired before restoring state, so a second daemon fails
  before it can change persisted data. A separate state lock also prevents two
  sockets from sharing one live state file. Configuration, socket, state, and
  both lock paths must be distinct. Existing private directories are never
  silently chmodded.
- State writes use a same-directory 0600 temporary file, file sync, atomic rename,
  and directory sync. A single owner coalesces updates and retries failed writes.
  Corrupt files are preserved under a unique `.corrupt-*` name before defaults are
  used. State warnings and write failures are visible through sanitized logs.
- Output limits apply to the whole job, including all workflow steps. Results
  retain per-step timing, error, skipped state, and bounded stdout/stderr. Job
  snapshots are defensive copies, and terminal history cannot evict active work.
- Queue time counts toward invocation deadlines. Expiry while queued creates a
  failed job with code `timeout` and frees the slot without starting a worker.
  Explicit cancellation creates a cancelled job. Synchronous wait handles pin
  their admitted job even if another completion trims it from public history.
- Git supports typed status/diff/log/pull/push/branch/stash/stash-pop operations.
  Branch listing is safe; creation/deletion/switching require confirmation.
  Force operations and arbitrary subcommands are unsupported. Tests use temporary
  repositories and local bare remotes without network access.
- `deckd` now runs the daemon by default. The complete `deckctl` command surface
  remains Phase 2; Phase 1 API integration tests and Unix-socket HTTP clients
  exercise the runtime without hardware.

## State and reload

Valid persisted context and persistent parameter values override defaults.
Unknown projects clear selection; invalid modes, values, and parameter types
fall back to validated defaults. New context value keys use configuration
defaults. Explicit parameter writes reject out-of-range values; rotations clamp
with wide arithmetic before converting to int64/float64. State is limited to
8 MiB. The writer serializes immutable snapshots before accepting them, coalesces
dirty revisions, and retains dirty data after failed writes for retry. Logs use
`state/persist_failed` and `state/recovered` outcomes without file contents.

Reload parses a fresh file and constructs registries outside the transaction
lock. Under that lock it reconciles state, prepares the persistence snapshot,
swaps the generation, adjusts scheduling limits, and publishes changes.
Increasing concurrency admits waiting jobs; decreasing it lets existing workers
finish. Removing a project clears selection. Parameter removal drops its value;
type/bounds changes preserve a value only if it remains valid. Policy changes
apply to new admissions; already admitted work retains its captured policy.

## Provider options

Git action `inputs` may declare only the options below. Omitted options use the
listed provider defaults. Each value still passes the action's declared schema.

| Operation | Inputs and defaults | Minimum permission |
| --- | --- | --- |
| `status` | None; porcelain v1 with NUL delimiters | safe |
| `diff` | `path` string (all), `staged` boolean (false); external diff/textconv disabled | safe |
| `log` | `limit` integer (20, range 1–1000); NUL-separated hash/subject | safe |
| `pull` | `remote` string (`origin`), `branch` string (configured tracking); fast-forward only | confirm |
| `push` | Same remote/branch options; no force | confirm |
| `branch` | `mode` enum (`list`, `create`, `delete`, `switch`; default `list`), `name` string | safe for list; confirm for mutations |
| `stash` | `message` string (`deckd stash`, max 4096 bytes), `untracked` boolean (false) | confirm |
| `stash-pop` | `index` integer (0, range 0–1000) | confirm |

Remote names and branch names are validated before building argv. Git credential,
editor, sequence editor, and pager prompts are disabled. SSH runs with BatchMode
enabled; Git/SSH askpass programs are disabled. Configured Git hooks and
repository configuration still run with the owning user's authority. Subprocess
errors expose a safe code plus exit status and bounded output in the job result.

Exec resolves its literal executable using the prepared PATH and pins that path.
Environment precedence is inherited process, project, then action. Rendering
substitutes validated variables once and treats substituted strings as data.
Missing variables fail during whole-workflow preflight. Open supports file paths
and file/http/https URLs; `/usr/bin/open` is invoked with separate arguments.
Open completion means the launch request finished, not that a launched app exited.

## Concurrency ownership

| Owner | Background work | Stop/join path |
| --- | --- | --- |
| Daemon | One HTTP serving goroutine and net/http connection handlers | HTTP shutdown within the shared grace budget; forced connection close on expiry |
| Job manager | One goroutine per running job; cancellation callbacks for admitted jobs | Close admission, cancel queued/running work, wait for running count to reach zero |
| Process runner | Direct child and os/exec output readers/context watcher | Kill dedicated process group on cancellation/failure, reap child, bound pipe drain to 250 ms |
| State writer | One coalescing writer goroutine | Stop timer, flush latest revision, close done channel |
| Event bus | No persistent subscriber goroutine; context cancellation callbacks only | Unsubscribe/bus close stops callbacks and closes bounded queues |
| Workflow | No additional worker or goroutine | Execute each step on the parent worker; cancellation skips remaining steps |

The runtime transaction lock protects context, parameter mutation, generation
activation, and admission. Providers run outside that lock. Disk writes happen
outside it as well. Snapshot encoding is bounded; subscribers cannot block
publishers. Filesystem I/O itself cannot be forcibly interrupted: if it exceeds
the shutdown budget, shutdown reports failure and the process exits.

See [Phase 1 verification](reviews/PHASE1.md) for checks, performance measurements,
and Prism findings and dispositions.

## Phase 6 executable lifecycle

The runtime owns a plugin host with a serialization slot per explicitly configured
plugin. Each admitted invocation owns one fresh subprocess through hello, health,
execute and shutdown. Prepared args/context and policy retain their admitted
generation. Plugin configuration requires restart; action reloads share the host.
All I/O, negotiation and cleanup are bounded; cancellation attempts a cooperative
message then kills the process group and reaps the child. Runtime shutdown cancels
jobs before closing the host. No subprocess remains for an idle successful plugin.
See [PLUGINS.md](PLUGINS.md) for deadlines and OS isolation limits.

## Experiment evidence extension

See [Experiments and local evidence](EVIDENCE.md) for the CC-1/CC-2 schema, preparation,
storage, capture, comparison, export, API, CLI, and recovery contracts.

## Durable feature execution

[Evidence](EVIDENCE.md) records reservations before scheduler dispatch and terminal
outcomes before waiters resume. [Recipe composition](RECIPES.md) publishes reviewed
configuration changes atomically. [Agent control](AGENT_CONTROL.md) adds frozen context,
private sessions, effective-definition grants and exact proposal approvals. Bounded
patches use the same job/run admission, plus a durable staging journal and per-project
advisory write lock. Interrupted work is recorded without replay. Recovery file states
remain separate from immutable completed/interrupted run evidence.
