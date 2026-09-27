# Deckd local API v1 contract

Wire types live in `pkg/protocol`; Phase 1 implements the server and transport.
All routes use JSON over HTTP on a Unix domain socket;
there is no TCP listener. Default socket: `~/.deckd/deckd.sock`.

Requests and responses use `Content-Type: application/json`. Unknown JSON fields,
trailing JSON documents, duplicate object keys, null values, oversized
bodies, and incompatible types are invalid requests. Field names are case-sensitive.
Decode untyped numbers with
`json.Decoder.UseNumber` to preserve integers. Resource names are URL path segments
and must be escaped by clients. Collection responses use stable lexical ordering
for named resources and newest-created-first ordering for jobs. Empty collections
are arrays, not null. Lists remain bounded by configuration/history limits.

## Routes and examples

Examples below omit optional fields. Job timestamps use RFC 3339 UTC. IDs and
generation numbers shown here are illustrative.

| Method and route | Request example | Success response example |
| --- | --- | --- |
| `GET /v1/status` | No body | `{"version":"0.1.0","uptime_ms":1000,"config_path":"/Users/me/.config/deckd/config.yaml","project":"demo","mode":"development","running_jobs":0,"generation":1,"providers":{"exec":{"available":true}}}` |
| `GET /v1/context` | No body | `{"project":"demo","mode":"development","values":{"branch":"main"}}` |
| `PATCH /v1/context` | `{"mode":"meeting","values":{}}` | `{"project":"demo","mode":"meeting"}` |
| `GET /v1/projects` | No body | `{"projects":[{"id":"demo","name":"Demo","path":"/src/demo","language":"go"}]}` |
| `GET /v1/projects/{id}` | No body | `{"id":"demo","name":"Demo","path":"/src/demo","language":"go"}` |
| `PUT /v1/context/project` | `{"project":"demo"}` | `{"project":"demo","mode":"development"}` |
| `GET /v1/actions` | No body | `{"actions":[{"name":"test","type":"exec","safety":"safe","inputs":{"count":{"type":"integer","required":false,"default":1}}}]}` |
| `GET /v1/actions/{name}` | No body | `{"name":"test","type":"exec","safety":"safe","inputs":{"count":{"type":"integer","required":false,"default":1}}}` |
| `POST /v1/actions/{name}` | `{"mode":"async","args":{"count":1},"confirmed":false}` | HTTP 202: `{"job_id":"j1"}` |
| `GET /v1/workflows` | No body | `{"workflows":[{"name":"validate","stop_on_error":true,"steps":[{"action":"test"}]}]}` |
| `POST /v1/workflows/{name}` | `{"mode":"async","confirmed":true}` | HTTP 202: `{"job_id":"j2"}` |
| `GET /v1/jobs` | No body | `{"jobs":[{"id":"j1","action":"test","state":"queued","generation":1,"created_at":"2026-09-27T12:00:00Z"}]}` |
| `GET /v1/jobs/{id}` | No body | `{"id":"j1","action":"test","state":"success","generation":1,"created_at":"2026-09-27T12:00:00Z","finished_at":"2026-09-27T12:00:01Z","result":{"status":"success","message":"Completed."}}` |
| `DELETE /v1/jobs/{id}` | No body | HTTP 202 while cancellation is pending: `{"id":"j1","action":"test","state":"running","generation":1,"created_at":"2026-09-27T12:00:00Z"}` |
| `GET /v1/parameters` | No body | `{"parameters":[{"name":"level","type":"integer","value":50,"min":0,"max":100,"step":5,"unit":"%","persistent":true}]}` |
| `GET /v1/parameters/{name}` | No body | `{"name":"level","type":"integer","value":50,"min":0,"max":100,"step":5,"unit":"%","persistent":true}` |
| `PUT /v1/parameters/{name}` | `{"value":55}` | `{"name":"level","type":"integer","value":55,"min":0,"max":100,"step":5,"unit":"%","persistent":true}` |
| `POST /v1/events` | `{"type":"control.rotated","source":"adapter","payload":{"device":"deck","control":"dial-1","delta":1}}` | `{"event_id":"e1","matched":true}` |
| `POST /v1/controls/snapshot` | `{"controls":[{"device":"deck","control":"dial-1"}]}` | Atomic control guard, context and effective gesture targets; see below |
| `POST /v1/config/reload` | `{}` | `{"generation":2}` |

Successful responses are HTTP 200 unless indicated otherwise. Status lists all
configured built-in provider health summaries; it contains no environment values
or credentials. Action discovery describes effective project actions and input
schemas; it never exposes command arguments or environment values. Workflow
discovery exposes action names and ordering without static step argument values.
Single-action discovery was added during Phase 2 review for typed CLI arguments;
it uses the same effective project definition as the collection route and returns
`action_not_found` for unknown names.

## Context and parameters

A context patch changes only supplied fields. Empty `project` clears selection;
empty `mode` clears the mode. Supplying `values` replaces the entire values map,
including `{}` to clear it. Explicit null is invalid. Project selection validates
against the active configuration for both context endpoints.

Parameter values are typed JSON scalars. Integer values remain signed 64-bit
integers end to end; fractional/exponential integer representations are rejected.
Writes outside bounds fail. Rotation clamps to bounds, while direct writes do
not silently clamp. Unchanged values do not emit redundant change events.

## Invocation, confirmation, and results

The invocation envelope contains `args`, `mode` (`sync` or `async`, default async),
`confirmed` (default false), and optional positive `timeout_ms`. A request timeout
can shorten an action's configured timeout, never extend it. `args` is checked
against the action's declared input schema. Workflow invocations currently accept
no top-level arguments; individual steps have configured arguments.

The daemon supplies context, resolves project overrides, and calculates effective
safety. Clients cannot submit a provider, executable, environment, safety label,
or trusted runtime context. `confirmed:true` is explicit evidence for this
invocation. It does not enable dangerous actions; the daemon must also have
`security.allow_dangerous_actions:true`. Workflow preflight checks all reachable
steps before executing any of them. These controls prevent accidental execution;
the socket trusts processes running as the owning local user.

Async admission returns HTTP 202 with a job ID. Sync uses the same bounded job
manager and returns its final result and job ID. A sync example:

```json
{
  "job_id": "j1",
  "result": {
    "status": "success",
    "message": "Completed.",
    "data": {
      "exit_code": 0,
      "stdout": "ok\n",
      "stderr": "",
      "truncated": false
    },
    "display": {"title": "Test", "state": "success"}
  }
}
```

The combined stdout/stderr budget is configured per job. `truncated:true` signals
discarded output. Captured output may contain sensitive command results; clients
may display it, but runtime logs must not copy it. Structured workflow data adds
ordered `steps` entries with step index, action, result, error if present, and
`skipped:true` for unstarted steps. Optional display progress is an integer 0–100;
absence means progress is unknown, while zero remains explicitly representable.

Sync execution failure returns the mapped non-2xx status with the same `job_id`,
`result`, and an `error` object. Admission failures have only the standard error
envelope. Fetching a failed job itself succeeds with HTTP 200 and reports failure
in the job's state/result/error fields.

## Job lifecycle and cancellation

States are queued, running, success, failed, and cancelled. Queued work may become
running, cancelled, or failed if its deadline expires before starting. Running
work becomes exactly one terminal state. Queue wait counts toward the timeout. A timeout
produces state failed and error code timeout; explicit cancellation produces
cancelled. Races are serialized; already terminal outcomes cannot change.

Cancelling a running job acknowledges the request with HTTP 202 and its current
snapshot. Queued cancellation can return a cancelled snapshot immediately with
HTTP 200. Repeated cancellation of a terminal job returns the terminal snapshot
with HTTP 200. Unknown IDs return not_found. Cancelling cannot undo external
effects already completed. Asynchronous jobs survive a client disconnect;
synchronous jobs inherit request cancellation.

## External events

Clients may submit `control.pressed`, `control.released`, `control.rotated`,
`control.long_pressed`, `control.touched`, and `control.long_touched`.
Payloads contain `control`, optional `device`, rotation `delta` (signed integer),
and optional `confirmed` evidence for action gestures. Event IDs and
timestamps are generated by the daemon. Client source labels are for diagnostics,
not authorization. Daemon lifecycle/state event types are rejected at this route.
Rotation requires `delta` (zero is allowed); action gestures forbid it. Rotation
forbids `confirmed`, even when false.

A matched action event that schedules a job returns HTTP 202 with `event_id`,
`matched:true`, and `job_id`. Parameter rotation returns HTTP 200 without a job.
An unmatched valid event returns HTTP 200 with `matched:false`. Permission failures
use the same errors as direct action invocation. No subscription endpoint exists
in V1; clients read authoritative status/context/job/parameter snapshots.

### Guarded controls and confirmation (Phase 3)

`POST /v1/controls/snapshot` is read-only despite using POST for its request body.
It accepts zero to 64 unique `{device,control}` references; `device` may be empty.
Under one runtime lock it returns:

```json
{"guard":{"instance":"opaque-daemon-id","revision":4},"generation":2,"context":{"mode":"review"},"controls":[{"device":"deck","control":"dial-1","targets":{"control.rotated":{"enabled":true,"parameter":{"name":"level","type":"integer","value":50,"unit":"%","persistent":true}},"control.pressed":{"action":"test","safety":"confirm","enabled":true}}}]}
```

The instance changes on daemon restart. The control revision advances on every
context change and successful reload, including a context changed away and back.
An event with `payload.guard` must match both values before any mutation. This
prevents stale device input from executing a newly selected binding. Old clients
without a guard retain V1 behavior. No streaming endpoint is added.

For a guarded action requiring confirmation, the normal HTTP 409 error includes
`error.confirmation: {token,action,expires_at}`. Nothing has run at that point.
A later deliberate gesture may send the same original event type, source,
device/control and guard with `payload.confirmation` set to that token. Tokens
expire after five seconds and are consumed on an attempted use. Source, control,
gesture, guard, and expiry are checked under the runtime lock. A context change,
reload, restart, or replacement challenge for the same source/control invalidates
the token. At most 256 live challenges are stored; capacity returns `busy`.
Rotation cannot carry confirmation; guarded events cannot carry `confirmed`.
Dangerous actions still require the daemon's configuration opt-in.

Snapshots expose resolved action names, effective safety and policy availability,
or current typed parameter metadata. They exclude binding arguments, command
lines, environments and subprocess output. An absent target means no binding.
`enabled` describes permission policy; runtime preflight still checks arguments,
provider availability and templates when input arrives.

## Error contract

All errors contain stable `error.code` and human-readable `error.message`:

```json
{"error":{"code":"confirmation_required","message":"Explicit confirmation is required."}}
```

| Code | HTTP status | Meaning |
| --- | --- | --- |
| `invalid_config` | 400 | Reload candidate did not validate. Previous configuration stays active. |
| `invalid_request` | 400 | Malformed JSON, incompatible values, or unsupported options. |
| `not_found` | 404 | Unknown workflow/job/parameter or route. |
| `action_not_found` | 404 | Unknown action. |
| `project_not_found` | 404 | Unknown project. |
| `permission_denied` | 403 | Policy forbids the operation, including disabled dangerous actions. |
| `confirmation_required` | 409 | Invocation needs explicit confirmation. |
| `provider_unavailable` | 503 | Provider cannot perform the requested operation. |
| `execution_failed` | 422 | Admitted synchronous work failed. |
| `cancelled` | 409 | Synchronous work was cancelled. |
| `timeout` | 504 | Synchronous work reached its deadline. |
| `internal` | 500 | Unexpected failure; sanitized diagnostics only. |
| `busy` | 503 | Bounded queue is full; work was not admitted. |
| `shutting_down` | 503 | Daemon is draining; work was not admitted. |

Method errors use HTTP 405 with `invalid_request` and an Allow header. Payload
limit errors use HTTP 413 with `invalid_request`. Do not automatically retry
non-idempotent invocations after an ambiguous connection failure; V1 provides no
idempotency key. On an explicit busy response, callers may retry after backoff.
Confirmation retries require a new explicit user confirmation. A failed reload
must not increment the configuration generation.
