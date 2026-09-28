# Selected-context agents

Agent sessions are optional. Experiments, recipes and saved comparisons remain
usable without a provider key. Existing configured text-only `agent` actions keep
their behavior.

## Enable explanations

Choose your existing configured model and add:

```yaml
agents:
  codex:
    model: your-model-id
    max_output_tokens: 4096
  proposals:
    enabled: true
    protected_paths: [private-notes, confidential.txt]
```

The daemon uses `OPENAI_API_KEY` from its environment. Never place the key in a
prompt, recipe, shared configuration, command line or context file. The destination
is `https://api.openai.com/v1/responses`; redirects, tools, tool execution, request
replay and server-side response storage are disabled. This does not make a claim
about all provider retention policies. The existing stream transport is documented
in [OpenAI's Responses streaming guide](https://developers.openai.com/api/docs/guides/streaming-responses).

Open **Agent control** in the workbench. Select project-relative text files and
artifacts from saved runs, enter a question, and choose **Review upload**. Inspect
the exact JSON input, item hashes, provider, model, byte count and Git observation.
The checkbox authorizes one generation. Previewing, startup, polling, reconnecting,
reading history and malformed output never trigger a provider call.

Limits are 20 selected items, 128 KiB per project file and 256 KiB for the final
input including its JSON envelope. Files must be regular UTF-8 text without NUL
bytes or symlink path components. Selected artifacts must be UTF-8 text or JSON
from the same project. Numeric series are sent as explicit data within the same
budget; no hidden summarization is applied. Binary and oversized artifacts are
rejected. The current config, state/evidence/recipe/session stores, credential-like
paths, `.git`, `.agents`, `.codex`, `AGENTS.md` and configured protected paths are
excluded. A pattern warning is advisory; arbitrary secrets cannot be identified
reliably. The configured provider key itself is rejected if present in selected input.

Consent expires after 60 seconds and binds the destination, model, output token
limit, project, request ID and frozen input. Changed selected bytes, Git source
state, project, parameters or configuration generation require a fresh preview.
A later edit cannot change the already frozen request bytes. These checks do not
lock external editors or freeze the entire checkout.

## Sessions and privacy

Sessions have their own private store beside the configured state file, suffixed
`.agents`. Retention is bounded to 100 sessions and 50 MiB of metadata. There are
at most two active generations, 64 context previews and 16 MiB of frozen preview
input. Each generation also uses the ordinary bounded job scheduler and a ten-minute
deadline. Provider output is capped at 64 KiB and parsed only after completion.
The output format is schema 1 with a summary, selected context references and at
most eight suggestions. Explanation alone grants no execution authority; configured grants and a separate approval are required for every proposed operation.

By default the store retains selection metadata/hashes and the model response;
exact submitted context is retained only when **Retain the exact submitted context**
is checked. A model response may repeat selected text, so treat session history as
private even with snapshot retention off. Partial output exists only in bounded job
history. Unknown model references are labeled unsupported and have no evidence link.
Actual token counts appear when returned; absent usage and price remain unknown.
Model text is an interpretation, never proof that a command ran or tests passed.

The generation request is fsynced before provider dispatch. A dropped response can
be retried using the identical request ID and body; it returns the same session.
Changed content with that ID conflicts. Requests must be timestamped within 24
hours (at most five minutes ahead); deletion receipts survive for 25 hours.
Cancellation invalidates pending work and cancels the exact active job. Effects
already completed cannot be undone. Restart marks unfinished sessions interrupted
and never calls the provider again. A storage failure before admission prevents
upload; uncertain writes close further admission until restart. Forgetting a
terminal session deletes its local context and audit but retains a deduplication
receipt and leaves linked experiment runs alone.

## CLI and API

```sh
deckctl agent catalog
deckctl request-id
# Substitute that request_id into a selection JSON object:
deckctl agent context '{"prompt":"Explain these results","files":["results.txt"],"request_id":"TIMESTAMP-ID"}' --json
# Review the returned input before using its preparation ID and digest:
deckctl agent start PREPARATION DIGEST REQUEST_ID --confirm
deckctl agent list
deckctl agent show SESSION
deckctl agent cancel SESSION
deckctl agent forget SESSION --confirm
```

`agent page CURSOR` reads the next bounded 20-session page. The matching endpoints
are `GET /v1/agents/catalog`, `POST /v1/agents/context/prepare`, `GET|POST
/v1/agents/sessions`, `GET|DELETE /v1/agents/sessions/{id}` and `POST
/v1/agents/sessions/{id}/forget`. The last requires `{"confirmed":true}`. Capability
`agent_context:1` advertises the selected-context/session contract. Proposal execution has its own `agent_proposals:1` capability. Patches remain unavailable until their separate increment.

No live provider account or physical controls have been verified by the deterministic
fixtures. Such checks require separately reviewed context and recorded device evidence.

## Grant an action, then approve one proposal

`agent_proposals:1` adds execution review. An empty catalog grants no execution.
Inspect a configured target before editing local configuration:

```sh
deckctl agent grant action project.test --json
# Also supported: workflow TARGET or experiment TARGET.
```

The result shows effective reachable actions, workflow arguments, project, input
schemas and a local definition digest. Environment names and sensitive input
references remain visible; secret values are masked. Add the reviewed digest to
that project's `agent_grants`, then reload:

```yaml
projects:
  demo:
    name: Demo
    path: /absolute/project
    agent_grants:
      - kind: action
        target: project.test
        digest: COPY_THE_REVIEWED_64_CHARACTER_DIGEST
        inputs: {}
```

No generated content or recipe can add a grant. Supported targets are explicit
exec actions, Git `status`/`diff`/`log`, and workflows/experiments containing only
those operations. Every wrapper and nested step is checked. SCPI, plugins, open,
Git writes, dangerous actions, agent generation, recipe/config management and
recursion are excluded. Arbitrary granted executables remain trusted code running
as your user, with possible filesystem/network effects. This is not an OS sandbox.

The digest covers reachable effective definitions, workflow structure, experiment
mappings/collectors, project metadata/environment and inherited environment. It is
signed with a private persistent local key, so publishing the digest does not
publish a guessable hash of secret environment values. Changed definitions or an
inherited environment change invalidate the grant; inspect and replace the digest
explicitly. Grants are local and must not be copied to another installation.

Only inputs listed under a grant's `inputs` can be supplied by a model. Use the
same declared type and optional narrower `min`/`max`/`enum`; both original and grant
bounds are checked, including effective defaults. A grant cannot add defaults or
sensitive inputs. Omitted action inputs retain configured defaults; workflows have
no top-level inputs. Experiment inputs use mapped parameter names, affect only the
prepared run, and leave persistent parameter values unchanged. The uploaded catalog
contains these allowed targets and public input schemas; review it with the rest of
the exact upload.

Final schema-1 suggestions have `kind`, `target`, optional scalar `inputs`,
`rationale`, `expected_outcome`, and optional existing `baseline_run_id`. Unknown
fields, versions, non-scalar inputs, more than eight suggestions or truncated output
fail visibly without another generation. Invalid targets are marked invalidated;
valid suggestions become independent pending proposals for ten minutes.

```sh
deckctl agent show SESSION
deckctl agent review PROPOSAL --json
deckctl request-id
# Review the full command, argument positions, project and context hashes:
deckctl agent approve PROPOSAL PREPARATION DIGEST REQUEST_ID --confirm
deckctl agent reject PROPOSAL
# An expired pending suggestion can be explicitly revalidated without a model call:
deckctl agent duplicate PROPOSAL NEW_REQUEST_ID
```

Each review creates a one-use, 60-second approval. At most 64 active proposal
previews / 16 MiB of private prepared plans are retained. Approval binds the exact
proposal, context, selected file hashes, Git observation, config generation,
parameter/control revision and effective grant. A pending on-disk config edit also
blocks approval until reload/review. No generic `confirmed` capture call can execute
an agent's private prepared plan. A full queue preserves the token until expiry.

Run reservation and the session decision are both durable before dispatch. The run
contains immutable agent session/proposal provenance and the exact approval request
digest. Same-request retries return the same job/run after a dropped response or
restart. An audit failure after reservation records an unexecuted failed reservation
and closes further agent admission; it never dispatches the action. Crash recovery
links reserved runs to sessions and never retries them. One proposal per session
may execute; other proposals remain choices requiring their own reviews. After
execution, changed preconditions invalidate remaining suggestions.

Actions/workflows without collectors still save bounded daemon-owned step outcomes
and timestamps. Experiments retain their declared measurements and artifacts, and
can be compared through the ordinary run comparison API. Expected outcomes and
model explanations never count as test results. Sensitive defaults are masked in
preview and run metadata while preserving argument positions and input references;
configured executables can still print them in ordinary job output.

Additional endpoints: `GET /v1/agents/grants/{kind}/{id}` and `POST
/v1/agents/proposals/{id}/{prepare,approve,reject,duplicate}`. Prepare/reject accept
an empty object; duplicate takes `request_id`; approve takes `preparation`, `digest`,
`request_id` and `confirmed`. These routes never treat model text as authorization.

## Review and supervise

A completed session lists each suggestion with its short identity and state. Choose
**Review** to see the full effective action, project, arguments, masked sensitive
input positions and context references. Check the review box and choose **Approve
once**. Open the measured result to see actual daemon outcomes; the ordinary
baseline comparison also works for these runs. A model-supplied baseline is a link
to an existing same-project run, never an invented measurement.

The 60-second approval token can expire while you read. The full displayed review
stays on screen. **Refresh this review and pair deck** rechecks the same suggestion
without calling the provider. Unchanged effective content preserves your review;
changed content clears the checkbox and returns the preview to its start. An
expired ten-minute suggestion can be explicitly duplicated, with a new identity,
fresh preconditions and a new review. No completed action is automatically repeated.

All browser controls work with the keyboard and show text states. A lost connection
pauses approvals. A failed approval response shows an unknown outcome and the exact
request needed for a deliberate retry; the browser never retries it automatically.

### Optional Stream Deck supervision

Bindings use semantic operations, for example:

```yaml
bindings:
  - control: key-1
    press: {agent: select_proposal}
  - control: key-2
    press: {agent: review}
  - control: key-3
    press: {agent: approve}
  - control: key-4
    press: {agent: reject}
  - control: key-5
    press: {agent: select_job}
  - control: key-6
    press: {agent: cancel}
```

Select cycles through pending proposals or active jobs in the current project.
The screen shows the project, state and short identity. **Review** opens the selected
proposal in a connected workbench. Approval requires that full preview to remain
open, visible and connected; its lease lasts five seconds and polling renews only
that exact lease. A short press requests the existing five-second hold challenge.
Hold the same control to approve the same selection. A reconnect, new selection,
project/configuration change or expired lease requires an explicit fresh review.
A compact deck label alone cannot authorize execution. Cancel addresses the exact
selected job and never follows a replacement job in that session.

Capability `agent_supervision:1` adds `GET|PUT /v1/agents/selection` and `POST
/v1/agents/review`. Selection mutations require the current revision. Review links
bind that revision, preparation and digest; `renew:true` cannot resurrect an expired
lease. Control events carry `agent_revision` alongside the existing daemon guard.
Generic `confirmed` events cannot operate agent decisions.

Software fixtures exercise the adapter and daemon. A physical Stream Deck walkthrough
remains pending; no device/app/firmware compatibility claim is inferred from fixtures.

## Try the optional offline agent demo

Start `deckd --config configs/benchmark-agent.yaml` and open its workbench. This
example explicitly sets `agents.proposals.demo: true`. The destination is
`local://patchbay/benchmark-fixture`, the model label is `offline-demo-v1`, and no
provider request or charge occurs. It produces a fixed explanation and can suggest
only the locally granted `benchmark` experiment. It does not analyze selected data.
No targets are granted in the shipped file.

1. Inspect `deckctl --config configs/benchmark-agent.yaml agent grant experiment
   benchmark --json`. Review the effective command and project.
2. Add that returned digest to `projects.benchmark.agent_grants` in the local config:
   `{kind: experiment, target: benchmark, digest: YOUR_REVIEWED_DIGEST}`. Reload the
   config. Digests are private to this installation and its effective environment;
   never copy somebody else's digest or have a recipe grant itself access.
3. Capture one ordinary baseline. In Agent control, choose a small text file or run
   artifact, review the exact local input, and generate once.
4. Review the proposed benchmark, approve it once, open its measured result and
   compare with your baseline. The fixed explanation cannot establish improvement.

For a real model, set `demo: false`, configure `agents.codex.model`, and provide the
existing key through the daemon environment. Review the now-remote destination and
exact selected bytes before consenting. The offline fixture is never an automatic
fallback for a missing key or provider failure. It does not change legacy text-only
agent actions. No recipe installation is needed; disabled recipe grants stay excluded.

The packaged native smoke test is `python3 scripts/verify_agents.py --bundle .`.
It removes the provider key, uses a private temporary project, grants a bounded
local benchmark, checks duplicate generation/admission, and compares two real helper
runs. It tests no live model, user usability, physical instrument or signed release.

## Upgrades and recovery

Clients check `agent_context`, `agent_proposals` and `agent_supervision` before using
new contracts. Older daemons can still serve ordinary workbench functions. Pending
previews are process-local and must be reviewed after restart. Session requests and
run admissions retain their durable identities; no generation or action is replayed.
Stored private metadata remains bounded and records unsupported/interrupted outcomes.
Keep the state, `.agents` and run stores together when moving an installation so its
grant signing key and retained evidence remain consistent. Explicit forgetting removes
a terminal session; linked runs have separate retention and deletion controls.
