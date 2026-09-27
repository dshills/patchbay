# Phase 3 verification and Prism review

Date: 2026-09-27. Reference machine: macOS 26.6.2 arm64, Apple M4 Pro,
14 CPUs, 64 GiB RAM. Go 1.27.1, golangci-lint 2.13.2, Stream Deck app 7.6.0.

## Delivered scope and gate

The isolated native `decksd` plugin implements Stream Deck+ keys, encoders,
touch/hold gestures, labels, parameter/context/job feedback, bounded input and
polling, reconnect/resynchronization, and explicit confirmation. The daemon adds
device-independent gestures, atomic control snapshots, stale-input guards and
one-use confirmation tokens. Headless V1 and existing clients remain compatible.

Implementation and simulated/native-process checks are complete. **The physical
hardware exit gate is still pending.** No Stream Deck+ physical input, display,
USB reconnect, or tactile latency is claimed. The installed Stream Deck app and
vendor schema validation cannot establish those results. No plugin was installed
into the user's profile and no settings, service, or Marketplace entry changed.

## Verification matrix

| Area | Evidence | Result |
| --- | --- | --- |
| Build and core regression | `make check`: formatting, vet, lint, unit/integration tests, full race suite, three binary builds | Pass |
| Headless V1 | `scripts/verify_v1.py`: CLI, cancellation, reload, permissions and restart persistence | Pass |
| Input normalization | Fake-device tests for key/dial press/release, long press, touch hold, held rotation and duplicate releases | Pass |
| Ordered rotation | Every signed delta retained, including reversals at bounds; final authoritative frame rendered under throttling | Pass |
| Permission and confirmation | Runtime tests for no early execution, one use, wrong source/control/gesture, expiry, capacity and replacement | Pass |
| Context/configuration races | Revision guard rejects stale input after context away-and-back, reload and daemon-instance change | Pass |
| Offline and stale feedback | Engine tests plus real-daemon restart and new app registration; no old input/confirmation replay | Pass |
| Backpressure and errors | Reader saturation terminates the session; lost admission is not retried and displays uncertainty | Pass |
| Real wire protocol | Fake app WebSocket drives native binary against a real private Unix daemon, including hold confirmation | Pass |
| Property inspector | Node test verifies settings routing/preservation and disabled disconnected saving; browser layout inspected | Pass |
| Vendor package | Elgato CLI 1.10.1 validates manifest, layout, assets and native executable | Pass |
| Architecture | Universal binary inspected with `lipo`; arm64 executes in the fake-app test | Pass; Intel execution unverified |
| Physical hardware | Key/dial/touch/display/unplug matrix in `STREAMDECK.md` | Pending |

## Performance and bounds

The native-binary fake-app test records 20 alternating encoder changes against a
real daemon: median **50.730 ms**, p95 **51.080 ms**, maximum **51.194 ms** from
WebSocket send to received feedback. The 50 ms frame interval is included. These
are software transport measurements, not physical display latency. An earlier
periodic renderer added an extra tick; scheduling the next eligible frame removed
that delay while preserving the 20 Hz limit.

`BenchmarkRotationAndFrame` with a fake backend: **1,342 ns/op**, **1,001 B/op**,
**27 allocations/op** on the reference machine. Network/provider time is excluded.
There are at most 64 visible controls/devices, 256 queued 64 KiB app messages,
64 references per snapshot, and 256 five-second daemon challenges. Polling is
250 ms; each HTTP exchange and each complete poll have a one-second deadline.
WebSocket writes have a two-second deadline and reconnect backs off to five
seconds. The session joins its reader before reconnecting. Jobs remain daemon-owned.

`go test -coverpkg=./internal/...,./pkg/...,./adapters/... -coverprofile=coverage.out ./...`
records **90.7% aggregate coverage** of core, protocol and adapter packages,
including cross-package integration tests. Command-entry signals are also
exercised through the built-binary walkthrough.

The existing daemon benchmark remains below the plan's startup/dispatch targets:

| Operation | Samples | Median | p95 | Maximum |
| --- | ---: | ---: | ---: | ---: |
| Startup to readiness | 5 | 9.543 ms | 260.916 ms | 260.916 ms |
| Context dispatch | 100 | 0.072 ms | 0.112 ms | 0.412 ms |
| Persistent parameter rotation | 100 | 0.099 ms | 0.153 ms | 0.279 ms |
| Async action admission | 100 | 0.163 ms | 0.348 ms | 1.136 ms |

The startup sample includes the first cold launch. Over a 3.006-second idle
window the daemon used 0% CPU at `ps` resolution and 14,160 KiB RSS; loaded RSS
was 20,720 KiB. These are daemon measurements, not adapter or hardware results.

## Package and installation

`make streamdeck-package` produces a universal macOS `.sdPlugin` directory and a
ZIP with fixed ordering, modes and timestamps. The dependency license, inspector,
layout, generated icons, example YAML and operating guide are included. No Node
runtime is needed by the native plugin. Node is used only for development checks.
The package is validated with the official vendor CLI; signing/notarization and
Marketplace distribution are not part of this phase.

Two successive builds with version `0.2.0`, base commit
`755848f82a7a81c812a4155ae8ee7bfdb6b5fa20`, and build time
`2026-09-27T00:00:00Z` produced the same ZIP SHA-256:
`a387d899bf03b98915c6caa09169996b115660815569bf66b0e5fc79e974fe31`.
The archive's sorted entries, fixed timestamps, executable/file modes, required
assets and checksum were checked. The packaged universal executable passed the
native wire test on arm64; vendor validation also passed on this final package.
This is a pre-commit verification artifact; release builds should supply their
actual committed revision.

The repeat-build check caught a read-only dependency license copied from the Go
module cache. Packaging now normalizes generated file permissions and replaces
the generated plugin directory after building successfully, which also removes
obsolete assets. Both repeat builds passed after this fix.

## Prism

Prism 0.5.0 reviewed the staged implementation twice with
`gemini:gemini-3-flash-preview` and repository-specific rules. Findings were
checked against implementation and tests rather than accepted automatically.

### Initial review

Run `e0a6ef848ba0481784771d4aec0a0a41`: one high and one medium finding.

- **Release after a failed press (high): false positive.** `Engine.Handle`
  emits release only when `emit(Press)` succeeds; all backend errors return
  false. `TestFailedPressSuppressesRelease` now explicitly covers keys and dials
  with unavailable, confirmation-required, denied, busy and transport errors.
  The existing token test also proves a confirmation prompt admits no release.
- **SVG generation on every input (medium): incorrect premise, with an
  optimization applied.** `Commands` generates SVG only for changed, eligible
  frames returned by `Render`. Frame formatting now also waits until the
  control's 50 ms deadline. The rapid-rotation test verifies all deltas, latest
  feedback, throttling, and that bursts cannot postpone that deadline.

### Follow-up review

Run `3a5f88c89e15e14436e54178c0c03b76`: zero high and two medium findings.

- **Missing home expansion (medium): false positive.** The unchanged shared
  `internal/client.New` expands `~/` using `os.UserHomeDir` and `filepath.Join`
  before dialing. `TestHTTPBackendExpandsHomeSocketPath` now exercises that path
  through the adapter against a real temporary Unix daemon.
- **Release suppressed after a context change (medium): intentional contract.**
  Press/release bindings are independent semantic action invocations, dispatched
  on physical release. They do not maintain a shared pressed state. A changed
  guard suppresses the companion to avoid running an action in another context.
  `TestContextChangeAfterPressSuppressesOldRelease` verifies this behavior. The
  finding cited `engine/engine.go`, which is not a repository path.

The four findings are dispositioned above; no confirmed functional issue remains
from those reviews. A third review request after the packaging correction was
blocked by automatic approval review because staged code would be transferred
to the configured external provider without destination-specific approval.
It was not retried. The final packaging fix and added regression tests were
reviewed locally and passed the full checks; a clean final Prism report is not
claimed.

## Remaining limits

- Hardware smoke and Intel execution remain unverified. The native route is an
  advanced vendor integration; Elgato recommends its Node SDK for most plugins.
- Only Stream Deck+ geometry is enabled; other device models and multi-action /
  key-logic wrappers are disabled. The host owns swipes and profile navigation.
- Socket trust remains per-user. This adapter does not add an execution sandbox.
- A failed response can hide an already-admitted action; no automatic replay is
  attempted. Inspect CLI jobs when the adapter reports an unknown outcome.
- Only the latest job associated with each visible control is displayed. Jobs
  continue when a page disappears; context changes discard old display state.
- CI is configured for these checks; remote CI execution is not claimed here.
