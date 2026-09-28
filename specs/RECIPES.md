# Portable recipes

Recipes are local folders or ZIP files containing declarative actions, parameters,
experiments, control suggestions and illustrative samples. Inspection is offline:

```sh
bin/deckctl recipe inspect recipes/benchmark
bin/deckctl recipe inspect /path/to/shared.zip --json
```

This command verifies content and explains unresolved roles. It does not execute
commands, probe tool versions, contact devices/providers, install dependencies or
change daemon configuration. Activation and guided setup are separate RP-2/RP-3 work.
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
