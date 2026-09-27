# Stream Deck+ integration decisions

Recorded before Phase 3 implementation, 2026-09-27.

## Route and boundary

Use a native Go plugin launched by Elgato Stream Deck, isolated in
`adapters/streamdeck` with a `cmd/decksd` entry point. It connects to the app's
loopback WebSocket and Deckd's private Unix socket. The daemon never imports
adapter code. No listener, HID driver, shell bridge, or Node runtime is added.
Gorilla WebSocket v1.5.3 supplies framing and connection handling; the existing
Unix client supplies bounded HTTP requests. License notices ship with the plugin.

Routes considered:

| Route | Tradeoff | Decision |
| --- | --- | --- |
| Official Node SDK | Recommended by Elgato; direct Unix HTTP is possible, but adds a second language/runtime and SDK toolchain | Viable alternative |
| Native WebSocket plugin | Documented advanced route; supports keys, dials, touch and feedback; reuses Go transport and tests | Selected |
| Direct HID | Requires USB ownership and image/report handling outside the Stream Deck app | Defer |
| Browser-only plugin | Cannot directly open a Unix socket; needs another bridge process | Defer |

Current primary references: [native plugin protocol](https://docs.elgato.com/streamdeck/sdk/references/websocket/plugin/),
[manifest](https://docs.elgato.com/streamdeck/sdk/references/manifest/),
[dials and touch](https://docs.elgato.com/streamdeck/sdk/guides/dials/),
[layouts](https://docs.elgato.com/streamdeck/sdk/references/layouts/),
[HID API](https://docs.elgato.com/streamdeck/hid/intro/), and
[WebSocket dependency](https://github.com/gorilla/websocket/releases/tag/v1.5.3).
The locally installed Stream Deck app reports 7.6.0. Physical verification is
recorded separately from simulated app/device verification.

## Device-independent protocol additions

- Bindings add `long_press`, `touch`, and `long_touch`, using action targets.
  Corresponding event types are `control.long_pressed`, `control.touched`, and
  `control.long_touched`. Existing press/release/rotation requests remain valid.
- `POST /v1/controls/snapshot` accepts up to 64 `{device, control}` references.
  One runtime lock captures the daemon instance, control revision, generation,
  context, and effective targets with policy and parameter metadata. Action
  arguments, commands, environments, and subprocess output are not included.
- Adapter events carry a snapshot guard `{instance, revision}`. Context changes
  (including away-and-back), reload, or restart invalidate old guards before
  mutation. Parameter changes do not change control identity.
- For guarded action events, `confirmation_required` errors include a random,
  one-use challenge expiring after five seconds. A token is bound to its source,
  device, control, gesture and guard. At most 256 challenges are retained;
  replacement/expiry and context changes invalidate stale challenges. The
  confirmed request is revalidated by the daemon. No request is retried.
- Existing CLI `confirmed` evidence remains supported. The adapter uses tokens
  instead. A snapshot is always required after reconnect before accepting input.

## Adapter behavior

The device identifier is `streamdeck.` plus SHA-256 of Elgato's device ID. It is
stable for that app/device identity, independent of temporary action contexts.
Default controls on Stream Deck+ are `key-1` through `key-8` (row-major) and
`dial-1` through `dial-4`. The inspector allows a semantic control name and label;
it never accepts commands or action arguments. The Unix socket is a global
plugin setting. Only Stream Deck+ geometry is supported in this phase.

Short button/dial presses dispatch press then release on release; a hold of at
least 700 ms dispatches long-press instead. Rotation during a held dial suppresses
its release click. Touch taps/holds map to the two touch gestures. Swipes remain
owned by the Stream Deck app. When confirmation is pending, a later hold on that
same control confirms only the challenged gesture; its ordinary press/release
bindings are suppressed. Another input, expiry, settings/context changes, removal,
or disconnect clears pending confirmation.

Input is serialized in a bounded queue. Rotation deltas are forwarded in order,
without coalescing opposite directions across clamp boundaries. Saturation closes
the app session and discards its queue visibly rather than replaying stale input.
Daemon failures disable controls and discard input until a fresh snapshot succeeds.
Snapshots poll at 250 ms; changed display frames are coalesced to at most 20 Hz.
Parameter values are authoritative; no speculative local increments are rendered.
Job polling is bounded to the latest job associated with each visible control.
Changing a control revision discards feedback from its prior context.

The WebSocket reader is owned by its session, closes on cancellation, and is
joined before reconnect. The main session owns all adapter state and writes.
Each HTTP operation has a deadline. Connection backoff is bounded; no device
input or mutation is retained across a reconnect.

## Packaging and validation

Build a universal macOS binary in a `.sdPlugin` directory and a deterministic ZIP
for local distribution. Installation and app profile configuration are explicit
operator steps. No Marketplace upload, signing, user profile modification, or
service installation occurs as part of the build. A fake app WebSocket and real
ephemeral daemon exercise the same executable and wire messages without hardware.
Hardware evidence and outstanding checks belong in `reviews/PHASE3.md`.

## Build, install and configure

Requirements: macOS 13+, Go 1.27+, Xcode Command Line Tools (`lipo`), Python 3,
and Stream Deck 7.0+. Node 24 is used only for inspector tests and the optional
vendor validation CLI. The installed plugin is a native universal Go executable.
The macOS floor matches [Go's requirements](https://go.dev/wiki/MinimumRequirements).

From a source checkout:

```sh
make check
node --test adapters/streamdeck/inspector.test.cjs
make streamdeck-verify
make streamdeck-package VERSION=0.2.0 COMMIT="$(git rev-parse HEAD)" BUILD_TIME=2026-09-27T00:00:00Z
npm exec --yes --package @elgato/cli@1.10.1 -- streamdeck validate dist/local.patchbay.deckd.sdPlugin
(cd dist && shasum -a 256 -c DECKSD-SHA256SUMS)
```

The output includes `dist/local.patchbay.deckd.sdPlugin` and a ZIP containing that
directory. Fixed source, toolchains (Go, Python/zlib, `lipo`) and metadata produce
identical ZIPs. The ZIP is for manual installation; it is not a signed Marketplace
installer. `WEBSOCKET-LICENSE.txt` includes the dependency's BSD license.

For a first local installation, quit the Stream Deck app, then run this explicit
operator step. It refuses to replace an existing plugin:

```sh
python3 - <<'PY'
from pathlib import Path
import shutil
target = Path.home() / 'Library/Application Support/com.elgato.StreamDeck/Plugins/local.patchbay.deckd.sdPlugin'
target.parent.mkdir(parents=True, exist_ok=True)
shutil.copytree('dist/local.patchbay.deckd.sdPlugin', target)
PY
```

Reopen Stream Deck. Drag **Deckd Control** onto Stream Deck+ keys and dials. The
default names match their positions; set a custom control name in the inspector
when using semantic bindings. The same instance on another page gets fresh
feedback when it appears. Multi-actions and key-logic wrappers are disabled so
press/hold semantics remain explicit. Other device types display as unsupported.

For a disposable demo:

```sh
DECKD_SD_DEMO=$(mktemp -d /tmp/deckd-sd.XXXXXX)
cp configs/streamdeck.yaml "$DECKD_SD_DEMO/config.yaml"
./bin/deckctl config validate --config "$DECKD_SD_DEMO/config.yaml"
./bin/deckd --config "$DECKD_SD_DEMO/config.yaml"
```

In the inspector, set **Daemon socket** to the absolute path
`<DECKD_SD_DEMO>/private/deckd.sock`. This setting applies to all Deckd controls.
The plugin waits for saved global settings before accepting input. Key 1 runs a
description (hold starts a ten-second task), key 2 requires confirmation, key 3
is disabled by dangerous-action policy, and dial 1 rotates the persistent level.
Dial press requires confirmation; touch describes the project; long touch starts
the long task. Use the CLI to inspect/cancel jobs and change context.

The plugin has the same user authority as Stream Deck and requires no new TCP
daemon listener, root access, or direct HID permission. Socket/state protection
is unchanged. An interrupted or lost event response may already have performed
work: the adapter displays an unknown-outcome warning and never retries it.
Inspect `deckctl job list` when an action's outcome is uncertain.

Quit Stream Deck before an upgrade; preserve or move the existing plugin directory
before copying a replacement. To uninstall, quit the app and remove only
`~/Library/Application Support/com.elgato.StreamDeck/Plugins/local.patchbay.deckd.sdPlugin`.
Daemon configuration and state remain separate. Remove actions from profiles in
the app if they are no longer needed. This task did not install the plugin or
change any user profile.

## Capability and smoke matrix

| Capability | Implementation and simulator evidence | Physical check |
| --- | --- | --- |
| Eight key positions | Row-major defaults; short press/release and long press | Press each key, check matching control and labels |
| Four encoders | Signed multi-tick deltas; hold/rotation suppresses accidental click | Rotate both ways, reverse at limits, press/hold each dial |
| Four touch regions | Tap and SDK hold event mapped separately | Tap/hold each region; confirm no adjacent control fires |
| Confirmation | Exact original gesture token, five-second expiry, one-use admission | Prompt then hold; change context/let expire and verify refusal |
| Feedback | Label, value/unit, context, policy, job/progress/error and offline frames | Read key and dial displays; check long labels and all states |
| Reconnect | Snapshot before input after daemon/app/device reconnect | Unplug/replug, restart app/daemon, verify no stale input replay |
| Unsupported | Swipes belong to the host; other models and wrappers are disabled | Confirm expected host behavior |

Simulator and native-binary results are recorded in
[the Phase 3 report](reviews/PHASE3.md). **Physical smoke checks remain pending.**
The Phase 3 hardware exit gate cannot be claimed from simulation or vendor schema
validation. Tests and the headless CLI remain usable without a device.
