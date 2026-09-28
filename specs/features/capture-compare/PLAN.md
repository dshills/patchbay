# Capture & Compare implementation plan

**Status:** CC-1–CC-4 software implemented; CC-5 software and simulator checks complete; external release and physical verification gates remain pending. Manual screen-reader verification remains pending.\
**Specification:** [Capture & Compare](SPEC.md)\
**Dependencies:** Existing runtime/release tooling; no recipes or agent execution required.

## Delivery approach

Implement a complete software experiment before connecting equipment. Use the
existing daemon, scheduler, CLI, control guards, and release machinery. Phase IDs
are local to this feature and do not renumber the original implementation plan.
Every phase follows the [shared review/completion policy](../README.md).

## CC-1 — Contracts and durable evidence

**Outcome:** A versioned experiment/result model and bounded run store that remain
correct across crashes, concurrent captures, and incompatible inputs.

**Depends on:** Current action/job/API contracts.

- [x] Add strict experiment definitions, typed input mappings, collector schemas,
  layout references, feature capabilities, run/measurement/series/artifact DTOs,
  and schema-version rejection. Finalize CLI names and error/status mapping.
- [x] Extract only the shared preparation hooks needed from
  `internal/runtime/prepare.go`; preserve effective overrides, input validation,
  workflow preflight, job deadlines, and provider permission floors.
- [x] Define canonical digests and expiring preparation records; include effective
  execution inputs privately and produce a redacted public preview.
- [x] Add declared sensitivity to input/preview schemas, preserve redacted argument
  positions/reference names, and distinguish rejected sensitive inputs from existing
  provider environment metadata before persistence.
- [x] Define experiment action wrappers and reject recursive experiment targets.
  Add revisioned per-project/experiment baseline state shared by all clients.
- [x] Build private storage with durable manifests, immutable artifacts, annotations,
  quota reservations, interrupted-run recovery, quarantine, reference-aware deletion,
  and bounded request-ID tombstones. Document the reservation/dispatch commit point.
- [x] Add cursor-paginated reads, annotation revisions, storage diagnostics, and
  capability discovery to `pkg/protocol`, API, client, and CLI.
- [x] Write schema fixtures and migrations policy: new stores have an explicit
  version; unsupported newer versions open for diagnosis without destructive repair.

**Verification:** Strict decoding and collector-reference fixtures; restart at each
persistence boundary; quota races; disk-full/fsync/rename failures; duplicate request
IDs before/after deletion; corrupt records; traversal and symlinks; annotation races.
Use injected storage faults and synchronization barriers, not timing-only assertions.

**Deliverables:** Store and schema packages, documented wire contracts, CLI inspection,
and meaningful unit/integration/race tests.

**Exit:** CC-F03/F08/F11 contracts and persistence parts of CC-A02/A03/A04/A08 pass.
No provider execution is required for the store fixtures.

**Commit boundary:** Schema/preparation contract; durable storage/recovery; API/CLI.

**Evidence:** [CC-1 review and verification](../../reviews/CC1.md),
[implemented contracts](../../EVIDENCE.md).

## CC-2 — Execute, collect, and compare

**Outcome:** CLI users can run a real software experiment twice and compare durable
results without the graphical client.

**Depends on:** CC-1.

- [x] Add prepared capture admission with queue/store reservation, atomic stale-input
  checks, confirmation, idempotent responses, pinned values, and existing cancellation.
  Persist identity before dispatch; expose recording failures separately from effects.
- [x] Bind declared experiment parameters into prepared action inputs without changing
  the existing workflow API or adding loops/expressions to workflow definitions.
- [x] Collect native result fields and opt-in strict JSON stdout envelopes. Preserve
  step identity, partial outcomes, truncation, timestamps, and required/optional status.
- [x] Observe bounded Git context at start/end with explicit unavailable/changed states.
- [x] Add the packaged `deckdemo` workload with bounded CPU/runtime/input limits and
  measurement JSON. Include deterministic sample results labeled as samples; live
  benchmarks record repeat count, variability, and host metadata without secrets.
- [x] Implement baseline selection and pure metric/series comparison: exact units,
  finite values, signed deltas, zero baseline, grid compatibility, and partial status.
- [x] Implement export preview and escaped offline HTML/JSON generation with field
  selection, destination handling, and immutable source references.

**Verification:** Real ephemeral daemon and demo process; one-worker workflow behavior;
concurrent/dropped-response retries; cancellation between steps; config/project/parameter
changes; source changes; malformed data; exact comparison fixtures; export injection,
privacy defaults, missing references, and storage failure after external completion.

**Deliverables:** End-to-end CLI walkthrough, benchmark example, comparison/export API,
golden result/report fixtures, and updated runtime/API/CLI guides.

**Exit:** CC-F03 through CC-F08 work from the CLI; CC-A02 through CC-A06 have automated
evidence for applicable paths. An interrupted experiment is never silently retried.

**Commit boundary:** Capture admission; collectors/demo; comparisons; report export.

**Evidence:** [CC-2 review and verification](../../reviews/CC2.md),
[CLI walkthrough and contracts](../../EVIDENCE.md).

## CC-3 — Visual workbench

**Outcome:** Users can complete the full experiment through an accessible local UI.

**Depends on:** CC-2.

- [x] Add the explicitly launched loopback helper to `deckctl`, packaged static assets,
  Unix-socket connection, session-token lifecycle, exact Host/Origin rules, restricted
  routes, CSP, request limits, and bounded shutdown. Keep `deckd` transport unchanged.
- [x] Build project/experiment selection, declared parameter controls, action preview,
  confirmation, run/cancel, baseline selection, charts/tables, annotations, storage
  management, export preview, and download.
- [x] Add authoritative polling, hidden-tab backoff, stale-state disabling, reconnect
  resynchronization, and clear job/run outcome distinctions.
- [x] Cover first-run/sample, empty, disconnected, missing dependency, full storage,
  interrupted, malformed result, and incompatible comparison states.
- [x] Make controls keyboard accessible, label units and desired/observed values,
  provide chart data tables, and preserve visible focus at common window sizes.

**Verification:** Browser integration against a real temporary daemon; authenticated
read/write requests; malicious site/Origin/Host/CSRF attempts; reload/token loss;
reconnect and two tabs; injected titles/notes; keyboard-only walkthrough and manual
screen-reader checks. Verify no mutation occurs on initial render or reconnection.

**Deliverables:** Embedded workbench, browser test harness and CI job, screenshots of
main/error states, and documented launch/session behavior.

**Exit:** CC-F01/F02 work from the browser; CC-A06/A08 browser/accessibility evidence
is recorded. The client performs no execution that bypasses daemon policy.

**Commit boundary:** Browser bridge; workbench controls; comparison/history/export UI.

**Evidence:** [CC-3 review and verification](../../reviews/CC3.md),
[workbench guide](../../WORKBENCH.md). Manual screen-reader verification is pending.

## CC-4 — Packaged first experience

**Outcome:** A new user can try Patchbay using a downloaded macOS bundle.

**Depends on:** CC-3.

- [x] Include workbench assets, demo executable/configuration, sample data, required
  licenses, provenance, and hashes in both architecture bundles. Update verifiers.
- [x] Add an explicit launcher path that prepares a private demo workspace and starts
  a compatible daemon/helper when needed. Detect an existing incompatible daemon;
  report the conflict without replacing configuration or killing another service.
- [x] Document whether launched processes are owned by this session and how to stop
  them. Closing the browser leaves jobs running; explicit Quit cancels/waits according
  to the normal bounded shutdown policy. Do not silently install a login service.
- [x] Verify extracted bundles and missing/corrupt assets; test compatible reuse and
  incompatible demo configuration; document stopped backup/restore and uninstall.
- [ ] Verify upgrade/restore on a fresh user account and native Intel Mac.
- [x] Provide a short first-run guide and adjust/run/compare demonstration.
- [ ] Conduct the five-user walkthrough and record obstacles and results by consent.
- [ ] Assess signing/notarization and Gatekeeper on a clean Mac; record actual status
  and installation friction before public distribution. Publishing is a separate step.

**Verification:** Full `make check` plus browser checks; archive verification for both
architectures; native execution on each architecture before claiming runtime support;
fresh-user-account install/run/export/restart with no Go, API key, or hardware. Test
offline operation after download. Measure startup, polling load, chart rendering,
and comparison latency at the stated limits on a documented reference machine.

**Performance targets:** First workbench render within two seconds of a ready daemon;
ordinary control feedback within 250 ms excluding provider time; comparison of two
1,000-point traces within 200 ms. Record distributions and hardware; avoid flaky
wall-clock assertions in unit tests. Investigate idle-load regressions from baseline.

**Deliverables:** Verified release candidate, operations/quick-start updates, usability
notes, performance evidence, and Prism review report.

**Exit:** CC-A01 passes; software release gates are complete. Unverified architectures,
signing, or usability targets remain explicit release limitations.

**Commit boundary:** Packaging/launcher; onboarding and release evidence.

**Evidence:** [CC-4 review and release evidence](../../reviews/CC4.md). The local
candidate is unsigned; no release was published. Native Intel, fresh-account,
screen-reader, polling-load/chart-limit, and usability checks remain pending.

## CC-5 — Rigol and physical controls

**Outcome:** The same capture/compare experience works with the user's DG812,
MHO954, and Stream Deck+ within verified equipment capabilities.

**Depends on:** CC-4 and the pending physical checks in [SCPI](../../SCPI.md) and
[Stream Deck](../../STREAMDECK.md). Simulator work can proceed before hardware access.

- [x] Add typed SCPI-result collectors and a bench experiment with generator inspection
  before/after transfer, waveform scaling, observation times, firmware evidence, and
  explicit unknown acquisition time/operator assertion.
- [x] Preserve the provider's stopped-acquisition check immediately before transfer
  and add a post-transfer check. Retain both observations; reject invalid initial
  state and fail captures with changed/unknown final state without auto-stop/trigger.
  Preserve bounded suspect traces for diagnostics, excluded from baselines/deltas.
- [x] Present the actual sequence: disable outputs before changing settings, explicit
  apply, separate confirmed enable, operator acquisition/stop, then capture. Never
  label transfer of an old stopped trace as a fresh automatic acquisition.
- [x] Add semantic capture/baseline/result controls and Stream Deck feedback. Reuse
  existing gesture guards and bind approval to exact prepared capture identity.
- [x] Show desired/readback disagreement, observation changes, partial capture,
  cancellation, and disconnected hardware without implying output-off guarantees.
- [x] Test against stateful SCPI fakes and the adapter fake app.
- [ ] Perform bounded real-hardware checks with an operator-authorized setup and
  documented load.
- [ ] Record device model, firmware, transport/adapter, channel, limits, date, command
  outcomes, waveform comparison, disconnect behavior, and physical control latency.

**Verification:** Existing SCPI/profile suites plus collector scaling and mismatched
readback tests; physical scope-versus-export comparison; guarded concurrent inputs;
front-panel changes; disconnected transfer; no replay after uncertain writes; unrelated
software experiments remain usable during equipment failure.

**Deliverables:** Bench example, adapter integration, hardware verification report,
updated SCPI guide, and a real capture/comparison demonstration.

**Exit:** CC-F09/F10 and CC-A07 have physical evidence. If equipment verification is
unavailable, leave CC-5 pending and release the software experience with that label.

**Commit boundary:** Native collectors/bench UI; adapter controls; hardware evidence.

**Evidence:** [CC-5 software review](../../reviews/CC5.md). Hardware acceptance
CC-A07 remains pending; simulator coverage does not establish physical compatibility.

## Acceptance traceability

| Requirements/scenarios | Owning phases |
| --- | --- |
| CC-F01, CC-A01 | CC-3, CC-4 |
| CC-F02 | CC-3 |
| CC-F03/F04, CC-A02/A03 | CC-1, CC-2 |
| CC-F05, CC-A04 | CC-1, CC-2 |
| CC-F06, CC-A05 | CC-2 |
| CC-F07, CC-A06 | CC-2, CC-3 |
| CC-F08 | CC-1, CC-2, CC-3 |
| CC-F09/F10, CC-A07 | CC-5 |
| CC-F11, CC-A08 | CC-1, CC-3, CC-4 |

## Risks and follow-on decisions

Persistence is the largest correctness risk; prove the reservation/dispatch boundary
before UI work. Browser access creates a new local entry point; review its exact
route/authentication surface with Prism before connecting mutations. Measurements
can mislead when units, source revision, acquisition time, or sampling differ;
retain those distinctions in both UI and exports. Revisit automatic acquisition,
sweeps, and richer plots only after the first complete experiment sees real use.
