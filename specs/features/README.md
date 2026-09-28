# Patchbay feature roadmap

**Status:** Implementation underway. CC-1/CC-2 capture, comparison, and export are implemented; the workbench is in progress.\
**Date:** 2026-09-28\
**Baseline:** [Core specification](../SPEC.md), [foundation implementation plan](../PLAN.md), and the current macOS daemon, CLI, Stream Deck+, agent, SCPI, and plugin contracts.

## Product direction

Make Patchbay a useful workbench for developers and hardware makers: adjust a
parameter, run an experiment, compare the result, and share the setup. The first
experience must work without equipment, an AI account, or a development toolchain.
Physical controls add a tactile way to use the same actions and results.

The audience and adoption targets are product hypotheses. Validate them with
people doing real work before expanding the number of integrations.

## Specifications and implementation plans

| Feature | User outcome | Specification | Implementation plan |
| --- | --- | --- | --- |
| Capture & Compare | Save a run with its inputs and evidence, then see what changed. Includes the visual workbench and first-run experience. | [Specification](capture-compare/SPEC.md) | [Plan](capture-compare/PLAN.md) |
| Shareable Recipes | Load a useful control layout, experiment, and sample results with a clear preview of what will run. | [Specification](recipes/SPEC.md) | [Plan](recipes/PLAN.md) |
| Agent Control Panel | Inspect proposed actions, approve a specific operation, follow its job, and compare its results. | [Specification](agent-control/SPEC.md) | [Plan](agent-control/PLAN.md) |

Each feature has independent phase IDs: `CC`, `RP`, and `AC`. Existing Phases 0–7
retain their meaning and evidence. An unchecked item is planned work. Checked items link to implementation evidence. Unchecked CLI commands, configuration
fields, and API routes remain proposals.

## Recommended delivery order

| Release milestone | Included phases | Demonstration and gate |
| --- | --- | --- |
| 1. Try Patchbay | CC-1 through CC-4 | Download a bundle, open the workbench, change a demo parameter, save two runs, compare them, and export an offline report. |
| 2. Use it at the bench | CC-5 | Repeat the experience with physically verified DG812/MHO954 profiles and Stream Deck+ controls; preserve explicit apply/output/acquisition steps. |
| 3. Share a setup | RP-1 through RP-4 | Export a recipe and sample data, inspect it on a second installation, map local dependencies, and run it. |
| 4. Supervise an agent | AC-1 through AC-4 | Ask for an explanation, review a proposed configured action, authorize that exact action, and inspect the saved result. |
| 5. Review a proposed edit | AC-5 | Inspect a bounded project patch, apply it with explicit approval, and separately approve validation and comparison. |

RP-1 schema work may start after CC-1 stabilizes. RP-2 activation requires CC-2;
RP-3 UI requires CC-3. The software recipes can ship without CC-5, but the Rigol
recipe must retain a pending-verification label until CC-5 passes. AC-1 through
AC-4 require CC-1 through CC-4; recipe installation is optional for agent use.
AC-5 depends on AC-4. No feature requires the next feature to reach its own exit gate.

## Shared design decisions

1. **One runtime owns execution.** The existing scheduler, effective action
   resolution, permission floors, provider deadlines, and cancellation remain
   authoritative. Workbench and Stream Deck clients render state and submit intent.
2. **One record of a run.** Capture & Compare owns versioned run manifests,
   structured artifacts, comparison rules, and offline reports. Recipes reference
   those schemas; agent sessions reference run IDs. They do not invent alternate
   result stores.
3. **One workbench.** Start with embedded browser assets served by an explicitly
   launched local helper in `deckctl`. Keep `deckd` on its protected Unix socket.
   The browser bridge, authentication, and route restrictions are specified in
   [Capture & Compare](capture-compare/SPEC.md).
4. **Declared UI.** Layouts contain approved widget types and semantic references.
   No recipe JavaScript, custom HTML, remote images, expressions, or plugin UI code.
5. **Preview before a new grant.** Binding an imported command, sharing data with
   an AI provider, and approving an agent proposal are distinct user decisions.
   Existing trusted CLI calls retain their current permission semantics.
6. **Explicit versions and limits.** Strictly decode new schemas; reject unknown
   fields/versions. Add capability discovery for new clients. Keep v1 plugin NDJSON
   unchanged; executable plugins still return bounded text unless a separately
   specified protocol extension is implemented later.
7. **Local data and clear export.** Credentials and environment values are never
   stored in manifests. Outputs and notes can contain private data; export and AI
   context selection must show the exact selected material before it leaves its
   current local boundary. No automatic uploads or usage telemetry.

## Changes to the baseline

These proposals extend, rather than silently reinterpret, the current contracts:

- Durable run records are new; current job history is bounded and in memory.
- A loopback browser helper is new and opt-in. The daemon still has no TCP listener.
- Structured artifacts need new internal collection and wire types. They cannot be
  inferred from arbitrary stdout or assumed available in plugin protocol v1.
- Workflow-level argument binding is limited to declared experiment parameters;
  it must not add arbitrary expressions, branches, or model-selected steps to the
  existing workflow engine.
- Recipes add explicit configuration composition and transactional activation.
  Opening or cloning a project must never load executable definitions automatically.
- Agent proposals and bounded patch application add separate opt-in capabilities.
  Existing text-only agent actions retain their current behavior. Agent-directed
  instrument actions remain outside this roadmap's execution scope.

Implementers must update the relevant [API](../API.md), [runtime](../RUNTIME.md),
[development](../DEVELOPMENT.md), [SCPI](../SCPI.md), and
[operations](../OPERATIONS.md) guides as each extension lands. Proposed fields are
not valid in the current configuration loader.

## Review and completion policy

Use Prism as the primary review mechanism at each phase boundary and for changes
to execution, imports, browser access, and approval contracts. Record the run ID,
confirmed findings, fixes, and reasoned dispositions in `specs/reviews/`. A review
with findings is not a clean pass. If Prism is unavailable, record the limitation
and leave its review gate pending.

Each implementation phase requires its listed acceptance evidence, relevant
normal/failure/cancellation tests, current documentation, Prism review, and a
focused commit. Run the existing full `make check` gate at release milestones;
include browser checks and archive verification once those assets ship. Keep real
hardware, live-provider, signing/notarization, and publication evidence separate
from simulated and local checks. Writing these plans does not authorize publishing
a release, sending project content to an AI provider, or operating instruments.

## Adoption checks

For milestone 1, observe five new users on supported Macs. Target at least four
completing their first comparison within five minutes of launching the downloaded
bundle, without editing YAML or installing Go. Record install obstacles separately
from in-app completion time. For milestone 3, ask users to exchange a recipe and
record which local mappings required help. For milestone 4, verify that users can
explain exactly what a proposed action will do before approving it.

These are release learning targets, not claims of market demand or statistical
proof. Collect feedback by consent; do not add background telemetry to measure it.
