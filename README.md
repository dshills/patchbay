# Deckd

Deckd connects physical controls and local clients to semantic actions. The
executables are `deckd` and `deckctl`; this repository is named Patchbay.

## Current status

Phase 0 provides a buildable Go foundation, strict offline YAML validation, core
domain and wire types, safe structured logging, build metadata, and automated
checks. The event bus, action execution, persistence, jobs, and Unix-socket server
are scheduled for Phase 1. The full CLI follows in Phase 2.

## Build and validate

Requirements: Go 1.27 or later and Make. macOS is the initial supported platform.

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

## Development checks

```sh
make install-lint
make check GOLANGCI_LINT="$PWD/.tools/golangci-lint"
make coverage
go tool cover -func=coverage.out
```

`make check` checks formatting, runs vet, lint, unit tests, race tests, and builds
both commands. `make fmt` formats source. The lint version is pinned in the
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

The local module is `patchbay` because the repository has no configured remote.
Set a canonical published module path before exposing packages to external Go
consumers. Application behavior is internal; `pkg/protocol` contains only wire
contracts and depends exclusively on the standard library.

The single runtime dependency is `go.yaml.in/yaml/v3`, pinned to v3.0.5. YAML
decoding is absent from the standard library. This security-maintained v3 line
provides the stable node API needed for source locations; the loader adds strict
schema/type checks and excludes aliases and merge keys. The upstream project's
[version policy](https://github.com/yaml/go-yaml#version-intentions) directs new
feature work to v4; adopting that API can be evaluated separately. No dependency
is needed for HTTP, JSON, logging, argument parsing, or process execution.
