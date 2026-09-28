# Experiments and local evidence

## Experiment contracts (CC-1)

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

Capture dispatch, collection, and experiment wrappers are now available (CC-2).
Clients must consult `/v1/capabilities`.

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
CC-2 reserves queue capacity and publishes that manifest before making work visible
to a worker; a persisted queued record with no dispatch recovers as interrupted.

## Capture, comparison, and export (CC-2)

```
make build
bin/deckd --config configs/benchmark.yaml
# In another terminal:
bin/deckctl --socket .cache/benchmark/deckd.sock experiment run benchmark
bin/deckctl --socket .cache/benchmark/deckd.sock param set iterations 20000
bin/deckctl --socket .cache/benchmark/deckd.sock experiment run benchmark
bin/deckctl --socket .cache/benchmark/deckd.sock run compare FIRST_RUN SECOND_RUN
bin/deckctl --socket .cache/benchmark/deckd.sock export prepare FIRST_RUN SECOND_RUN
# Inspect the exact document in that preview, then use its ID and digest:
bin/deckctl --socket .cache/benchmark/deckd.sock export save PREVIEW DIGEST html report.html
```

`experiment run` is a convenience for trusted local definitions. It prepares the
current parameter values, applies the normal permission policy (`--confirm` when
required), and waits for durable completion. `--async` returns run/job/request IDs.
Ctrl-C requests normal job cancellation. For a preview/approval split, use:

```
deckctl experiment prepare benchmark --json
deckctl request-id
deckctl experiment capture PREPARATION DIGEST REQUEST_ID --confirm
```

`POST /v1/captures` accepts `preparation`, `digest`, `request_id`, and `confirmed`.
Admission takes the runtime and scheduler locks, validates the preparation, reserves
capacity, and flushes the queued run manifest with the job ID **before** a worker can
see it. Permission failure or a full queue consumes no preparation. Successful
admission consumes it. Repeated identical requests return the durable original
identity, even after expiry/deletion. The CLI reports the exact retry command on an
uncertain admission response; it does not automatically resubmit.

Experiment action wrappers use this same path, including effective wrapper safety
and timeout. Existing sequential workflow grouping and stop-on-error behavior remain
intact. Collectors run at leaf completion, preserving the subprocess outcome even
when required evidence makes the overall experiment fail. Cancellation while queued
also writes a terminal record. Durable completion precedes notification of job
completion. If external work finishes but recording fails, the live run reports
`recording_failed`; its last committed state may recover as `interrupted` on restart.
The external job outcome remains available separately and is never replayed.

Exec JSON collection requires exactly one strict envelope:

```json
{"schema_version":1,"data":{"duration":{"value":1.2,"unit":"ms","quantity":"duration","repeats":5,"spread":0.1}}}
```

A collector path such as `[duration]` addresses the envelope's `data` object. A
measurement can be a finite number (using declared units/quantity) or the strict
object above. Object units and quantity must match the declaration. `spread` is the
max-minus-min range in the demo; `repeats` is the number of timed repetitions. Series
objects use the versioned series schema. Text collectors require UTF-8 strings.
Unknown fields/versions, duplicate JSON keys, missing data, invalid series, or
truncation invalidate required collectors. Optional failures remain visible without
failing otherwise successful work. Native collectors address typed provider result
fields; they never infer measurements from arbitrary prose.

The bundled workload performs bounded SHA-256 work (1–100000 iterations, 2–20 repeats,
10-second internal ceiling plus a configured job timeout). It records the median,
range, repeat count, OS/architecture, logical CPU count, and Go runtime version.
No hardware, network, AI credentials, or uploads are involved. Timings are observations;
comparison does not claim statistical significance.

Git source observations at start/end use a one-second deadline and 64 KiB output
budget. Missing/unavailable context is explicit. Commit, dirty status and bounded
status digest can reveal source changes, but do not fingerprint every dirty file's
contents or provide full reproducibility.

`GET /v1/samples` returns a separate read-only catalog with `origin: sample`, no job
or request identities, and deterministic illustrative values. It never populates
measured history. `deckctl sample list` inspects it; `run compare sample:benchmark-small
sample:benchmark-large` compares fixtures. Comparisons involving a sample are labeled
`illustrative` and cannot become measured baselines.

`POST /v1/comparisons` accepts typed references:

```json
{"baseline":{"kind":"run","id":"FIRST_RUN"},"candidate":{"kind":"run","id":"SECOND_RUN"}}
```

Kinds are `run` or `sample`. Exact experiment schemas, units, quantity and collector
identity are required. Delta is candidate minus baseline; percent uses the absolute
baseline denominator and is unavailable at zero. Partial runs retain their status.
Series overlay keeps original coordinates; subtraction requires identical grids.
Suspect acquisition data cannot produce a comparison delta.

`POST /v1/exports/prepare` accepts `runs` (one or two IDs) and `options` containing
`inputs`, `notes`, `logs`, and `source` booleans. All four default false. The exact
selected document is returned for review, with a 60-second preview token. Default
exports omit paths, environments, device addresses, serial numbers, raw text logs,
annotations, and parameter values. Selected text can contain private data; review it.
Snapshots hold references against deletion, expire without extending approval, and
are capped at 16 previews/16 MiB combined. Individual previews are at most 8 MiB.

`POST /v1/exports` accepts `preparation`, `digest`, and `format` (`html` or `json`).
It returns bounded base64 bytes with SHA-256, and consumes the preview. HTML escapes
all text and contains no scripts, remote resources, or active form actions. Output is
capped at 16 MiB. `export save` checks integrity and creates a new 0600 file with
exclusive creation and directory durability; it refuses overwrites, traversal and
symlinked parent paths. The daemon never accepts an export filesystem destination.
