# Phase 1 verification and Prism review

Date: 2026-09-27. Platform: macOS 26.6.2 arm64 (25G83).
Toolchain: Go 1.27.1; golangci-lint 2.13.2.

## Scope

All eight Phase 1 milestones are implemented: state/persistence, bounded events,
bindings, action preparation and permissions, exec/open/Git providers, jobs,
sequential workflows, Unix-socket HTTP and signal lifecycle, and atomic reload.
The full CLI, physical device adapters, agent integrations, SCPI, and plugins
remain in their planned later phases.

## Validation

- `make check`: formatting, vet, lint, unit/integration tests, race detector,
  and builds of both executables pass. Socket tests need permission to bind
  local Unix sockets when run in a restricted sandbox.
- Tests use temporary private directories, compiled helper subprocesses, local
  Git repositories and bare remotes, and an injected opener. They do not open
  applications, contact Git hosting, change user repositories, or need hardware.
- API tests exercise every route, strict/case-sensitive JSON, bounded bodies,
  permission refusal, failed commands, queue saturation, cancellation, timeout,
  failed reload, protected live/stale socket handling, and cleanup identity.
- Process tests verify shared output budgets, literal argv, exit codes, missing
  tools, timeouts, process-group descendant cleanup, and inherited pipe cleanup.
- Runtime tests cover concurrent rotation, all parameter types, state restart
  and corrupt-file recovery, override and nested workflow permission checks,
  complete preflight, panic isolation/events, synchronous cancellation,
  asynchronous lifetime, pinned job generations, and atomic reload/removal.
- Separate daemon processes receive SIGINT and SIGTERM with work active. Both
  stop within the configured grace period, flush pending state, and remove the
  socket. The performance run also exercises 20 complete start/stop cycles.
- Coverage uses `go test -coverpkg=./internal/...,./pkg/...
  -coverprofile=coverage.out ./...` so integration coverage counts toward the
  packages it exercises. Final aggregate statement coverage is **91.7%**.
- Final `make check` passed after review; both built binaries also validated
  `configs/example.yaml` with `{"valid":true}`. Whitespace and local Markdown
  link checks passed.
- macOS CI is configured but was not run on a remote service during this work.

## Performance baseline

Reference machine: Apple M4 Pro, 14 logical CPUs, 64 GiB RAM. Development build
with `-trimpath`, no race instrumentation. One local client; one no-op exec action,
one persistent integer parameter and one rotary binding; concurrency 4, queue 64,
history 100, persistence coalescing 250 ms. Socket and state use a temporary local
directory. Measurements include Python HTTP client and JSON overhead.

Reproduce with `make build && python3 scripts/benchmark_runtime.py`.

| Measurement | Samples | Median | p95 | Maximum |
| --- | ---: | ---: | ---: | ---: |
| Process start to successful status response | 20 | 12.314 ms | 256.551 ms | 256.551 ms |
| Context read over Unix HTTP | 1,000 | 0.064 ms | 0.098 ms | 0.281 ms |
| Persistent rotary update over Unix HTTP | 1,000 | 0.091 ms | 0.128 ms | 0.500 ms |
| Async action admission over Unix HTTP | 1,000 | 0.154 ms | 0.265 ms | 3.712 ms |

Readiness is polled at 2 ms intervals; the startup sample includes a first launch
and subsequent restarts with saved state. Admission stops timing at HTTP 202 and
does not wait for provider completion. Each no-op job is subsequently awaited
before the next sample, avoiding queue saturation in the dispatch measurement.
Rotations alternate direction so every sample changes the persistent value.

Over 10.012 idle seconds, `ps` reported no measurable CPU-time increase (its
centisecond resolution limits this observation). Idle RSS was 13,744 KiB; after
the request workload it was 21,424 KiB. These are local baselines, not a guarantee
for large configurations or CI machines. The observed startup and dispatch
latencies meet the specification's 1 s and 25 ms targets for this workload.

## Prism

Command:

```sh
prism review staged --format json --max-diff-bytes 400000 \
  --max-findings 30 --fail-on medium --rules .cache/prism-phase1-rules.json
```

Prism executable reports 0.5.0; JSON report schema reports 1.0. Provider/model:
`gemini:gemini-3-flash-preview`. Run ID: `07fa234d5b3f6010466707515320a013`.
The rules supplied verified toolchain/dependency facts, Phase 1 scope, and the
documented local-user trust and lifecycle contracts. No severity overrides were
used. Prism exited successfully: **0 high, 0 medium, 2 low** findings.

1. **Job listings copy all retained job results.** Accepted as a bounded V1
   performance tradeoff. Full result snapshots match the published API and
   prevent callers from mutating stored history. Active jobs and terminal
   history have separate limits; output has a shared per-job budget. A default
   history of 100 jobs can retain roughly 100 MiB of captured output before
   JSON/metadata overhead, and listing may temporarily allocate additional
   copies. Reduce history/output limits for output-heavy use. Pagination or
   summary-only listings should be a documented API extension, with load
   measurements, rather than an implicit response-shape change in this phase.
2. **State submission serializes the complete snapshot.** Accepted as a bounded
   V1 performance tradeoff. This produces an immutable, size-checked snapshot
   before mutation is acknowledged; the single writer coalesces actual disk
   writes and retries failures. State is capped at 8 MiB. Directly streaming
   mutable runtime maps to disk would weaken snapshot ownership and rollback
   semantics. The rotary benchmark changes a persistent parameter on every
   sample; its p95 is 0.128 ms for the documented small configuration. Larger
   state requires separate profiling before optimizing the serialization path.

There are no unresolved medium/high findings. These two low-severity scaling
limitations are recorded for future API/performance work. Final checks also
caught a test fixture whose omitted safety field correctly defaulted to confirm;
the fixture now explicitly marks read-only branch listing safe. Production
permission behavior was correct and did not change after the Prism review.
