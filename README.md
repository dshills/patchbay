# Deckd

Deckd connects physical controls and local clients to semantic actions. The
core executables are `deckd` and `deckctl`; this repository is named Patchbay.

## Current status

Phase 2 completes headless V1: the runtime and Unix-socket API are available
through the full `deckctl` command set, with typed arguments, human/JSON output,
job waiting and cancellation, persistence, and atomic reload. Device adapters
now include a Phase 3 Stream Deck+ plugin with simulated/native-process checks;
physical smoke verification remains pending. Phase 4 adds opt-in project
conventions, named prompts, and cancellable Codex text jobs. Phase 5 adds TCP SCPI
profiles for the Rigol DG812 and MHO954, explicit generator actions, scope capture,
and separate desired/observed parameter state. Instrument simulator checks are
complete; physical model/firmware validation remains pending. Phase 6 adds a
versioned executable plugin host, explicit operation allowlists, bounded lifecycle
handling, and a reusable conformance checker.
Phase 7 hardens release packaging with source provenance, dependency notices,
verified file inventories, and smoke tests against extracted native binaries.

Start with the [temporary-project quick start](specs/QUICKSTART.md), then see
the [CLI reference](specs/CLI.md) and [macOS operations/release guide](specs/OPERATIONS.md).
See [Stream Deck+ setup and capabilities](specs/STREAMDECK.md) for the adapter.
See [developer integrations](specs/DEVELOPMENT.md) for conventions, prompt/file
selection, credentials, and the agent permission boundary.
See [SCPI bench setup](specs/SCPI.md) for instrument limits, transport requirements,
output policy, and the bench example.
See [executable plugins](specs/PLUGINS.md) for the v1 protocol, example and
conformance checker, including the limits of process isolation.

## Build and validate

Requirements: Go 1.27 or later and Make. Release tooling and its tests also require
Python 3.10 or later. macOS is the initial supported platform.

```sh
go mod download
make build
./bin/deckd --version
./bin/deckctl --version --json
./bin/deckctl config validate --config configs/example.yaml
./bin/deckd --validate --config configs/example.yaml --json
```

Validation needs no daemon, hardware, project directory, or installed action
executable. It parses and checks configuration without running commands. A
successful JSON response is `{"valid":true}`. Validation failures return exit code
1; usage errors return 2. Help and version return 0. JSON results go to stdout;
human diagnostics and daemon operation logs go to stderr.

The default configuration path is `~/.config/deckd/config.yaml`. Use `--config` to
override it. Flags are accepted before `config validate` or after that command.
Copy and customize [configs/example.yaml](configs/example.yaml) before using a
different project. Paths are relative to the configuration file, so the example's
project path `..` points at this checkout.

## Run the daemon

```sh
./bin/deckd --config configs/example.yaml
# In a second terminal:
./bin/deckctl status
./bin/deckctl action run project.status
./bin/deckctl workflow run validate --json
```

Socket and state parent directories must be owned by your user and private
(0700). The daemon creates missing directories and uses 0600 socket/state files.
SIGINT or SIGTERM cancels jobs, flushes state, and removes the owned socket.
Adjacent `.lock` files stay on disk; they are advisory locks, not stale daemons.

Commands run as your user with explicit argument arrays. Action output is bounded
and available in job results. Confirmation and dangerous-action policy are checked
by the daemon. Opening an application reports launch completion; cancelling that
job does not close the application. See the [API](specs/API.md) and
[runtime decisions](specs/RUNTIME.md) for invocation, reload, and cancellation rules.

## Development checks

```sh
make install-lint
make check GOLANGCI_LINT="$PWD/.tools/golangci-lint"
make coverage
go tool cover -func=coverage.out
```

`make check` checks formatting, runs vet, lint, unit tests, race tests, release-tool
tests, builds all four commands, and probes the compiled example plugin.
`make fmt` formats source. The lint version is pinned in the
Makefile; the selected checks are `errcheck`, `govet`, `ineffassign`, `staticcheck`,
and `unused`. These catch correctness and maintenance issues without a broad
style policy. CI runs these commands on macOS. The race suite uses the platform's
C toolchain; install the Xcode Command Line Tools if the toolchain is missing.

To keep tool caches inside the checkout when needed:

```sh
GOCACHE="$PWD/.cache/go-build" GOLANGCI_LINT_CACHE="$PWD/.cache/lint" make check
```

Build metadata is supplied with `make build VERSION=0.1.0`; `COMMIT` and
`BUILD_TIME` can also be overridden for reproducible builds. Direct `go build`
uses `dev`, `unknown`, and `unknown` defaults.

## Design and dependencies

- [Engineering specification](specs/SPEC.md)
- [Implementation plan](specs/PLAN.md)
- [Foundation decisions and configuration schema](specs/FOUNDATION.md)
- [V1 API contract](specs/API.md)
- [Phase 0 verification and Prism review](specs/reviews/PHASE0.md)
- [Phase 1 runtime decisions](specs/RUNTIME.md)
- [Phase 1 verification, performance, and Prism review](specs/reviews/PHASE1.md)
- [CLI reference](specs/CLI.md)
- [Quick start](specs/QUICKSTART.md)
- [macOS operation and release builds](specs/OPERATIONS.md)
- [Phase 2 acceptance and Prism review](specs/reviews/PHASE2.md)
- [Stream Deck+ adapter and setup](specs/STREAMDECK.md)
- [Phase 3 verification and Prism review](specs/reviews/PHASE3.md)
- [Developer integrations and Codex setup](specs/DEVELOPMENT.md)
- [Phase 4 verification and Prism review](specs/reviews/PHASE4.md)
- [SCPI profiles and bench setup](specs/SCPI.md)
- [Phase 5 verification and Prism review](specs/reviews/PHASE5.md)
- [Executable plugin protocol and setup](specs/PLUGINS.md)
- [Phase 6 verification and Prism review](specs/reviews/PHASE6.md)
- [Phase 7 release hardening and verification](specs/reviews/PHASE7.md)

The local module is `patchbay` because the repository has no configured remote.
Set a canonical published module path before exposing packages to external Go
consumers. Application behavior is internal; `pkg/protocol` contains only wire
contracts and depends exclusively on the standard library.

The core runtime dependency is `go.yaml.in/yaml/v3`, pinned to v3.0.5. YAML
decoding is absent from the standard library. This security-maintained v3 line
provides the stable node API needed for source locations; the loader adds strict
schema/type checks and excludes aliases and merge keys. The upstream project's
[version policy](https://github.com/yaml/go-yaml#version-intentions) directs new
feature work to v4; adopting that API can be evaluated separately. No dependency
is needed for HTTP, JSON, logging, argument parsing, or process execution.

The isolated Stream Deck adapter additionally uses `github.com/gorilla/websocket`
v1.5.3 for the vendor WebSocket protocol. It is not imported by the core daemon or
CLI; its BSD license ships with the plugin. Inspector/vendor validation uses Node
only during development, not at plugin runtime.
