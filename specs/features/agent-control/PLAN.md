# Agent Control Panel implementation plan

**Status:** AC-1 through AC-5 software implemented; external validation pending\
**Specification:** [Agent Control Panel](SPEC.md)\
**Dependencies:** CC-1 through CC-4 for evidence, previews, workbench and packaged onboarding. Recipes are optional.

## AC-1 — Selected context and visible sessions

**Outcome:** Users can request an explanation of selected evidence and follow the
generation without granting the model execution authority.

**Depends on:** CC-4 and the current bounded text-only agent provider.

- [x] Add explicit proposal-mode configuration, feature discovery, context selection
  DTOs, destination/model preview, frozen-byte digests and expiring upload consent.
  Preserve existing agent action semantics and provider/file limits.
- [x] Add bounded selection of CC artifacts and project text files with confinement,
  protected-path checks, optional deterministic series summaries, and explicit local
  snapshot-retention choice. Avoid environment/config/credential dumps.
- [x] Add session metadata storage and links to provider jobs/CC artifacts, including
  durable generation request IDs, quota handling, cancellation and interrupted recovery.
- [x] Implement one explicit generation request per user action; expose actual usage
  when available, label estimates, and make provider errors/missing-key states useful.
- [x] Build context preview, generation status, explanation and source-link views in
  the workbench and `deckctl agent`. Invalid source references have no evidence link.

**Verification:** Fake provider with streamed/truncated/time-limited output; exact
request-byte assertions; changed/symlinked files; planted secrets; unknown references;
duplicate paid-request submissions; session quota; cancellation/restart; no automatic
provider calls on startup, preview, reconnect or failed output parsing.

**Deliverables:** Context/session contracts, read-only explanation flow, API/CLI/UI,
privacy guide and deterministic provider fixtures.

**Exit:** AC-F01/F02/F07/F08/F11 explanation scope and AC-A01/A06/A07 pass. No generated
content can execute an action. Optional live-provider checks require a separately
authorized context selection; fixture evidence never claims a live account was tested.

**Commit boundary:** Context/consent; durable sessions; explanation UI and CLI.

AC-1 review and deterministic evidence: [AC1.md](../../reviews/AC1.md).

## AC-2 — Structured proposals and exact approval

**Outcome:** A user can approve one precisely described, locally granted operation.

**Depends on:** AC-1 and CC preparation/admission contracts.

- [x] Define strict versioned proposal schema, final-output parser, bounded suggestions,
  independent IDs/digests, and context-reference validation. Never parse partial text
  into actions; malformed output produces a visible failure without automatic retry.
- [x] Add per-project action grants with complete effective-definition digests and
  typed input narrowing. Resolve all overrides/nested steps and reject protected
  provider operations, recursion, agent generation and recipe/config management.
- [x] Construct previews from actual prepared runtime definitions, including effective
  command, arguments, project and relevant preconditions. Treat model rationale as
  explanatory text only. No model-controlled field supplies confirmation or policy.
- [x] Preserve positions and declared reference sources for masked secrets, reject
  model-supplied sensitive inputs, and test that redaction never hides secret usage.
- [x] Add token expiry, selection revisions, generation/context/input invalidation,
  explicit approve/reject, queue-full behavior and exact request-ID deduplication.
- [x] Integrate durable approval plus run reservation before dispatch, using the
  existing scheduler and permissions. Audit failure must fail closed before effects.
- [x] Implement per-session sequencing, no worker held while awaiting review, one
  approved proposal at a time, remaining-proposal reevaluation and cancellation races.
- [x] Record child jobs as CC runs with declared collectors or bounded outcomes;
  attach baseline links and comparisons without inferring measurements from logs.

**Verification:** Injection corpus with plausible allowed-looking but invalid targets;
unknown fields/versions; provider/policy bypass attempts; nested workflow restrictions;
grant changes; stale context and parameter edits; concurrent approve/reject/cancel;
token reuse; worker saturation; crash between audit/run reservation/dispatch; and
restart/dropped-response duplicate admission. Run full race checks for changed state.

**Deliverables:** Proposal validator, grant/approval service, daemon contracts, CLI
review/approve/reject, and threat/failure test matrix.

**Exit:** AC-F03/F04/F05/F09 and AC-A02/A03/A04 pass with deterministic fakes and real
configured helper commands. Every generated operation requires its own explicit review.

**Commit boundary:** Proposal parser/grants; preview/approval; execution/audit lifecycle.

AC-2 review and deterministic evidence: [AC2.md](../../reviews/AC2.md).

## AC-3 — Workbench and physical supervision

**Outcome:** The user can manage concurrent work and operate the selected proposal
or job confidently from the screen and Stream Deck.

**Depends on:** AC-2; existing adapter snapshot/guard behavior.

- [x] Add proposal queue, full effective-action preview, expiry/invalidated states,
  approve/reject, live job links, cancellation and result comparison to the workbench.
- [x] Preserve the displayed review after token expiry and support explicit revalidation
  without another model call. Changed content resets review; duplicating an expired
  suggestion requires fresh identity, precondition checks, preview and approval.
- [x] Add semantic proposal/job selection and preview-reference DTOs for adapters.
  Do not route agent decisions through a generic unguarded `confirmed` event.
- [x] Add deck controls for select, open review, approve reviewed selection, reject,
  and cancel selected job. Show short identities and project alongside state.
- [x] Bind physical approval to the active full preview token and selection revision;
  clear it on reconnect, new selection, context change, expiry or restart.
- [x] Add keyboard equivalents, visible focus, non-color status labels, missing-provider
  guidance and reconnect recovery; never require hardware for the core workflow.

**Verification:** Browser-to-daemon proposal flow; adapter fake app plus real daemon;
two clients selecting/approving concurrently; expired previews; delayed snapshots;
context switches; wrong-job cancellation; disconnect/reconnect. Then run a controlled
physical Stream Deck walkthrough and record model/app/firmware and behavior.

**Deliverables:** Agent workbench, adapter controls, accessible interaction walkthrough,
fake-app regressions and separate physical verification report.

**Exit:** AC-F06 and AC-A05 pass in software; hardware claims require recorded physical
evidence. The on-screen route remains releasable with an explicit pending hardware gate.

**Commit boundary:** Proposal UI; adapter protocol/controls; UX and hardware evidence.

AC-3 software review and verification: [AC3.md](../../reviews/AC3.md). The physical walkthrough remains pending.

## AC-4 — Release the supervised action loop

**Outcome:** Users can explain results, approve an action, and compare its measured
outcome using a supported packaged release.

**Depends on:** AC-3. This is the first agent-control release boundary.

- [x] Ship an optional Benchmark Playground agent example with explicit local grants,
  selected context, bounded proposal outputs and existing baseline comparisons.
- [x] Document exactly what enabling proposal mode grants and which controls require
  provider access. Keep the ordinary workbench and sample data available without it.
- [x] Validate packaged assets, feature/version negotiation, upgrade/recovery, session
  and artifact retention, privacy defaults, and behavior when a recipe is absent/disabled.
- [x] Run the adversarial corpus and full release gate, including crash, duplicate
  admission, replay, protected-operation, stale-preview and cancellation tests.
- [ ] Observe users interpreting previews and selecting the intended proposal/job.
  Resolve wrong-target behavior before release; record usability outcomes by consent.
- [x] Run Prism review and record all finding dispositions. Keep optional live-provider,
  device and platform evidence separate from deterministic CI results.

**Verification:** Full `make check`, browser tests, native packaged demo with a fake
provider, archive verification, text-only agent regressions and offline ordinary
workbench smoke. Any live test uses an explicitly reviewed data selection and the
user's configured provider; it is not a CI prerequisite.

**Deliverables:** First agent-control release candidate, operator documentation,
explain/approve/compare demo, evidence and Prism report.

**Exit:** AC-A01 through AC-A07 have applicable evidence and no confirmed unresolved
execution/approval defects. AC-F10 and AC-A08/A09 remain explicitly pending AC-5.

**Commit boundary:** Example/configuration; release hardening; documentation/evidence.

AC-4 software evidence: [AC4.md](../../reviews/AC4.md). User observation, live provider, physical devices, Intel execution and signed-distribution gates remain pending.

## AC-5 — Review, apply, validate a bounded patch

**Outcome:** The user can inspect an exact proposed edit, authorize its application,
then independently authorize validation and compare the outcome.

**Depends on:** AC-4; this is a separate feature increment and capability flag.

- [x] Define strict patch artifacts and explicit user-selected patchable paths. Reject
  protected/config/credential paths, binary files, symlinks, path escapes, untracked
  targets, file create/delete/rename, mode changes, oversized files and fuzzy hunks.
- [x] Implement a daemon-owned `workspace.apply_patch` capability outside the text
  provider. Calculate canonical before/after contents and hashes; provide the complete
  diff for workbench/CLI review. Truncation prevents approval.
- [x] Bind authorization to file hashes, Git HEAD, proposal/diff digest and exact targets.
  Recheck under a per-project Patchbay write lock immediately before each replacement.
- [x] Persist staging intent before file creation; stage, flush and hash-verify every
  after-image before replacing any target. Add private preimages, ready-journal commit,
  atomic per-file updates, verified staging cleanup, exact progress and partial
  failure/cancellation. Document external-editor and rename-sequence limits.
- [x] Reconcile interrupted writes from before/after/current hashes without replay or
  automatic revert. Present changed/unchanged/conflicted files and let a user request
  a separately reviewed restoration diff when appropriate.
- [x] Add separate validation proposals and CC comparison links. Applying a patch
  never grants tests, commits, pushes, another generation, or subsequent edits.
- [x] Extend capability negotiation, grant configuration, artifact limits, docs, release
  packaging and fake/live demonstrations; preserve text-only proposal fallback.

**Verification:** Temporary Git repositories with dirty files; exact and malformed
diffs; CRLF/Unicode edge cases; symlink swaps; changed preimages; protected files;
duplicate approval; concurrent Patchbay patches; external editor changes; permission
and disk errors; kill/cancel after each file; unknown post-crash file contents; and
restoration requiring fresh approval. Validate before/after source and run comparisons
with deterministic helper tests, then a user-reviewed real project example.

**Deliverables:** Bounded patch capability, complete diff UI/CLI, recovery workflow,
patch-specific adversarial tests, documentation and Prism review evidence.

**Exit:** AC-F10 and AC-A08/A09 pass. Every changed file has traceable approved content
or an explicit partial/unknown outcome. Patch failure never starts an automatic agent
repair loop or silently overwrites a conflicting external edit.

**Commit boundary:** Patch schema/parser; apply/journal/recovery; review/validation UI;
release evidence. Prism review is required before enabling write capability by default
in any example; the capability itself remains opt-in for users.

AC-5 review and deterministic evidence: [AC5.md](../../reviews/AC5.md). A user-reviewed live project/model demonstration remains pending; temporary fixtures are not a live-user result.

## Acceptance traceability

| Requirements/scenarios | Owning phases |
| --- | --- |
| AC-F01/F02, AC-A01 | AC-1, AC-3 |
| AC-F03/F09, AC-A02 | AC-2 |
| AC-F04/F05, AC-A03/A04 | AC-2 |
| AC-F06, AC-A05 | AC-3 |
| AC-F07, AC-A06 | AC-1, AC-2, AC-4 |
| AC-F08 | AC-1, AC-2, AC-4 |
| AC-F10, AC-A08/A09 | AC-5 |
| AC-F11, AC-A07 | AC-1, AC-4 |

## Review and future scope

Follow the [shared completion policy](../README.md), including Prism as the primary
review mechanism, meaningful normal/failure/race tests, current docs and focused
commits at each phase. Review proposal admission and patch recovery separately so
UI work cannot hide a gap in authority or durability.

External coding-agent adapters, MCP, autonomous retry/repair, multi-agent orchestration,
and hardware action proposals require separate specifications. Their absence does
not block the explain/approve/compare experience described here.
