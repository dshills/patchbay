# RP-2: local recipe lifecycle

Date: 2026-09-28. Private installation storage, explicit local mappings, composition
previews, transactional selection, updates/rollback, removal and cleanup implemented.
The browser setup/export workflow is RP-3.

## Prism

Initial review `27f7a8872b319e5664b90c7ceb80baf5`, Gemini / gemini-3-flash-preview:

- Medium, ignored numeric bound parsing: host inputs already pass core validation,
  and nil bounds are guarded. Added explicit parse-error rejection in the SCPI
  intersection anyway so malformed internal values fail closed.
- Low, nil action input map: confirmed for a parameter-backed SCPI reference, whose
  host action has no generated value-input map. Initialize the owned map before
  adding its bounded value input. Regression covers this path.

Follow-up `7800a8101b275458c6f0eb76e46710c5`:

- Medium, failed temporary writes retained: the reported file path does not exist,
  but the behavior applies to recipe/store.go. Changed ordinary pre-rename failures
  to remove their owned temporary file. Abrupt-process leftovers remain charged and
  visible for explicitly reviewed cleanup; active/current/previous/staged content
  is never swept. Fault and abrupt-exit tests still pass.
- Low, global upload semaphore: retained intentionally. Two concurrent uploads is
  the process-wide memory/processing budget, including multiple handlers. A second
  handler must not multiply that budget. Busy is an expected bounded-load response.

No confirmed unresolved findings remain in this phase.

## Verification and implementation corrections

Full `make check` passed: format, vet, lint, Go tests and race suite, binary builds,
plugin conformance, and 20 release-tool tests. Focused lifecycle/CLI/race tests passed
again after later test additions and review corrections.

- Offline import/activation do not call providers or rewrite host YAML. Local Unix
  API and real-daemon CLI tests cover upload media type, strict requests, explicit
  mappings, confirmation and durable retry across restart.
- Same-name different-content installations keep separate stable IDs/aliases. Update
  staging preserves the current version; compatible values survive; invalid values
  require explicit reset; rollback retains the preceding content.
- Host/device/recipe input ranges intersect without widening. Provider, operation,
  model, channel and unit mismatches reject composition. Config source grammar still
  rejects supplied SCPI value inputs; the internal composition parser accepts only
  compatible generated inputs within device limits. Tests caught and fixed normalized
  SCPI definitions failing a second strict parse.
- Scope checks, namespace and control validation prevent cross-project execution or
  overwrites. Renaming after an offline host change cannot renew an activation grant.
- Tests inject write, file sync, rename and directory sync failures for intent and
  selection. Child test processes exit immediately before selection write and after
  selection rename; restart uses the selected file, never promotes an intent, and
  does not replay work. Directory-sync uncertainty blocks new runtime admission until
  restart; the recovered receipt resolves the original request.
- Two prepared management clients cannot overwrite each other. Expiry, parameter
  changes and generation changes reject stale commits. Request conflicts are retained
  across restart. A backward clock, unsupported schema, links or excessive stored
  bytes fail safely while preserving files.
- Removing a recipe during a blocked capture preserves its owned plan and saved
  provenance. Current/previous/candidate archives survive explicit unused-content
  cleanup. Saved runs remain readable without the package.

These tests exercise process termination and injected filesystem failures. They do
not certify storage-device behavior under physical power loss or replace the pending
native Intel, real-equipment and user-study release gates recorded in the roadmap.
