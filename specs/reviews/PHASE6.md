# Phase 6 verification and Prism review

Date: 2026-09-27. Platform: macOS arm64, Go 1.27.1,
golangci-lint 2.13.2, Prism CLI 0.5.0.

## Delivered scope

The v1 executable plugin protocol uses bounded JSON lines over stdin/stdout for
hello/version selection, capability discovery, health, one execution, cancellation
and shutdown. Explicit configuration allowlists executable paths, fixed args/cwd/
environment, operations and safety floors. The host supervises one fresh child per
invocation, serializes each plugin, enforces deadlines and byte limits, validates
all messages, and reaps children after graceful or forced cleanup.

Plugin actions share the existing jobs, workflows, confirmation and CLI/API paths.
Prepared arguments/context retain their admitted generation; plugin settings
require restart. Result text is bounded by the job budget and cannot forge runtime
state. `deckplugincheck`, the Go conformance harness, and a minimal echo/wait plugin
are included. [PLUGINS.md](../PLUGINS.md) specifies the protocol, compatibility,
configuration, examples and actual isolation limits.

## Verification

| Area | Evidence | Result |
| --- | --- | --- |
| Configuration | Offline validation; explicit paths/operations; literal bounded env/args; permission floors; invalid fields/timeouts; no startup execution | Pass |
| Protocol | Golden hello fixtures; invalid version/ID/type; malformed/oversized/partial frames; null/duplicate/unknown fields; UTF-8; extra/missing capabilities | Pass |
| Supervision | Unexpected exit; startup/execution/shutdown timeouts; blocked stdin; stderr flood; refused cancellation; held descendant pipes; concurrent failure cleanup | Pass |
| Policy | Unconfirmed/undeclared/oversized input refused before spawn; advertised stronger risk checked before execute; explicit environment only | Pass |
| Runtime | Workflow results, action reload, restart-required plugin changes, pinned context/args, forged state rejection, unrelated actions remain usable | Pass |
| CLI/API | Real temporary Unix daemon; discovery/health, typed input refusal, workflow output, asynchronous cancellation, subsequent invocation | Pass |
| Conformance | Compiled example passes execution, cooperative cancellation and clean shutdown; deliberately broken plugins fail the reusable harness | Pass |
| Full checks | Formatting, vet, lint, unit/integration tests, race detector, four command builds, example build and conformance | Pass |
| V1 and adapter regressions | Built-binary V1 walkthrough; native fake-app/real-daemon restart test; Node inspector test | Pass |
| Examples | All six YAML examples validate, including plugins | Pass |
| Release bundles | Both macOS architectures built with checker/example; bundle paths/executable modes verified; packaged arm64 conformance | Pass |

Aggregate coverage is **89.0%**, using
`go test -coverpkg=./internal/...,./pkg/...,./adapters/...`. The V1 walkthrough
passes cancellation exit 130 and restart persistence. Its 50 context CLI calls
measured median 8.36ms and p95 12.72ms locally; these are observations, not guarantees.
The amd64 binaries were cross-built and their packaging checked, not executed.
Remote CI has not run here.

A test fixture initially omitted its temporary state path. It created a fresh
`~/.deckd` directory with only its state and lock. Birth times and exact test data
were checked, those files were removed, and the fixture now uses its temporary
configuration directory. The subsequent full test run left no default state.

## Prism

The user pre-approved Prism as the primary review mechanism. Reviews use the
staged diff and repository-specific rules with `gemini:gemini-3-flash-preview`.

### Initial review

Run `513453181a5a6bfedd62069a7d7a3291`: one high, one medium and two low findings.

- **Incomplete defer statement (high): false positive.** The cited cleanup is a
  complete deferred function call; the full repository compiles, passes vet/lint,
  and passes tests/race checks. The review appears to have treated a truncated
  snippet as the source file. Cleanup was expanded across lines for readability.
- **Concurrent close of the failure channel (medium): false positive.** The
  existing `sync.Once.Do` encloses the error publication, channel close and
  process/pipe teardown. It is now formatted across lines. An explicit regression
  invokes failure concurrently from 32 goroutines and completes cleanup under
  the race detector.
- **Null accepted as an empty payload (low): false positive.** `wire.Decode`
  calls the strict JSON decoder, which requires a root object and rejects nulls
  recursively before typed decoding. A new regression explicitly rejects null,
  arrays and nonempty objects when `{}` is required.
- **UTF-8 check is redundant (low): false positive.** Go's ordinary JSON decoder
  replaces invalid UTF-8 within strings rather than rejecting it. The wire check
  is required by the protocol. A regression demonstrates ordinary decoding
  accepting such a string while protocol decoding rejects it.

### Follow-up review

Run `5c4438b93c6272d1e4ce3a7bf3a1b648`: zero high, one medium finding.

- **Shutdown might wait after an asynchronous failure (medium): not reproduced;
  the requested safeguards already exist.** Each shutdown join selects both
  `p.broken` and `ctx.Done()`. Failure publishes `p.broken` before killing the
  group and closing every owned pipe. A new regression receives a shutdown ack
  from a child that deliberately stays alive, triggers failure, and verifies
  shutdown exits within 250ms rather than waiting for its 5s deadline. It passes
  with the race detector. Existing blocked-pipe, held-descendant, stderr flood,
  refused-shutdown and concurrent-failure tests cover cleanup on the supported
  platform. The documented kernel/OS containment limits are unchanged.

All five findings have been checked and dispositioned. No confirmed functional
defect remains; these are not described as clean Prism reports. The full check
passes, and the added review regressions and final static checks also pass.

## Scope and limits

This is lifecycle and protocol isolation. Trusted installed plugins run with the
user's filesystem/network permissions and could access the Unix socket directly;
OS confinement is separate. Process-group cleanup cannot contain intentionally
detached descendants or guarantee kernel scheduling. Cancellation cannot undo
external effects and failed work is never automatically replayed.

No marketplace, installer, raw instrument authority, dynamic registration,
state-mutation messages or broad SDK is introduced. Earlier physical Stream Deck+
and Rigol DG812/MHO954 verification gates remain pending.
