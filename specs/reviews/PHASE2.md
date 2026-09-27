# Phase 2 verification and Prism review

Date: 2026-09-27. Platform: macOS 26.6.2 arm64, Apple M4 Pro (14 CPUs,
64 GiB RAM). Toolchain: Go 1.27.1; golangci-lint 2.13.2.

## Delivered scope

The complete reference CLI supports every V1 command, typed action arguments and
parameter writes, human/JSON output, explicit confirmation, default job waiting,
asynchronous admission, deadlines, and Ctrl-C cancellation. The Unix transport is
bounded and does not use TCP, proxies, redirects, or mutation retries.

The phase also adds a disposable-project quick start, launchd template and
operations guide, an automated walkthrough of built binaries, and deterministic
macOS release bundles. Hardware adapters, agent integrations, SCPI, and plugins
remain outside V1. No persistent service was installed and no release was published.

## V1 acceptance matrix

| SPEC §34 criterion | Evidence | Result |
| --- | --- | --- |
| 1. Valid startup / invalid config diagnostics | Foundation validation tests; real daemon fixtures; CLI offline validation and rejected reload | Pass |
| 2. Protected Unix socket | Phase 1 socket ownership/live/stale tests; binary walkthrough checks mode 0600 and removal | Pass |
| 3. CLI inspection of all resources | `TestAllRuntimeCommandsHumanAndJSON`; built-binary walkthrough | Pass |
| 4. Persist project and mode | `TestTypedValuesPermissionsOutcomesAndPersistence`; binary restart retains demo/review | Pass |
| 5. Exec, open, Git, workflows | Phase 1 compiled-process/local Git/opener-fake tests; CLI invokes exec/Git/workflows | Pass; GUI launch uses an injected opener in tests |
| 6. Cancellable long-running jobs | Real-daemon job cancellation and CLI subprocess SIGINT; bounded failed acknowledgement tests | Pass |
| 7. Deterministic workflow failure/cancellation | Phase 1 workflow tests; CLI verifies stop/continue results and skipped steps | Pass |
| 8. Typed/bounded parameters and events | All five CLI types, 64-bit precision, invalid values, and API rotation in binary walkthrough | Pass |
| 9. Daemon-side permissions | Direct/binding/nested tests from Phase 1; CLI confirmation, dangerous refusal, overrides, and no automatic retry | Pass |
| 10. Failed reload preserves service | CLI and binary walkthrough check unchanged generation and continued status access | Pass |
| 11. Race detector | Full `make check`, including `go test -race ./...` | Pass |
| 12. No hardware requirement | All tests and the binary walkthrough run using local temporary resources | Pass |

## Checks and coverage

- `make check` passes formatting, vet, lint (zero findings), all tests, the race
  suite, and both executable builds.
- `go test -coverpkg=./internal/...,./pkg/... -coverprofile=coverage.out ./...`
  records **91.8% aggregate core coverage**. Integration tests count toward the
  packages they exercise; entry-point signals are additionally exercised through
  the built-binary walkthrough.
- Transport tests cover Unix routing despite proxy environment variables, path
  escaping, integer precision, malformed/trailing/null/oversized responses,
  redirects, invalid API errors, timeouts, cancellation, and unavailable sockets.
  Mutation requests use fresh connections, including bodyless DELETE requests;
  a dropped response is not replayed.
- CLI failure tests check unknown/duplicate/misplaced options, sensitive-value
  diagnostics, required confirmation, incompatible metadata, lost responses,
  known job IDs on polling failure, and a two-second cancellation acknowledgement
  budget. Mutations are never retried automatically.
- The binary walkthrough initially caught an incorrect `/usr/bin/test` path in
  the quick-start fixture. It now uses the verified macOS `/bin/test` path.
- Remote CI is configured to validate examples, lint the launchd plist, execute
  the binary walkthrough, and cross-build release archives. Remote CI execution
  is not claimed here.

## Binary walkthrough and service environment

`python3 scripts/verify_v1.py` passed with the built binaries. It validates the
quick-start YAML, starts Deckd with the supplied launchd template's arguments,
working directory, explicit PATH, and umask, then exercises inspection, selection,
workflow execution, read-only Git, async cancellation, SIGINT cancellation (exit
130), parameter writes/rotation, failed/successful reload, shutdown, and restart.
State preserves project `demo`, mode `review`, and display level 65.

The plist passes `plutil -lint`. Template permissions, absolute expanded paths,
and exit budget are checked. launchd's installed manual and `launchctl help`
were used to verify configuration keys and bootstrap/bootout/kill syntax, with
the [Apple guide](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)
as the primary documentation reference. The template was **not registered with
launchd**; actual service installation remains an operator action.

For 50 complete `deckctl context show --json` processes on the reference machine:
median **6.798 ms**, p95 **7.055 ms**, maximum **7.227 ms**. This includes process
startup, HTTP/JSON work, and parent-process measurement overhead. The daemon's
Phase 1 startup/idle/dispatch baseline remains in [PHASE1.md](PHASE1.md); no core
scheduler/provider behavior changes in this phase. The API adds read-only
single-action metadata discovery using the effective project registry.

## Release verification

The release script successfully cross-builds macOS arm64 and amd64 bundles with
`CGO_ENABLED=0`. Repeating the build with the same source, toolchains, version
`0.1.0`, fixed commit metadata, and timestamp `2026-09-27T00:00:00Z` produced
identical archive SHA-256 hashes. ZIP timestamps, ordering, executable modes,
configuration, docs, and verification scripts are explicit and repeatable.
Native arm64 binaries are exercised by the walkthrough. Intel binaries are
cross-compiled and their Mach-O architecture is inspected; **Intel execution
and an Intel race suite remain unverified** on this arm64 machine.

## Prism

Prism 0.5.0 reviewed the staged diff using `gemini:gemini-3-flash-preview`, with
the documented CLI contract and verified toolchain versions supplied as context.

First run `8a94ee183caee2c7023b41f2d03d79a9`: **0 high, 2 medium, 0 low**.

- **Full action-list fetch for one invocation:** resolved with read-only
  `GET /v1/actions/{name}`. CLI argument encoding now fetches just the effective
  action schema. API, runtime, and CLI tests cover lookup, missing names, project
  overrides, input isolation, and no retry after an admission/schema failure.
- **Parameter metadata/write race:** reviewed against the daemon implementation.
  `Runtime.SetParameter` validates the current definition while holding the same
  lock used to publish reloads, so stale values cannot bypass validation.
  `TestParameterReloadBetweenMetadataAndWriteRejectsStaleValue` forces both a type
  change and a tighter bound between the real CLI's metadata GET and value PUT.
  Both writes are rejected, the current state stays intact, and no retry occurs.
  A stale schema may cause a request to fail; this is the documented contract,
  not a state-integrity defect. No optimistic-write protocol is required here.

Second run `5f28ef65d14c3101e106b8415024113a`: **1 high, 1 medium, 1 low**.

- **Missing-argument panic:** not reproducible through the CLI. `runDeckctl`
  calls `ctlOptions.validate` before `executeCommand`; the arity table rejects
  every incomplete command. Added `TestIncompleteCommandsFailBeforeDispatch`
  covering all truncated command forms, including `project use`, with exit 2.
  Prism's location was line 2, outside the cited code; no dispatch fix is needed.
- **Ambiguous transport option error:** split timeout and response-size checks
  into separate actionable diagnostics. Tests assert both paths.
- **Repeated response-size bounds:** introduced shared minimum/maximum constants
  used by transport validation and CLI parsing/diagnostics.

Final run `4578fbfc186d9c9a9d529cc4d37fac24`: **0 high, 0 medium, 0 low**.
The final implementation passes `make check` after all review changes. No
unresolved material findings remain.

## Remaining limits

- Socket trust is per-user; exec and Git hooks are not sandboxed. Opening apps
  has launch semantics. Detached subprocess sessions are outside group cleanup.
- Job history is bounded and disappears on restart; polling can lose a result
  if terminal history is trimmed between requests. Large histories/state retain
  the bounded serialization costs documented by the Phase 1 review.
- Ctrl-C can only cancel a known job. If admission loses its response, execution
  may already have started; the CLI reports uncertainty and does not retry.
- macOS signing/notarization, distribution, and launchd installation are separate
  release/operator actions. No physical device behavior is claimed by this phase.
