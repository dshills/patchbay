# Capture & Compare specification

**Status:** Proposed, version 0.1\
**Date:** 2026-09-28\
**Plan:** [Implementation plan](PLAN.md)\
**Parent:** [Feature roadmap](../README.md)

## 1. Purpose and first release

Let a user adjust a value, run an experiment, save its context and results, and
compare it with a baseline. Provide one visual workbench for terminal-only users
and Stream Deck owners. The first release includes a bundled software experiment,
durable run records, numeric and waveform comparisons, and an offline HTML report.
Real Rigol capture follows a separate physical validation gate.

Success means a new user can produce a meaningful comparison in five minutes
after launching the downloaded bundle, without equipment, credentials, Go, or YAML
editing. Saved evidence must remain understandable after the daemon restarts or
the project changes.

### User journeys

1. **Try it:** launch the workbench, choose Benchmark Playground, inspect sample
   results, run the bundled workload, change worker count, run again, and compare.
2. **Keep evidence:** name a baseline, add a note to a later run, and export the two
   runs with a chart and measurement table.
3. **Use the bench:** inspect generator settings, capture an already stopped scope
   acquisition, adjust desired settings, explicitly apply them through existing
   controls, acquire/stop another trace, and compare the two captures.
4. **Recover:** reopen Patchbay after an interrupted run, see what was recorded,
   and choose whether to start a fresh run. No operation resumes automatically.

## 2. Scope boundaries

Included: one local workbench; declared buttons, parameter controls, job status,
measurement tables and line charts; explicit experiments; saved runs; baseline
selection; comparisons; export; packaged demo; CLI access to all durable results.

Deferred: arbitrary dashboards, graphical workflow programming, remote browser
access, cloud sync, automatic parameter sweeps, live oscilloscope streaming,
automatic setting restoration, and background instrument polling. Continuous
acquisition/trigger control requires a later instrument-specific specification.
The current stopped-capture limit of 1,000 points remains in force.

## 3. Functional requirements

| ID | Requirement |
| --- | --- |
| CC-F01 | Launch an optional workbench from the release bundle; show sample data and a runnable local demo without external services or developer tools. |
| CC-F02 | Select a project and experiment; show declared controls, units, bounds, pending/observed values, job state, and results. Keyboard interaction and textual chart equivalents are required. |
| CC-F03 | Prepare an experiment from a consistent project/configuration/parameter snapshot. Show the effective steps and permission requirements before execution. |
| CC-F04 | Execute through the existing scheduler and provider policy, record the run before dispatch, and preserve bounded evidence through success, failure, cancellation, and daemon restart. |
| CC-F05 | Collect explicitly declared numeric measurements and series with units and provenance. Reject malformed or truncated required results; retain valid partial evidence with clear status. |
| CC-F06 | Select a baseline and compare compatible results without changing either source record. Explain incompatibility and missing values. |
| CC-F07 | Export a self-contained HTML report and versioned JSON data after a content preview. Both must work offline and exclude undeclared private context. |
| CC-F08 | List, inspect, annotate, pin, export, and explicitly delete saved runs through the CLI and workbench, with bounded storage and clear recovery behavior. |
| CC-F09 | Capture DG812 readback and MHO954 traces using existing SCPI constraints, including a stopped-acquisition check before waveform transfer; distinguish requested settings, observations, and actual acquisition timing. |
| CC-F10 | Use the same experiments and baseline selection from Stream Deck controls, with stale-input protection and independent physical validation. |
| CC-F11 | Preserve existing CLI/API/plugin behavior, and advertise supported feature/schema versions to new clients. |

## 4. Ownership and integration

`deckd` owns experiment resolution, job admission, run storage, artifact validation,
comparisons, permissions, and export data. Extend `internal/runtime`,
`internal/config`, `internal/api`, `internal/client`, and `pkg/protocol`.
Introduce focused internal packages for experiments, run storage, and comparisons
as needed; avoid placing mutable store logic in wire types.

`deckctl workbench` owns the local browser bridge and embedded assets. Its UI
uses the same daemon operations as the CLI. The Stream Deck adapter gains semantic
capture/baseline controls and result summaries, without owning experiment logic.
The new built-in demo executable, provisionally `deckdemo`, is packaged and
versioned with Patchbay. It emits only the declared benchmark data format.

### Experiment definition

An experiment has a schema version, stable ID, title, project applicability,
action or ordered workflow target, parameter-to-input mappings, collectors, and
layout. A collector identifies an exact prepared step occurrence and a bounded
result field path; it cannot evaluate code, regex programs, or arbitrary JSONPath.
Collectors compile against the effective workflow, including project overrides.
An incompatible override fails preparation before any step runs.

Mappings copy typed values into declared action inputs at preparation. They never
modify global workflow definitions. Existing workflow calls still accept no
top-level arguments. The prepared experiment owns its expanded sequential plan,
with the existing 1,024-action expansion ceiling and strongest permission floor.
Capture must use the same immutable values it records, even if a dial moves later.

Expose configured experiments to physical bindings through a new semantic
`experiment` action type that references an experiment ID. Experiment targets may
not include another experiment action, directly or through workflows; reject such
recursion during configuration validation. The wrapper uses the same capture
preparation/admission path and effective risk as the CLI and workbench.

Layouts reference configured actions, experiments, parameters, and result series.
Initial widgets are button, number/enum/boolean input, status, metric table, and
line chart. Limit a layout to 32 widgets. A widget cannot contain executable code,
HTML, arbitrary paths, or remote resources. Failed references invalidate the layout.

### Preparation and approval

Preparation returns an opaque ID, canonical plan digest, runtime instance,
configuration generation, context/control revision, relevant parameter revision,
effective targets/arguments, risk, and expiry. Tokens last 60 seconds, with at most
64 outstanding preparations. Public previews mask secret values but preserve field
names, argument positions and declared secret-reference sources; they must not hide
that a secret is used. Never dump inherited environment values. The private digest
includes all effective execution inputs; any exposed approval digest is an opaque
keyed value so it cannot be used to guess secret inputs from a plain hash. Preparing does no
provider I/O and grants no execution authority.

Execution supplies the preparation ID, digest, a client request ID, and explicit
confirmation when required. Under the runtime admission lock, the daemon rejects
expired preparations and changed context, generation, or relevant values. It
rechecks dangerous opt-in and consumes approval only with successful admission.
New preparations are required after stale-input failures. A displayed preview remains
readable after token expiry; an explicit Refresh approval re-prepares it without
executing or making a provider request. Preserve the review position if its effective
digest is unchanged; otherwise show the changes and require review again. Expired
tokens never gain an implicit validity extension. Existing Stream Deck
five-second gesture tokens keep their current semantics; these are separate tokens.

For a repeated request ID with identical admitted content, return its original
run/job identity. Check durable receipts before enforcing a consumed token's expiry;
this returns evidence only and never admits another operation. Different content
using that ID fails with conflict. Persist this
deduplication record with the run; if the run is deleted during the retry window,
retain a compact tombstone until 25 hours after original admission. It returns only
the original identity and a deleted-state indication. Reject unknown request IDs
older than 24 hours, using a timestamped
request-ID format validated at admission (at most five minutes in the future), so
expiry cannot cause an old retry to execute again. Reject new admissions after a
detected clock rollback until the recorded retention horizon is safe; expiry checks
must never shorten the persisted deduplication guarantee. Clients must request a
fresh run explicitly after an uncertain result.

## 5. Saved data contract

| Entity | Required content and behavior |
| --- | --- |
| Experiment | Schema version, ID/version digest, effective plan digest, collector definitions, and presentation metadata. |
| Run | Unique ID, origin (`measured`), experiment/project identity, runtime instance, config generation, request ID, job ID when admitted, creation/start/end times, outcome, pinned inputs, context allowlist, collector results, and artifact inventory. |
| Source context | Git commit when available, dirty flag, observation time, and a bounded worktree-status digest. Missing Git is explicit. This identifies observed context, not a complete copy of the checkout. |
| Measurement | Stable name, finite numeric value, unit, source step, observation time, and optional repeat count/spread. Invalid, unavailable, and missing values are explicit statuses, never zero. |
| Series | Stable name, X/Y quantity and unit, finite ordered sample pairs or an equivalent uniform-sampling representation, acquisition/scaling metadata, and source step. |
| Artifact | Schema/type version, daemon-owned opaque ID, media type, byte count, SHA-256, and parent run. The API resolves IDs; clients cannot request filesystem paths. |
| Annotation | Run ID, revision, title/note, and timestamps; maintained separately so evidence stays immutable. Updates require the prior revision. |
| Comparison | Two run IDs, explicit baseline, compatibility decision/reasons, measurement deltas, plot metadata, and algorithm version. |

Maintain the selected baseline per project/experiment as a revisioned daemon-owned
reference, separate from immutable runs. Selecting it executes nothing, checks that
the run belongs to the project and compatible experiment, and invalidates affected
control previews. Persist the reference so the CLI, browser and Stream Deck agree;
deletion clears it explicitly and reports the missing baseline to clients.

Input and parameter schemas add a `sensitive` boolean, default false. Captures with
any supplied/defaulted argument or bound parameter marked sensitive fail preparation
before recording it. Clients cannot pass a secret literal or a secret-reference object
to evade that rule; ordinary non-capture actions keep their existing behavior.
Environment secrets are a different source: only existing locally configured provider
environment may supply them, and evidence contains its declared name/source metadata
without resolved values. This adds no client-side secret expression or environment
override syntax. Secrets are never copied into manifests or public previews.
Do not treat secret-pattern scanning as a
substitute for declared sensitivity or the export preview.

Sample results use the same measurement/artifact schemas in a separate read-only
catalog, with `origin: sample`, package-local IDs and no runtime job/request identity.
Only daemon collection creates measured runs. Comparison requests use typed
references (`run` or `sample`); a comparison involving samples is visibly illustrative
and cannot become the measured baseline for a project. Bundled/recipe imports add
samples to the catalog without creating completed jobs or measured history entries.

Observe Git context at start and finish. If it changes, label the run as having
changed source context. Do not claim reproducibility from a Git SHA alone when
the worktree is dirty or changes during execution. Dependency/environment summaries
are an explicit allowlist of non-secret metadata, never a dump of the environment.

### Collection

Native SCPI results are adapted directly from their typed fields. Exec collection
is opt-in per experiment and accepts one strict JSON measurement envelope from
bounded stdout; arbitrary command output remains text. Reject duplicate fields,
unknown versions, nonfinite numbers, impossible units, oversized series, and any
truncation affecting required data. Optional collectors report unavailable data
without failing an otherwise successful run. Required collector failure makes the
experiment fail even if its subprocess succeeded; preserve that subprocess outcome.

Plugin v1 remains text-only. No artifact paths, event publishing, or new messages
are silently added to its protocol. A future structured-plugin capability requires
its own protocol design and conformance fixtures.

### Durability and resource limits

Use a private daemon-owned store at `~/.deckd/runs` by default, configurable at
startup. Directories are mode 0700 and files 0600. Start with versioned JSON
manifests and content files using same-directory temporary files, flush, atomic
rename, and parent-directory durability; do not add a database without evidence.
Reject symlinks and traversal on all store/import/export paths.

Reserve queue capacity and durable run identity before provider dispatch. If the
initial record cannot be persisted, nothing executes. Persist terminal evidence
before reporting the run as durably complete. An error saving a completed external
operation reports `recording_failed` with the known job outcome and never retries
the operation. Runtime job states stay unchanged; run states additionally include
`interrupted` and `recording_failed`.

On restart, queued/running nonterminal runs become interrupted, retaining available
evidence and an unknown-external-effect warning. They never resume. A committed
terminal manifest is authoritative; orphan staging files are quarantined/cleaned
without following links. Corrupt records remain quarantined and visible as errors;
one corrupt run must not prevent unrelated runtime work.

Initial limits: 4 MiB per artifact, 16 MiB per run, 32 artifacts per run, 10,000
points per generic series, 1,000 saved runs and 1 GiB per store. Existing provider
output limits still apply and may be smaller. Account for manifests, staging,
in-flight reservations, and deduplication tombstones. Maintain a separate bounded
tombstone budget; reject admission if its retention guarantee cannot be maintained.
Store reservations must prevent concurrent captures exceeding the quota.

No automatic deletion in the first release. At the quota, reject new captures with
an actionable storage error; ordinary non-capture actions continue. Users can pin
runs and inspect size before explicit deletion. Active runs and artifacts used by
an export are protected by references. Deleting a baseline requires acknowledgement;
remaining comparisons show a missing source rather than silently choosing another.

## 6. Comparison semantics

Compare only matching quantity, unit, collector identity, and compatible schema.
Initially require exact units; no implicit unit conversion. Metric deltas are
`candidate - baseline`; percent is `100 * delta / abs(baseline)`. A zero baseline
has an absolute delta and an unavailable percent. Whether higher/lower is better
is explicit experiment metadata; otherwise label only the change.

Waveforms overlay in their original time coordinates. Use the existing SCPI
scaling formula and retain preamble/sample metadata. Different sample grids may
be plotted together with a warning; pointwise subtraction requires identical
grids. Do not silently align triggers, interpolate, resample, or infer circuit
improvement. Missing or incompatible measurements remain visible with reasons.
Partial/failed runs can be inspected side by side but cannot receive an unqualified
success/improvement summary. Benchmarks show repeat count and variability when
available; a single run is an observation, not a significance claim.

## 7. Workbench and browser boundary

The helper listens only on `127.0.0.1` at an ephemeral port, starts only on explicit
launch, and connects to the selected Unix socket. Closing the helper revokes access.
It serves packaged static assets and an explicit API allowlist; it cannot proxy
arbitrary paths/hosts, read arbitrary files, or execute shell commands.

Launch with a cryptographically random, at least 256-bit session token in the URL
fragment. Before issuing any requests, the page uses `history.replaceState` to remove
the fragment from its current history entry and keeps the token in memory.
All API requests require that token in an authorization header, including reads.
Never put it in query strings, logs, persistent browser storage, cookies, or exports.
Validate exact Host; validate exact Origin on mutations and reject cross-site
fetches. Do not enable CORS. Require JSON and a custom authenticated header for
mutations, use a restrictive CSP and no-referrer policy, and render untrusted text
as text. Refresh/reopening without the token requires a new authorized launch.
These controls resist hostile websites; they do not sandbox same-user processes.

Poll bounded authoritative snapshots at 500 ms while active, with backoff when
hidden/disconnected. After two seconds without a successful refresh, mark live
controls stale and disable mutation until resynchronized. Closing a tab does not
cancel an admitted job; a visible Cancel control performs explicit cancellation.
Do not infer global execution order from polling or introduce an event stream in
this release. Cap bridge sessions, requests, response sizes, and open connections.

UI states cover empty project, sample data, missing dependency, queued/running,
pending approval, success, partial failure, cancellation, interrupted run, storage
full, stale connection, and incompatible comparison. Charts include units, legend,
baseline/candidate labels, data tables, keyboard-accessible controls, and visible
focus. Color alone must not encode state.

## 8. Proposed interfaces

All routes below are additions to the Unix-socket API; the bridge exposes only
the subset needed by its UI. Existing endpoints and `confirmed` behavior remain.

| Interface | Purpose |
| --- | --- |
| `GET /v1/capabilities` | Supported feature and data-schema versions. |
| `GET /v1/experiments` and `GET /v1/experiments/{id}` | Effective experiment/layout discovery without provider execution. |
| `PUT /v1/experiments/{id}/baseline` | Revision-checked baseline selection for the active project; never executes an action. |
| `POST /v1/captures/prepare` | Validate experiment, snapshot values, return preview and expiring preparation. |
| `POST /v1/captures` | Admit one prepared capture; return run ID and job ID. |
| `GET /v1/runs` and `GET /v1/runs/{id}` | Cursor-paginated summaries and detailed evidence; list pages default 50, maximum 100. |
| `GET /v1/samples` and `GET /v1/samples/{id}` | Read-only sample catalog, with the same pagination bounds and explicit provenance. |
| `PATCH /v1/runs/{id}/annotation` | Revision-checked title, note, and pin changes. |
| `DELETE /v1/runs/{id}` | Explicit deletion with reference/quota handling. |
| `GET /v1/runs/{id}/artifacts/{artifact}` | Bounded validated content by opaque ID. |
| `POST /v1/comparisons` | Pure comparison of two immutable run/sample references; no action execution. |
| `POST /v1/exports/preview` and `POST /v1/exports` | Select exact fields/artifacts, then generate report/data for that selection digest. |

New errors include `stale_preparation`/409, `request_conflict`/409,
`incompatible_results`/422, `storage_full`/507, and `recording_failed`/500. Add them
to protocol validation, API docs, and client rendering together. Captures are
asynchronous; cancel via their existing job ID. CLI commands provisionally group
under `deckctl experiment`, `deckctl run`, `deckctl compare`, and
`deckctl workbench`; settle subcommand syntax with help/golden tests in CC-1.

## 9. Export and equipment details

Reports contain escaped text, inline SVG/static tables, and selected data. They
contain no JavaScript, remote fonts, trackers, or external resource references.
Default exports exclude absolute project paths, instrument addresses/serials,
raw command output, and notes until selected. Commit IDs and dirty flags are shown
in the preview. Hashes detect accidental corruption; they do not prove authorship.
Use private temporary files and a caller-chosen destination with explicit overwrite
handling. Browser download names never determine daemon filesystem paths.

For bench runs, record generator inspection immediately before and after scope
transfer, plus per-operation timestamps and identity/firmware. Mark changed
readback clearly. The scope provider must verify stopped acquisition immediately
before and after transfer using its supported profile. Running, armed, unknown or
malformed state before transfer rejects capture without issuing an automatic stop;
after transfer it fails the capture and preserves successfully collected bounded
data with `quality: suspect` and reason `acquisition_state_changed` or
`acquisition_state_unknown`. Suspect data is available for diagnostic display/export
with a warning, but cannot serve as a baseline or produce numeric comparison deltas.
Preserve both state observations with their timestamps. Two stopped observations
cannot detect an intervening run/stop cycle, so exclusive operator access remains
required. A stopped scope trace may predate those observations; record
acquisition time as unknown unless the instrument provides reliable evidence.
Do not imply those settings produced the trace. Ask the operator to confirm the
intended acquisition when attaching it to an experiment; preserve that as an
operator assertion, distinct from instrument evidence.

Changing frequency/amplitude still requires both generator outputs off. Enabling
output remains a separate dangerous, explicitly confirmed operation. Capture
neither enables output nor starts/stops acquisition. A one-button apply-and-acquire
cycle is deferred until new scope operations and a bounded multi-stage experiment
have their own model-specific validation. External/front-panel changes can race
with inspection; the UI must retain this limitation.

## 10. Acceptance scenarios

- **CC-A01:** On a clean supported Mac, the extracted bundle opens the workbench,
  runs the demo twice, compares results, and produces an offline report without Go,
  Stream Deck, AI credentials, or network access after download.
- **CC-A02:** Moving a dial or changing project after preparation prevents the
  stale capture. Concurrent duplicate submissions produce at most one admitted run.
- **CC-A03:** Kill the daemon before dispatch, during execution, and during final
  persistence. Restart shows honest evidence/outcome, performs no replay, and
  preserves request deduplication, including after deletion.
- **CC-A04:** Disk full, concurrent reservations, corrupt manifests, symlinks,
  oversized/truncated results, and failed collectors never produce a false complete
  record or crash ordinary actions.
- **CC-A05:** Known metrics and waveforms yield exact expected deltas, units,
  coordinate scaling, zero-baseline behavior, and incompatibility explanations.
- **CC-A06:** Hostile origins, missing tokens, injected notes/SVG markup, arbitrary
  file requests, and export path traversal fail; selected secret fixtures stay out
  of default exports and logs.
- **CC-A07:** Physically verified DG812/MHO954 capture matches displayed scaling;
  uncertain settings and acquisition time remain labeled. Stream Deck and browser
  produce the same semantic run and reject stale input. Running, armed, unknown and
  malformed acquisition states fail before waveform transfer; state changes or
  failed status queries after transfer fail the capture and preserve its bounded
  trace as suspect diagnostic evidence, excluded from baselines and numeric deltas.
- **CC-A08:** Old clients and plugin v1 continue to work; unsupported feature
  versions fail clearly; keyboard and screen-reader paths reach all core actions.

## 11. Decisions and future work

The local browser bridge, JSON file store, polling transport, strict comparison
rules, and manual scope acquisition are deliberate first-release decisions.
Revisit a database after measuring indexing/recovery at the stated quota. Revisit
streaming after measuring polling cost. Automatic sweeps, trigger control, richer
plugin results, and arbitrary chart builders each require a separate scope decision.
