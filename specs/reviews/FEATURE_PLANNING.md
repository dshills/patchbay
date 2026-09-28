# Feature specifications and implementation plan review

**Date:** 2026-09-28\
**Scope:** Documentation for Capture & Compare, Shareable Recipes, and Agent Control Panel.\
**Entry point:** [Feature roadmap](../features/README.md)

## Delivered documents

Each feature has a separate specification and phased implementation plan. The
roadmap records shared contracts, dependency order, release boundaries and review
policy. The existing core specification and plan link to these extensions while
preserving the original Phases 0–7 and their pending hardware gates.

The specifications describe proposed behavior. No runtime, UI, plugin, instrument,
recipe installer, or agent execution capability is implemented by this change.

## Verification

- Seven feature documents, comprising the index and three spec/plan pairs.
- Fourteen implementation phases with goals, dependencies, tasks, verification,
  deliverables, exit gates and suggested commit boundaries.
- All 32 functional requirements and 25 acceptance scenarios are referenced by
  their respective implementation plans.
- Local Markdown links, code fences, new-file whitespace and tracked diff checks
  validated. Proposed CLI/API/configuration interfaces are explicitly labeled;
  they were not run against the current implementation.
- Existing source contracts inspected: action preparation, scheduler/job states,
  guarded control confirmation, public protocol types, configuration, SCPI limits,
  plugin v1 and text-only agent behavior.
- No executable code changed; the Go/browser/hardware test suites were not run for
  this documentation-only change. Planned test descriptions are not test evidence.

## Prism review scope

Prism CLI 0.5.0, provider `gemini`, model `gemini-3-flash-preview`. Reviews received
only the new feature documents and repository-specific review context. No runtime
configuration, credentials, implementation source files, logs, or selected user project
files were included. The user previously approved Prism as the primary reviewer.

Automatic approval initially rejected a command that left the provider implicit.
Prior successful review metadata verified the provider/model, and inspection of the
new review input found no credential-like tokens or real user paths. Naming that
destination explicitly and documenting the input scope allowed the reviews to run.
No approval remains outstanding.

Prism reviewed concatenated documents, so some reported line locations refer to
the combined input. Findings below are mapped to their actual feature contracts.

### First review

Run `639d53df99e45d35fc25b640396b9aef`: one medium and one low finding.

- **Recipe namespace collisions — addressed.** Publisher-declared IDs are not
  globally unique. Local installations now receive stable opaque IDs and use them
  for namespaces, updates and management APIs. Same-name packages can coexist;
  local bindings and saved provenance remain independent.
- **Implicit stopped-scope precondition — clarified.** The existing provider already
  requires stopped acquisition. The feature spec now states that requirement
  directly and includes running/armed/unknown/malformed-state acceptance cases.

### Second review

Run `4d1a73e004e41e1f643c68117ad7c5f1`: five medium and three low findings.

- **Secret use hidden by redaction — addressed.** Previews retain field names,
  argument positions and declared reference sources while masking values. Sensitive
  capture inputs are rejected before persistence; model-supplied sensitive inputs
  are rejected. Public approval digests must not expose guessable plain secret hashes.
- **Partial multi-file patch risk — strengthened, limitation retained.** All after-images
  must be staged, flushed and hash-verified before any target replacement. A durable
  journal and preimages precede the rename sequence. Individual atomic renames still
  cannot make a multi-file update atomic; partial effects and recovery remain explicit.
- **Scope state can change during transfer — addressed within observable limits.**
  Require a post-transfer status check as well as the existing pre-check. Changed or
  unknown final state fails the capture and marks retained data inconsistent. A
  run/stop cycle between observations is still undetectable; exclusive operator access
  and unknown acquisition-time labels remain necessary.
- **Require an operator acquisition timestamp — not adopted.** A current host time
  or user confirmation does not establish when a stopped trace was acquired. The
  specification records observation times and a separate operator assertion while
  preserving unknown acquisition time. Waveform comparison uses recorded sample
  coordinates and does not promise absolute temporal correlation.
- **Require a ZIP compression-ratio threshold — resource controls strengthened.**
  Absolute compressed/expanded limits were already required during reads. The spec
  now adds two concurrent imports, a 30-second total processing deadline, 64 KiB
  stream buffers and cancellation checks. A fixed ratio threshold is not required
  because it would reject valid highly compressible samples without replacing these
  absolute resource bounds.
- **Tombstone retention exceeds retry window — addressed.** Deleted-request receipts
  now expire 25 hours after original admission, covering the 24-hour unknown-ID window
  and five-minute allowed future timestamp. Detected clock rollback cannot shorten
  the deduplication guarantee.
- **Browser fragment cleanup — clarified.** Require `history.replaceState` before
  requests, memory-only tokens, no persistent storage and renewed launch after loss.
  This does not claim protection from malicious same-user software/extensions.
- **Same-name recipe selection ambiguity — addressed.** Local aliases are unique
  after normalization/case folding, with suggested ID suffixes. Lists show alias,
  short ID and version; ambiguous CLI selection is rejected.

### Final correction review

Run `2d0309a814d60fb45f4d425c4759ef62`: four medium and three low findings on the
correction diff.

- **Permit approval of truncated large patches — not adopted.** AC-5 deliberately
  limits files/bytes and requires the complete diff before approval. Vendor updates
  and large migrations are outside this increment; a digest alone does not reveal
  their effects. A CLI override would weaken the stated review contract.
- **Batch parent-directory synchronization — not adopted.** At most ten files can
  change, and the per-file durability boundary is intentional. No measured I/O
  problem was supplied. Optimizing this later requires evidence and equivalent
  crash-recovery guarantees.
- **Sensitive literals versus references — clarified.** A `sensitive` schema flag
  rejects captured argument/parameter values, including attempted reference objects.
  Existing locally configured provider environment is a distinct source; only
  name/source metadata can enter evidence. No client secret-expression syntax is added.
- **Trace invalidation retention — clarified.** Failed post-transfer checks preserve
  bounded trace data with suspect quality and a reason. Such data remains available
  for labeled diagnostics/export but is excluded from baselines and numeric deltas.
- **Orphan patch staging cleanup — addressed.** Persist staging intent before creating
  files. Recovery and normal completion clean only verified journal-owned staging
  objects; unknown/changed files are reported instead of deleted by a name pattern.
- **Missing integer recovery error codes — not applicable.** Patchbay uses stable
  string error codes with HTTP status mapping. These documents propose no integer
  recovery-code protocol. Per-file outcomes and existing/new string errors remain
  explicit implementation deliverables.
- **Short approval review time — improved.** Token expiry leaves the review visible.
  Explicit refresh revalidates the same content without a provider call; changed
  content requires renewed review. An expired suggestion may be explicitly copied
  into a new review session only after its original preconditions still match, with
  new identity, audit, preview and approval. No expired token is extended implicitly.

All findings have fixes, clarifications, or reasoned dispositions. These are not
zero-finding Prism passes. The final documentation corrections received local
consistency/link/traceability checks; runtime behavior remains unimplemented and
must receive the tests and phase reviews listed in the plans.

## Additional consistency decisions

The self-review made baseline ownership explicit, separated sample data from measured
runs, defined experiment action wrappers and recursion refusal, excluded a manifest
from its own payload hash inventory, and required an independently valid host config.
It also specified receipt lookup before consumed-token expiry, bounded context
previews, portable-to-local namespace mapping, and compatibility of saved evidence
after recipe removal. These refinements were included in the follow-up review inputs.

## Implementation status

All feature phases remain unstarted. Physical Stream Deck+/DG812/MHO954 validation,
live-provider data consent, signing/notarization and publication retain separate
implementation/release gates. Begin implementation with CC-1 when requested.
