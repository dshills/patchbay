# RP-4: curated recipes and offline exchange

Date: 2026-09-28. The three curated recipes, authoring guide, release inventory and
packaged offline exchange checks are implemented. Hardware and human gates remain pending.

## Prism

Review `4caaa60a0cb34c682feeb205922739d6`, Gemini / gemini-3-flash-preview:

- Medium, unchecked capture HTTP status: not present. The shared Unix HTTP `call`
  raises on status >= 300 before returning, including the initial capture response.
  The smoke check caught a permission denial through this path during development.
- Low, ten-second capture deadline: retained for the bounded packaged fixture. It
  runs tiny deckdemo workloads, not arbitrary user recipes; a finite deadline makes
  a stuck regression fail visibly. User action deadlines remain independently configured.

No confirmed unresolved findings remain. Curated action references now explicitly
request confirmation, preserving stronger local policy. The outcome collector uses
monotonic elapsed time and daemon-owned status even when provider output is spoofed,
truncated or failed. A closed configuration vocabulary rejects unsupported paths.

## Verification

Full `make check` passed, including Go race checks, lint, plugin conformance and all
20 release-tool tests. Focused outcome/curated tests passed. Recipe generation is
byte-for-byte reproducible. The real-daemon browser test waits for the asynchronous
experiment refresh after deactivation before checking that the host remains available.

The packaged offline exchange uses two fresh private installations, three captures,
sample comparison, updated parameter defaults, update/rollback, and removal with
saved evidence retained. It executes only packaged binaries and needs no API key,
network or development toolchain. The Rigol recipe never installs write operations;
its instrument mappings and sample documentation clearly say physical verification
is pending. Project Checkup records real check/test states and durations.

Local unsigned candidates `0.4.0-rc.3`, base commit `6c9975504906128d5168294c39d9505aa0948619`,
recorded `source_dirty: true` for these reviewed changes:

- darwin-arm64 SHA-256 `f3a381b746bed4ec2e4a59acb4ef3e030377c63434e0da28af0d78ccd6a99afd`
- darwin-amd64 SHA-256 `f926f760f5c1b381133cfee2798b156782820aa7cc45bda30cd8f2a9c12fcf97`

Both archive inventories verified. Apple Silicon native smoke passed core CLI,
workbench captures/comparison/export, recipe exchange and plugin lifecycle checks.
Intel native execution, signing/Gatekeeper, a two-person Mac exchange, manual
accessibility/usability observation and physical Rigol/Stream Deck testing remain
pending; no hardware was operated and no artifacts were published.
