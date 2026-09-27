# Executable plugins

## Phase 6 design decision

Recorded before implementation, 2026-09-27. The v1 executable protocol exposes
bounded text actions through the existing jobs, permissions and workflows.
Plugins cannot register runtime actions, emit internal events, change context or
parameters, select workflow steps, or receive daemon credentials through this
protocol. Their executable and fixed arguments, working directory, explicit
literal environment, and allowed operation names are configured by the operator.
No shell or executable search is implicit.

A confirmed invocation starts a fresh subprocess, negotiates version/capabilities,
checks health, executes one operation, then requests shutdown and reaps it. This
avoids retaining plugin state across jobs or replaying uncertain work. One job per
configured plugin owns a serialization slot; pending work stays in the existing
bounded job system. Independent plugins can run concurrently. Configuration
validation, discovery through the API, and daemon startup never execute plugins.
Last negotiated capabilities and health are cached for inspection. Plugin
configuration changes require restart; action reloads preserve the shared host.

The host enforces at least confirmation, the configured operation/action floor,
and any stronger plugin-advertised requirement. Discovery must match the explicit
operation allowlist; advertising extra operations is a protocol error. Results
contain only status and text; the host constructs job identity and metadata.
Cancellation sends a cooperative cancel message for the active execution, then
kills its process group after a short grace period. Shutdown is also bounded.
There are no automatic restarts or retries. Stderr is drained but never published
or logged; a byte ceiling prevents endless diagnostic floods.

This is process supervision and protocol validation, not an OS sandbox. A plugin
runs with the daemon user's filesystem and network authority. Empty inherited
environment and a fixed cwd reduce accidental exposure; they do not prevent a
malicious same-user executable from reading files, contacting the daemon's Unix
socket, or starting detached processes. Only install trusted executables; use
separate OS accounts/sandbox controls where confinement is required. The host
kills the process group it created, but cannot contain descendants that deliberately
escape that group. There is no marketplace, installation service, or broad SDK.

## Configuration and operation

[`configs/plugins.yaml`](../configs/plugins.yaml) declares the example plugin.
`plugins` accepts at most 16 entries. Each entry is an explicit executable
allowlist entry, with these fields:

| Field | Contract |
| --- | --- |
| `command` | Required literal path containing `/`; relative paths and `~/` resolve at config load. No PATH lookup or implicit shell. Availability is checked only on invocation. |
| `args` | Up to 64 fixed strings, at most 4096 bytes each; no templates. |
| `cwd` | Literal path; defaults to the configuration directory. |
| `environment` | Up to 32 explicit literal string entries. The daemon environment and project/action overlays are not inherited. |
| `operations` | Required map of 1–64 names to safety floors. Names match `[A-Za-z][A-Za-z0-9_.-]{0,63}`. `safe` is raised to `confirm`; `dangerous` requires the daemon opt-in. |
| `startup_timeout` | Hello plus health deadline; default 2s, allowed 10ms–30s. |
| `execution_timeout` | Whole invocation including waiting for the plugin slot; default 30s, allowed 10ms–10m. |
| `cancel_timeout` | Cooperative cancel and subsequent shutdown grace; default 500ms, allowed 10ms–2s. |
| `shutdown_timeout` | Normal shutdown response and process/pipe exit; default 500ms, allowed 10ms–2s. |

Paths and environment values are capped at 4096 bytes and reject NUL/newline and
substitution markers. Configured executables are trusted code, not content-addressed
or signed packages. Replacing a binary at the allowed path changes what runs on
the next explicit invocation. Config validation does not run or inspect its code.

An action uses `type: plugin`, `plugin: <configured name>`, `operation`, ordinary
typed `inputs`, optional `safety`, and optional `timeout`. Executable, argument
array, cwd, environment and other provider-specific fields are not accepted on
the action. The effective permission is the strongest of confirmation, the
configured operation floor, the action floor and negotiated plugin metadata.
Project overrides retain the existing inherited action floor. Permission and
input checks happen before admission; the stronger negotiated floor is checked
again before an execute frame is sent. A plugin advertising a dangerous operation
can make a job fail policy after discovery, but cannot grant itself permission.

`status.plugins` shows protocol version, allowed operations with effective floors,
and whether discovery has succeeded. `providers["plugin:<name>"]` gives the last
invocation's result, initially `not_checked`. No background probe executes code.
`action list` exposes routing; clients cannot choose an arbitrary executable,
operation, environment or context through invocation arguments.

A job's prepared operation, arguments and context belong to its admitted
configuration generation. Changing action definitions can use ordinary reload;
changing plugin process settings or its operation allowlist requires restart.
Plugin output cannot update daemon context, parameters, actions or workflows.

## Wire protocol v1

Transport is UTF-8 JSON objects, one frame per LF-terminated line on stdin/stdout.
The LF is mandatory, including the final message. Maximum frame size is **65536
bytes excluding LF**, including JSON encoding overhead. Blank lines, invalid UTF-8,
nulls, duplicate/unknown fields, trailing JSON, nesting beyond 64 levels, invalid
versions/IDs/types, and mismatched payload schemas fail the session. JSON whitespace
is accepted. Stdout must contain only protocol frames; use stderr for diagnostics.

Every frame has exactly `version`, `id`, `type`, and `payload`. `version` is `1`.
`id` is a positive decimal string without leading zeros, at most 16 digits. The
host uses sequential IDs starting at `1`, echoed by the response. `payload` is an
object with the schema below; optional context members may be omitted, not null.

```json
{"version":1,"id":"1","type":"hello","payload":{"versions":[1]}}
```

Only one request awaits a response. Each fresh process receives hello, health,
execute and shutdown, in that order. Cancellation is a control message for the
active execute ID, not a second pending request. The response channel has one
slot. Unsolicited, duplicate, unknown-ID or out-of-order replies immediately fail
the session. Messages claiming internal events or extra result fields are rejected.

### Hello and capabilities

Host request type `hello`, payload `{"versions":[1]}`. Plugin reply type `hello`:

```json
{"version":1,"id":"1","type":"hello","payload":{"selected_version":1,"operations":[{"name":"echo","safety":"confirm"},{"name":"wait","safety":"confirm"}]}}
```

The plugin chooses a supported offered version. This implementation offers only
v1 and rejects every other selection. The operation names must match the configured
allowlist exactly, with no missing, duplicate or additional entries. Each safety
is `safe`, `confirm` or `dangerous`. Metadata never lowers a host floor and cannot
register actions or request capabilities beyond execution of configured operations.

### Health

Host type `health`, payload `{}`. Reply type `health`, payload
`{"available":true}`. The boolean is required. `false` fails the job before execute.
There is no plugin-supplied diagnostic string in this response.

### Execute and result

Host type `execute`:

```json
{"version":1,"id":"3","type":"execute","payload":{"operation":"echo","args":{"text":"Hello"},"context":{"mode":"dev","values":{"branch":"main"}}}}
```

`args` contains only validated action inputs with defaults resolved. `context`
contains optional `project` ID, `mode`, and string `values` snapshotted at admission.
No credentials, project file contents, parameter values or executable routing are
added automatically. Input framing is checked before a process starts. Plugins
should independently check the argument schema their operation expects.

Reply type `result`, with required `status` (`success` or `failed`) and required
string `text` (empty is allowed):

```json
{"version":1,"id":"3","type":"result","payload":{"status":"success","text":"Hello"}}
```

Text counts against the job's shared output budget and may be truncated there,
even when the protocol frame fits. The host returns `stdout`, `truncated`, plugin
name and operation within ordinary result data. It adds `stderr_bytes` and, when
attempted, `shutdown_clean` and `cooperative_cancel` diagnostics. A result never
sets job identity, permission, generation, context or arbitrary display fields.
There are no partial-result events, nested tool calls or arbitrary structured
result extensions in v1. A reported `failed` status uses the host's sanitized
`execution_failed` error while retaining bounded text for inspection.

### Cancellation

On cancellation or an execution deadline, the host sends `cancel` with the active
execute ID and `{}` payload. The plugin must stop its operation and reply
`cancelled` with that same ID and `{}`. A normal result can cross a cancel; the
host still reports cancellation/timeout and never retries it. If the plugin has
already sent its final result, it should ignore a crossing cancel instead of
sending a duplicate terminal response.

The cancel grace includes sending cancellation and, after a cooperative reply,
requesting clean shutdown. Refusal or a blocked pipe ends in process-group kill
and closed host pipes. If the request cannot be fully written, startup is still
incomplete, or the protocol has already failed, the host immediately kills the
child instead of trying to insert cancellation into a partial frame. A cancelled
job can therefore take up to `cancel_timeout` beyond its original deadline to
finish cleanup. Cancellation cannot roll back external side effects.

### Shutdown and process exit

After a valid terminal result or cooperative cancellation, the host sends a new
`shutdown` ID with `{}`. The child responds with type `shutdown`, the same ID and
`{}`, closes its outputs, and exits zero. Success requires the reply, process
exit, and stdout/stderr completion within the remaining shutdown deadline.
Extra frames, unsuccessful exit, or held pipes fail the session. A malformed,
unhealthy, incompatible or policy-rejected process is killed directly.

Every exit path closes pipes, kills any remaining members of the created process
group and reaps the direct child. Explicit pipe ownership prevents inherited
stdout/stderr handles from keeping Go copy goroutines alive indefinitely.
OS scheduling and an uninterruptible kernel operation remain outside userspace
protocol deadline guarantees. Reaping deliberately detached descendants requires
OS-level containment; this host cannot guarantee it.

Stderr is drained without retaining or displaying its content. More than 65536
stderr bytes aborts the session; at most one 4096-byte read can cross that ceiling.
This avoids unbounded diagnostic storage and accidental credential logging.
Stdout is bounded per frame and by the four-response lifecycle; extra replies
are protocol failures. Job concurrency and queue limits remain the global bounds.

## Compatibility policy

Executable protocol versioning is independent of the Unix API version and YAML
schema version. Version 1 has strict field validation: adding a message, field or
capability requires an explicitly negotiated new protocol version. A future host
may offer several versions; neither party silently guesses or downgrades an
unsupported version. Unknown messages are never treated as successful execution.
The golden hello fixtures are in `pkg/plugin/testdata`.

## Example and conformance harness

Build and check the included Go example (no third-party plugin runtime needed):

```sh
make plugin-example
make plugin-verify
```

`make check` also runs conformance. The example implements `echo` and cancellable
`wait`; its source is [`examples/plugin/main.go`](../examples/plugin/main.go).
The build also provides `bin/deckplugincheck`, whose default probes target those
two operations. It loads plugin configuration without starting a daemon or using
its state directory. Running the checker explicitly authorizes the specified
probes, including starting the executable; dangerous operations still require
`security.allow_dangerous_actions: true`.

For another configured plugin, select a harmless successful operation and a
long-running cancellable operation, with appropriate arguments:

```sh
./bin/deckplugincheck --config path/to/config.yaml --plugin example \
  --operation echo --args '{"text":"conformance"}' \
  --cancel-operation wait --cancel-args '{"milliseconds":30000}'
```

The checker has a 10s overall deadline, returns JSON, exits zero only when version,
capabilities, health, execution, cooperative cancellation and clean shutdown pass,
and does not regard forced kill as successful cancellation conformance. It uses
the host's default timing limits for a common baseline. Go tests can reuse
`pkg/pluginconform.Check(ctx, Specification)`. This harness exercises the selected
operations, not every side effect or every possible input to a plugin. The host's
own adversarial suite covers malformed frames, unexpected exit, stderr flooding,
hung children, refused cancellation/shutdown, and forged state.

To run through the daemon after reviewing the executable and config:

```sh
./bin/deckd --config configs/plugins.yaml
./bin/deckctl --socket ~/.deckd/plugins.sock action run plugin.echo --confirm
./bin/deckctl --socket ~/.deckd/plugins.sock workflow run plugin.demo --confirm
./bin/deckctl --socket ~/.deckd/plugins.sock action run plugin.wait --confirm --async
# Use the returned job ID:
./bin/deckctl --socket ~/.deckd/plugins.sock job cancel JOB_ID
```
