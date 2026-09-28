# RP-3: guided setup and portable export

Date: 2026-09-28. Browser recipe setup, lifecycle review, separate sample comparison,
exact portable export preview, ZIP download and CLI export implemented.

## Prism

Review `95c92e977af0ff90eabf2feddaa01b2c`, Gemini / gemini-3-flash-preview:

- High, export property mismatch: not present. The existing protocol.ExportFile.Data
  has `json:"data_base64"`; both browser consumers use that field. The real browser
  downloads, verifies SHA-256 and re-imports the exported ZIP successfully.
- Medium, UTF-8 document excerpt: fixed. Byte-budget truncation backs up to a valid
  rune boundary. A multibyte boundary regression checks the bounded excerpt while
  exact export review retains the full original file.

Follow-up `35bcb3d47c1f1eebab8d3767a3081b20`:

- Medium, undeclared measurement disclosure: the selected successful run must match
  this installation/content. recipe.Build runs the ordinary sample verifier before
  exposing any preview; it rejects measurements outside the portable collector name,
  kind, step, unit and quantity. Text logs, host/source/device observations are never
  copied. Numeric sample values and timestamps are explicitly selected and shown in
  full; literal private data is not claimed to be automatically detectable.
- Low, missing README/license content: Store.Package re-imports and verifies every
  immutable archive. Both files are mandatory inventoried payloads; missing content
  returns an error before export preparation. Silently skipping them would weaken
  the portable format. No change was needed.

No confirmed unresolved findings remain. During tests, fixed typed CLI serialization
that emitted null optional export lists and omitted explicitly empty control maps.
Omitted and empty selections now retain their distinct semantics across the wire.

## Verification

Full `make check` passed: format/vet/lint, complete Go test/race suites, builds,
plugin conformance and 20 release-tool tests. Focused recipe/CLI/browser-boundary
race tests and lint passed again after the final serialization corrections.

The real-daemon Chromium test now covers ZIP import, explicit project/tool mapping,
keyboard confirmation, recipe capture, sample-to-measured comparison, exact export
review, SHA-256 download/re-import and deactivation while retaining host experiments.
A planted script/image in README renders as text; the page creates no script/image
nodes and sends no external browser requests. Existing token loss, second-tab denial,
mobile layout and disconnect checks still pass. Inspected the recipe screenshot.

Runtime tests exchange the ZIP with a second fresh installation, preserve original
portable content identity, remap sample references for comparison, reject expired/
changed exports, block invalid dependency selections, and verify default exclusions
using planted host/log text. Explicit saved-run samples use new package-local IDs.
Browser/API tests reject unauthorized uploads, wrong origins/media types and unknown
mutation routes. CLI tests verify private new-file export, no overwrite, hash checks,
re-import identity, and clearing physical assignments with an empty object.

Browser uploads have progress and cancellation; cancellation never approves a recipe.
Unknown transport outcomes instruct inspection or retry with the original durable
management request, never an automatic mutation retry. Long documents and exports
have explicit memory/response budgets. Original files remain unchanged.

Manual screen-reader checks, real two-person exchanges on both supported Mac
architectures, and physical Rigol/Stream Deck verification remain external release
gates; this evidence covers automated software behavior on the local Apple Silicon Mac.
