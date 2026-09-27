# Phase 0 verification and Prism review

Date: 2026-09-27. Platform: macOS arm64. Toolchain: Go 1.27.1.

## Validation

- `make check`: formatting, vet, golangci-lint 2.13.2, unit tests, race tests,
  and both executable builds passed locally.
- Built `deckd` and `deckctl` validated `configs/example.yaml` in human and JSON
  modes; version output included injected build metadata.
- `go mod verify`: all modules verified.
- The pinned `make install-lint` command successfully installed golangci-lint
  v2.13.2 under `.tools/`; that binary then reported zero lint issues. Its first
  sandboxed attempt could not reach the Go checksum service; the network-enabled
  retry completed with checksum verification intact.
- Statement coverage: 90.6% overall; config 87.7%; CLI 98.2%; parameter, binding,
  permission, job-transition, and logging logic 100%. Entry-point wrappers are
  exercised by the binary smoke checks rather than coverage instrumentation.
- Local Markdown links resolve. macOS GitHub Actions checks are configured but
  have not been run on a remote CI service during this implementation.

## Prism pass 1

Command: `prism review staged --format json --max-diff-bytes 300000 --max-findings 30 --fail-on medium`.

Prism executable reports version 0.5.0. Provider/model:
`gemini:gemini-3-flash-preview`. Run ID: `0a2dee87d3142b41fdd864e13ee483d5`.

Prism reported two high-severity findings. Both were investigated and rejected
based on concrete environment and upstream evidence:

1. **Go 1.27 does not exist.** The installed toolchain reports
   `go version go1.27.1 darwin/arm64`, and all builds and checks pass. The
   [official Go downloads](https://go.dev/dl/) list Go 1.27.1 as stable.
   The review's claim that 1.23 is current is stale.
2. **`go.yaml.in/yaml/v3` is an invalid dependency domain.** The module is present
   in the module cache and passes `go mod verify`. The
   [official package documentation](https://pkg.go.dev/go.yaml.in/yaml/v3@v3.0.5)
   documents this exact version. The YAML organization's
   [repository](https://github.com/yaml/go-yaml) explains the maintained module
   paths. Replacing it with the older archived module would not fix a defect.

## Prism pass 2

The follow-up staged review used the same severity threshold and supplied the
verified environment facts plus Phase 0 scope through `--rules`. It requested
concrete implementation defects in validation, type precision, project/workflow
graphs, and secret-safe diagnostics. No severity overrides were applied.

Run ID: `e7e033aacec6991d8c0352a20d293dd3`, same provider/model. Prism reported
two medium-severity findings:

1. **golangci-lint v2 and its import path do not exist.** Rejected. The exact
   pinned Makefile command successfully installed v2.13.2, its version command
   reported v2.13.2, and that newly installed binary passed lint with zero issues.
   The project's [current installation documentation](https://golangci-lint.run/docs/welcome/install/)
   also distinguishes current documentation from its separate v1 documentation.
2. **Bindings with disjoint predicate keys should be allowed at equal rank.**
   Rejected. For example, `mode: dev` and `values.role: coder` both match context
   `{mode: dev, values: {role: coder}}`. With the same control and gesture they
   tie at rank 2, so SPEC §12 requires rejection. Different values for the same
   predicate key make the conditions mutually exclusive; the suggested change
   inverted that rule. `TestPrecedenceAndOverlap` and the invalid ambiguous YAML
   fixture cover these cases. The finding also referenced a nonexistent
   `pkg/bindings/overlap.go`; the implementation is `internal/binding/types.go`.

## Disposition

Both Prism runs completed and returned findings; neither returned a clean exit
status. All four findings were investigated and dismissed with concrete evidence.
There are no unresolved material findings. No implementation was changed to
follow these incorrect suggestions. The final formatting, vet, lint, unit/race
tests, and builds passed after the implementation changes. Phase 0 is ready for
the requested commit; Phase 1 runtime work remains outstanding.
