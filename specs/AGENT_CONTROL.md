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
most eight suggestions. AC-1 displays an explanation and grants no execution authority.

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
`agent_context:1` advertises the selected-context/session contract. It does not
advertise proposal execution or patches before those increments are implemented.

No live provider account or physical controls have been verified by the deterministic
fixtures. Such checks require separately reviewed context and recorded device evidence.
