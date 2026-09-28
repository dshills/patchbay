# AC-1: selected context and durable explanation sessions

Date: 2026-09-28. Opt-in configuration, frozen upload review/consent, strict completed
response parsing, durable sessions and API/CLI/workbench explanation flows implemented.
No generated suggestion can execute in this increment.

## Prism

Review `bb446ca837d3580ff09d26119afd7ca5`, Gemini / gemini-3-flash-preview:

- Medium, verification's 128 KiB file cap differs from preparation: not present.
  Preparation uses min(128 KiB, remaining budget); verification uses 128 KiB. A
  boundary regression accepts an unchanged 128 KiB file through generation and
  rejects a larger file during preparation.
- Low, checking the JSON envelope can miss escaped credential characters: fixed.
  The configured credential is checked against the raw prompt and each selected
  item's text before the preview is returned. Tests cover JSON-escaped characters
  in both prompt and file. This exact-key protection is separate from advisory
  pattern warnings and makes no promise to discover arbitrary secrets or encodings.

## Evidence

Full `make check` passed: Go tests and race checks, format/vet/lint, all binaries,
plugin conformance and 20 release-tool tests. Focused final race tests cover new
session and CLI behavior; legacy agent tests continue to pass.

Durable-store tests exercise every write/sync/rename boundary, restart interruption,
deduplication after forgetting, quota exhaustion and strict output parsing. Runtime
fixtures prove no preview upload, exact frozen request bytes, eight simultaneous
retries producing one generation, changed/protected/expired selection rejection,
two-generation limit, cancellation, malformed final output, optional local retention,
actual token usage and audit failure before provider dispatch. Provider tests inspect
the actual HTTP request through an in-memory transport and verify tools/replay/store
remain disabled. No live API account is used.

Real-daemon browser checks use a test-only fake provider and cover keyboard consent,
no startup/review generation, exact destination/input, escaped script-like model
text, unsupported reference labels, usage display and forgetting a session. Existing
capture/recipe/token-loss/mobile/disconnection checks also pass. Inspected the
agent-context screenshot; no external browser requests occurred.

The operator contract is in [AGENT_CONTROL.md](../AGENT_CONTROL.md). Context is capped
at 20 items / 256 KiB including its envelope; sessions at 100 / 50 MiB; previews at
64 / 16 MiB. Restart never reissues generation. Existing text-only actions are
unchanged. AC-2 will add independent action grants and explicit proposal admission;
AC-3 adds physical supervision. Live-provider, manual accessibility/usability and
physical-device checks remain separate pending evidence.
