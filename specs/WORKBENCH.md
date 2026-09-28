# Local workbench

## Packaged demo

Keep the extracted bundle together, then open `Patchbay.command` or run:

```
./bin/deckctl demo
# Optional separate private workspace:
./bin/deckctl demo --demo-dir /tmp/my-patchbay-demo
```

The launcher creates a private workspace (default `~/.deckd/demo`), checks the
sibling demo executable's build metadata, and creates its benchmark configuration
only when absent. It starts an in-process daemon and the loopback browser helper.
It uses neither Go nor an AI provider at runtime. Samples and browser assets are
embedded, so it works offline after download. No login service is installed.

An existing compatible daemon is reused. A different configuration, incompatible
version, or unexpected file is reported without replacement. When moving bundles
or changing the demo configuration, choose a new `--demo-dir`, or manage the
existing configuration with `deckd` and connect with `workbench` below.

The page and terminal identify ownership. **Quit demo** or Ctrl-C closes a daemon
owned by that launcher, cancels its jobs, and waits for bounded shutdown. Saved
results remain. A reused daemon keeps running when that helper closes. Closing a
browser tab alone never stops the daemon. Do not delete its live lock files.

A quick demonstration: capture the default workload, select **Use as baseline**,
change Iterations to 20000, capture again, and compare. The benchmark measures
actual runtime on your computer; thermal state and other work affect it. Review
repeat count and spread alongside the result.

## Connect to a running daemon

Start the browser client while a compatible daemon is running:

```
deckctl workbench --socket ~/.deckd/deckd.sock
```

For the benchmark configuration, use `.cache/benchmark/deckd.sock` instead. The
helper opens your default macOS browser. Keep its terminal open; Ctrl-C or **Close
session** closes browser access. Closing a tab leaves daemon jobs running. A page
refresh deliberately loses its session token; launch the helper again for access.
No service is installed, and no network API is added to `deckd`.

## Working with results

1. Choose a project and experiment. Set the declared inputs.
2. Choose **Review capture**. Inspect the effective steps and parameter values.
3. Check the approval box and choose **Capture this run** within 60 seconds.
4. Select a successful run and choose **Use as baseline**. Change an input and
   capture again, then compare the selected run with the shared baseline.
5. Add a title or note, pin useful runs, or explicitly delete old evidence.
6. Select report fields, inspect **Preview report**, then download HTML or JSON.

The sample panel shows illustrative benchmark values without making a measured run.
A baseline can only reference measured results. Failed or partial results remain
inspectable. Unknown, malformed, and missing measurements are never displayed as
zero. Charts retain their original coordinates and have complete data tables.

Default exports exclude input values, notes, raw text, source context, paths, device
addresses, and environment metadata. Selected text may contain private information;
the preview shows the exact document before download.

## Browser boundary

The helper binds only `127.0.0.1` on an ephemeral port. It uses a fresh 256-bit random
session token in the launch URL fragment, removes the fragment with
`history.replaceState` before requests, and retains the token only in page memory.
It sends the token in the Authorization header for **all** API reads and mutations.
There are no cookies, browser storage, token query parameters, or token logs.

Exact Host is required; mutations also require exact Origin and JSON content type.
Foreign Origin and cross-site fetch metadata are rejected. CORS is disabled. The
CSP permits packaged scripts/styles and same-origin connections only; frames,
external resources, forms, and inline scripts are forbidden. Text from configuration,
results, and annotations is rendered as text, never as HTML.

The bridge proxies an explicit allowlist for project selection, parameters, capture,
run inspection/annotation/deletion, baseline selection, comparison, exports, and
specific job cancellation. It cannot proxy arbitrary URLs or config reload, execute
arbitrary actions, or read filesystem paths. Request bodies are capped at 1 MiB,
responses at 32 MiB, with bounded HTTP and daemon request deadlines. These controls
protect against hostile websites, not other processes running as the same user.

## Freshness and accessibility

The active page polls authoritative state every 500 ms after a completed refresh;
hidden/disconnected pages back off to two seconds. More than two seconds without a
successful refresh disables live mutations. Configuration, context, input, and daemon
instance changes clear capture approval. Reconnection resynchronizes state before
controls become available. Initial rendering and reconnecting perform reads only.

Every input has a label, status changes use live regions, controls have visible
keyboard focus, and charts include full values in tables. Wide tables scroll within
their panel at narrow widths. Browser numbers beyond JavaScript's exact integer range
are rejected with a CLI fallback, rather than silently approving rounded input.
Automated Chromium tests include keyboard capture, desktop/mobile widths, comparison,
export privacy/download, token loss, second-tab isolation, and disconnect behavior.
Manual screen-reader testing remains a release follow-up.

## Development

```
make build
npm ci --ignore-scripts
npx playwright install chromium
npm run test:browser
```

Playwright is a development-only dependency and is never included in release bundles.
The harness runs a real temporary daemon and uses a test-only helper with a private
IPC descriptor for its launch token. Tokens are never written to screenshots, disk,
stdout, or test logs. Screenshots go to `.cache/workbench-*.png`. CI runs the same
workflow and retains its screenshots as test artifacts.
