# Portable recipes

Recipes are local folders or ZIP files containing declarative actions, parameters,
experiments, control suggestions and illustrative samples. Inspection is offline:

```sh
bin/deckctl recipe inspect recipes/benchmark
bin/deckctl recipe inspect /path/to/shared.zip --json
```

This command verifies content and explains unresolved roles. It does not execute
commands, probe tool versions, contact devices/providers, install dependencies or
change daemon configuration. Activation uses the separate review-and-commit flow below.
A declared author/name is attribution, not verified publisher identity.

## Format 1

See the complete [Benchmark Playground manifest](../recipes/benchmark/recipe.yaml).
The required root files are `recipe.yaml`, `LICENSE`, and `README.md`. Additional
UTF-8 text lives under `docs/` with `.md`/`.txt` extensions; strict CC sample JSON
lives under `samples/`. Samples must have origin `sample` and match the declared
experiment, parameter bounds, collectors and units. Measured runs remain separate.

The manifest requires schema_version, id, name, description, author, license,
SemVer version, digest, requirements, actions, and inventory. It may include feature/
schema version requirements, parameters, workflows, experiments, and control suggestions.
Each inventory entry has path, media_type, size and SHA-256. No unlisted files are
accepted. Root LICENSE/README are plain text; Markdown is treated as untrusted text.
No executable, script, HTML, SVG, remote asset or nested archive payload is allowed.

Roles are `project` (one per installation), `tool`, or `action`. Action roles name
an explicit provider and may constrain SCPI operation/model/channel/units/limits.
An action definition uses a tool role for exec, a project role for working context,
or a reference role for an existing local action. Git/open/sequential workflows use
the core grammar. Recipes cannot supply provider credentials, environment values,
socket/storage settings, plugin executables, device addresses, dangerous opt-in,
or agent execution grants. Portable parameter defaults cannot embed instrument
bindings; those remain in explicitly mapped host actions. Absolute/home paths must
be replaced by local mappings. Text can still contain private information; inspection
is not a guarantee that a package is free of secrets.

Controls suggest exactly one action, parameter, capture, baseline or result reference.
They do not assign a physical device/control automatically. Invalid references,
unknown fields/versions, duplicate keys, aliases/anchors, invalid scalar types and
unsupported required capabilities fail inspection. Core validation checks templates,
inputs, workflow expansion/cycles, collector identities and declared permissions.

## Identity and limits

Manifest identity is SHA-256 over Go's canonical `encoding/json` representation of
the schema, with `digest` empty and inventory sorted by path. Maps are sorted;
struct field order follows format 1. The package digest hashes a JSON object with
`manifest` (that digest) and `files` (the sorted verified inventory). Every payload
is independently hashed. Formatting/comments in recipe.yaml are not identity;
inspection retains a canonical YAML form. Hashes do not prove publisher identity.

Initial limits: two concurrent imports, 30 seconds total processing time, 20 MiB
compressed/expanded bytes, 64 files including the manifest, 4 MiB per payload,
256 KiB manifest, 128 actions/parameters, 32 workflows/experiments/controls, and
64 KiB read/write buffers. The manifest leaves room within the expanded limit.
File paths use portable ASCII segments (1–64 characters, total at most 256), with
no traversal, colons, backslashes or case collisions. Unicode filenames are rejected,
which also prevents normalization collisions across macOS filesystems. ZIP directory
entries are omitted; directories are inferred from validated file paths.

Directory inspection reads only inventoried payloads. Declared directories are
checked for extra entries; unrelated trees are not traversed. Source components
must be directories without links, payloads must be regular non-executable files,
and identity is checked while opening/copying. Copied bytes are hashed and owned by
the inspection result. ZIPs reject links, special files, encryption, duplicate names,
file/directory collisions, unsupported compression, corrupt CRCs and bounded-length
violations before any activation is possible. Cancellation is checked between chunks;
filesystem reads remain subject to the operating system's filesystem behavior.

Maintainers can regenerate the bundled example with
`go run ./scripts/generate_recipes.go`. Deterministic ZIP generation re-imports its
output with the same verifier before returning it.

## Install and activate locally

First import a folder or ZIP. Identical content returns the existing installation.
A different package with the same publisher name gets a separate stable ID and alias.

```sh
bin/deckctl recipe import recipes/benchmark --json
bin/deckctl recipe list --json
bin/deckctl recipe show LOCAL_ID --json
bin/deckctl recipe prepare LOCAL_ID '{"operation":"activate","mappings":{"benchmark":{"project":"my-project"},"demo":{"tool":"/absolute/path/to/deckdemo"}}}' --json
```

Use an existing project ID and the absolute executable you intend to run. Mapping
resolves the executable but does not launch it. Action roles map to named local
host actions; SCPI roles additionally require the model, channel, operation, units,
and intersecting local/device/recipe limits. Imported actions have at least `confirm`
safety. Stronger local/provider policy still applies to every later invocation.
Optional unmapped roles disable their dependent definitions with diagnostics.

The preview shows the installation/content identity, mappings, control assignments,
before/after portable manifests, effective definitions, ordered workflows, experiment
commands expanded with retained parameter values, changes and explicit resets.
Ordinary action definitions with required arguments show `<input:name>` placeholders;
those values are chosen and checked at invocation. Environment names are listed,
never resolved values. Secret input positions are redacted. Review the complete
preview; arbitrary literal text may still contain private content.

A preview expires after 60 seconds. Copy its ID and digest and generate a request ID:

```sh
bin/deckctl request-id
bin/deckctl recipe commit LOCAL_ID PREPARATION DIGEST REQUEST_ID --confirm
```

Use the exact installation ID returned by the preview when committing. If the
connection fails, retry the **same complete command and request ID** to retrieve its
receipt. Do not invent a new ID until you know the earlier result. Unknown request
IDs must be within 24 hours (at most five minutes ahead). Receipts survive restart
for at least 25 hours. Changes to context, parameter values, configuration, generation
or recipe selection invalidate uncommitted previews. Import trust does not approve
a future capture, command, model request, or hardware operation.

Suggested controls start unassigned. Add explicit assignments in preparation JSON,
for example `"assignments":{"capture":{"device":"deck","control":"key1","gesture":"press"}}`.
Use a control name from the manifest and your adapter's device/control IDs. Gestures
are `press`, `long_press`, `touch`, `long_touch`, and parameter `rotate`. Core ambiguity
and target validation still applies. Physical assignments require a mapped project.

## Update, disable, remove and recover

Stage an update with `recipe stage LOCAL_ID /path/to/new.zip`. Prepare with
`{"operation":"update"}`, then review and commit as above. Updates and rollback
require the exact installation ID. A rejected update leaves the active version
intact. Current and previous versions remain selected; `{"operation":"rollback"}`
uses the same review process. Compatible parameter values survive publication.
If a value no longer fits, preparation requires the explicit choice
`"reset_parameters":true` and identifies the affected parameters.

Other operations are `deactivate`, `remove`, and `rename` with `"alias":"new-name"`.
Renaming changes only the display alias. Deactivation/removal prevent new recipe
invocations and invalidate old preparations. Admitted jobs finish with their owned
plans; saved runs retain recipe identity and experiment metadata after removal.
Rollback changes configuration only; it cannot undo completed commands or equipment
settings. No lifecycle operation runs an install/uninstall hook.

The private store is `<state.path>.recipes`, limited to 200 MiB, 50 installations,
one staged candidate and the current/previous selections per installation. Staging,
old archives and incomplete files count toward the quota. Nothing is evicted to
make room. `recipe list` reports bytes and unused files. To review explicit cleanup:

```sh
bin/deckctl recipe prepare store '{"operation":"cleanup"}' --json
bin/deckctl recipe commit store PREPARATION DIGEST REQUEST_ID --confirm
```

Only the listed unselected archives/staging files are removed. Selected content and
saved runs are retained. Failed cleanup leaves files charged and reports diagnostics;
a new cleanup review can retry. In-flight jobs own their definitions and do not
reopen recipe archives.

### Transaction recovery

The host YAML is independently valid and never rewritten by recipe management.
All publication and work admission use the same runtime lock. The selection journal
has these recovery rules:

| Failure boundary | Runtime and restart behavior |
| --- | --- |
| Before `intent.json` or before selection rename | Old selection/generation stays authoritative. Incomplete files remain charged. |
| Intent persisted, selection absent/unchanged | Restart ignores the inert intent and uses `selection.json`. |
| Selection renamed but directory sync fails | Stop new admissions; restart resolves the selected file and receipt. |
| Selection durable, publication interrupted | Restart validates the selected composition before accepting work; never replays an action. |
| Publication succeeds, response lost | Same management request returns its durable receipt. |

Restart reloads immutable packages and the current host YAML. Changed host config
invalidates recipe grants; invalid packages/bindings are disabled with diagnostics
while the host remains usable. Re-review activation to grant the new composition.
An unsupported/corrupt store is preserved and recipe management becomes unavailable.
A backward clock makes the store read-only. Stop the daemon before backing up the
host config, state, runs and recipe directory together; preserve private permissions.
