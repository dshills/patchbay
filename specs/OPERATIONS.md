# Operating and releasing Deckd on macOS

## Configuration and runtime behavior

The [foundation schema](FOUNDATION.md) lists all YAML fields and defaults.
[Runtime decisions](RUNTIME.md) cover provider options, state/reload reconciliation,
and process ownership. [API](API.md) and [CLI](CLI.md) define request, output, and
exit-code contracts. Use [QUICKSTART.md](QUICKSTART.md) for a disposable project.

- Choose descriptive semantic action names. Project actions replace complete
  global definitions of the same name; their effective safety cannot fall below
  the global classification. Discovery shows the active project's definitions.
- Templates allow project fields, mode/context values, and declared arguments.
  Shell expansion is absent. Executables are literal; argument arrays and
  inherited/project/action environment precedence are explicit.
- The private socket trusts processes running as your user. Permissions prevent
  accidental invocation; exec commands, Git hooks, and opened apps retain that
  user's filesystem/network authority. This is not an OS sandbox.
- Socket/state parents must be user-owned 0700 directories. Socket/state files
  use 0600. Existing public directories and symlink leaves are rejected rather
  than chmodded. Configure short paths to fit macOS Unix socket limits.
- SIGINT/SIGTERM closes admission, cancels work, flushes state, and cleans up the
  socket. Do not remove live lock files; their identity protects concurrent
  ownership. Process-group cancellation does not cover deliberately detached
  sessions, nor does it close applications started by `open`.
- Reload changes definitions and mutable limits atomically. Existing jobs retain
  their captured generation and policy. Socket/state locations, state flush
  interval, subscriber capacity, and shutdown grace require a restart.
- JSON operation logs go to stderr. Raw arguments, environments, subprocess
  output, and file contents are excluded. Output remains available in job results
  and may contain secrets; treat exported results as user data.

## User-level launchd agent

Apple documents per-user agents in `~/Library/LaunchAgents`, with executable
arguments in `ProgramArguments`. The supplied template runs Deckd continuously
as the logged-in user. It uses its own Unix listener; socket activation is not
implemented. [Apple launchd guide](https://developer.apple.com/library/archive/documentation/MacOSX/Conceptual/BPSystemStartup/Chapters/CreatingLaunchdJobs.html)

Installation is an explicit operator action. The build and verification scripts
do not bootstrap services or write to your home configuration. For a new install:

```sh
install -d -m 700 "$HOME/.local/bin" "$HOME/.config/deckd" "$HOME/.deckd"
install -m 755 bin/deckd bin/deckctl "$HOME/.local/bin/"
# Prepare ~/.config/deckd/config.yaml using configs/example.yaml and absolute
# project paths for your checkout. Preserve an existing configuration.
"$HOME/.local/bin/deckctl" config validate --config "$HOME/.config/deckd/config.yaml"
mkdir -p "$HOME/Library/LaunchAgents"
python3 - "$HOME" <<'PY'
import pathlib, plistlib, sys
user_dir = pathlib.Path(sys.argv[1])
template = plistlib.loads(pathlib.Path('configs/local.patchbay.deckd.plist').read_bytes())
def expand(value):
    if isinstance(value, str):
        return value.replace('@HOME@', str(user_dir))
    if isinstance(value, list):
        return [expand(item) for item in value]
    if isinstance(value, dict):
        return {key: expand(item) for key, item in value.items()}
    return value
target = user_dir / 'Library/LaunchAgents/local.patchbay.deckd.plist'
with target.open('xb') as output:
    plistlib.dump(expand(template), output)
target.chmod(0o600)
PY
plutil -lint "$HOME/Library/LaunchAgents/local.patchbay.deckd.plist"
launchctl bootstrap "gui/$(id -u)" "$HOME/Library/LaunchAgents/local.patchbay.deckd.plist"
launchctl print "gui/$(id -u)/local.patchbay.deckd"
"$HOME/.local/bin/deckctl" status
```

`@HOME@` is expanded by the preparation snippet, not launchd. The template uses
absolute executable/config/log paths, an explicit working directory, PATH
`/opt/homebrew/bin:/usr/local/go/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin`,
umask 077 (decimal 63), ten-second restart throttling, and ten seconds to exit.
Keep `ExitTimeOut` longer than `server.shutdown_grace` (default five seconds).
These keys and the `bootstrap`, `bootout`, and `kill` syntax were checked against
the installed macOS manuals and `launchctl help` during Phase 2.

A login shell's PATH and environment are not service configuration. Adjust the
template PATH to the actual tool locations before installation; version-manager
shell initialization is not run. Put action-specific variables in project/action
environment configuration. Do not place API tokens in the plist. Provider health
reports missing executables; a typed Git operation can still fail if the project,
credentials, branch, or remote is unavailable.

The default daemon socket/state paths are `~/.deckd/deckd.sock` and
`~/.deckd/state.json`. Use `deckctl --socket` when configuring another socket.
Pre-created log files should be 0600; new files inherit the template umask.
The service does not rotate logs automatically. Archive or rotate them while
the service is stopped, then bootstrap again so its file descriptors are reopened.

### Stop, upgrade, and uninstall

```sh
launchctl bootout "gui/$(id -u)/local.patchbay.deckd"
```

Bootout unloads the agent and stops its process. Poll the socket/status and log
for the stopped outcome before replacing binaries. `KeepAlive` means sending
SIGTERM without unloading is a restart, not a permanent stop. A loaded restart
can be requested with `launchctl kill SIGTERM "gui/$(id -u)/local.patchbay.deckd"`.

To uninstall after bootout, remove the specific plist and installed binaries:

```sh
rm "$HOME/Library/LaunchAgents/local.patchbay.deckd.plist"
rm "$HOME/.local/bin/deckd" "$HOME/.local/bin/deckctl"
```

Configuration, state, logs, and corrupt-state backups remain available. Delete
them only after deciding whether to retain the data. No system-wide service or
root installation is needed. Actual launchd registration was not performed as
part of implementation; the template and its operating environment were verified
without installing a persistent service.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Exit 3 / cannot connect | Check the daemon process, `--socket`, socket parent ownership/mode, and sanitized stderr log. CLI never falls back to TCP. |
| Startup fails | Run offline config validation, check private paths, and look for another owner of the socket/state. Do not unlink a live `.lock` file. |
| Confirmation required (4) | Review the effective action or workflow and explicitly retry with `--confirm`. Nothing is retried automatically. |
| Dangerous action denied (4) | Confirmation alone is insufficient; configuration must opt in. |
| Provider unavailable (3) | Check the service PATH and the declared executable/cwd. Shell aliases/functions cannot be executed as commands. |
| Failed execution (5) | Inspect `job show ID --json` for the exit code, bounded output, and workflow step errors. |
| Timeout (124) | Distinguish HTTP `--request-timeout` from action `--timeout`. Queue wait counts toward the action deadline. |
| Cancel unacknowledged (130) | Inspect the reported job ID; the daemon may be unavailable and the job may still run. Cancellation cannot undo completed effects. |
| Result missing | Terminal history is bounded and in-memory; a busy daemon can evict a result between polls. Increase history retention when needed. |
| Output/response too large | Check truncation, lower retained output/history, fetch one job, or increase the bounded `--max-response-bytes`. |
| State recovered / persist_failed log | Inspect `.corrupt-*` backups, private directory permissions, disk space, and state limits. Failed writes retry and shutdown reports a final failure. |
| Reload rejected (1) | Validate the daemon's file; restart for immutable settings. Its prior configuration stays active. |

## Reproducible release builds

Use the same Go toolchain, Python/zlib versions, source revision, module sums,
version, commit, and timestamp for byte-identical archives. The release script
builds with `CGO_ENABLED=0`, `-trimpath`, and `-buildvcs=false`, then writes sorted
ZIP entries with fixed timestamps and permissions. It embeds supplied metadata
and creates arm64/amd64 bundles with binaries, example configurations, and docs.

```sh
make check
make release VERSION=0.1.0 COMMIT="$(git rev-parse HEAD)" BUILD_TIME=2026-09-27T00:00:00Z
(cd dist && shasum -a 256 -c SHA256SUMS)
unzip -l dist/deckd-0.1.0-darwin-arm64.zip
```

Build from a clean checkout for a published release. `make release` only writes
local archives; signing, notarization, publication, and installation are separate
operator actions. Architectures and runtime verification results are recorded in
[the Phase 2 report](reviews/PHASE2.md). No claim of Intel runtime verification
is made solely from cross-compilation.
