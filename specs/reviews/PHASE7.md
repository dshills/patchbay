# Phase 7 release hardening and Prism review

Date: 2026-09-28. Local platform: macOS arm64, Go 1.27.1,
golangci-lint 2.13.2, Python 3.14.7, Prism CLI 0.5.0.

## Delivered scope

Both release packagers share source/metadata checks, deterministic ZIP handling,
source and toolchain manifests, dependency notices, and archive validation. Clean
source is required by default; local dirty builds and verification require an
explicit flag. Failed builds retain previous output; replacement publishes the
checksum file last. Dependency discovery covers the packages for both supported
architectures, including common license/notice filenames and Go runtime notices.

The core bundle verifier explicitly executes extracted native binaries for
metadata, all example configurations, V1 lifecycle and plugin conformance checks.
CI checks both CPU architectures, native smoke, packaged universal adapter behavior
and repeat-build hashes. Archive verification bounds sizes and rejects unexpected
paths, case/Unicode collisions, links, duplicates, invalid permissions, mismatched
hashes/inventory, wrong CPUs and malformed provenance.

`deckplugincheck --version [--json]` inspects build metadata before configuration
loading. The V1 walkthrough accepts `--bundle` to exercise the extracted artifact.
The operations guide records upgrade/rollback preparation and publication gates.

## Verification

| Check | Result |
| --- | --- |
| Full `make check`: formatting, vet, lint, unit/integration tests, race detector, builds and example conformance | Pass |
| Release regression suite, 20 tests | Pass |
| Missing/tampered payloads, paths/links/collisions, duplicate JSON, modes, sizes, CPU slices, metadata and checksum failures | Pass |
| Failed second-architecture builds preserve prior core/adapter output and clean staging | Pass |
| Standard/Homebrew Go license layouts; read-only source license; additional dependency and common license names | Pass |
| Real dirty working tree refused before creating output | Pass |
| arm64 and amd64 core bundle structure, hashes, notices and architecture | Pass |
| Extracted arm64 versions, all six configs, V1 jobs/cancellation/reload/shutdown/restart, plugin conformance | Pass |
| Universal adapter structure and native version smoke | Pass |
| Packaged universal adapter with fake app and real daemon/restart; Node inspector checks | Pass |
| Each architecture ZIP and universal adapter built twice with identical inputs | Byte-identical checksums |
| Pinned Elgato CLI 1.10.1 validation with remote updates disabled | Pass |

Builds during implementation explicitly used `--allow-dirty` and recorded the
base commit plus `source_dirty: true`. They are local verification artifacts.
The full Go checks passed after correcting an unchecked version-output write;
subsequent Python changes passed the complete release regression suite. Real
packaging exposed Homebrew's relocated Go license and read-only source license
permissions; both fixes have regression coverage.

The vendor CLI initially reported failed remote schema fetches but completed
successfully using its available local schemas. A second run with
`--no-update-check` passed without network access. CI uses that explicit mode;
latest remote schema compatibility is not claimed. Remote CI has not run here.
Intel binaries were cross-built and structurally checked, not executed.

## Prism

The user pre-approved Prism as the primary review mechanism. Reviews used the
staged diff with `gemini:gemini-3-flash-preview` and repository-specific context.

### First review

Run `712da2f8faa80c619c2dd361de6b8868`: one medium and one low finding.

- **Fixed dependency count (medium): addressed.** Discovery now reads dependencies
  from the actual Go package graph for both architectures. The manifest records
  each dependency's notice paths; the verifier checks them without a fixed module
  count/list. Tests cover adding a dependency and requiring its license.
- **Reading completed ZIPs again for checksums (low): retained deliberately.**
  Checksums are computed over the final bytes on disk. The bounded extra read is
  appropriate for these local artifacts and avoids mixing a streaming hash with
  ZIP header/central-directory rewrites. No functional defect was identified.

### Second review

Run `abc68b9ad1b22200bcfedb312102fe25`: two medium findings.

- **Only accepting the filename LICENSE (medium): addressed.** Collection and
  verification support common LICENSE/LICENCE/COPYING variants, plus notices,
  copyrights and patent files. Missing license text fails the build clearly.
- **Requiring exactly two universal CPU slices (medium): intentional contract.**
  The supported package format contains exactly arm64 and amd64, both produced by
  the packager. This verifier validates Deckd releases; accepting an unrequested
  CPU or arbitrary third-party universal binary would weaken the release gate.
  Future target additions must update packaging, validation and runtime evidence
  together. Valid two-slice and invalid overlap/CPU cases are tested.

### Third review

Run `ea36bb13284394fbdc963b20d2ca971b`: one high and one medium finding.

- **Duplicate-key validation (high): no vulnerability described.** The finding
  acknowledges the existing strict decoder's protection; its cited location is
  beyond the length of `release.py`. Release manifests, bundled vendor manifests
  and version responses already use that decoder. As a consistency improvement,
  source vendor manifests now use it too. A regression rejects duplicate source
  UUID keys before artifact publication. Go-generated module metadata remains
  ordinary JSON from the trusted build tool.
- **Untracked Stream Deck assets (medium): addressed.** Both packagers now share
  `git ls-files` input collection and reject symlink inputs. Ignored/untracked
  local files are excluded, including an explicit `.DS_Store` regression.
  The corrected packagers again produced identical repeat-build checksums and
  passed native smoke and vendor validation.

### Final follow-up

Run `359bd79c5e2a5249258e606193059275`: one high and one medium finding.

- **Missing `safe_name` (high): false positive from partial diff context.**
  `safe_name` is defined in the same `release_common.py` module and is available
  when `tracked_files` executes. Unit tests exercise that call directly, including
  leaf/parent symlink refusal; both real packagers also completed successfully.
- **Adapter provenance outside its directory (medium): false positive.**
  `add_manifest` selects `PLUGIN + "/"` for `product == "decksd"`. The ZIP contains
  `local.patchbay.deckd.sdPlugin/RELEASE.json`, and the verifier requires precisely
  that location. The adapter round-trip test, real archive verification and
  extracted native smoke all pass.

All findings are dispositioned. Confirmed packaging issues were fixed and no
confirmed functional defect remains. The reports are not clean Prism passes.

## Remaining release gates

Physical Stream Deck+ input/rendering/latency and Rigol DG812/MHO954 model/firmware
validation remain pending. The optional live Codex account/model smoke, actual
launchd installation, signing/notarization, publication and remote CI are not
established by local software verification. Cross-compilation and CPU-header
inspection do not establish runtime behavior on the other architecture.
