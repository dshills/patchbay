# Experiments and local evidence

## Implemented contracts (CC-1)

Patchbay accepts strict, version 1 experiment definitions. Definitions select one
configured action or workflow, map named parameters into leaf action inputs, and
name collectors and layout references. A leaf index starts at zero and follows
nested workflow execution order. `action` on each collector must match that leaf's
semantic action name. Project overrides must satisfy the same contract.

```yaml
parameters:
  iterations: {type: integer, value: 5, min: 1, max: 20}
actions:
  measure:
    type: exec
    safety: safe
    command: /path/to/measurement-program
    args: ['{{ .args.iterations }}']
    inputs: {iterations: {type: integer, required: true, min: 1, max: 20}}
  capture: {type: experiment, experiment: benchmark}
experiments:
  benchmark:
    schema_version: 1
    title: My benchmark
    action: measure
    inputs: [{step: 0, input: iterations, parameter: iterations}]
    collectors:
      - {name: duration, step: 0, action: measure, kind: measurement, source: json_stdout, path: [duration], unit: ms, direction: lower}
    layout:
      - {id: duration, title: Duration, kind: measurement, reference: duration}
```

Collector kinds are `measurement`, `series`, and `text`; sources are `native` and
`json_stdout` (exec only). Paths contain literal JSON object field names, without
expressions. Measurement direction is `higher`, `lower`, or `neutral`. Unit strings
are exact. Targets cannot contain other experiment actions. Existing workflows
remain valid independently: their ordinary arguments/defaults must satisfy each
step; experiment mappings replace only the declared leaf arguments.

Input and parameter declarations accept `sensitive: true`. Experiments reject
sensitive action inputs and sensitive bound parameters, including defaults.
Environment values remain private inside the prepared plan; previews and manifests
contain environment **names** only. Declaring sensitivity does not change existing
trusted local parameter inspection or execution APIs. Authors must not embed secrets
in literal commands, ordinary context values, or undeclared output fields.

`experiment prepare` validates and resolves the effective action graph without
executing it. The preview expires after 60 seconds and is tied to the daemon instance,
configuration generation, context, and parameter values. Any parameter/context
change, including an away-and-back change, invalidates pending previews. Each preview
has a private HMAC over the full effective plan (including environment values) and
its public metadata. The HMAC key lives only in daemon memory. Limits: 64 previews,
1 MiB encoded private plan each, and 16 MiB total.

CC-1 publishes preparation and inspection. Capture dispatch and experiment-wrapper
execution are enabled in CC-2. Clients must consult `/v1/capabilities`.

## API and CLI

All routes use the existing private Unix socket. No TCP listener is added.

| Route | Method | Purpose |
| --- | --- | --- |
| `/v1/capabilities` | GET | Feature and schema versions |
| `/v1/experiments` | GET | Experiments available in current project |
| `/v1/captures/prepare` | POST | `{ "experiment": "benchmark" }` → preview |
| `/v1/storage` | GET | Availability, quota usage, reservations, diagnostics |
| `/v1/runs` | GET | Filter `project`, `experiment`; `limit` 1–100, opaque `cursor` |
| `/v1/runs/{id}` | GET, DELETE | Read or delete; `acknowledge=true` to delete a baseline |
| `/v1/runs/{id}/annotation` | PUT | Revision-checked `title`, `note`, `pinned` |
| `/v1/runs/{id}/artifacts/{artifact}` | GET | Inventory metadata and bounded `data_base64` bytes |
| `/v1/baselines/{experiment}?project=...` | GET, PUT | Shared baseline; PUT `run_id`, `revision` |

```
deckctl capabilities
deckctl experiment list
deckctl experiment prepare benchmark --json
deckctl storage status
deckctl run list
deckctl run page CURSOR
deckctl run show RUN_ID
deckctl run annotate RUN_ID '{"revision":0,"title":"First run","note":"","pinned":true}'
deckctl baseline show benchmark
deckctl baseline set benchmark '{"run_id":"RUN_ID","revision":0}'
deckctl run delete RUN_ID --confirm
```

Annotations use their own revision counter. Editing with an old revision returns
`request_conflict` (409). Baselines use a separate shared revision; selection requires
a successful measured run with the current experiment definition and matching project.
An annotation never rewrites immutable measurements or artifacts. Deletion refuses
active runs, pinned runs, and records held by a reader/export. Baseline deletion needs
explicit acknowledgement and increments its revision.

Additional errors: `stale_preparation` (409), `incompatible_results` (422),
`storage_full` (507), and `recording_failed` (500).

## Storage and recovery

`runs.path` defaults to a `runs` directory beside `state.path` (`~/.deckd/runs` with
default settings). Run settings require a daemon restart. Defaults/hard maximums:
1000 runs, 1 GiB total, 16 MiB per run, 4 MiB per artifact, 32 artifacts per run,
10000 points per generic series, and 10000 deleted-request receipts. Limits can be
lowered; the total quota must also allow 16 MiB for metadata and atomic staging.
There is no automatic eviction. Storage failure disables evidence admission while
ordinary configured actions remain available.

The directory is private (0700), files are private (0600), and one process holds its
advisory lock. Opaque IDs select records; API callers cannot supply filesystem paths.
Reads reject symlinks, nonregular files, wrong ownership/permissions, and excessive
sizes. Artifact inventories bind size and SHA-256. Manifests are bounded to 1 MiB;
metadata to 8 MiB. Cursor order uses creation time and ID and survives deletion.

Writes create an exclusive temporary file, flush it, atomically rename it, then flush
the directory. Admission reserves a full per-run allowance plus metadata/staging
headroom. Terminal runs release unused reservations. A write failure makes storage
read-only for the lifetime of that instance, because a rename may already have
committed. Restart scans the store and marks all nonterminal records `interrupted`.
No provider call is reconstructed or replayed. Corrupt or newer-version data remains
in place for diagnosis, and mutation is disabled. There is no automatic migration or
repair. Back up the directory while the daemon is stopped before offline inspection.

A timestamped request ID is accepted only within the previous 24 hours or five
minutes ahead. Known request IDs are resolved before checking time/preview validity;
different content conflicts. Deletion durably records a receipt before removing files.
Receipts remain for 25 hours from admission, and longer if interrupted cleanup left
files. Clock rollback prevents mutation until a safe restart. Staged/orphaned data
counts against the quota and is never silently discarded. Cleanup of such data is an
explicit offline maintenance operation.

The dispatch commit point is the durable queued manifest with its job identity.
CC-2 must reserve queue capacity and publish that manifest before making work visible
to a worker; a persisted queued record with no dispatch recovers as interrupted.
