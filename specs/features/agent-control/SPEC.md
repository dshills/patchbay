# Agent Control Panel specification

**Status:** Proposed, version 0.1\
**Date:** 2026-09-28\
**Plan:** [Implementation plan](PLAN.md)\
**Parent:** [Feature roadmap](../README.md)

## 1. Purpose and release boundaries

Help a user supervise AI-assisted project work from the workbench and Stream Deck.
Show what the agent has read, what it proposes, which action needs approval, what
is running, and what happened. Link results to Capture & Compare so explanations
and suggested changes can be checked against actual evidence.

The first release supports explanation, bounded structured proposals for locally
allowlisted actions/experiments, explicit approval, job tracking, cancellation, and
result comparison. A second increment adds review and application of bounded text
patches. The existing text-only agent provider remains available unchanged.

### User journeys

1. **Explain a result:** select two saved runs and a few project files; preview the
   exact context to be sent; request an explanation referencing those artifacts.
2. **Authorize work:** receive a proposal to run an existing benchmark with specified
   parameters; inspect its command, project and inputs; approve it; watch the job;
   compare the saved result with the selected baseline.
3. **Use the desk controls:** a key shows Needs review; open its full preview, use a
   dial to choose among pending proposals, and explicitly approve the selected one.
   A separate key cancels the selected job, with its identity displayed.
4. **Review an edit:** in the second increment, inspect an exact diff to selected
   project files, approve application, then separately approve validation. Preserve
   the baseline and compare results after the change.

No automatic multi-step agent loop, unattended approval, generic shell tool, remote
agent connection, MCP server, browser automation, automatic commit/push, dependency
installation, or agent-directed instrument action is part of this specification.
An agent can explain user-selected bench data; it cannot operate the instruments.

## 2. Functional requirements

| ID | Requirement |
| --- | --- |
| AC-F01 | Show agent generation, pending proposals, approved execution and terminal outcomes in one workbench view with clear job/run identities. |
| AC-F02 | Let the user select and preview exact files, run artifacts, and catalog descriptions before sending them to the configured provider. |
| AC-F03 | Accept only a bounded, strictly validated proposal schema referencing allowed configured actions, workflows or experiments and typed inputs. Model text never dispatches work. |
| AC-F04 | Construct a daemon-authoritative execution preview and bind approval to exact proposal, project, configuration, arguments, relevant values and expiry. Every agent-requested execution needs individual user approval. |
| AC-F05 | Execute approved work through existing runtime policy, scheduling and cancellation; prevent duplicated admission and automatic retries. |
| AC-F06 | Support Stream Deck selection, open-review, explicit approval and selected-job cancellation using guarded state. |
| AC-F07 | Save proposal decisions and execution links durably, with bounded private retention and honest restart/partial-effect reporting. |
| AC-F08 | Compare measured runs and attach agent explanations with source references, while distinguishing computed measurements from model interpretation. |
| AC-F09 | Reject unsupported capabilities, prompt-injected targets, malformed output, stale approvals, and attempts to invoke protected operations. |
| AC-F10 | In the patch increment, preview and apply exact bounded text changes with file preconditions, explicit approval, cancellation/recovery evidence, and separately approved validation. |
| AC-F11 | Preserve existing text-only agents, normal CLI use, recipe policy, plugin v1, and optional provider availability. Core workbench functions work without an AI account. |

## 3. Authority and architecture

The provider produces untrusted data. A new opt-in proposal adapter calls the
configured text-generation provider with an explicit catalog and selected context,
then strictly parses a complete final response. It never executes streamed partial
JSON, interprets prose as commands, or gives the model a runtime client/token.
The existing agent action path remains text-only and does not acquire tools.

The daemon owns the proposal validator, session store, action allowlist, preparation,
approval records, and dispatch. Workbench and Stream Deck are trusted local clients
of those operations. Reuse CC run/artifact storage and private browser transport.
Do not build a second scheduler or let provider output call `Invoke` directly.

### Allowed action catalog

Agent execution is disabled by default. Local configuration enables proposal mode
and supplies a per-project allowlist of exact action, workflow, and experiment IDs.
For each entry, store the effective definition digest and permitted input bounds.
Being named `test` or classified `safe` is not sufficient to enter the catalog.
Configured commands are trusted executable code, and may have filesystem/network
effects; review the effective command when granting access. This is policy-controlled
execution under the user's account, not an OS sandbox.

For each proposal, resolve project overrides and all nested workflow steps. Enforce
the strongest of normal provider policy and agent policy, with a minimum of explicit
confirmation even for otherwise safe actions. An agent cannot request or supply
`confirmed`, safety labels, shell strings, working-directory overrides, executables,
environment, model credentials, provider names, or alternate projects.

The initial catalog excludes all SCPI actions, dangerous actions, plugin execution,
open actions, Git writes, configuration/recipe management, other agent-generation
actions, and workflows containing any excluded step. Allowlisted exec actions and
read-only Git operations can be included by explicit local grant. Device prohibition
does not claim to sandbox an arbitrary user-granted exec binary; grants require
review of the configured implementation. Imported recipes cannot self-grant access.
An effective definition change invalidates its grant for new proposals until reviewed.

## 4. Context and data handling

Context selection is independent from action approval. Before each provider request,
show project, provider/model, selected files/artifacts, byte counts, and whether
content contains an unsaved/dirty working-tree version. Freeze exactly those bytes
for the request. Never let the model add files or follow references automatically.
Any follow-up generation with new content requires a new context preview.

Keep existing file confinement and size rules; new context additionally caps at
20 text files/artifacts and 256 KiB combined, or the smaller current provider limit.
Reject binary/unsupported artifacts; numeric series may be sent as explicit bounded
data or a deterministic summary whose transformation is shown. Reject symlink escapes,
protected credential files, and paths outside the selected project or CC artifact
store. Credentials remain in the daemon environment and never enter prompts, logs,
session exports, or result manifests. A secret scan can warn about selected text but
cannot guarantee arbitrary project content is free of secrets.

Context records keep content hashes and selection metadata. Store exact selected
snapshots only with explicit local-retention consent, within the same privacy/quota
controls as CC artifacts. Provider delivery itself requires explicit consent for
that selection; locally available content is not automatically authorized for upload.
The preview uses selected content bytes, not a model-written summary of those bytes.

Code, notes, logs and recipe descriptions are untrusted context and may contain
instructions. Catalog membership, strict schema validation and daemon policy must
enforce authority independently of the prompt. Valid-looking JSON cannot enlarge
the allowed action set. Agent explanations cite only actual context/run/artifact IDs;
unknown references are marked unsupported and never fabricated as evidence links.

## 5. Proposal and lifecycle contracts

### Proposal schema

| Field | Contract |
| --- | --- |
| `schema_version` | Integer 1; unknown versions and fields are rejected. |
| `summary` | Bounded human explanation, treated as untrusted text. |
| `context_refs` | IDs of already selected context items; no arbitrary URLs/paths. |
| `proposals` | At most 8 independent suggestions; no automatic ordering or dependencies. |
| Proposal target | Exactly one configured action, workflow, or experiment ID from the current catalog. |
| Proposal inputs | Declared typed scalars only; workflow arguments remain disallowed unless a CC experiment defines the mapping. |
| Expected outcome | Bounded explanation and optional existing baseline run ID; an expectation is never a measured result. |

The daemon assigns session/proposal IDs, generation and digests. It does not trust
IDs, timestamps, permission claims or tool-result messages fabricated by the model.
Limit each final proposal response to 64 KiB within the existing output/token cap.
Malformed/oversized output fails that generation with a visible reason and no action.
Do not silently regenerate or attempt to repair output through another paid request.
Users can explicitly request a fresh generation.

### State and execution

Session states are `generating`, `awaiting_review`, `executing`, `completed`, `failed`,
`cancelled`, and `interrupted`. Each proposal separately records `pending`, `rejected`,
`expired`, `invalidated`, `admitted`, and its linked job/run outcome. Rejecting every
proposal completes a session with no execution. Generating alone holds a provider
job; awaiting review consumes no worker permit or open provider connection.

Execute at most one proposal per session at a time. Multiple suggestions are choices;
approving one never approves later suggestions. After a linked job terminates, the
session returns to awaiting review if valid pending proposals remain; otherwise it
completes, preserving per-proposal failures. Generate-again starts an explicit new
request using a newly reviewed context selection. Cancellation invalidates pending
proposals and cancels an active generation/job; completed effects remain recorded.

Pending proposals expire after 10 minutes or on context selection, active project,
relevant parameter, configuration/catalog, or recipe-definition changes. Before
approval, recheck selected context file hashes and Git HEAD; a changed precondition
requires a new preview/generation as appropriate. These checks do not freeze the
whole checkout or prevent another application from editing it during execution;
CC start/end source observations expose detected changes.

### Approval transaction

1. The user selects a proposal and asks for its execution preview.
2. The daemon prepares the exact operation using current allowed definitions and
   returns full effective targets/arguments, project, selected context hashes, risk,
   expected effects, preparation digest, and a one-use approval token. Secret values
   are masked in place, preserving field names, argument positions and declared
   reference sources so the user can see where sensitive data would be used. Reject
   model-supplied sensitive inputs; inherited environment contents are never dumped.
3. The trusted client displays those details and offers an explicit Approve control.
   A compact deck label alone is insufficient for a command or patch preview.
4. Approval includes proposal ID, preparation digest, token and request ID. Under
   serialized admission, verify instance/generation, catalog grant, relevant context
   and value revisions, expiry, cancellation state, and capacity.
5. Persist the approval/admission identity and run reservation before provider
   dispatch. Consume the token only when admission succeeds. Duplicates return the
   admitted identity; altered/replayed content fails. A full queue leaves a valid
   token unconsumed until its original expiry; refreshing requires explicit review.

Approval tokens last 60 seconds, with at most 64 active previews. Follow CC's durable
request-ID deduplication/tombstone contract. Decisions must refer to the proposal
actually shown, not whichever proposal is currently newest. Selection/context changes
invalidate physical approval affordances. The model never receives approval tokens
or bridge credentials. Existing trusted CLI `confirmed:true` calls remain unchanged;
they cannot masquerade as an agent proposal decision in the proposal API.

Token expiry must not erase the displayed review. An explicit Refresh approval
revalidates the same proposal without a model call; identical content receives a
fresh token and preserves review position, while changed content requires a new
review. An expired proposal itself cannot be revived by a token refresh. The user
may explicitly duplicate an expired suggestion into a new proposal, using the same
validated model output only if all source/capability preconditions still match.
The duplicate starts a new session in `awaiting_review`, linked to its source session,
and counts toward normal session quotas. It has a new identity, audit entry, preview
and approval requirement; no provider job or upload occurs.

Same-user processes remain trusted by the local socket. Do not claim cryptographic
proof that a human read a preview, physical-presence attestation, or containment of
malicious local software. The guarantee is exact, explicit intent through Patchbay's
supported clients and no model-controlled route around that decision.

## 6. Results, persistence and failures

Each admitted proposal links to an ordinary job and a CC run. A target without
numeric collectors still records its bounded outcome and selected artifacts; it
does not invent measurements from log text. Persist session metadata and decisions
separately from immutable run evidence, with IDs/digests linking them.

Sessions are private, with a maximum of 100 retained sessions and 50 MiB of metadata;
artifact bytes count toward CC quotas. Limit concurrent generations to two globally,
one per session, and retain the existing provider deadline/output limits. Each click
authorizes one generation; no background polling of the model or hidden paid retries.
Display configured model, sent bytes, and actual usage when returned; estimated token
counts must be labeled estimates, and unknown price is shown as unknown.

Restart invalidates every pending approval. Interrupted generation/execution sessions
are marked interrupted, with linked CC evidence and unknown external effects where
applicable. Recover admitted request IDs before accepting new work. Never reissue a
provider request, patch or action on recovery. If audit persistence fails before
admission, nothing runs; if it fails after an effect, report the recording failure
without replay. Quota limits stop new sessions and leave ordinary workbench jobs usable.

Store failure, timeout, cancellation, provider rejection, invalid schema and permission
denial have separate visible outcomes. Model output that claims a job succeeded is
never substituted for its daemon-owned result. Explanations label source references,
computed facts and model interpretation. The user can open the underlying measurement
and compare it without making another provider request.

## 7. Stream Deck and workbench behavior

Workbench displays context selection, generation status, proposal queue, detailed
preview, job progress/logs, saved result and comparison. Show model text as escaped
text/restricted Markdown. The default screen and ordinary experiments remain useful
when no provider key exists; offer configuration guidance without blocking the UI.

Proposed deck controls are select proposal/job, open review, approve reviewed proposal,
reject proposal, and cancel selected job. Show project, short proposal/job identifier,
state, and whether an approval is still valid. Opening review does not approve.
Physical approval requires an active detailed preview in the paired workbench/CLI
and a matching daemon-issued preview token; store only the opaque selection/token
reference in adapter memory. A long press confirms the specific displayed selection.
Keyboard/browser approval is an equivalent path; hardware is optional.

Use bounded snapshots and existing control guards. Device disconnect, app restart,
daemon restart, new selection, expired preview or context change clears the approval
affordance. Cancel binds to the displayed job ID and current selection revision; it
does not select the most recent job implicitly. Cancellation is best effort and
cannot undo completed writes. Do not label it as an equipment emergency stop.

## 8. Bounded patch increment

AC-5 introduces a separate opt-in `workspace.apply_patch` capability with its own
daemon-generated preview. A model can propose a unified diff artifact, never write
directly. Patch proposals remain unavailable until capability discovery advertises
support; otherwise a patch is displayed as a suggestion only.

First-version limits: at most 10 existing tracked UTF-8 text files, 256 KiB per file,
1 MiB combined before/after content, all under the selected project root. No file
creation/deletion/rename, binary patch, executable-bit change, symlink, path outside
the root, Git metadata, credential file, or policy/configuration file modification.
Explicitly protect `.git`, `.patchbay`, `.agents`, `.codex`, `AGENTS.md`, environment
credential files, and locally configured protected paths. Patchable paths are a
user-selected allowlist. Preserve line endings and modes; reject unsupported forms.

Parse diffs strictly and apply exact hunks without fuzz. Bind preview/approval to
canonical paths, before-content hashes, after-content hashes, Git HEAD and a diff
digest. Show every changed line; truncated previews cannot be approved. Edits since
preview invalidate approval. Applying a patch cannot alter the catalog or authorize
validation, commit, push, install, instrument actions, or another model call.

Use a per-project Patchbay write lock and private preimages. Persist a preparation
journal with operation identity, intended temporary paths and expected hashes before
creating any staging file. Before replacing any target, stage every after-image in
its target directory, flush it and verify its hash; abort without changing targets
if any staging step fails. Durably mark the journal ready, then recheck each preimage
and atomically replace each target,
preserving mode and syncing the parent directory. Multi-file replacement is still
not atomic: failure/cancellation during the rename sequence can
leave a partially applied patch. Report exactly which files changed and retain
preimages for explicit recovery. External editors do not honor Patchbay's lock;
recheck each preimage immediately before replacement, surface detected races, and
document the remaining filesystem race limitation rather than promise isolation.

On restart, compare target hashes with before/after records to classify applied,
unapplied and externally changed files. Do not automatically replay or overwrite
them. Clean up only journal-recorded temporary files verified as owned staging
objects through no-follow checks; remove unused after-images on normal failure or
completion too. Changed/unverifiable files are reported for operator recovery, never
deleted by a filename-prefix sweep. Recovery retains preimages until explicit user
cleanup under the CC storage policy. Any requested restoration is a new exact diff
with its own preview and approval.
Validation is a separate proposal/action, with its own outcome and optional CC run;
failed tests do not trigger an automatic rewrite or revert.

## 9. Proposed interfaces

| Interface | Purpose |
| --- | --- |
| `GET /v1/agents/catalog` | Current granted project targets and typed inputs; no secret values. |
| `POST /v1/agents/context/prepare` | Freeze selected bounded context; return exact upload preview/digest and expiring consent token. |
| `POST /v1/agents/sessions` | Start one explicitly authorized generation with that context digest/token. |
| `GET /v1/agents/sessions` and `GET /v1/agents/sessions/{id}` | Bounded, paginated session/proposal/job/run state. |
| `POST /v1/agents/proposals/{id}/prepare` | Produce daemon-authoritative action or patch preview. |
| `POST /v1/agents/proposals/{id}/approve` | Admit exactly the reviewed proposal once. |
| `POST /v1/agents/proposals/{id}/reject` | Record rejection without execution. |
| `POST /v1/agents/proposals/{id}/duplicate` | Explicitly revalidate an expired suggestion into a new review session without a provider call. |
| `DELETE /v1/agents/sessions/{id}` | Cancel active work and invalidate pending proposals; retain the audit record. |
| `POST /v1/agents/sessions/{id}/forget` | Explicitly delete a terminal session's context/audit metadata subject to deduplication retention; linked runs remain until separately deleted. |

Context consent tokens last 60 seconds and bind frozen bytes, destination provider/
model, project, and request ID. Their duplicate-submission behavior follows CC admission
deduplication, preventing duplicate paid calls after a dropped response. Limit pending
context previews to 64 and frozen context bytes to 16 MiB globally; expired or consumed
previews release their memory. CLI commands
group under `deckctl agent` for context preview, start, list/show, proposal review/
approve/reject, cancel and forget. Physical clients gain dedicated proposal/job
selection DTOs; arbitrary inbound lifecycle events remain forbidden.

Advertise `agent_proposals:1` and, later, `agent_patches:1`. Reuse CC stale/conflict/
storage errors, with explicit `invalid_proposal`/422 and `context_changed`/409.
Update error validation/rendering and bridge allowlists together. Future external
agent or MCP integration needs separate authentication/capability design.

## 10. Acceptance scenarios

- **AC-A01:** Select two runs and files, preview their exact bytes, approve one provider
  request, and receive an explanation/proposal referencing only supplied evidence.
- **AC-A02:** Embedded hostile instructions, valid JSON naming an unlisted action,
  nested forbidden workflow steps, injected executable/environment fields, oversized
  output, and streamed partial JSON never dispatch work.
- **AC-A03:** Changed arguments, project, context hashes, parameter values, grant,
  configuration, recipe or expired token prevents approval. Two clients approving
  simultaneously produce at most one job, including dropped-response and restart cases.
- **AC-A04:** Approval runs an allowed action through normal policy; cancellation,
  timeout, queue full, partial output, and recording failure produce honest states.
  Waiting for approval holds no worker or provider connection.
- **AC-A05:** Deck selection/reconnect/context-change races cannot approve or cancel
  a different item. Browser/CLI users can perform the same actions without hardware.
- **AC-A06:** Restart invalidates pending approvals, preserves decisions and linked
  evidence, and makes no provider calls or action retries. Deletion retains required
  deduplication tombstones; quota exhaustion leaves ordinary tasks usable.
- **AC-A07:** Existing text-only agent actions and non-AI workflows retain behavior.
  Missing provider credentials disable only the relevant agent controls.
- **AC-A08:** A patch touching protected paths, altered preimages, unsupported diff
  forms or quotas is rejected. Inject failure/crash/cancellation between file writes;
  recovery reports each file accurately and never silently replays or reverts it.
- **AC-A09:** A reviewed patch is applied once, validation receives separate approval,
  and comparison uses measured results. A model's claimed success is never shown as
  a successful test or measured improvement without runtime evidence.

## 11. Product validation

Observe whether users can identify the proposed command, working project, selected
inputs, and expected effects before approving. Record mistakes in proposal selection,
approval expiry, and cancellation under concurrent jobs. Treat one unintended target
execution as a release blocker. Keep provider/API-key setup and physical-device
validation separate from the deterministic proposal/policy test suite.
