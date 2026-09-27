# Deckd — Phased Implementation Plan

**Source:** [SPEC.md](SPEC.md), version 0.1, dated 2026-09-27

**Plan status:** Phases 0–2 complete. Phase 3 implementation and simulator verification are complete; its physical hardware exit gate remains pending. See [Phase 3 evidence](reviews/PHASE3.md). Phase 4 implementation and verification are complete; see [Phase 4 evidence and Prism review](reviews/PHASE4.md). Phase 5 implementation and simulator verification are complete; physical DG812/MHO954 validation remains pending. See [Phase 5 evidence](reviews/PHASE5.md). Phase 6 implementation and local conformance verification are complete; see [Phase 6 evidence](reviews/PHASE6.md).

**Planning baseline:** Before Phase 0, the repository contained `specs/SPEC.md` and no application code, build configuration, or tests.

**Initial target:** Go daemon and CLI on macOS; Stream Deck+ follows the headless release.

## 1. Delivery strategy

Build a usable, testable headless runtime first. Deliver V1 after Phases 0–2 satisfy all twelve acceptance criteria in SPEC §34. Add the Stream Deck+ adapter in Phase 3, then developer integrations, electronics, and executable plugins in Phases 4–6.

Each phase below identifies dependencies, implementation tasks, deliverables, verification, and an exit gate. Checkboxes represent work to complete, not work already done. Implementation should proceed in the listed order unless a documented dependency permits otherwise.

### 1.1 Specification review and scope decisions

The following interpretations reconcile gaps in the specification. They are proposed implementation decisions and must be recorded in the relevant schema, API documentation, or decision record as implementation begins.

| Issue | Implementation direction |
| --- | --- |
| Git is a V1 provider and acceptance requirement, but Phase 4 mentions Git refinement. | Implement status, diff, log, pull, push, branch, stash, and stash-pop in Phase 1. Phase 4 adds conventions and refinements. |
| SPEC §§23–24 call AI and SCPI “Phase 2,” while §33 places them later. | Follow the delivery sequence in §33: CLI in Phase 2, AI in Phase 4, SCPI in Phase 5. |
| The adapter “ultimately” supports several hardware features, but V1 must work without hardware. | Phases 0–2 are the headless V1 gate. Hardware capabilities and capability-dependent features have their own Phase 3 gate. |
| Persistence is described as SHOULD, but V1 requires persisted project and mode. | Persist active project and mode for V1; also implement context values and parameters explicitly marked persistent. Persistent job history remains optional and deferred. |
| YAML examples omit action safety classifications. | Normalize omitted classifications to `confirm`; make classifications explicit in shipped examples. Built-in provider operations also have minimum safety requirements. |
| Projects are keyed by ID in examples, while the domain requires `id`. | Use the YAML map key as canonical ID. If an explicit `id` is accepted, require it to match the key. |
| Examples use both `test` and `project.test`. | Resolve exact configured action names. A `project.*` name must be declared or supplied by an explicit convention; do not invent an implicit alias. |
| The API does not specify execution mode, confirmation fields, output limits, or overload responses. | Define and test these contracts in Phase 0 before implementing clients. Section 3 gives the proposed behavior. |
| The event bus has no client subscription endpoint. | Use the specified status, context, parameter, and job endpoints for V1 polling. Evaluate a bounded streaming endpoint only when implementing adapter feedback. |
| The sample repository layout lists root `SPEC.md`. | Keep the existing specification and this plan under `specs/`; link them from the root README. |

### 1.2 Scope boundaries

V1 includes all named core providers, daemon-side permissions, bounded execution, atomic configuration reload, state persistence, a protected Unix-socket API, and the reference CLI. It includes the models and input routing needed to test bindings without a device.

Defer TCP listeners, remote clients, AI execution, SCPI, executable plugin hosting, persistent job history, parameter acceleration/logarithmic scaling, and richer workflow control flow. V1 configuration should clearly reject unsupported features rather than silently ignore them. Any future TCP listener requires explicit opt-in, and non-loopback exposure requires authentication before it is supported. Provider interfaces may leave room for extensions without implementing their protocols early.

## 2. Architecture and ownership

### 2.1 Package responsibilities

Start with the layout in SPEC §6. Add small supporting packages only when their responsibility is clear.

| Path | Responsibility |
| --- | --- |
| `cmd/deckd/` | Startup, dependency assembly, signals, readiness, shutdown, and version flags. |
| `cmd/deckctl/` | Command parsing, human/JSON output, exit codes, and signal handling. |
| `pkg/protocol/` | Versioned wire DTOs and stable error codes shared by daemon and clients. Keep runtime implementation out of this package. |
| `internal/config/` | Strict YAML decoding, validation, normalization, immutable configuration snapshots, and reload preparation. |
| `internal/action/` | Domain action contracts, registry, project overrides, request preparation, and dispatch. |
| `internal/context/` | Synchronized runtime context and consistent snapshots; alias imports to avoid confusion with Go's `context`. |
| `internal/project/` | Project definitions, lookup, path resolution, and active-project validation. |
| `internal/parameter/` | Typed values, validation, atomic updates, numeric rotation, and persistence eligibility. |
| `internal/event/` | Event envelopes and bounded, nonblocking in-process subscriptions. |
| `internal/binding/` | Proposed addition: binding validation and deterministic control-event resolution. |
| `internal/permission/` | Effective safety classification, dangerous-action opt-in, and confirmation checks. |
| `internal/job/` | Admission, bounded scheduling, job state, cancellation, results, and recent history. |
| `internal/workflow/` | Ordered step execution, aggregation, dependency validation, and cancellation. |
| `internal/provider/` | Small execution/lifecycle/health capabilities and built-in exec, open, and Git implementations. |
| `internal/feedback/` | Device-independent display state derived from runtime outcomes. |
| `internal/api/` | Unix-socket HTTP handlers, request validation, error mapping, and shared client transport if useful. |
| `internal/state/` | Proposed addition: versioned local state loading and atomic persistence. |
| `configs/example.yaml` | Runnable, explicitly classified developer automation examples. |
| `specs/` | Specification, plan, API/schema decisions, and any necessary architectural decisions. |

Keep transport types separate from mutable runtime state. Core packages must not import API handlers, CLI commands, or device adapters. Providers must not call back through HTTP to execute workflows. The daemon composition root wires services together through small interfaces.

Choose the Go module path from the actual repository identity in Phase 0. Do not assume that the checkout name `patchbay` changes the executable names `deckd` and `deckctl`.

### 2.2 Request path

1. A client submits an action/workflow invocation or a supported control event.
2. The daemon validates input and resolves bindings, project overrides, and the action definition.
3. The daemon captures a consistent configuration generation and runtime context, validates arguments/templates, and evaluates permission requirements.
4. The job manager admits work within bounded capacity; synchronous callers wait on the same execution machinery.
5. The provider or workflow executes with cancellation, output limits, and a deadline where configured.
6. The runtime stores a structured outcome and publishes lifecycle/feedback events.
7. Clients retrieve state and render results. Persistence handles durable runtime state independently of rendering.

Maintain separate identifiers for requests, events, actions, and jobs where needed for correlation. Generate IDs without adding a dependency solely for UUID formatting.

## 3. Contracts to settle before feature implementation

These contracts prevent API, CLI, workflow, and hardware implementations from making incompatible assumptions.

### 3.1 Configuration and naming

- [ ] Define schemas for server settings, concurrency/history/output limits, context defaults, security, projects, actions, workflows, parameters, bindings, and state persistence.
- [ ] Preserve the specified defaults: configuration at `~/.config/deckd/config.yaml` and socket at `~/.deckd/deckd.sock`. Propose `~/.deckd/state.json` for runtime state; document explicit path overrides and precedence.
- [ ] Reject unsupported schema versions, unknown fields, duplicate YAML keys, invalid names, unresolved references, and ambiguous bindings. Return useful field paths and source locations when available.
- [ ] Set finite, documented defaults for configuration/request sizes, queue capacity, worker concurrency, retained jobs, captured output, and shutdown grace. Validate all limits.
- [ ] Treat project overrides as complete validated effective action definitions or a precisely specified merge. Prefer replacement semantics to avoid surprising partial overrides; validate every project-specific workflow graph.
- [ ] Define a small template allowlist, initially project identity/path and approved context values. Exclude functions, commands, arbitrary expressions, and environment access. Missing variables fail before execution.
- [ ] Expand `~` only for supported filesystem fields; resolve relative paths against the configuration directory. Require an applicable project when a template refers to one.
- [ ] Define environment precedence explicitly: inherited daemon environment, project environment, then action environment. Never print environment values in diagnostics.
- [ ] Reject future provider types until their phase is implemented.

Use one maintained YAML library because Go's standard library does not decode YAML. Record the dependency choice and justification; prefer the standard library for HTTP, JSON, logging, flags, process execution, synchronization, and state files.

### 3.2 Execution and result semantics

- [ ] Separate transport invocation options from `ActionRequest`: execution mode, timeout, and confirmation evidence belong to the invocation envelope. The daemon supplies the authoritative runtime context.
- [ ] Accept `context.Context` on public operations where cancellation/deadlines apply, and propagate it through providers, workflow steps, subscriptions, and I/O. Identify daemon-owned contexts explicitly for asynchronous work.
- [ ] Support explicit synchronous and asynchronous requests. Both use bounded scheduling and receive a job ID; synchronous completion returns the final structured result, while asynchronous admission returns HTTP 202 and the job reference.
- [ ] Pin the resolved action, effective project configuration, arguments, and context when work is admitted. A queued or running job must not switch configuration generations mid-execution.
- [ ] Define supported `Result.Status` values and their mapping to job states. Keep provider errors, protocol errors, and human-facing messages consistent; represent subprocess exit status and bounded output as structured data.
- [ ] Store bounded stdout/stderr with truncation metadata. Do not retain unlimited command output in memory or logs. Only opt into more durable output storage when a later feature requires it.
- [ ] Make job transitions explicit: `queued -> running -> success|failed|cancelled`, with `queued -> cancelled` allowed. Terminal states are immutable.
- [ ] Define timeout as a failed job with stable `timeout` error metadata; explicit cancellation becomes `cancelled`. Serialize terminal transitions so cancellation/completion races yield one final outcome.
- [ ] An asynchronous job outlives its submitting connection. A synchronous request is cancelled when its request context ends. CLI wait-mode cancellation is an explicit job cancellation request.
- [ ] Bound pending work as well as running work. Specify the HTTP status, stable error code, and retry behavior for a full queue and for shutdown. Document any addition to SPEC §28's error categories before implementing it.

### 3.3 Permissions and workflows

- [ ] Require classification for every effective executable action after normalization. Effective risk is at least the provider operation's minimum and cannot be reduced by an alias or project override.
- [ ] `safe` executes normally; `confirm` requires explicit request confirmation; `dangerous` requires both daemon configuration opt-in and explicit confirmation.
- [ ] Accept `deckctl --confirm` as request-level confirmation evidence. The daemon resolves and checks the required policy; a client cannot supply its own safety classification or enable dangerous actions.
- [ ] Bind confirmation to the admitted invocation, including its resolved workflow and arguments. Do not store a global confirmed state or reuse one device gesture to approve unrelated invocations.
- [ ] Preflight all statically known workflow steps for references, arguments, and permissions before executing any step. Apply the strongest effective requirement across the workflow, and enforce checks on internal dispatch as well.
- [ ] Detect recursive workflow references, including cycles reached through workflow actions or project overrides. Keep execution ordered and bounded without reserving a new worker for each nested step.
- [ ] Fix stop-on-error semantics: `true` stops after the first failed step; `false` continues in order and reports aggregate failure if any step failed. Cancellation always stops subsequent steps.
- [ ] Document that cancellation cannot reverse completed external effects. No implicit rollback, retries, branches, loops, or scripting in V1.

### 3.4 Events, bindings, and parameters

- [ ] Define payloads for press, release, rotation, context/project/parameter changes, reload, and job/action lifecycle events. Validate event source and payload size/type.
- [ ] Permit clients to submit supported external control events only. The daemon owns job, action, configuration, and state lifecycle events; clients cannot forge them via `POST /v1/events`.
- [ ] Publish only after the authoritative state changes. Preserve defined event order within a job without promising global ordering across concurrent jobs.
- [ ] Use bounded subscriber queues with an explicit overflow policy, such as dropping the oldest event, plus a drop counter. Publish must remain nonblocking; state snapshots are the recovery mechanism for lost notifications.
- [ ] Apply the exact binding precedence in SPEC §12. Within a precedence tier, reject overlapping matching predicates that could produce equally specific results. Do not use YAML declaration order to break ties.
- [ ] Define an unmatched control event as an accepted no-op. Reject malformed events before routing them.
- [ ] Preserve integer precision when decoding JSON values; reject fractional integers, nonfinite floats, invalid enums, incompatible types, invalid bounds, and nonpositive steps.
- [ ] Apply numeric rotation atomically as `current + delta * step`, clamp to bounds, and guard against arithmetic overflow. Define an unchanged clamped value as a no-op without a duplicate change event.
- [ ] Limit V1 rotation to numeric parameters. Boolean/enum/string values are set through typed operations; advanced dial behaviors remain later work.

### 3.5 Reload, persistence, and security boundaries

- [ ] Build and validate a complete candidate configuration before activation. Prepare required provider resources, then atomically swap the generation; close prepared resources if activation fails.
- [ ] Serialize reloads. Keep old immutable definitions/resources alive while admitted jobs reference them; retire them when no longer used. New admissions use the new generation.
- [ ] Define reconciliation: retain an active project only if it remains configured; otherwise clear selection. Retain parameter values only when valid under the new definition; otherwise use the new validated default. Retain compatible context values and apply defaults for missing values.
- [ ] Emit `config.reloaded` only after successful activation, plus state-change events for reconciliation changes. Failed reloads preserve the active configuration, state, and service availability.
- [ ] Store state with a schema version and restrictive permissions using a same-directory temporary file, flush/close, and atomic rename. Coalesce frequent dial updates into bounded writes and flush at shutdown.
- [ ] Start with validated defaults if the state file is missing. For corrupt/incompatible state, preserve the original file, warn clearly, and apply documented recovery behavior. Invalid startup configuration remains fatal.
- [ ] Restrict the socket directory to the user and the socket to user read/write. Do not unlink arbitrary files, symlinks, or a live daemon's socket during stale-socket cleanup.
- [ ] Treat local configuration and explicitly allowed commands as trusted code with policy controls, not as a sandbox. A process running as the same user may access the local API.
- [ ] Never log credentials, environment values, arbitrary request arguments, expanded secret-bearing templates, or raw subprocess output. Use sanitized structured summaries and a safe health/error vocabulary.

## 4. Phase roadmap

| Phase | Depends on | Primary outcome | Release gate |
| --- | --- | --- | --- |
| 0 — Foundation | Existing specification | Buildable skeleton, schema, contracts, validation pipeline | Configuration and contracts are testable. |
| 1 — Headless runtime | Phase 0 | Complete core services and Unix-socket API | Core behavior works through automated clients without hardware. |
| 2 — deckctl and V1 release | Phase 1 | Usable reference CLI and operational documentation | All SPEC §34 criteria pass. |
| 3 — Stream Deck+ | Phase 2 | Device input and dynamic presentation | Semantic control works with hardware and simulator coverage. |
| 4 — Developer integrations | Phase 2; Phase 3 optional | Git/project conventions and agent provider | Agent jobs obey the same execution and safety contracts. |
| 5 — Electronics | Stable Phase 1 parameter/provider contracts; Phase 3 for physical bench UX | SCPI and semantic instrument operations | Bench behavior passes simulator and controlled hardware checks. |
| 6 — Executable plugins | Proven provider contracts from earlier phases | Versioned subprocess plugin host | Compatibility, lifecycle, and failure isolation are verified. |

Follow the published sequence by default. Phases 4 and 5 must remain usable through the CLI, even if physical hardware work is delayed. These phase numbers denote delivery stages rather than permission to implement deferred scope during V1.

## 5. Phase 0 — Foundation

**Goal:** Establish a small, buildable repository with explicit contracts and strict configuration validation.

### 0.1 Repository and toolchain

- [x] Create `go.mod`, the package skeleton, and daemon/CLI entry points. Declare a supported Go version after checking the implementation environment.
- [x] Add build/version metadata: version, commit, build time, and clear development defaults.
- [x] Establish `gofmt`, `go vet`, lint, unit/integration tests, race tests, and build commands. Select and pin a small lint configuration with documented installation; avoid an unexplained all-rules policy.
- [x] Add CI on macOS for supported-platform behavior. Add other operating systems only where the implementation actually supports them.
- [x] Add a README describing current scope, build instructions, and links to `specs/SPEC.md` and `specs/PLAN.md`.

### 0.2 Domain and wire contracts

- [x] Define core types from SPEC §§7–16, stable errors, provider capabilities, and configuration structs without prematurely publishing internal implementation interfaces.
- [x] Specify JSON request/response examples for every V1 endpoint, including collections, jobs, confirmation, synchronous results, asynchronous admission, and errors.
- [x] Define lifecycle transition rules and default limits from section 3.
- [x] Define `slog` fields for component, event/action/job ID, duration, outcome, and sanitized error; provide test log capture with secret-leak assertions.

### 0.3 Configuration pipeline

- [x] Implement file loading, strict YAML decoding, supported-version checks, normalization, and field-level diagnostics.
- [x] Validate actions/provider-specific options, project overrides, workflow graphs, parameters, bindings, and all cross-references.
- [x] Keep validation independent of a running daemon or physical device so `deckctl config validate` can use it offline.
- [x] Ship `configs/example.yaml` with a real declared action name for each binding/workflow reference, explicit safety classifications, and a project path the user can change.
- [x] Add invalid fixtures for duplicate names/keys, ambiguous bindings, invalid values, unknown providers, missing references, cycles, unsafe templates, and unsupported versions.

**Deliverables:** Buildable skeleton, documented contracts, strict config loader, runnable example, test fixtures, and CI configuration.

**Verification:** Unit tests for valid/invalid YAML, normalization, error envelopes, and template grammar; build both commands; run formatting, vetting, linting, tests, and applicable race tests.

**Exit gate:** The example validates offline, malformed configurations fail with actionable diagnostics, and the initial package boundaries and protocol decisions are documented and committed.

## 6. Phase 1 — Headless runtime

**Goal:** Implement the complete V1 engine, including initial Git support. Build this phase as the following independently reviewable increments.

### 1.1 Runtime state, parameters, and persistence

**Depends on:** Phase 0 schemas and core types.

- [x] Implement project lookup and zero-or-one active selection, context defaults, updates, and defensive snapshot copies.
- [x] Implement all five parameter types, typed reads/writes, bounds, enums, numeric rotation, and persistent flags.
- [x] Implement state loading, reconciliation with configuration, dirty-state tracking, coalesced atomic writes, and shutdown flush.
- [x] Define the local state path and precedence: valid persisted values override configuration defaults; new values fall back to defaults.
- [x] Ensure state mutation remains responsive during disk writes. Report write failures and retain dirty state for bounded retry rather than silently declaring persistence successful.

**Verify:** Concurrent reads/updates, copy isolation, integer/float edges, persisted/nonpersistent behavior, restart recovery, missing/corrupt state, permission failures, failed writes/rename, and interruption leaving a complete previous state file.

**Done when:** Active project, mode/context, and marked parameters round-trip across restart and pass race tests.

### 1.2 Event bus, bindings, and feedback

**Depends on:** Phase 0 contracts; uses state services from 1.1.

- [x] Implement subscriptions, bounded queues, cancellation/unsubscribe, overflow accounting, and explicit bus shutdown.
- [x] Implement the binding matcher and overlap validation using SPEC §12 precedence.
- [x] Route control events to semantic action requests or parameter mutations through interfaces; action dispatch can use a test fake until 1.3 is complete.
- [x] Derive device-independent feedback states and display values; preserve optional progress without claiming precision a provider cannot supply.
- [x] Publish project, context, and parameter changes after state commit. Document owners and stop paths for subscription goroutines.

**Verify:** Multiple subscribers, stalled subscribers, concurrent unsubscribe/publish, full queues, ordered per-job events, ambiguous predicates, all binding tiers, unmatched controls, and simultaneous dial updates.

**Done when:** Synthetic events exercise bindings and parameter changes without hardware or unbounded queues.

### 1.3 Action registry and permission enforcement

**Depends on:** 1.1 and Phase 0 action contracts.

- [x] Implement action registration with duplicate rejection and stable action discovery metadata.
- [x] Resolve exact names, active-project overrides, declared argument schemas, working directories, and allowlisted templates.
- [x] Build the prepared invocation with immutable definitions and server-owned context; reject attempts to substitute executable paths, safety flags, or raw provider operations through undeclared arguments.
- [x] Implement the shared permission gate for direct actions, bound events, workflow steps, and later provider/plugin entry points.
- [x] Add configuration generation and correlation IDs to prepared invocations and safe diagnostics.

**Verify:** Duplicate names, unknown actions, absent/invalid projects, missing template variables, argument injection attempts, all three safety classes, override classification, and confirmation isolation between requests.

**Done when:** Every execution path must cross the same daemon-side validation and permission boundary.

### 1.4 Built-in exec, open, and Git providers

**Depends on:** 1.3; job integration follows in 1.5.

- [x] Implement provider lifecycle and health summaries with small interfaces and deterministic cleanup.
- [x] Implement exec using `os/exec.CommandContext` and argument arrays. Validate command, cwd, environment, timeout, and output limits; never add implicit shell interpretation.
- [x] Implement macOS process cancellation with a documented process-group strategy, bounded termination grace, forced cleanup where required, and child reaping. Isolate platform-specific code.
- [x] Implement open using the macOS opener with separate arguments, validated file/directory targets, and an explicit supported URL scheme allowlist. Return a structured launch result.
- [x] Implement Git using the installed Git executable and typed semantic operations for status, diff, log, pull, push, branch, stash, and stash-pop.
- [x] Define the supported branch/stash suboperations and arguments. Use command-specific validation and `--` where supported; never accept an arbitrary Git subcommand string under a safe operation.
- [x] Mark read-only Git operations safe; require confirmation for mutations such as pull, push, branch creation/deletion, stash, and stash-pop. Disallow or separately classify stronger options such as force operations.
- [x] Use machine-readable Git output where available, bound diff/log output, disable interactive credential/editor prompts, and report conflicts or missing tools as structured failures.

**Verify:** Use a compiled helper process for exit codes, large output, timeouts, cancellation, and descendant cleanup. Inject an opener fake. Exercise Git in temporary repositories with local bare remotes, covering each named operation, dirty trees, conflicts, invalid refs, missing executables, and cancellation. Require no external network or user repository mutation.

**Done when:** All four external-operation concerns—execution, cancellation, result capture, and policy—work through the provider boundary with failure-path coverage. Opening an application is documented as a launch operation; cancellation does not promise to close that application.

### 1.5 Job manager and bounded scheduling

**Depends on:** 1.3–1.4; uses 1.2 lifecycle events.

- [x] Implement bounded admission and workers, unique IDs, timestamps, progress/result snapshots, and configurable terminal history.
- [x] Keep queued/running jobs discoverable; trim only terminal history. Bound output and associated retained memory per job.
- [x] Implement queued and running cancellation, timeout propagation, deterministic terminal transitions, and repeated cancellation behavior.
- [x] Isolate provider panics at the job boundary, sanitize diagnostics, release capacity, and finalize the failed job without crashing the daemon.
- [x] Expose a shared wait mechanism for synchronous API calls and independent lifetime for asynchronous jobs.
- [x] Document each worker, dispatcher, watcher, and output reader's owner and shutdown path.

**Verify:** Admission under load, queue saturation, configured maximum concurrency, cancellation before start/during execution/after completion, simultaneous finish/cancel, provider panic, history trimming, bounded output, and repeated startup/shutdown without goroutine leaks.

**Done when:** Scheduling cannot grow memory or goroutines without bound and every admitted job reaches exactly one terminal state.

### 1.6 Deterministic workflows

**Depends on:** 1.3–1.5.

- [x] Execute named workflows through the same prepared-action, permission, and job paths as direct actions.
- [x] Snapshot the workflow definition/context once and preflight its complete reachable action graph before starting.
- [x] Record each attempted step's index, action, timing, result, and error, and distinguish unstarted steps after failure/cancellation.
- [x] Implement sequential execution, both stop-on-error modes, cancellation, aggregate results, and workflow display feedback.
- [x] Run steps within the parent job's execution capacity. Do not queue child jobs and wait while holding the only worker permit.
- [x] Enforce cycle detection and a documented defensive nesting limit if nested workflow actions are allowed.

**Verify:** Successful workflow, first/middle/last-step failure, continue-on-error, cancellation between and within steps, nested permission requirements, cycles, project overrides, and concurrency set to one.

**Done when:** Outcome and step order are deterministic; nested dispatch cannot bypass permissions or deadlock the scheduler.

### 1.7 Unix-socket API and daemon lifecycle

**Depends on:** 1.1–1.6.

- [x] Implement every route in SPEC §19, using request size limits, strict JSON validation, appropriate HTTP methods/statuses, and the stable error envelope.
- [x] Define context patch semantics for project, mode, and values; ensure project changes use the same validation as the dedicated project endpoint.
- [x] Return bounded job collections and structured output; validate unknown resources, malformed path values, duplicate/conflicting invocation options, and cancellation requests.
- [x] Protect socket creation and cleanup; reject a second daemon on a live socket and handle stale sockets conservatively. Start no TCP listener.
- [x] Expose version, uptime, config path, active project/mode, running jobs, and sanitized provider health through status.
- [x] Wire startup order: parse flags -> load/validate config -> acquire private listener -> restore state -> prepare services -> serve and report readiness (see RUNTIME.md for the locking rationale).
- [x] Wire SIGINT/SIGTERM handling: close admission -> cancel queued/running jobs -> stop/retire providers -> flush state -> close listener/subscriptions -> exit within the grace period. Drain existing handlers within the same bounded shutdown budget.
- [x] Use a temporary socket directory and ephemeral daemon fixture for API tests.

**Verify:** Every endpoint's normal/error cases, socket permissions, live/stale socket handling, malformed and oversized bodies, unsupported methods, client disconnect semantics, timeout mapping, forged internal events, status fields, and signal shutdown.

**Done when:** Automated clients can inspect and exercise the full headless runtime using only the documented local API.

### 1.8 Atomic reload and integrated runtime verification

**Depends on:** 1.1–1.7.

- [x] Implement reload as read -> validate -> prepare -> reconcile -> atomic activation -> publish. Keep reconciliation invisible until activation succeeds.
- [x] Retain prior configuration/resources on every failure; release unused candidate resources.
- [x] Test action/parameter/project removal and edits while requests, jobs, and state writes are active.
- [x] Verify that accepted jobs retain their captured definitions and new jobs use the new configuration generation. Document that a policy reload affects new admissions rather than retroactively undoing admitted work.
- [x] Add end-to-end fixtures combining bindings, actions, workflows, cancellation, persistence, reload, and shutdown.
- [x] Benchmark dispatch separately from action execution; measure startup, idle resource use, and rotary parameter update latency on documented hardware.

**Deliverables:** Working `deckd`, complete V1 API, built-in providers, state file, example configuration, integration suite, and initial performance report.

**Phase 1 exit gate:** All SPEC §33 headless behavior is usable without hardware. Unit/integration/race tests pass, reload failures preserve service, safety checks cover every ingress path, queues/history/output are bounded, and the work is documented and committed.

## 7. Phase 2 — deckctl and headless V1 release

**Goal:** Make developer automation usable entirely from the terminal and validate every V1 acceptance criterion.

**Depends on:** Phase 1 API and stable protocol contracts.

### 2.1 Transport and command surface

- [x] Implement Unix-socket HTTP transport with configurable socket path, request deadlines, cancellation, and actionable connection errors.
- [x] Implement `status` and `project list|current|use <id>`.
- [x] Implement `context show|set <key> <value>` with documented keys and routing of project changes.
- [x] Implement `action list|run <name>` and `workflow list|run <name>` with typed argument input, confirmation, and timeout options.
- [x] Implement `job list|show <id>|cancel <id>`.
- [x] Implement `param list|get <name>|set <name> <value>`; obtain parameter metadata to encode typed values correctly and leave final validation to the daemon.
- [x] Implement offline `config validate` and daemon-backed `config reload`; clearly distinguish the local file being validated from the daemon's configured file.
- [x] Add help/version output and consistently placed global options for socket path and JSON output.

### 2.2 Execution UX and output

- [x] Default `action run`/`workflow run` to submit an asynchronous job and wait for its outcome; provide `--async` to return the job ID immediately.
- [x] Implement `--confirm` as explicit invocation evidence. A confirmation-required response must explain how to retry without automatically reissuing work.
- [x] On Ctrl-C while waiting, request cancellation of that job, wait for a bounded acknowledgement, and return a documented cancellation exit code.
- [x] Separate human-readable output from stable JSON output. Emit JSON data to stdout, diagnostics to stderr, and define consistent exit codes for usage, transport, permission, execution, timeout, and cancellation failures.
- [x] Present per-step workflow outcomes, bounded command output/truncation, parameter units, active context, and meaningful provider health.
- [x] Use bounded polling with cancellation while awaiting jobs; avoid a busy loop.

### 2.3 Release verification and operations

- [x] Add CLI tests against an ephemeral real daemon, including each required command, JSON parsing, exit codes, offline validation, unavailable daemon, confirmation, and invalid reload.
- [x] Publish a quick-start workflow: configure a temporary project -> validate configuration -> start daemon -> inspect/select project -> run validation workflow -> cancel a long task -> rotate/set a parameter through API/CLI -> restart and verify state.
- [x] Document configuration fields/defaults, action naming/overrides, templates, safety limitations, API contracts, cancellation, reload behavior, logging, and troubleshooting.
- [x] Document a macOS user-level launchd setup and clean shutdown/uninstall procedure. Verify the service's PATH, environment, socket/state locations, and file permissions; installation remains an explicit operator action.
- [x] Provide reproducible build/release instructions for `deckd` and `deckctl`, with version metadata and example configuration. Exercise supported macOS architectures or record any unverified architecture clearly.
- [x] Run the V1 matrix in section 12, resolve material defects, and record performance measurements and remaining limitations.

**Deliverables:** Reference CLI, complete headless V1, user/API/configuration documentation, macOS operation instructions, and release verification evidence.

**Exit gate:** All twelve SPEC §34 criteria pass through a combination of automated tests and the documented CLI walkthrough. No hardware is required. All phase definition-of-done checks pass and work is committed.

## 8. Phase 3 — Stream Deck+ adapter

**Goal:** Translate physical input into daemon semantics and render authoritative runtime feedback.

**Depends on:** Phase 2; this phase does not block the headless V1 release.

### 3.1 Integration investigation and adapter boundary

- [x] Evaluate available macOS integration routes against keys, encoder rotation/press, touch, labels, reconnect, packaging, and distribution constraints. Verify current vendor documentation when implementing this phase.
- [x] Record the selected route, dependencies, transport to the Unix-socket daemon, permission requirements, and any required bridge. Keep device-specific code in a separate adapter subtree/process.
- [x] Build a fake device/adapter harness first so CI can replay input and inspect rendered feedback without hardware.
- [x] Specify stable device/control identifiers, input normalization, capability reporting, and reconnect/resynchronization behavior.

### 3.2 Input and display integration

- [x] Map key press/release and encoder rotation/press into validated control events. Use daemon bindings to select semantic actions and parameters.
- [x] Preserve rotation direction and delta, coalescing only where the total delta is preserved. Keep rendering throttled without dropping authoritative parameter changes.
- [x] Implement long press and touch interactions where supported, documenting unavailable capabilities. Define any additional event types as protocol extensions before using them.
- [x] Implement confirmation gestures tied to a particular pending invocation and context. A context change or expiry invalidates stale device confirmation.
- [x] Render action/job state, labels, parameter values/units, active context, errors, and disabled/disconnected state from daemon data.
- [x] Start with bounded snapshot polling. If streaming is justified, specify a bounded subscription endpoint, overflow/reconnect behavior, and mandatory snapshot resynchronization; keep V1 clients compatible.

- [ ] Complete the physical Stream Deck+ smoke matrix and record hardware feedback latency.

**Verification:** Simulator tests for input normalization, binding/context changes, rapid rotation, confirmation, disconnect/reconnect, stale feedback, and daemon restart. Run a hardware smoke matrix for each supported key/encoder/touch capability and measure feedback latency.

**Deliverables:** Isolated adapter, installation/configuration instructions, fake-device tests, and a capability matrix with hardware evidence.

**Exit gate:** A Stream Deck+ operates semantic actions and parameters, updates feedback and context correctly, and recovers from disconnects. No business logic or shell commands are embedded in device bindings. Core tests still run without hardware.

## 9. Phase 4 — Developer integrations

**Goal:** Add developer conventions and agent jobs using the proven runtime.

**Depends on:** Phase 2; physical controls are optional.

- [x] Refine Git results and UX based on V1 usage without removing the existing semantic operations or weakening mutation policies.
- [x] Define opt-in project conventions such as validate/test/build and language-specific overrides. Preview or document generated definitions and keep every effective action discoverable.
- [x] Add named reusable prompts with validation, explicit variable expansion, and project-scoped working directories. Treat prompt changes as configuration generations.
- [x] Define a small agent-provider capability and implement the Codex integration selected after checking its current supported interface.
- [x] Expose agent job status, bounded output, result summaries, timeouts, cancellation, provider health, and sanitized diagnostics through existing job/protocol conventions.
- [x] Declare and enforce the provider's effective permission floor for workspace writes, network use, and tools. Never classify an arbitrary agent prompt as safe merely because it is text.
- [x] Keep AI-generated decisions outside deterministic workflow control. Agent output is data unless a separate, explicitly authorized action consumes it.
- [x] Withhold instrument/hardware capabilities from agent execution by default. Any later delegation needs explicit capability restrictions and daemon-side checks.

**Verification:** Fake-agent contract tests for completion, streaming/output truncation, malformed output, missing credentials/provider, transport failure (the selected API provider has no subprocess), cancellation, timeout, and permission refusal. Gate optional real-provider smoke tests separately from deterministic CI.

**Deliverables:** Project conventions, prompt library, agent provider, documented credential/permission setup, and CLI examples.

**Exit gate:** Developer and agent actions are inspectable and cancellable through the same CLI/API, with no alternate safety path and no dependency on agent availability for core runtime operation.

## 10. Phase 5 — Electronics and SCPI

**Goal:** Control supported instruments through typed semantic actions and synchronized parameters.

**Depends on:** Stable provider, parameter, permission, and job contracts; Phase 3 is needed only for physical bench controls.

- [x] Define device configuration, capability/health models, per-device safe limits, and supported transport settings. Begin with configured TCP SCPI transport.
- [x] Implement bounded dial/read/write timeouts, message framing, response-size limits, cancellation, connection cleanup, and serialized access per instrument.
- [x] Build a fake SCPI server before connecting real equipment; keep transport and instrument-specific command mappings separate.
- [x] Add semantic operations for generator frequency/amplitude/output and scope capture with validated units, ranges, output state, and structured results.
- [x] Model desired and observed parameter values separately where hardware may reject or round settings. Surface synchronization failure rather than showing an unconfirmed value as applied.
- [x] Define safe reconnect and shutdown behavior, including explicit output-state policy. Do not automatically re-enable output after reconnect; do not blindly retry commands with side effects.
- [x] Use stronger permissions for hazardous output changes and optional raw SCPI. Ordinary clients receive semantic operations, not raw command authority.
- [x] Add sequential bench workflows using existing workflow semantics and safe examples with instrument-specific limits.

**Verification:** Simulator tests for partial reads, framing, delayed/malformed responses, disconnect/reconnect, concurrent requests, units/ranges, cancellation, rejected settings, and output safety. Conduct controlled hardware checks against explicitly supported instruments and record model/firmware assumptions.

**Deliverables:** SCPI transport/provider, supported instrument profiles, synchronized parameters, bench examples, and hardware validation notes.

**Exit gate:** Supported instrument operations are bounded, observable, cancellable where the transport permits, and policy-controlled. Hardware failure leaves the daemon and unrelated jobs operational; CI requires no bench equipment.

## 11. Phase 6 — Executable plugin protocol

**Goal:** Expose proven provider capabilities to versioned external subprocesses.

**Depends on:** Enough experience with built-in, agent, and SCPI providers to stabilize the smallest useful capability contract.

- [x] Specify a versioned JSON protocol over stdin/stdout with message framing, request IDs, handshake/version negotiation, capability discovery, health, execution, cancellation, and shutdown.
- [x] Define maximum message size, pending-request limits, deadlines, stderr handling, and behavior for unknown/duplicate/out-of-order messages.
- [x] Implement process supervision, bounded I/O, cancellation propagation, crash handling, and deterministic host cleanup. Do not use Go's `plugin` package.
- [x] Require an explicit executable allowlist/configuration entry and validate every message before it affects runtime state.
- [x] Apply daemon policy to exposed plugin actions; plugin-advertised metadata cannot lower the configured permission floor.
- [x] Document actual isolation guarantees. A subprocess boundary provides lifecycle/crash separation; filesystem/network confinement requires separate operating-system controls.
- [x] Supply a minimal example plugin and reusable conformance harness. Defer marketplace, automatic installation, and broad SDKs.

**Verification:** Golden protocol fixtures and conformance tests for compatible/incompatible versions, malformed/oversized frames, hung children, cancellation refusal, stderr floods, unexpected exit, and attempts to claim undeclared capabilities or forge internal state.

**Deliverables:** Protocol specification, host implementation, example plugin, compatibility policy, and conformance tests.

**Exit gate:** Supported plugins load, execute, report health, cancel, and terminate within bounds. A broken plugin cannot bypass daemon checks or crash the runtime through protocol handling.

## 12. Verification and acceptance matrix

### 12.1 V1 acceptance traceability

| SPEC §34 criterion | Implementation milestone | Required evidence |
| --- | --- | --- |
| 1. Start from valid YAML; reject invalid configuration clearly. | 0.3, 1.7, 2.1 | Valid/invalid fixtures, startup tests, offline CLI validation. |
| 2. Listen on a protected Unix socket. | 1.7 | Directory/socket mode checks, live/stale socket cases, no default TCP listener. |
| 3. Inspect status, context, projects, actions, jobs, parameters with deckctl. | 2.1–2.2 | CLI integration tests for every command in human and JSON modes. |
| 4. Change and persist active project and mode. | 1.1, 2.1 | API/CLI changes followed by daemon restart and restored-state assertions. |
| 5. Exec, open, Git, and workflow actions function. | 1.4, 1.6 | Helper-process, opener-fake, temporary Git repository, and workflow integration tests. |
| 6. Long actions are cancellable jobs. | 1.4–1.5, 2.2 | Queued/running cancellation, descendant cleanup, Ctrl-C, and timeout cases. |
| 7. Workflow failure/cancellation is deterministic. | 1.6 | Ordered step traces for both error policies and cancellation timing cases. |
| 8. Parameters validate types/bounds and publish changes. | 1.1–1.2 | All type/boundary cases, rotation/clamping, concurrent updates, change events. |
| 9. Permission and confirmation are daemon-enforced. | 1.3, 1.6–1.7 | Direct API, binding, override, and nested workflow attempts cannot bypass policy. |
| 10. Invalid reload preserves the valid configuration. | 1.8, 2.1 | Failed validation/preparation with active jobs; continued API use and unchanged generation. |
| 11. Core tests pass with the race detector. | All core increments | Passing `go test -race ./...` on the supported macOS CI runner. |
| 12. Core functionality requires no Stream Deck hardware. | Phases 0–2 | Clean test/release walkthrough using temporary files, fake devices/providers, and CLI only. |

### 12.2 Broader specification coverage

| Specification area | Plan location |
| --- | --- |
| Purpose, goals, non-goals, principles, architecture, layout (§§1–7) | Sections 1–2; Phase 0. |
| Events, actions, projects, context, bindings, parameters (§§8–13) | Section 3; milestones 1.1–1.4. |
| Workflows, jobs, feedback, safety (§§14–17) | Section 3; milestones 1.2–1.6. |
| Configuration, API, CLI (§§18–20) | Phases 0–2. |
| Adapter, providers, AI, SCPI (§§21–24) | Milestone 1.4; Phases 3–6. |
| Persistence, logging, concurrency, errors (§§25–28) | Section 3; milestones 1.1, 1.5, 1.7–1.8. |
| Testing, security, performance, shutdown (§§29–32) | Milestones 1.7–1.8; sections 12–13. |
| Delivery, acceptance, implementation rules, definition of done (§§33–36) | Roadmap, phase exit gates, sections 12–13. |
| Future direction (§37) | Preserved by device-independent contracts; extensions remain scoped to later phases. |

### 12.3 Test and performance policy

- Add meaningful normal, failure, and cancellation tests with each implemented feature. Required unit suites cover configuration, bindings, parameters, permissions, workflows, event bus, and state.
- Integration suites cover API, jobs, cancellation, exec, Git, and reload. CLI tests use a real ephemeral daemon with unique temporary sockets, state files, and repositories.
- Use controllable clocks, synchronization barriers, and injected processes/providers when practical. Avoid relying on arbitrary sleeps for concurrency correctness.
- Race-test shared state and lifecycle code at every natural checkpoint; run the full race suite before phase completion. Aim for 80%+ core coverage where meaningful and inspect uncovered safety/failure branches directly.
- Measure startup to readiness against the under-one-second target and local dispatch against the under-25-ms target, excluding provider execution. Record hardware, configuration size, load, and latency distribution rather than reporting a single unexplained average.
- Measure idle CPU and resident memory over a steady interval; record an initial baseline and investigate regressions. Exercise sustained rotation and slow subscribers to verify responsiveness and bounded memory.
- Keep performance benchmarks separate from pass/fail unit timing assertions on variable CI machines. Use repeatable reference-machine measurements for release target checks.

## 13. Execution discipline and risk management

### 13.1 Definition of done for every phase

Before declaring a phase complete:

1. Re-read the applicable specification sections and confirm the delivered scope and dependencies.
2. Complete the listed features and normal/failure/cancellation tests, with each goroutine's ownership and shutdown path documented.
3. Run formatting checks, `go vet ./...`, the selected lint command, `go test ./...`, applicable builds, and race tests. At phase completion run `go test -race ./...` for the complete supported codebase.
4. Verify the phase's exit criteria and update its documentation, example configuration, and protocol/schema decisions.
5. Review with Prism when available and resolve material findings. Record unavailability and the alternative review performed without claiming a Prism review occurred.
6. Document proposed architectural deviations before implementing them; update the specification when accepted implementation decisions materially refine it.
7. Commit at focused milestone boundaries and phase completion, including validation evidence in the change description.

Suggested Phase 1 commit boundaries are state, events/bindings, action permissions, providers, jobs, workflows, API/lifecycle, and reload/integration. Split larger increments further when that improves reviewability; do not postpone all verification until the final integration step.

### 13.2 Principal risks and mitigations

| Risk | Mitigation and decision point |
| --- | --- |
| Scope drifts into hardware, agents, or plugins before the core is usable. | Enforce the headless V1 gate and reject unimplemented provider types. Revisit extensions only in their phase. |
| Config reload changes a job while it runs or closes resources it still uses. | Immutable generation snapshots, transactional activation, reference-aware provider retirement; prove behavior in 1.8. |
| Nested workflows exhaust worker permits or bypass confirmation. | Parent-owned sequential execution, whole-graph preflight, shared internal permission checks; test with one worker. |
| Subprocess cancellation leaves children running or hangs on inherited pipes. | macOS process-group cleanup, bounded pipe/output handling, helper-process tests, and bounded shutdown. |
| Frequent rotations create races, disk churn, or stale labels. | Atomic parameter updates, coalesced persistence/rendering, bounded queues, and snapshot resynchronization. |
| Secrets escape through command errors, health, or logging. | Never log raw arguments/environment/output; sanitize errors and test with planted secret values. |
| Git or instrument operations have irreversible partial effects. | Conservative classification, preflight validation, no implicit retries/rollback, and structured partial outcomes. |
| Stream Deck integration cannot access the local socket or a desired capability. | Investigate route/bridge/capabilities in 3.1; keep the adapter isolated and preserve CLI operation. |
| Provider or plugin contracts expand before real requirements exist. | Use small internal capability interfaces; stabilize the external protocol only in Phase 6. |

### 13.3 Immediate starting sequence

1. Confirm the module identity and supported Go toolchain; create the Phase 0 skeleton and validation commands.
2. Record the section 3 contracts, especially configuration defaults, invocation envelopes, safety normalization, error mapping, and snapshot semantics.
3. Implement strict configuration loading and fixtures; make the example validate offline.
4. Complete Phase 0 verification and commit it.
5. Begin Phase 1.1 and progress through the dependency order to the headless V1 gate.
