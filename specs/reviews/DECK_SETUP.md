# Deck setup review

## Implementation

`deckctl deck use demo|benchmark` closes Stream Deck, durably commits a verified
private backup, installs the plugin and native profile, selects the device profile,
starts a separate managed service, and reopens the app. Restore takes a fresh backup
first and supports latest, original, explicit IDs and interrupted recovery.
Complete core bundles now include the matching native `decksd` adapter.

No developer Stream Deck configuration was changed. Native profile and preference
structure was inspected read-only; only source, fixtures and documentation were
submitted to Prism. Physical profile loading remains an external verification gate.

## Prism review and dispositions

Prism 0.7.1 used Gemini `gemini-3-flash-preview` with existing credentials.

- `aa2941ac37611f85f855bd608ed66dff` was incomplete: five chunks exhausted the
  provider output limit. It is not accepted as complete review coverage.
  Its documentation consistency suggestions were applied. The explicit `/tmp`
  fixture path is retained and explained: macOS's default temporary home exceeds
  the Unix socket path limit. Full-store restore requires freeing space manually;
  that condition is now explicit in the guide. Review evidence is saved here.
- `3579c22b1e4368367b944d273b0fcbd8` covered all 27 staged files, with no skipped
  or truncated input. Findings were checked against source and regression tests:
  - Alleged manifest traversal: rejected. `loadBackup` requires `filepath.IsLocal`
    and normalized relative paths. Verification obtains names from a confined tree
    walk, not from the manifest. A regression rejects absolute, parent and noncanonical
    manifest paths before stopping the app.
  - Concurrent connection close: rejected. Gorilla closes its underlying `net.Conn`,
    which permits concurrent methods. This cancellation behavior already existed.
  - Managed settings preservation: improved with `json.RawMessage` values. A wire
    regression preserves nested integers above the float64 precision limit.
    Non-object settings already fail the preceding struct decode; null is an empty
    initial settings object. Unknown object fields are preserved.
  - Copy race: rejected. Source files open with `O_NOFOLLOW` under a held nonlinked
    parent. Descriptor identity is checked before creating the destination; identity,
    size and modification time are checked after copying. Failed staging is never
    committed. Symlink rejection and unchanged original state are tested.
  - Sequential verification: retained. At most one bounded file is read at a time;
    parallel reads would increase peak memory. No measured performance problem.
  - Alleged absence of `crypto/rand.Text`: rejected. The installed Go 1.27.2
    documentation and compiling tests confirm its availability and cryptographic
    randomness.
  - Corrupt active metadata: accepted diagnostic limitation. It only supplies the
    optional preset label. Files are still backed up as bytes, and restoration uses
    verified manifests rather than this label.
  - UUID letter case: retained. Patchbay's private index owns lowercase identities;
    the app's preference uses uppercase as observed in native profiles.
  - Repeated small index writes: retained for a consistent transaction commit.
  - Repeated hardware model literal: changed to `modelDeckPlus`.
- `6eb9c6ce387b594c2ad15d27a89b1e50` reviewed all 28 updated files completely,
  including the release packaging correction and the new regressions:
  - Alleged missing symlink protection: rejected. Its quoted evidence already
    contains `O_NOFOLLOW`, followed by descriptor identity validation, exactly the
    proposed protection. Regular-file validation precedes opening and identity
    comparison rejects a replacement.
  - Settings map race: rejected. Each map is local to one message handled by the
    single session loop; no concurrent message mutates it. Race tests pass.
  - File descriptor accumulation: rejected. `readFile` closes its file and parent
    before returning on each iteration. Verification reads one file at a time.
  - Additional hardware revisions: declined. Only the observed Deck+ model is
    supported. Unknown hardware is refused before stopping the app, preventing
    unverified profile installation. The suggested extra model ID is unverified.
  - Preference sync: no change. The app is stopped, XML is synced before the
    synchronous native `defaults import`, and failed imports trigger rollback from
    the durable snapshot. `defaults read` is not a disk synchronization primitive.
    Physical macOS preference behavior remains explicitly pending.
  - XML entity expansion: rejected. `encoding/xml` does not fetch external entities
    or expand a document's DTD definitions. No custom Entity map is set; strict
    decoding, the byte cap and recursion limit remain. Disabling strict mode, as
    suggested, would weaken validation.
  - Backend error shadowing: ordinary Go scoped error handling; no error is lost.
  - Requiring a generated profile directory to already exist: declined. A new
    identity intentionally has no live directory; its staged profile is prepared
    before stopping the app.
  - Ignored random read error: rejected. Go 1.27.2's `crypto/rand.Read` contract
    always fills the buffer and never returns an error; entropy failure terminates
    the program. The backup/journal protects interruption during installation.

A final SDK contract check caught an outbound global-settings envelope error. The
adapter now writes settings directly in `setGlobalSettings.payload`; incoming events
still use `payload.settings`, as required by the [official plugin WebSocket reference](https://docs.elgato.com/streamdeck/sdk/references/websocket/plugin/#setglobalsettings).
The fake-app regression now checks the exact outbound payload, including preserved
nested integer values. This correction followed the complete Prism reviews above.

Prism follow-up `dbc1f6f2b12c75ef6e0531f094f0280c` covered all 16 selected
adapter/CLI/release files completely after the SDK envelope correction.
- Alleged variable shadowing: rejected. The quoted `all`, `installed` and `managed`
  variables have ordinary declarations, no conflicting short declaration. Socket
  strings are always JSON-serializable. Missing/invalid managed-marker values mean
  repair is needed; invalid socket types are refused by the preceding decode.
- Inspector input size: applied. Messages exceeding 64 KiB are ignored before
  parsing, matching the adapter's existing bound. The JavaScript test covers that
  limit and the managed read-only socket display.
- Socket path limit: retained. The 100-byte bound is a conservative macOS Unix
  socket limit shared with the installer; length refusal happens before mutation.
- Alleged CLI nil dereference and missing error check: rejected. Wrapping happens
  only inside `if err != nil`; `report` handles success and failure explicitly.
- Suggested error wrapping: no underlying error exists for invalid launch arguments.
- Repeated clock reads: retained. Rendering can consume time; a fresh scheduling
  timestamp after flush avoids using a stale delay. No measured performance issue.

No confirmed security, correctness or data-loss finding remains unresolved.

## Verification

Full `make check` passed: formatting, vet, lint (zero issues), ordinary and race tests,
binary builds, plugin conformance and all 20 Python release tests. The inspector
JavaScript test passed separately. Tests cover exact restoration of original files,
preferences and absence; switching, undoing restore, repeated profile identities,
startup and phase failures, cancelled recovery, corruption, unsafe paths, private
metadata, concurrent installers and interrupted journals. Launchctl mocks distinguish
missing jobs from inspection failures and reject unowned loaded services.

## Packaged verification and remaining hardware gate

Both macOS architectures passed static archive integrity and required-file checks.
The arm64 archive passed native smoke verification: matching binary versions,
offline deck preset listing, all example configs, V1 context/cancellation/persistence,
workbench evidence/export, recipe exchange/rollback, offline agent generation/action/
patch validation and restoration, and plugin conformance. No setup install or restore
was invoked against the developer's real Stream Deck.

Unsigned local candidates are `0.2.0-rc8`, built at `2026-10-08T19:05:00Z`
from base `aa0c299` plus these changes (`source_dirty:true`). They are local
verification artifacts. The final review-report update followed packaging without
changing binaries or embedded assets.

| Architecture | SHA-256 |
| --- | --- |
| darwin-arm64 | `e8df237b345c06854496b74ca9a04d3b4ddac6b6480d63621930bf5caf347d67` |
| darwin-amd64 | `4a4e1f61f0afdfc91a8d44df7dd0730b26c0632dee7c96917a2a04e3f2d36874` |

Physical profile loading, selected-profile behavior and first-run macOS permission
behavior on a connected Deck+ remain pending. This installer intentionally refuses
unknown hardware/profile formats; it does not claim Windows support. Backups retain
file bytes with private permissions; ACLs and original directory permission metadata
are not an archival guarantee. Signed distribution remains a separate release gate.


## Service readiness correction

A physical trial exposed missing `MaxResponseBytes` options in both native service
clients. `client.New` rejects zero, so startup failed after bootstrap and rollback
failed after bootout. Both checks now use an explicit 1 MiB response bound. Client
validation precedes the launchctl mutation so constructor errors cannot change the
service state. Manual quit guidance is clearer when macOS prevents automatic quit.

The new fixture substitutes only launchctl for service startup and shutdown. The
actual native methods construct their real HTTP clients and query an actual daemon
through its private Unix socket. Its switch/restore test reproduced the reported
startup and recovery error before the fix. After the fix, startup, demo-to-benchmark
switching and exact restoration pass. A failed bootstrap also restores and restarts
an existing demo service through the real native readiness path. All pass under the
race detector. Real Stream Deck files and preferences are unchanged by these tests.

Prism `7e58cd0ab94ad1f75f6e1588ab271cb4` reviewed the three source/test/guide
files completely, with no skipped input or high findings.
- Client leak: false positive. The quoted code already places `defer c.Close()`
  immediately after the constructor error check; no intervening failure exists.
- Alleged readiness race: false positive. `client.New` validates options and creates
  transports; it performs no I/O or listening. Polling begins after launchctl returns.
- Alleged nil active struct: false positive. `active()` returns an `Active` value.
- Distinct output limits: retained. The 16 MiB native-command cap accommodates full
  preference export; the 1 MiB service-status bound has a different purpose.
- Missing-job exit code: its supported-host behavior and fail-closed treatment of
  other errors are now explained in a source comment and covered by existing tests.

The complete `make check` suite passed after the correction, including ordinary and
race tests, vet, zero lint issues, native builds, plugin conformance and all 20 Python
release tests. No private backup content was supplied to Prism.
