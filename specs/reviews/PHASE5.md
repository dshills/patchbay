# Phase 5 verification and Prism review

Date: 2026-09-27. Platform: macOS arm64; Go 1.27.1,
golangci-lint 2.13.2, Prism CLI 0.5.0.

## Delivered scope

Configured TCP SCPI profiles target the user's Rigol DG812 generator and MHO954
oscilloscope. The provider separates bounded line/block transport from semantic
commands, checks identity on every connection and serializes each instrument.
Generator operations validate limits and output preconditions; stopped scope
captures return scaled, bounded samples. Explicit shutdown policy and no automatic
retry/re-enable behavior cover uncertain connections.

Instrument-bound parameters retain desired state separately from transient
observations. CLI/API and Stream Deck views expose synchronization. Edits stage
values; explicit actions pin and apply them under the existing permissions/jobs.
Reload, persistence, cancellation and unrelated jobs retain their contracts.
[SCPI.md](../SCPI.md) records profiles, manual references, setup, limits and bench
procedures; [`configs/bench.yaml`](../../configs/bench.yaml) provides workflows.

## Verification

| Area | Evidence | Result |
| --- | --- | --- |
| Configuration | Offline bench validation, device/profile/model/endpoint/time/limit constraints, generated typed inputs, bound parameter units/ranges, permission floors, output persistence denial | Pass |
| Transport | Fragmented loopback replies, bounded lines and definite blocks, malformed/short/oversized responses, timeout, cancellation and cleanup | Pass |
| Generator | Frequency/amplitude apply and readback, both outputs off, mode/unit/offset/load/range checks, disabled coupling/tracking, model/firmware mismatch, error queue, rounded setting | Pass |
| Reconnect/shutdown | Disconnect after a write with no replay, preserve and unused-device silence, both-channel OFF attempts despite a first-channel error, bounded shutdown waiting | Pass |
| Concurrency | One instrument serializes requests; a blocked device does not block another; queued cancellation performs no network I/O | Pass |
| Runtime | No startup writes, no I/O on desired edits or denied requests, pinned desired value, desired changes during work, generation invalidation, stale confirmation rejection, persistence/restart | Pass |
| CLI/API | Real Unix daemon, capability discovery, parameter state, prepare/capture workflows, JSON voltage samples, disconnected generator plus successful scope/unrelated job | Pass |
| Adapter | Desired/readback warning remains visible after an older successful job; native fake-app/real-daemon restart test and Node inspector test | Pass |
| Full checks | Formatting, vet, lint, unit/integration tests, race detector, three binary builds | Pass |
| Examples | All five YAML examples validate, including bench; CI adds bench validation | Pass |
| Built V1 walkthrough | Context, actions/workflows, cancellation exit 130, reload and restart persistence | Pass |
| Physical DG812/MHO954 | No configured equipment addresses; model, adapter and firmware behavior require controlled bench verification | Pending |

The aggregate coverage measurement is **91.2%**, using
`go test -coverpkg=./internal/...,./pkg/...,./adapters/...`. The V1 walkthrough
reported 50 context CLI invocations with median 8.18ms and p95 9.22ms on this
machine; this is a local observation, not a hardware latency guarantee.
Remote CI has not run here. No live instrument was contacted.

## Prism

The user explicitly pre-approved Prism as the primary review mechanism. Reviews
use the staged diff and repository-specific rules with
`gemini:gemini-3-flash-preview`.

### Initial review

Run `102dbb962f5097bdb82c8e972a4a7fad`: zero high, two medium findings.

- **Whitespace in textual responses (medium): fixed.** Text queries now trim
  surrounding whitespace consistently before enum comparisons. Regression tests
  exercise padded responses and CRLF across generator apply/enable/disable and
  scope capture. Binary framing and payload bytes remain exact.
- **Configured response limit versus 4096-byte line cap (medium): documented
  contract, retained.** The configured limit is a ceiling; textual lines also
  have a fixed 4096-byte ceiling and scope captures a 1000-byte payload ceiling.
  These are explicit in SCPI.md and independently bound allocation. Removing the
  smaller ceiling is unnecessary for any supported command. The existing
  oversize-line test verifies rejection with the larger default device limit.
  The finding named `scpi_wire.go`; the actual file is `scpi_transport.go`.

### Follow-up review

Run `9ff73df9cf1e0bb197685e46e6656c91`: zero high, two medium findings.

- **Two-channel generator shutdown applied to scopes (medium): false positive.**
  Shutdown skips every device whose policy is not `output_off`. Normalization
  rejects that policy for `rigol-mho900`; its only permitted policy is `preserve`.
  The only accepted output-off profile/model is the two-channel DG812. A new
  config regression explicitly rejects MHO954 with `output_off`. Future profiles
  must define their own channel/capability contract when introduced.
- **Exact float equality causes automatic synchronization loops (medium):
  documented contract, retained.** There is no automatic apply or retry loop.
  Exact comparison deliberately exposes instrument rounding and never overwrites
  desired values. A new regression verifies that scientific-notation frequency
  and amplitude responses equal the same numeric desired values. Existing
  rounding and desired-change tests verify honest mismatch/error state. A generic
  epsilon would mask differences across properties with different units and
  resolutions. The finding named nonexistent `internal/runtime/sync.go`; actual
  synchronization is in `internal/runtime/scpi.go`.

One confirmed parsing issue was fixed. All four findings are dispositioned above;
no confirmed defect remains. These reports contain findings and are not described
as clean Prism passes. The full `make check` passes after the whitespace fix;
the added contract regressions also pass under the race detector.

## Hardware limits

Profiles are based on the official DG800 and MHO900 programming guides. Simulator
firmware strings do not establish compatibility with any installed firmware.
DG812 TCP access requires a supported USB-LAN adapter; direct USB/VISA is outside
this phase. MHO954 capture is stopped, normal-mode BYTE data, up to 1000 samples.

No automated software check is an equipment interlock. Operators must establish
exclusive access and suitable limits for the connected circuit. Cancellation or
lost connectivity cannot undo an accepted write. `preserve` performs no shutdown
I/O; opt-in `output_off` cannot guarantee OFF after a disconnect or abrupt exit.
The physical verification matrix in SCPI.md and the earlier Phase 3 Stream Deck+
smoke gate remain pending.
