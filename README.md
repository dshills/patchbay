# Patchbay

Change a setting, capture a result, and see what improved — from your browser,
terminal, or Stream Deck+.

Patchbay brings the tasks you repeat into one place. Define an action once, then
run it from the command line or assign it to a physical control. A button can
start your project checks; a dial can adjust a value; switching projects can
change what those controls do.

Try the local benchmark playground with no equipment or AI account. Save a
baseline, change the workload, compare the results, and share an offline report.

## What can you do with it?

- **Keep everyday tasks close at hand.** Check Git status, run tests, open a
  project, or group several steps into a workflow.
- **Give your Stream Deck+ a role in your work.** Map buttons, dials, and touch
  controls to actions, with feedback as jobs run.
- **Switch between projects and modes.** Use the same controls for different
  tasks as you move from development to your electronics bench.
- **Add AI assistance when you need it.** Run saved Codex prompts with project
  files you select, then read the result as it arrives. This optional integration
  requires your own OpenAI API key and model access.
- **Connect and extend your setup.** Try the instrument profiles for the Rigol
  DG812 and MHO954, or add your own actions through executable plugins.

**Current status:** Early development on macOS. The browser workbench, local
benchmark, and terminal workflows have automated coverage. Release candidates
include a demo launcher; public signed releases and physical Stream Deck+/Rigol
validation are still pending.

## Try the workbench

In an extracted, verified release candidate, double-click **Patchbay.command** or
run `./bin/deckctl demo`. It opens your browser and saves results privately in
`~/.deckd/demo`. No Go installation, API key, or hardware is needed for the demo.

1. Choose **Review capture**, inspect the steps, and capture your first run.
2. Choose **Use as baseline**, increase **Iterations**, and capture again.
3. Choose **Compare with baseline** to see the difference. Preview and download
   an HTML report to keep or share.

**Quit demo** stops the daemon started by that launcher. Closing just the browser
tab leaves it running. Keep the launcher terminal open while using the workbench.
See the [workbench guide](specs/WORKBENCH.md) for existing daemons and saved data.

## Build from source

You'll need **macOS, Git, Go 1.27 or later, and Make**.

### Download and build

```sh
git clone https://github.com/dshills/patchbay.git
cd patchbay
make build
```

## Try it on your Stream Deck+

With the Stream Deck app installed and your Deck+ connected, run this from the
Patchbay folder (a downloaded bundle or the source checkout after `make build`):

```sh
./bin/deckctl deck use demo
```

Patchbay saves your current profiles and settings, installs its plugin and button/dial
layout, starts its service, and reopens Stream Deck with the demo selected. Press the
top-left button or turn the first dial. The service runs in the background, so you
can close Terminal.

Try another setup, or return to the one you had:

```sh
./bin/deckctl deck use benchmark
./bin/deckctl deck restore
```

`deck restore` goes back one step. `deck restore original` returns to your first
saved setup, and `deck backups` lists every saved setup. Restoring also makes a
backup, so you can undo a restore. See [setup and recovery](specs/DECK_SETUP.md)
for details. Automated recovery tests pass; physical profile loading remains
pending validation on a connected Deck+.

## Try your own project actions

Run `./bin/deckctl demo` to open the benchmark playground. To try your own project
actions, continue below. The two programs you'll use are:

- **`deckd`** — runs the local Patchbay service.
- **`deckctl`** — lets you choose projects, run actions, and see results.

### Start Patchbay

The included example uses this checkout as its project. Check the configuration,
then start the service:

```sh
./bin/deckctl config validate --config configs/example.yaml
./bin/deckd --config configs/example.yaml
```

Leave that terminal open while you try the commands below.

### Try a few actions

Open a second terminal in the same `patchbay` folder:

```sh
./bin/deckctl status
./bin/deckctl action list
./bin/deckctl action run project.status
./bin/deckctl workflow run validate
```

`project.status` shows the checkout's Git status. The `validate` workflow runs
Go's code checks and tests, then reports the result. To stop Patchbay, press
**Ctrl-C in the first terminal**.

For a longer walkthrough using a temporary demo project, including cancellation
and saved settings, follow the [quick start](specs/QUICKSTART.md).

## Make it your own

Your configuration describes your projects, the actions you want to run, and
which controls trigger them. Start with [the example](configs/example.yaml),
then adapt the project paths and commands to your own work.

An **action** is one task, such as checking a project or opening a folder.
A **workflow** runs several actions in order. A **binding** connects a button,
dial, or touch gesture to an action or adjustable value.

Actions run with your user account's permissions. Some require explicit
confirmation, and actions classified as dangerous also require a configuration
opt-in. Review an action before enabling it. For equipment, read the
[bench setup guide](specs/SCPI.md) before connecting an instrument.

| I want to… | Start here |
| --- | --- |
| Find a command or inspect a running job | [Command-line guide](specs/CLI.md) |
| Set up buttons and dials | [Stream Deck+ setup](specs/STREAMDECK.md) |
| Add project tasks or Codex prompts | [Developer integrations](specs/DEVELOPMENT.md) |
| Connect a Rigol generator or oscilloscope | [Electronics bench setup](specs/SCPI.md) |
| Add a custom plugin | [Plugin guide](specs/PLUGINS.md) |
| Run Patchbay at login or manage an upgrade | [macOS operations guide](specs/OPERATIONS.md) |

## Help shape Patchbay

Found a confusing step, have a useful workflow to share, or want another device
supported? [Open an issue](https://github.com/dshills/patchbay/issues) with what
you're trying to do. Feedback on getting started is welcome too.

If you're working on the code, the full development checks also need Python 3.10+
and the Xcode Command Line Tools:

```sh
make install-lint
make check GOLANGCI_LINT="$PWD/.tools/golangci-lint"
```

These run formatting, static checks, tests, race checks, builds, and release-tool
verification. For the technical details, see the [specification](specs/SPEC.md),
[implementation plan](specs/PLAN.md), [API reference](specs/API.md), and
[verification reports](specs/reviews).

## License

Patchbay is available under the [MIT License](LICENSE).

### Let an agent suggest the next experiment

The optional **Agent control** panel lets you choose a few files or saved results,
review exactly what gets sent, and ask for an explanation. Each suggested action
has its own full review and approval. Its measured result joins your normal history,
so you can compare what actually happened.

Try the [offline Benchmark agent walkthrough](specs/AGENT_CONTROL.md#try-the-optional-offline-agent-demo)
without a provider key, or connect your configured model. The offline demo uses fixed
suggestions; ordinary experiments, recipes and comparisons always work without AI.
