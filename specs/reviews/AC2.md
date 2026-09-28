# AC-2: exact grants and durable proposal admission

Date: 2026-09-28. Strict suggestions, per-project local grants, complete effective
previews, one-use approval, durable run/audit admission, sequencing and recovery
are implemented through the API and CLI. The proposal workbench/adapter UI is AC-3.

## Prism

Review `0b9c6ae848ac7b8289451b4859fd4ed5`, Gemini / gemini-3-flash-preview:

- High, `crypto/rand.Text` does not exist: incorrect for the declared Go toolchain.
  `go doc crypto/rand.Text` confirms the API and its randomness guarantee. The full
  build, test and race suites pass on Go 1.27.1. No compatibility workaround needed.
- Medium, startup copies every run while recovering agent links: improved. Recovery
  now obtains compact agent admission identities directly from the already loaded,
  bounded run index. It does not paginate/copy all manifests or read artifacts.
  Existing crash/restart tests cover link recovery after this change. The reported
  source path did not exist; the relevant implementation is agent_proposals.go.

Follow-up `f5b520d0b732ee0799592ec2909b8013`:

- Medium, proposal lookup copies every session: fixed with a proposal-to-session
  index rebuilt at each metadata commit. Lookup clones only the selected session.
- Medium, short sensitive Git values can mask unrelated flags: fixed. The closed
  Git option surface now masks only the exact argument for path, staged or limit,
  preserving unrelated flags and argument positions. A short-value regression
  covers the problem. The reported path did not exist; the masking code was in
  agent_grants.go.

Recovery also batches all changed links into one metadata write rather than one
write per reserved run. No confirmed unresolved findings remain.

## Validation

Full `make check` passed: format, vet, lint, all tests/race checks, builds, plugin
conformance and release-tool tests. Focused final race tests pass with real configured
`/bin/echo`, exact frozen plans and the final compact recovery path.

Tests cover concurrent same-request approval, retry after restart, generic capture
route rejection, unconfirmed/modified requests, forbidden and nested targets, typed
input intersections, changed effective definitions, pending on-disk config changes,
parameter/context revision changes, expiry/reject, queue-full token preservation,
one execution per session, exact cancellation, explicit expired-proposal duplication,
experiment parameter overrides without persistent mutation, measured outcomes and
masked inherited sensitive inputs. Masking preserves argument positions and named
input sources; model-supplied sensitive values are rejected.

Subprocess crash fixtures terminate after durable run reservation and around the
session audit rename. Restart links the interrupted reserved run, retains exact
request deduplication and never calls a provider or executes the effect marker.
Injected audit failure before dispatch leaves a recording_failed unexecuted run
and closes agent admission. Existing experiment capture regression/race suites pass
with the shared scheduler hooks.

Grants cover reachable definitions, wrappers, workflow arguments, project and
inherited environments, and experiment collector/input definitions. A persistent
private signing key avoids publishing guessable secret-value hashes. Both original
and granted input bounds apply. Agent authority fields cannot be supplied as model
inputs. Imported recipes cannot create grants; protected provider types and Git
writes remain unavailable. Source hashes, Git state and local policy are rechecked
before approval, and remaining suggestions are rechecked after execution.

See [AGENT_CONTROL.md](../AGENT_CONTROL.md) for configuration, lifecycle, CLI and
API contracts. Physical supervision, live provider testing and user studies are
not claimed here. No hardware was operated and no context was uploaded to a live
model during execution tests; Prism received only source and synthetic fixtures.
