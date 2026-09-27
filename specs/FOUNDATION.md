# Phase 0 contracts and decisions

This document records the implementation decisions needed to turn SPEC v0.1 into
testable schemas. Configuration validation is implemented in Phase 0. Runtime
behavior described below is a contract for Phase 1 unless explicitly identified
as implemented.

## Boundaries

- Use local module `patchbay`, Go 1.27.0 minimum, and the standard library except
  for the pinned YAML parser. No repository remote exists to establish a public
  module identity.
- Add `internal/binding` for binding metadata/ambiguity checks, `internal/state`
  for future persistence, `internal/logging` for safe operation summaries,
  `internal/version` for metadata, and `internal/cli` for shared entry-point code.
- Domain contracts stay in `internal`. Wire DTOs in `pkg/protocol` are independent
  snapshots; handlers must explicitly translate runtime data into them.
- Phase 0's CLI surface is version, help, and offline configuration validation.
  No socket or provider starts. Runtime objects are types rather than managers.
- `config.Parse` returns a fresh normalized configuration owned by its caller.
  Publication and atomic reload belong to Phase 1; published configurations must
  be treated as immutable. `EffectiveActions` copies the outer map and shares
  immutable definitions.

## YAML schema

`version: 1` is required. Unknown fields, duplicate keys, multiple documents,
non-string mapping keys, nulls, aliases, anchors, merge keys, arbitrary tags,
implicit number-to-string conversions, and legacy `yes`/`no` booleans are rejected.
Use `true`/`false`; quote strings that YAML could parse as numbers or dates.
An input file is limited to 1 MiB and nesting to 64 levels. Diagnostics contain
the field path and available line/column information without scalar values.

### Settings and limits

All settings below are optional; explicit invalid values fail instead of falling
back to defaults. Numeric limits are positive. Upper bounds protect against
accidental unbounded resource allocation; tuning the aggregate memory budget is
part of Phase 1's scheduler work.

| Field | Default | Validation |
| --- | --- | --- |
| `server.socket` | `~/.deckd/deckd.sock` | Filesystem path; must differ from state path. |
| `server.max_request_bytes` | 1048576 | 1–67108864 bytes. |
| `server.shutdown_grace` | `5s` | Positive Go duration. |
| `jobs.concurrency` | 4 | 1–1024. |
| `jobs.queue_capacity` | 64 | 1–65536 pending jobs. |
| `jobs.history_limit` | 100 | 1–65536 terminal jobs. |
| `jobs.output_limit_bytes` | 1048576 | 1–67108864 bytes, combined stdout/stderr per job. |
| `events.subscriber_capacity` | 64 | 1–65536 events per subscriber. |
| `state.path` | `~/.deckd/state.json` | Filesystem path. |
| `state.flush_interval` | `250ms` | Positive Go duration. |
| `security.allow_dangerous_actions` | `false` | Boolean. |
| `context.defaults` | Empty context | Optional `project`, `mode`, and `values` string map. |

`projects`, `actions`, `workflows`, and `parameters` are maps keyed by name.
`bindings` is a list. Names start with a letter or digit, contain letters, digits,
dot, underscore, or hyphen, and have a maximum length of 128 characters. Context
value keys, environment variable keys, and input names use identifier syntax:
`[A-Za-z_][A-Za-z0-9_]*`. A selected default project must exist.

Literal paths expand `~` or `~/...`; named-user expansion is unsupported. Relative
paths resolve against the configuration file's directory, never the daemon's
working directory. Parameter strings and command arguments do not undergo path
or environment expansion. File existence and executable availability are runtime
checks so offline validation works on another machine.

### Projects

Each project requires `name` and `path`. The map key is its ID; an optional `id`
must match it. Optional fields are `github` (HTTPS URL without credentials),
`language`, `engine`, `metadata` (string map), `environment` (string map), and
`actions` (complete replacements of existing global action definitions).

Overrides cannot add undeclared action names or reduce the global action's safety
classification. Each effective project action graph is independently checked for
workflow cycles and invocation argument compatibility.

### Actions and inputs

Every action requires `type`. Shared fields are `safety` (`safe`, `confirm`,
`dangerous`; omitted means `confirm`) and optional positive `timeout` duration.

| Type | Required fields | Other accepted fields |
| --- | --- | --- |
| `exec` | `command`: literal executable | `args`: string list; `cwd`; `environment`; `inputs`. |
| `open` | `target`: file, directory, or URL | `inputs`. Literal URL schemes are limited to file/http/https. |
| `git` | `operation`: status/diff/log/pull/push/branch/stash/stash-pop | `cwd`; `environment`; `inputs`. No arbitrary Git argument array. |
| `workflow` | `workflow`: existing name | Shared fields only; inputs are declared by individual step actions. |

Exec/Git working directories default to the configuration directory. Commands
containing a path separator resolve relative to that directory; bare commands
are looked up on PATH at execution. A shell is never inserted. Shell metacharacters
in argument strings remain literal data. Explicitly configuring a shell executable
is trusted local configuration, not an escape from a claimed process sandbox.

Git pull/push/stash/stash-pop have a minimum `confirm` classification even if
configured safe. `branch` initially means branch listing; mutation variants must
define typed operation options and a minimum confirmation policy in Phase 1.
Raw/force options are unsupported by this schema. Workflow dispatch derives the
strongest policy of its resolved steps at admission; marking its alias safe does
not make a dangerous step safe.

Inputs use `type`, `required` (default false), optional `default`, `min`, `max`, and
`enum`. Types and bounds follow parameter rules. A required input cannot also
have a default. Workflow/binding arguments must match declared input names/types
and satisfy required inputs. Invocation normalization inserts declared defaults
in Phase 1; an optional input without a default remains absent.

```yaml
actions:
  repeat:
    type: exec
    safety: safe
    command: go
    args: [test, '-count={{ .args.count }}', ./...]
    inputs:
      count: {type: integer, default: 1, min: 1, max: 10}
```

Only substitutions are allowed in `args`, `cwd`, `target`, and environment values:
`.project.path`, `.project.id`, `.project.name`, `.context.mode`,
`.context.values.<identifier>`, and `.args.<declared-input>`, enclosed in `{{ ... }}`.
No functions, pipes, loops, conditionals, arbitrary property access, environment
lookups, or execution are allowed. Project environment values cannot reference
action inputs. Missing variables fail admission. Substituted values are data and
must not be interpreted as another template. Rendered cwd/target values must be
validated again, including URL scheme and NUL checks, before execution.

Environment precedence is inherited daemon environment, project environment,
then action environment. Environment values, action arguments, and raw output
must never be logged or included in action discovery responses.

### Workflows

`steps` is a list of 1–256 `{action, args?}` entries. `stop_on_error` defaults to
true. Steps resolve exact names; `project.test` has no automatic relationship to
`test`. Direct and indirect cycles through workflow actions are rejected, including
project overrides. Nesting is limited to 32 workflows. There are no expressions,
loops, branches, retries, or implicit rollback.

### Parameters

Required fields are `type` and `value`. Optional fields are `min`, `max`, `step`,
`enum`, `unit`, and `persistent` (default false).

- `integer`: signed 64-bit integers only, including exact JSON integer values.
  Fractional or floating-point representations are rejected.
- `float`: finite numbers, with integer YAML values converted to float64.
- `boolean`: true/false only.
- `enum`: string value included in a nonempty, unique `enum` string list.
- `string`: string values only.

Numeric bounds are optional, inclusive, and must match the numeric type. Min must
not exceed max, and the initial value must satisfy them. Numeric step defaults to
1 and must be positive. Nonnumeric types reject min/max/step; other types reject
enum metadata. Phase 0 normalizes and validates definitions; synchronized updates
and overflow-safe rotation are implemented in Phase 1.

### Bindings

Bindings require a `control`, optional `device`, optional `when` conditions, and
at least one of `press`, `release`, or `rotate`. Conditions support `mode`,
`project`, and `values.<identifier>`; all supplied conditions must match.

Press/release targets use `{action, args?}`. Rotation uses `{parameter}` and must
name a numeric parameter. Precedence is device+control+conditions, then
control+conditions, then device+control, then control. Condition count is not an
extra precedence rule. Overlapping conditions for the same control/device,
gesture, and tier are ambiguous and fail validation. Different gestures may be
declared separately. Unmatched valid input is an accepted no-op at runtime.

## Runtime contracts for Phase 1

- Admission captures effective action definitions, configuration generation,
  arguments, and context. Queued/running jobs retain this snapshot across reloads.
- Both synchronous and asynchronous work use bounded job scheduling. Queue full
  and shutdown return stable errors. Async jobs survive client disconnection;
  sync jobs inherit the request's cancellation. The CLI submits async work and
  waits by polling, with explicit cancellation on Ctrl-C.
- States are queued -> running -> success/failed/cancelled, or queued -> cancelled.
  Queued deadlines may also transition directly to failed. Terminal states
  cannot change. Timeout is failed with error code `timeout`, including queue wait.
- Results use success/failed/cancelled. Failed workflow steps aggregate to failure;
  cancellation always stops later steps. Steps run within the parent's worker
  capacity, so a nested workflow does not require an additional worker permit.
- Preflight workflow references/arguments/permissions before any side effect.
  Confirm requires invocation confirmation; dangerous requires both configuration
  opt-in and confirmation. Internal dispatch uses the same checks.
- Each subscriber gets a bounded queue. Drop the oldest event on overflow and
  increment a counter; publishers do not wait for subscriber progress. Clients
  recover from dropped notifications by fetching authoritative snapshots.
- Publish state-change events after mutation. Only the daemon emits lifecycle
  events; external event requests accept control input types only. Preserve
  per-job event order without promising cross-job order.
- Reload validates and prepares a complete candidate before atomically activating
  it. Existing jobs retain prior resources; failed candidates are discarded.
  Removed projects clear selection; incompatible parameter values reset to their
  new defaults. Compatible runtime context/values persist across reload.
- Persist active project, context, and marked parameters to a versioned state
  file using restrictive permissions and same-directory temp-file/rename.
  Coalesce writes and flush at shutdown. Preserve corrupt files for diagnosis and
  use validated defaults. No persistent job history is required in V1.
- Close admission on shutdown, cancel jobs, stop providers, flush state, close
  listeners/subscriptions, and finish within the configured grace period.

## Logging and errors

Use `internal/logging.Logger.Operation` with fixed component/outcome labels and
generated event/action/job IDs. It emits JSON through `log/slog`, adds duration,
and reduces errors to validated stable codes. Raw error strings are deliberately
excluded. CLI validation prints only the loader's sanitized diagnostics.

The API adds `busy` (queue full) and `shutting_down` to SPEC §28's categories, both
HTTP 503. These distinguish admission failures from provider health failures.
The complete wire/status contract is in [API.md](API.md).

## Verification ownership

No Phase 0 code starts goroutines or external commands. Pure validation has no
cancellation lifecycle; cancellable public runtime methods accept
`context.Context` when introduced. Unit suites cover schema/type failures,
templates, references, project overrides, cycles, binding precedence, parameter
edges, lifecycle transitions, JSON envelopes, command behavior, and safe logs.
Hardware and external provider accounts are unnecessary for all Phase 0 checks.
