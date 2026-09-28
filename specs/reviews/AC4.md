# AC-4 review — supervised-loop release candidate

Prism `467ce5a58789a6a53440b18d7d8e99af` reviewed staged source/docs with
Gemini `gemini-3-flash-preview`. Its two low-severity findings were addressed: the
example documents its deliberately fixed `benchmark` experiment ID, and context
preview construction uses the selected destination directly. No high/medium findings.

Validation passed:

- Full `make check`, including race checks, legacy text-only provider tests and all
  20 release-tool tests; all three real-browser scenarios.
- Native offline smoke: one generation, one separately approved action, duplicate
  generation and admission identities, two measured helper runs and duration comparison.
  The key was removed; PATH was `/usr/bin:/bin`; no recipes were installed.
- Both unsigned local macOS archives passed static verification. The arm64 archive
  passed native version/configuration, core daemon, ordinary workbench, recipe exchange,
  agent loop and plugin-conformance smoke checks.

Local pre-commit candidate provenance is HEAD `17383ba`, `source_dirty:true`, version
`0.2.0-rc4`. These are verification artifacts, not published or signed releases:

| Archive | SHA-256 |
| --- | --- |
| darwin-arm64 | `ff8906c23c8cbbe48dd46e0df18dd480ec83fce2314e550eba67d7caec2eb89a` |
| darwin-amd64 | `8aff90f6bd55c7f249ff353d41141ab39b92339075c14378937d3e6094c19707` |

The two final review cleanups do not change the demo's operations. A subsequent
release must build from its actual committed source and pass the same archive gate.
Human usability observation, live-provider data consent/testing, physical Stream Deck
and Rigol verification, Intel execution, signing/notarization and Gatekeeper installation
remain pending. No user-observation result or physical compatibility is claimed.
AC-5 patch writes are still unavailable in this release boundary.
