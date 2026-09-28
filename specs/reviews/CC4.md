# CC-4: packaged demo and local release candidate

Date: 2026-09-28. Software implementation complete; external release gates below
remain pending. No release was published, login service installed, or hardware used.

## Review

Prism run `fc31bb8a7bcb554dc10742232d93a440`, Gemini / gemini-3-flash-preview,
reviewed the staged launcher, browser ownership, packaging, tests, and documentation.

- Medium: benchmark `testing.B.Loop` requires Go 1.24. Not applicable: go.mod and
  the documented build require Go 1.27; the gate ran on Go 1.27.1.
- Low: README Go 1.27 allegedly unavailable as of early 2025. Stale reviewer date;
  this review occurred in September 2026 with the installed 1.27.1 toolchain.

No confirmed unresolved finding. The timing harness was subsequently corrected to
use fresh pages instead of fragment navigation; token-erasure assertions now compare
a boolean so a failing assertion cannot print its launch fragment.

## Verification

- Full `make check`: formatting, vet, lint, unit/integration/race tests, builds,
  plugin conformance and 20 Python release tests passed.
- Real demo ownership/reuse/shutdown tests pass with race detection. An incompatible
  binary is rejected before configuration creation; changed config and symlinks
  are rejected without overwriting existing data.
- Both arm64 and amd64 archives pass inventory, architecture, hashes, mode, and
  required-file verification. Local candidate `0.4.0-rc.1` records the staged tree
  as dirty against parent `3374bcc`; it is development evidence, not a public release.
- Extracted arm64 bundle passes binary identity/config checks, signal cancellation,
  persistence, plugin conformance, and offline workbench smoke. The latter runs with
  PATH restricted to system tools and no OPENAI_API_KEY: two captures, exact duplicate
  admission, comparison, samples, default-private export and content hash verified.
- Chromium against a real daemon passes keyboard capture, comparison, offline report,
  token loss, separate-tab isolation, narrow layout and stale/disconnected controls.
  Desktop, mobile and disconnected screenshots inspected; tables scroll within panels.

## Performance evidence

Reference: Apple M4 Pro, macOS arm64, local headless Chromium, Go 1.27.1.
Ten fresh workbench pages against a ready daemon: median 24.56 ms, maximum/p95
47.27 ms. These are automated local page-load timings, not human onboarding times.
Five Go benchmarks comparing two 1,000-point series: 2.828–2.902 microseconds per
comparison. This measures the pure comparison function, not HTTP or chart rendering.
The extracted CLI context smoke measured median 6.27 ms, p95 6.57 ms (five samples).

Native Intel execution, clean-account Gatekeeper, signing/notarization, screen-reader
verification, user onboarding study, idle polling load, full-size chart rendering,
and a stopped-workspace upgrade/restore drill are pending. No claims of passing
those gates are made.
