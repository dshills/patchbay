# Developer integrations

## Phase 4 integration decision

Recorded before implementation, 2026-09-27.

Use Codex models through the OpenAI Responses API for the initial agent
capability: a named prompt plus explicitly configured project files produces
streamed text. The current [Codex model documentation](https://developers.openai.com/api/docs/models/gpt-5.3-codex)
supports Responses and streaming. The [streaming contract](https://developers.openai.com/api/docs/guides/streaming-responses)
provides typed text and terminal events.

The alternative `codex exec --json` interface is supported, and local CLI
0.155.0 was inspected. Its local tools, plugins, hooks and layered configuration
require a wider execution capability than this phase needs. This provider uses
direct HTTPS with no tools, workspace writes, subprocesses, MCP servers, or
instrument capabilities. The working directory scopes the explicit file context;
it does not grant the model filesystem access. This refines SPEC §23 without
changing the provider/runtime boundary. Autonomous edits and command execution
are not part of this capability.

All agent actions have at least `confirm` safety because prompts and selected
files leave the machine and inference may incur charges. The runtime checks this
floor before loading files or making a request. Output remains data: workflow
steps cannot turn it into new actions, commands, or parameters. The API destination
is fixed to `https://api.openai.com/v1/responses`; redirects, ambient proxies and
automatic retries are disabled. Only the daemon's existing `OPENAI_API_KEY` is
used, exclusively in the authorization header. No credentials are stored in
configuration, results or logs. Health checks report local readiness, not account
entitlement or network reachability. Real-provider tests remain opt-in.

Project conventions are opt-in declarative command definitions. Generated
validate/test/build actions use the same exec provider, project overrides,
inspection and confirmation rules as handwritten actions. Generation examines
configuration only and never executes project code or scans for build files.

## Configuration and conventions

See [the complete example](../configs/developer.yaml). Nothing is generated until
that project opts in with `conventions: [validate, test, build]`. Only listed
operations are added, under the names `project.validate`, `project.test`, and
`project.build`. Defaults are explicit and do not depend on detected files:

| Project language | validate | test | build |
| --- | --- | --- | --- |
| `go` | `go vet ./...` | `go test ./...` | `go build ./...` |
| `node`, `javascript`, `typescript` | `npm run lint` | `npm test` | `npm run build` |
| `python` | `python3 -m compileall .` | `python3 -m unittest discover` | `python3 -m build` |

These are argument arrays, not shell snippets. Commands run in the selected
project and require confirmation because build/test tools can execute project
code and write files. Install required tools and Python build packages yourself.
An unavailable executable fails preflight without running any workflow steps.

`conventions.<go|node|python>.<operation>` replaces the default `command`/`args`
for that language. A project's `actions.project.test` (or another selected
operation) replaces the complete generated definition. Overrides cannot reduce
the generated confirmation floor or an existing global action's safety. An
opted-in convention takes precedence over a global action with the same name;
projects without that opt-in retain the global action, if one exists.

`deckctl action list` reports the effective actions and their `convention:<language>`
origin. Workflows may reference generated actions; invocation fails before any
step runs when the selected project does not provide a required action. Physical
bindings to a project-only convention require `when: {project: <id>}`.

## Prompts and agent actions

`prompts` maps names to UTF-8 text, at most 64 KiB per prompt. Agent actions name
`provider: codex`, a `prompt`, and optional `files`, `cwd`, `inputs`, `timeout`,
and `safety`. `agents.codex.model` is required when an agent action exists.
`agents.codex.max_output_tokens` defaults to 4096 and accepts 16–32768; this
includes reasoning tokens, so too small a value may produce an incomplete job.
The example uses a documented Codex model, not an automatic latest-model alias.
Account access to that model must be verified independently.

Prompt substitution supports the existing `.project.*`, `.context.mode`,
`.context.values.<name>`, and declared `.args.<name>` variables. No environment,
shell or template expressions are accepted. Missing values fail before admission.
Substituted text is never parsed a second time. Reloading any prompt publishes a
new configuration generation; admitted jobs retain their prepared prompt/model,
file list, directory, timeout and generation.

A selected project is required. `cwd` defaults to its root; relative agent working
directories resolve against that project, unlike ordinary exec paths. File names
are literal paths relative to this directory. At most 32 explicit regular UTF-8
files are read, each at most 128 KiB, with at most 512 KiB of prompt plus file text
before JSON encoding. Directories, binary files, FIFOs and symlink escapes are
rejected. The selected contents are read when the admitted job starts. Project
files may change between admission and execution; their content is not a stored
configuration generation. No globbing, recursive scan, automatic Git diff,
credential-file discovery, or local instruction-file loading occurs.

## Credentials and operation

The daemon must already have `OPENAI_API_KEY` in its environment, supplied by
your existing secret manager or launch environment. Reuse of the existing key
was authorized for implementation. The application does not write, provision,
print, or validate the value through a login endpoint. A missing key reports
`missing_credentials`; an unconfigured model reports `not_configured` in status.
The provider does not read `.env`, Codex CLI login state, alternate base URLs,
proxy environment variables or project environment overlays.

Agent actions send their expanded prompt and explicitly selected file contents
to OpenAI when confirmed. Inspect the YAML file list and inputs before invoking.
Output can contain project data and is visible to local clients. Operation logs
contain only normal job metadata and sanitized failures. API error bodies are
not copied into diagnostics. The exact configured key is redacted even across
streamed text fragments.

```sh
make build
./bin/deckctl config validate --config configs/developer.yaml
./bin/deckd --config configs/developer.yaml
# In another terminal:
./bin/deckctl --socket ~/.deckd/developer.sock action list
./bin/deckctl --socket ~/.deckd/developer.sock workflow run validate --confirm
./bin/deckctl --socket ~/.deckd/developer.sock action run agent.review --confirm --async
./bin/deckctl --socket ~/.deckd/developer.sock job show <job-id>
./bin/deckctl --socket ~/.deckd/developer.sock job cancel <job-id>
./bin/deckctl --socket ~/.deckd/developer.sock config reload
```

`job show --json` exposes bounded partial `result.data.stdout` while running.
Updates are coalesced to at most ten per second. Terminal results retain the
output plus `provider`, `model`, and `truncated`; cancellation retains captured
partial text. Results obey the job's shared output budget across workflow steps.
A workflow's live partial result describes its current agent step; the terminal
result contains the normal ordered step results.

Agent actions default to a two-minute deadline. Invocation timeouts can shorten
it; the HTTP client additionally caps a request at ten minutes. Connections and
TLS handshakes have ten-second limits, headers have a thirty-second limit, each
SSE event is limited to 1 MiB, and each stream to 16 MiB. Malformed, incomplete,
refused, failed or oversized streams fail the job. Unknown outcome requests are
never retried. Cancellation closes the local HTTP request promptly; it cannot
guarantee that upstream computation or billing has stopped. `store:false` avoids
application response storage; it is not a promise about provider data retention.

The model receives no callable tools. An unexpected tool item is rejected, and
no tool response or follow-up execution is sent. Tool access, workspace writes,
other network destinations and instruments have no configuration switches in
this phase. They would require a separate capability design and daemon checks.

## Git inspection

All V1 Git operations and mutation confirmation rules remain supported. Successful
status/log jobs retain their raw stdout and additionally provide `git_status` or
`git_log` records. The CLI displays quoted paths (including rename origins) and
commit lines instead of NUL delimiters. Partial status records are omitted;
`structured_incomplete` signals incomplete or capped structured output. Status/log
inspection is capped at 1000 records, and truncated output never claims a clean
working tree. Command failure continues through the existing job error path.

## Verification

Deterministic CI uses fake HTTP streams and fake agent implementations, a real
runtime/Unix API/CLI, and temporary projects. No credential or live model is
required. It covers streaming, truncation, malformed/oversized responses, missing
credentials, provider failures, cancellation, timeout, permission refusal,
context-file confinement, prompt reloads and unchanged workflow ordering. There
is no agent subprocess in the selected integration; existing process-runner
failure/cancellation tests continue to cover generated exec actions.

The optional live test sends only a fixed test prompt and no project files:

```sh
PATCHBAY_CODEX_SMOKE=1 PATCHBAY_CODEX_MODEL=gpt-5.3-codex go test ./internal/provider -run '^TestCodexLiveSmoke$' -count=1 -v
```

This uses the existing environment key and can incur API charges. It is not part
of `make check` or CI. See [the Phase 4 report](reviews/PHASE4.md) for results and
remaining limits.

## Roadmap regression gates

Run `make check` and `node --test tests/workbench.test.cjs` after workbench or authority
changes. The browser harness uses isolated fake providers and real local daemons; no
production key or project upload is needed. Patch tests use temporary Git repositories,
fault injection and killed subprocesses to verify stage/write/recovery boundaries.
`scripts/verify_agents.py --bundle PATH` checks native offline generation, exact action
approval, a pasted patch, separate measured validation and freshly approved restoration.
It requires Git for that optional patch scenario. Static release verification requires
all browser assets and the curated recipes; native arm64 smoke does not prove native
Intel behavior, physical hardware operation or successful Gatekeeper installation.
