# Phase 4 verification and Prism review

Date: 2026-09-27. Reference platform: macOS arm64; Go 1.27.1 and
golangci-lint 2.13.2.

## Delivered scope

- Opt-in Go, Node/JavaScript/TypeScript and Python conventions generate explicit
  validate/test/build actions with language and project overrides, discoverable
  origins, project scope and confirmation floors.
- Named prompts use the existing substitution grammar and typed action inputs.
  Admitted jobs retain their configuration generation across prompt reloads.
- Codex models run through the Responses API using the existing environment key.
  A small provider contract yields bounded text through ordinary jobs. It has no
  tool, subprocess, workspace-write or instrument execution capability.
- Git status/log add structured results and readable CLI output while preserving
  raw output and the existing mutation policies.

## Verification

| Area | Evidence | Result |
| --- | --- | --- |
| Configuration | Defaults, opt-in scope, language/project overrides, floors, invalid fields/templates/files, offline validation | Pass |
| Provider wire contract | Fixed API URL, request options, no tools/store/replay, selected-file content, authorization handling | Pass |
| Streaming | Partial results, shared output budget, exact-key redaction across fragments, terminal/error/refusal/malformed/oversized events | Pass |
| Local files | Symlink escape, parent traversal, missing, binary, oversized, directory and FIFO rejection | Pass |
| Failures | Missing credentials, HTTP rejection, transport failure, cancellation and timeout | Pass |
| Runtime | Confirmation before admission, inspectable capabilities, prompt generation pinning, immutable partial snapshots, deterministic workflow results | Pass |
| CLI/API | Real temporary Unix daemon; action discovery, confirmation refusal, partial output, cancellation and terminal status | Pass |
| Git | Existing real-repository operation suite plus structured rename/path, truncation and record-cap checks | Pass |
| Full checks | Formatting, vet, lint, unit/integration tests, race detector and three binary builds | Pass |
| Live provider | Optional fixed-prompt test using the authorized existing key, no repository files | Transport timeout; see below |

`make check` passes formatting, vet, lint (zero findings), all tests, the race
suite and all three builds. The final aggregate coverage measurement is
**91.2%**, using `go test -coverpkg=./internal/...,./pkg/...,./adapters/...`.
The built-binary V1 walkthrough passes, including CLI cancellation (exit 130),
reload and restart persistence (level 65). The native Stream Deck fake-app/real-
daemon regression passes, as do all four example YAML validations and the Node
inspector test. Remote CI is configured but has not run here.

The live smoke test failed before any API response with a Go transport timeout.
An unauthenticated Go GET using the same transport timed out dialing
`api.openai.com:443` after about 11 seconds; an unauthenticated curl check reached
HTTP 401 as expected without credentials. This discrepancy does not establish an
API authentication, quota or model-access failure. No model request was retried.
Connection timeouts now receive the standard `timeout` code, covered by a fake-
transport regression. Live account/model access remains unverified.

## Prism

Prism 0.5.0 reviewed the staged implementation three times with
`gemini:gemini-3-flash-preview` and repository-specific rules, following the
user's standing approval for Prism reviews. Findings were checked against the
implementation and regression tests.

### Initial review

Run `1f757655c842637de65666c455e5b55d`: zero high, one medium and one low finding.

- **Credential redaction across fragments (medium): false positive.** The
  pending buffer retains a possible credential prefix across every event, with
  no two-event limit. A new regression sends two adjacent credentials one byte
  per SSE event and verifies redaction. Another verifies that short credentials
  remain redacted; disabling redaction for short keys would expose a configured
  secret and is not appropriate.
- **Short Git status record indexing (low): false positive.** The parser already
  checks `len(record) < 4` before accessing the status prefix or path, and rename
  records are handled explicitly. New cases cover zero-, one-, two- and
  three-byte records. The finding cited `internal/git/git.go`, which does not
  exist; the implementation is `internal/provider/git_result.go`.

### Follow-up review

Run `6e5b0b79df4a0183305d7cf533ca1bad`: one high and one medium finding.

- **Nil-map read panic in CLI rendering (high): false positive.** Reading a nil
  map returns its zero value in Go; only writing to one panics. The renderer uses
  reads. A new regression exercises nil and incorrectly typed structured records
  while retaining stderr diagnostics.
- **Concrete slice assertion panic in CLI rendering (medium): false positive.**
  Both slice assertions use the checked `value, ok` form, which cannot panic.
  The CLI receives JSON over the Unix API; array values in `map[string]any`
  decode to `[]any`. A new transport test serializes the actual Git provider's
  concrete status/log slices and verifies readable CLI output after decoding,
  including renamed paths and suppression of raw NUL delimiters.

### Final review

Run `eaf3bb1704cb44f8875af1feee1aa156`: zero high, one medium and one low finding.

- **JSON encoding exceeds the raw input budget (medium): documented contract.**
  The 512 KiB limit explicitly applies to prompt plus file text before JSON
  encoding. JSON escaping adds bounded overhead; at most 32 files of 128 KiB
  each are admitted within that combined raw budget. This is a local resource
  bound, not a guarantee that every model accepts the resulting token count.
  Provider rejections follow the ordinary sanitized failure path. The finding
  does not establish an overflow of the documented limit.
- **Stream concatenation grows quadratically with total output (low): false
  positive.** After each delta, `pending` retains only a possible credential
  prefix, at most `len(key)-1` bytes, with credential length capped at 8192.
  It never accumulates the full stream. Each incoming event is bounded at 1 MiB,
  the stream at 16 MiB, and captured output uses the shared job budget.

All six findings have been dispositioned above; no confirmed functional defect
remains from these reviews. The reports contain findings and are not claimed as
clean Prism passes. Added provider and CLI regression tests pass with the race
detector, and the complete `make check` passes after those additions.

## Limits

The agent provides text analysis; autonomous edits and tool execution are outside
this capability. The model sees only the expanded prompt and explicitly selected
files. Confirmation authorizes sending that content to OpenAI and may incur API
charges. Local cancellation closes the request but cannot guarantee upstream
compute/billing has stopped. No live credential/account/model entitlement is
claimed from fake-provider tests. Core runtime operation requires no agent key.

The Phase 3 physical Stream Deck+ exit gate remains pending and is unaffected.
