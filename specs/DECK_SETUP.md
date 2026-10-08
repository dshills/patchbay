# Switch your Stream Deck+ setup

Run one command from an extracted Patchbay bundle:

```sh
./bin/deckctl deck use demo
```

Source users run `make build` once first. End users with a complete native bundle
need the Elgato Stream Deck app and macOS; setup needs no Python, Go, SDK, drag-and-drop
configuration, API key, or manually entered socket path.

The command closes Stream Deck, makes a verified private backup, installs the
Patchbay plugin and a ready-to-use profile, selects that profile, starts Patchbay,
and reopens Stream Deck. Your existing profiles remain available in the app's
profile menu. Patchbay's separate service starts at login; Terminal can close.

## Pick a setup

```sh
./bin/deckctl deck list
./bin/deckctl deck use demo
./bin/deckctl deck use benchmark
```

The **demo** maps the top-left button to a short description, button 2 to a
confirmation example, and dial 1 to a saved level. Hold the first button to start
a ten-second task. Button 3 demonstrates a denied dangerous action.

The **benchmark** maps button 1 to capture, button 2 to baseline selection,
button 3 to the latest result, dial 1 to iterations and dial 2 to repeats.
Capture asks for confirmation; hold the same button for 0.7 seconds within five
seconds to approve that reviewed capture. Save a run as baseline, change iterations,
and capture again. Results and parameter state stay in each preset's private workspace.

Reapplying a preset installs its default controls again. Its prior layout is in the
new backup. Patchbay reuses one profile identity per preset/device rather than adding
another duplicate on every switch.

If you have multiple Deck+ devices, setup uses the one selected in the app when it
can identify it. To select another:

```sh
./bin/deckctl deck devices
./bin/deckctl deck use demo --device 'DEVICE_ID_FROM_THE_LIST'
```

## Go back

```sh
./bin/deckctl deck restore
./bin/deckctl deck restore original
./bin/deckctl deck backups
./bin/deckctl deck restore BACKUP_ID
```

The first command restores the most recent backup. `original` restores the earliest
retained backup. Each restore saves the current setup first, making it undoable.
The output prints the new backup ID and the commands to go back.

Restoration returns all saved Stream Deck profiles and app/plugin settings to that
point in time, including profile selection. Changes made after that backup are
preserved in the new backup taken before restoration. Preset measurements and run
history remain in their workspaces.

## What is saved

Private, timestamped backups live under `~/.deckd/deck/backups`. Each includes:

- The complete `ProfilesV3` tree, including pages, folders and profile assets.
- Stream Deck's `Data` directory and complete macOS preference domain, retaining
  property-list types such as binary data and dates.
- The existing Patchbay plugin, if present.
- Patchbay's managed config, profile index, binaries and separate LaunchAgent,
  recording which were absent before first installation.
- A manifest containing per-file sizes and SHA-256 hashes.

Other installed plugin binaries, icon packs and resource libraries stay in place.
Your ordinary Patchbay configuration and any separately managed daemon are untouched.
The setup service owns `~/.deckd/deck/config.yaml` and its private socket. Its label is
`local.patchbay.deckd.setup`; existing service definitions with unrelated arguments
are refused. The installed plugin synchronizes its socket through Elgato's global
settings API, preserving unrelated fields. The inspector labels that socket as managed.

Backups can contain private actions and credentials from your existing Stream Deck
plugins. They remain local with private permissions. Nothing is uploaded. Keep an
offline copy of the whole backup directory when preserving or moving it; removing
old backups is a manual operation. The earliest remaining backup becomes `original`.

## Failure and recovery

If macOS does not let Patchbay close Stream Deck automatically, quit Stream Deck
from its menu and retry the same command. Allow Automation access if macOS asks.
Setup refuses to change configuration while the app remains open.

Backup copy, hash verification and durable directory commit happen before live
configuration is changed. A failed backup stops installation. Cancellation or a
later failure attempts to restore the already committed backup with an independent
recovery deadline. A failed recovery leaves the backup ID and a recovery journal.

If the process or Mac stopped during a switch, run:

```sh
./bin/deckctl deck restore
```

An interrupted operation makes this command select its recorded recovery backup.
Further setup switches are blocked until recovery. Restoring validates all backup
hashes before stopping the app; corrupt, missing, linked or unsupported backups
are refused rather than applied. Recovery never deletes retained backups.

Setup permits up to 100 backups, 4 GiB total retained file data, 1 GiB per snapshot,
128 MiB per file and 20,000 files per snapshot. It refuses a full store without
evicting backups. When the store is full, copy a backup elsewhere and remove its
entire directory before switching or restoring. Failed staging directories may remain after a process crash;
they are not usable backups or automatically promoted.

## Compatibility and verification

This implementation uses the macOS `ProfilesV3` version `3.0` layout observed in
Stream Deck 7.6.0 for the original Stream Deck+ model `20GBD9901`. Unknown profile
versions and other hardware are refused. Preferences are read/written with macOS
`defaults export/import`, retaining typed XML values; the app is stopped while its files change. Stream Deck must have been
opened with the Deck+ connected at least once.

The SDK supports [bundled profiles](https://docs.elgato.com/streamdeck/sdk/guides/profiles/)
and limits plugin switching to bundled profiles. This setup command instead owns an
explicit stopped-app backup/install/restore transaction; the on-disk profile layout
is an app implementation detail and can change between versions. Physical loading,
selection and first-run macOS permission behavior still need recorded verification.

Tests use isolated app directories and preferences, failure injection, corruption,
concurrent switches, interrupted journals, exact restoration and independent daemon/
adapter fixtures. They do not operate the developer's actual deck or overwrite its
profiles. Review evidence is in [DECK_SETUP.md](reviews/DECK_SETUP.md).
