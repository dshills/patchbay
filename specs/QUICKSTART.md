# Headless V1 quick start

Requirements: macOS, Go 1.27+, Make, and the Xcode Command Line Tools (for Git
and race tests). Python 3 is used by release/verification scripts. No device or
external account is needed. Run these commands from the repository root.

## 1. Create and validate a temporary project

```sh
make build
DECKD_DEMO_DIR=$(mktemp -d /tmp/deckd-demo.XXXXXX)
cp configs/quickstart.yaml "$DECKD_DEMO_DIR/config.yaml"
git init --quiet "$DECKD_DEMO_DIR"
./bin/deckctl config validate --config "$DECKD_DEMO_DIR/config.yaml"
```

Keep this shell open; the following commands use its `DECKD_DEMO_DIR`. The example
declares a project, two validation steps, a read-only Git action, a cancellable
long task, and a persistent parameter. All paths resolve from the copied file.

## 2. Start and inspect

```sh
./bin/deckd --config "$DECKD_DEMO_DIR/config.yaml" \
  >"$DECKD_DEMO_DIR/stdout.log" 2>"$DECKD_DEMO_DIR/daemon.log" &
DECKD_DEMO_PID=$!
DECKD_DEMO_SOCKET="$DECKD_DEMO_DIR/private/deckd.sock"
# If the first status request races startup, run it again after readiness is logged.
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" status
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" project list
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" project use demo
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" project current
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" context set mode review
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" context show --json
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" action list
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" workflow list
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" workflow run validate
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" action run project.status
```

Run commands wait and print their final results. `project.open` opens the project
directory in the macOS GUI when explicitly invoked. The automated walkthrough
does not open applications. Add `--confirm` only after reviewing an action that
requires confirmation; dangerous actions also require the configuration opt-in.

## 3. Cancel work and update state

```sh
DECKD_DEMO_JOB=$(./bin/deckctl --socket "$DECKD_DEMO_SOCKET" action run task.long --async)
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" job list
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" job cancel "$DECKD_DEMO_JOB"
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" job show "$DECKD_DEMO_JOB" --json
# Alternatively run task.long without --async and press Ctrl-C: deckctl exits 130.
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" param list
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" param set display.level 60
curl --silent --show-error --unix-socket "$DECKD_DEMO_SOCKET" \
  -H 'Content-Type: application/json' \
  -d '{"type":"control.rotated","source":"quickstart","payload":{"control":"dial-1","delta":1}}' \
  http://deckd/v1/events
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" param get display.level
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" config reload
```

The parameter is now 65% (step 5). A running cancellation may initially show
`running`; inspect it again to see the terminal `cancelled` state. `config reload`
reads the daemon's configured file. Edit that file, validate it offline, then
reload. Invalid reloads leave the current configuration and generation active.

## 4. Restart and verify persistence

```sh
kill -TERM "$DECKD_DEMO_PID"
wait "$DECKD_DEMO_PID"
./bin/deckd --config "$DECKD_DEMO_DIR/config.yaml" \
  >"$DECKD_DEMO_DIR/stdout.log" 2>>"$DECKD_DEMO_DIR/daemon.log" &
DECKD_DEMO_PID=$!
# Wait for readiness as in step 2.
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" context show
./bin/deckctl --socket "$DECKD_DEMO_SOCKET" param get display.level
kill -TERM "$DECKD_DEMO_PID"
wait "$DECKD_DEMO_PID"
```

The project should be `demo`, mode `review`, and display level 65%. State and logs
remain in the printed temporary directory for inspection; remove that directory
when finished. Job history is intentionally in-memory and does not survive a
restart. Shutdown removes the owned socket and leaves the stable lock files.

The equivalent automated binary walkthrough is
`python3 scripts/verify_v1.py`; it also checks invalid reload, SIGINT cancellation,
socket/state modes, and a launchd-style PATH using only temporary resources.
