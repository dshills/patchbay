# RP-1: offline recipe format and inspection

Date: 2026-09-28. Inert inspection and portable ZIP generation implemented. Lifecycle
activation is RP-2; this phase does not grant or execute any imported action.

## Prism

Initial run `6b88231b188c4d5213ee32bee4857b6d`, Gemini / gemini-3-flash-preview:

- High, source-parent TOCTOU: fixed. Absolute source paths now open by walking from
  a held filesystem root, checking each directory against its opened descriptor.
  No second absolute-path open can follow an unchecked ancestor.
- Medium, parser nesting/alias expansion: existing schema depth limit is 64 and
  rejects anchors/aliases before typed decode. The pinned YAML parser bounds initial
  scanner depth at 10,000; input is capped at 256 KiB. Added deep/alias regressions.
- Medium, special files: the existing readFile path rejects non-regular files and
  opens with O_NONBLOCK/O_NOFOLLOW. Also reject them during enumeration; FIFO tested.
- Medium, case collisions: already rejected by the folded-name seen map before
  insertion. Original names remain for exact inventory matching. Regression covers
  README/readme and duplicate names; ASCII paths reject Unicode normalization variants.
- Medium, Windows paths: added drive/UNC/backslash rejection for portable values.
- Medium, NUL detection allocation: changed to bytes.IndexByte.
- Low, payload cloning: retained deliberately to own reviewed bytes independently
  of the mutable caller/source; quotas bound memory.
- Low, directory-entry constant: named and explained (64 files × eight segments).
- Low, future canonical JSON changes: format 1 defines struct/map ordering and
  canonical inventory order. Future schema changes require a new version; round-trip
  tests pin identity. A new dependency does not fix versioning discipline.
- Low, allow unknown future versions: rejected by design. Capabilities/schema versions
  are integers, and unknown required versions must fail closed.

Follow-up on the corrected full importer: `e5620003085526a7c39d8b0641d499a1`.
Two findings were checked against full context: Windows source-root handling is
outside supported macOS/Linux platforms (ZIP names already use path, not filepath);
quadratic path-collision checks are bounded to 64 entries before the loop, at most
2,016 prior-name comparisons. Neither is an unresolved defect in supported scope.

## Verification

Full `make check` passed (including race suite and 20 release tests); focused recipe/
config race tests and lint passed again after review corrections. CLI tests verify
human/JSON inspection without a daemon and reject transport options. Adversarial
fixtures cover unknown/duplicate fields, aliases, depth, bad references, unsupported
capabilities, traversal, absolute/ambiguous names, case/Unicode collisions, links,
special files, oversized content, missing/unlisted payloads, forged hashes, cancelled
and concurrent imports, source edits, and measured-origin sample forgery.

Deterministic archive round trips preserve the package digest and sample origins.
Three-second manifest and ZIP fuzz smoke runs passed (seven and three mutated inputs,
respectively); these are short harness checks, not exhaustive fuzzing evidence.
No provider, subprocess, network request, config mutation or dependency install is
part of the inspection path. The benchmark package requires explicit project/tool
mapping before it can become executable in the later lifecycle phase.
