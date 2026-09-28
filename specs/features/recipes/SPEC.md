# Shareable Recipes specification

**Status:** Software implemented, version 1; external verification gates pending\
**Date:** 2026-09-28\
**Plan:** [Implementation plan](PLAN.md)\
**Parent:** [Feature roadmap](../README.md)

## 1. Purpose

Let users exchange a useful Patchbay setup: actions, parameters, a control layout,
experiments, charts, and sample results. Import should explain what is needed and
what will run, then help the user map their own project and devices. Opening a
project or viewing a recipe must remain inert.

The first release supports local directories and portable ZIP files. Users may
share those files through their preferred channel; Patchbay does not publish or
upload them automatically. A marketplace, accounts, remote registry, dependency
installer, and unattended updates are outside the first release.

### User journeys

1. **Try a setup:** open a bundled Benchmark Playground recipe, inspect sample
   data, preview its commands, activate it for a project, and run an experiment.
2. **Bring your own bench:** import a Rigol recipe, map generator/scope roles to
   already configured devices, inspect limits, and explicitly activate the layout.
3. **Share:** export a local setup with selected sample runs and documentation;
   review the exact package inventory and private fields excluded from it.
4. **Update:** compare an incoming version with the installed version, resolve new
   dependencies, inspect changed permissions/commands, and activate or cancel.
5. **Remove:** deactivate a recipe while a job is running; the admitted job keeps
   its pinned definitions, saved evidence remains readable, and new runs are blocked.

## 2. Functional requirements

| ID | Requirement |
| --- | --- |
| RP-F01 | Define a strict, versioned recipe format containing metadata, declared requirements, actions/parameters/workflows/experiments, a layout, and optional sample data. |
| RP-F02 | Inspect/import bounded local content without running commands, contacting devices, loading plugins, or making network requests. |
| RP-F03 | Preview exact effective actions, destinations, dependency mappings, permission floors, and configuration changes before activation. |
| RP-F04 | Bind abstract project/device/tool roles to explicit local choices; keep credentials, machine paths, addresses, and secret values out of portable definitions. |
| RP-F05 | Activate a fully validated recipe transactionally through the normal config-generation path, preserving existing work and preventing name collisions. |
| RP-F06 | Review updates by content digest; preserve local bindings when valid; require renewed approval for changed execution definitions or permissions. |
| RP-F07 | Deactivate, remove, and roll back configuration without replaying actions or deleting saved runs. Retain required definitions for admitted jobs. |
| RP-F08 | Export portable recipes and selected samples with inventory/content preview, checksums, license metadata, and private-data exclusions. |
| RP-F09 | Provide guided workbench and CLI flows with clear installed, unresolved, ready, disabled, and update-pending states. |
| RP-F10 | Ship Benchmark Playground, Project Checkup, and a correctly labeled Rigol Capture & Compare recipe with runnable or explicitly simulated examples. |

## 3. Format and package contents

Canonical directory layout:

```text
recipe.yaml
LICENSE
README.md
samples/                 # optional CC-schema data and report inputs
```

The manifest explicitly inventories every payload file other than `recipe.yaml`
by normalized relative path, media type, size, and SHA-256. Its own digest is
computed from the canonical
validated manifest excluding the digest field. A package digest covers that
manifest digest plus the ordered file inventory. The importer also hashes every
file; manifest claims alone are insufficient. Digests identify content, not the
author's identity or trustworthiness. Publisher signing is deferred.

| Manifest area | Contract |
| --- | --- |
| Metadata | Schema version, publisher-declared recipe ID, display name, description, author attribution, declared license, recipe version, minimum Patchbay feature/schema versions. The declared ID is not globally unique. |
| Requirements | Named roles for project roots, executable tools, existing actions/providers, and device capabilities; distinguish required and optional roles. |
| Parameters | Typed defaults, units and bounds; values cannot contain credentials or machine-specific paths. Instrument bounds must narrow locally configured limits. |
| Actions/workflows | Supported semantic operations and declared input mappings. Command templates use the existing constrained grammar and argument arrays. No implicit shell. |
| Experiments | Capture & Compare definitions with exact collector/step references and compatibility metadata. |
| Layout | Capture & Compare widgets and optional Stream Deck binding suggestions, all using recipe-local semantic names. |
| Samples | Declared immutable example results using supported Capture & Compare schemas; origin is always `sample`, never a completed local job. |
| Inventory | Allowlisted package files, sizes, media types and digests. |

This is a proposed new schema; current config YAML is not a recipe file. Unknown
versions, keys, duplicate keys, unresolved references, and incompatible capability
versions must fail inspection with a field/path explanation. Use integer schema
versions and SemVer recipe versions with no implied trust in a higher version.
The content digest remains the installation identity even if a publisher reuses
a version string.

### Allowed definitions and local bindings

Recipes may declare exec, open, Git, and sequential workflow actions using the
existing provider restrictions. They may reference explicitly mapped, locally
configured SCPI, plugin, or text-agent actions. They cannot install executables,
configure device endpoints, supply plugin binaries, embed API keys, alter provider
credentials, change the daemon socket/storage/security policy, or enable dangerous
actions. Agent execution grants from [Agent Control Panel](../agent-control/SPEC.md)
cannot be conferred by a recipe.

Prefer abstract tool requirements such as `go` mapped to a resolved local executable.
Resolve paths from explicit local choices and record those choices privately. Do
not execute `--version` or other discovery commands during import/preview. Optional
dependency checks run only as separately confirmed configured actions after review.
Tool availability inspection is limited to filesystem metadata and capability
discovery; it does not claim compatibility until a real check has passed.

A local SCPI role binds to an existing device/action with matching model/capability,
channel and unit. Show effective bounds as the intersection with local limits;
an empty intersection is an error. Device setup and dangerous opt-in remain separate
operator steps. A recipe cannot relax either provider floors or configured limits.

Host action references are pinned by effective definition digest. A reload changing
a referenced executable, arguments, provider target, risk, or input schema invalidates
that binding for new runs until reviewed. In-flight jobs keep their admitted snapshot.
Canonical digest rules must include project overrides and nested workflow steps.

Declared experiments receive the Capture & Compare semantic action wrapper; recipe
controls use that same prepared capture path. Run provenance stores both the local
installation ID and the declared recipe ID/version/content digest. Resolve sample
experiment/collector references through the installation namespace for comparisons,
while preserving their original portable IDs and payload hashes. Importing into a
different local namespace must not make otherwise compatible samples unusable.

## 4. Import and inspection boundary

The CLI may read a user-selected directory or ZIP. Directory import copies only
manifest-inventoried files into private staging; it never recursively sweeps a
project. Inspect path components and open without following symlinks; verify bytes
after copying so source edits during import cannot change reviewed content.

Initial limits: 20 MiB compressed input, 20 MiB expanded content, 64 files, 4 MiB per
file, a 256 KiB manifest, 128 actions, 32 workflows, 32 experiments, and 32 widgets
per layout. Nested archives and executable payloads are forbidden. Accepted payloads
are strict YAML/JSON, plain-text license/documentation, and declared sample data.
No HTML, SVG, scripts, remote images, custom CSS, or native libraries are accepted.
The workbench supplies its own icons and renders documentation as escaped text or
a restricted Markdown subset with no raw HTML, images, or automatic link fetching.

ZIP import must reject traversal, absolute paths, backslashes/ambiguous separators,
symlinks, special files, duplicate entries, Unicode/case-colliding names, encrypted
entries, missing/unlisted files, size mismatches, oversized expansion, and digest
mismatches. Enforce limits while reading, not after extraction. Do not invoke a shell
archive tool. Read-only preview produces no runtime configuration changes.

Allow at most two simultaneous imports, each with a 30-second total processing
deadline including decompression, parsing and hashing. Bound stream buffers to
64 KiB and check cancellation between chunks/entries. A compression-ratio threshold
is not required: small, highly compressible sample data is valid. Absolute compressed
and expanded byte limits, concurrency, cancellation and deadline jointly bound work.

Re-importing identical content is idempotent. The same declared ID/version with
different content becomes a new staged candidate. The user chooses to install it
separately or target an existing installation for a reviewed update; it cannot
silently overwrite the installed copy. Imported content remains untrusted even
when it claims an official name. Bundled recipes are identified from the release's
verified inventory, not from a field that an imported manifest can forge.

## 5. Activation, updates, and removal

### Activation state machine

`staged -> unresolved | ready -> active -> disabled -> removed`

An update stages a new content digest alongside the active version. It does not
change the active state until an approved transaction commits. Validation failures
retain the active version and expose the rejected candidate's diagnostics.

Activation requires a daemon-generated preview bound to the recipe digest, local
bindings, base configuration digest/generation, effective provider floors, and
complete composition digest. The user sees expanded non-secret commands/arguments,
working directories, open/Git destinations, device roles, persistence requests,
new bindings, and collisions. Mask secret values while preserving argument positions,
field names and declared local reference sources; never hide secret use or display
resolved environment values. Granting import trust does not confirm any future action invocation.

Use recipe-local IDs in the manifest. Assign each local installation a generated
opaque ID, stable across its reviewed updates, and a user-editable display alias.
Aliases must be unique after normalization/case folding; propose a short ID suffix
when a name already exists and show it in the import preview. Lists show alias,
short installation ID and version. CLI selection accepts an exact ID or unique
alias and rejects ambiguity rather than choosing an installation implicitly.
Map definitions under `recipe.<installation-id>.`, with all segments conforming to
the core name grammar. Publisher-declared IDs and display aliases do not grant
namespace ownership. Two unrelated recipes named `benchmark` can coexist. Updates
must explicitly target the installation ID; changing an alias never changes bindings.
Collision with host or other installed definitions is a validation error; no
last-writer-wins behavior. A recipe's suggested
Stream Deck bindings begin disabled until the user selects a device/control mapping.
Overlapping bindings must pass the existing ambiguity checks.

Store imported immutable content and local binding/approval metadata under a private
recipe directory. Build a complete candidate configuration from the host config and
explicit active recipe list. Never rewrite the user's source config to inline a
recipe. The host configuration must validate independently and cannot reference
recipe-only definitions; user assignments to recipe controls live in the local
recipe binding metadata. Commit through the existing validation and generation
machinery.

Implement a recoverable activation journal: prepare immutable candidate content,
validate against the current base, persist transaction intent, atomically select the
new active manifest, then publish the runtime generation. Admission is serialized
with this commit. If persistence fails before selection, retain the old generation.
If failure occurs after durable selection, recover to that selected state before
accepting new work; do not run with inconsistent disk/runtime composition. Restart
revalidates against the current host config; incompatible recipes are disabled with
diagnostics while the independently valid host configuration remains usable.

Imported device/daemon changes requiring restart cannot be activated; report them
as unsupported recipe content. Activation/deactivation must not start providers,
execute actions, automatically apply parameters to hardware, or launch AI requests.

### Update and rollback

Updates always show a semantic diff: actions, arguments, permissions, input bounds,
collectors, layout, required capabilities, and sample content. Require explicit
approval for activation, even when the version number increases. Preserve local
bindings only when their effective digests and constraints remain compatible.
Recipe parameters keep valid user values; invalid values require an explicit reset
choice before activation. They never silently change instrument output state.

Keep the current and immediately previous activated recipe versions, bounded by a
200 MiB recipe-store quota and 50 installation IDs. Staging counts toward the
quota; reject imports instead of evicting active content. In-flight jobs pin the
definitions/content they need. Remove older unused versions explicitly. Rollback
uses the same preview and validation transaction; it restores configuration only
and does not reverse completed commands, patches, or equipment effects.

Deactivation blocks new recipe admissions and invalidates preparations/agent
proposals referring to it. Existing jobs continue with pinned definitions unless
the user explicitly cancels them. Removal preserves saved runs and their recipe
digests; enough experiment/collector metadata lives in those runs to inspect them
without the package. No recipe-defined uninstall hook runs.

## 6. Export and sharing

Export includes selected declarative definitions, documentation, license, inventory,
and optionally selected saved results converted to sample records. Default excludes
local bindings, approvals, environment values, absolute paths, serial numbers,
network addresses, raw logs, notes, and AI conversation data. Parameter values
export only when explicitly selected as portable defaults and valid under bounds.

Generate a preview of the exact files/fields and portable placeholders. Unresolved
machine-specific values block portable export until mapped to requirements or
removed. Secret-pattern checks are advisory; users can place private text in any
field, so the content preview remains necessary. Never claim the package is free
of all sensitive data based on scanning alone.

Build deterministic ZIPs with stable entry order, timestamps, permissions, and
canonical metadata; recompute all hashes. Test the result by re-importing through
the same verifier. Sample data preserves units, compatibility metadata, and source
labels while replacing local run/job IDs with package-local IDs. Sample history is
kept separate from measured local runs in both lists and comparisons.

## 7. Interfaces and compatibility

| Proposed interface | Purpose |
| --- | --- |
| `deckctl recipe inspect <path>` | Offline bounded parse, verify and describe; no execution. |
| `deckctl recipe import <path>` | Stage a verified package in the selected daemon's recipe store. |
| `deckctl recipe list/show` | Inspect installed content, mappings, status and diagnostics. |
| `deckctl recipe activate/deactivate/remove` | Preview and explicitly change active composition. |
| `deckctl recipe export` | Review and write a portable package to a chosen destination. |
| `POST /v1/recipes/imports` | Stream a bounded ZIP into staging; route-specific media type/size limits override the normal JSON-body ceiling only here. |
| `GET /v1/recipes` and `GET /v1/recipes/{id}` | Paginated installed state and effective requirements. |
| `POST /v1/recipes/{id}/prepare` | Validate chosen version/bindings and create a one-use, 60-second activation/deactivation/removal preview. |
| `POST /v1/recipes/{id}/activate` | Commit matching preview/digest/request ID transactionally. |
| `POST /v1/recipes/{id}/deactivate` and `DELETE /v1/recipes/{id}` | Commit the corresponding prepared management operation. |
| `POST /v1/recipes/{id}/export/preview` and `POST /v1/recipes/{id}/export` | Select exact package content, then generate it. |

At most 64 preparations are outstanding. Management operations use durable request-ID
deduplication and stale-preview rules from Capture & Compare. Imports/exports must
have cancellable bounded I/O and progress; cancellation before transaction commit
discards staging, while after commit it reports the committed result. The browser
bridge adds these routes explicitly and retains its authentication/origin checks.
In management routes, `{id}` always identifies a local installation; response
metadata separately exposes the declared recipe ID, content digest and display alias.

Add capability `recipes:1`; older daemons reject recipe operations clearly. Current
plain config files remain valid and never gain automatic includes. Plugin protocol
v1 remains unchanged. Recipes declare needed CC schemas and optional AC features;
unsupported mandatory capabilities prevent activation, while optional widgets show
their unavailable state without altering the remaining recipe.

## 8. Bundled recipes

| Recipe | First version and constraints |
| --- | --- |
| Benchmark Playground | Packaged demo tool, worker-count parameter, sample results, repeated measurements, baseline/candidate chart, no external dependencies. |
| Project Checkup | Explicit project and tool mapping; configured check commands; saved per-step status and duration compared with a prior run. Dependency probing never executes automatically. |
| Rigol Capture & Compare | Existing generator/scope roles, desired/readback display, stopped waveform capture, operator acquisition notes, and the exact current output/apply policy. Requires CC-5 physical evidence before a verified-hardware label. |

## 9. Acceptance scenarios

- **RP-A01:** Import a hostile recipe with command definitions and broken references;
  inspection executes nothing, changes no active config, and performs no network I/O.
- **RP-A02:** Traversal, links, duplicate/colliding names, bombs, forged hashes, and
  source mutation during directory copy are rejected within resource limits.
- **RP-A03:** Import, map, preview, and activate a software recipe on a second clean
  installation. Its generated actions and controls work through ordinary policy.
  Two unrelated packages with the same declared ID coexist under distinct local IDs;
  updating or renaming one does not change the other's definitions or bindings.
- **RP-A04:** A stale preview, namespace collision, widened permission, changed host
  action, or incompatible parameter cannot silently activate. Inject failure/crash
  at every activation boundary; recover to one validated composition.
- **RP-A05:** Update/deactivate/remove during a running job preserves that job's
  admitted definitions and saved evidence. New admissions and stale proposals fail.
- **RP-A06:** Export and re-import preserve portable behavior and sample labels,
  exclude planted local secrets by default, and never mark sample runs as measured.
- **RP-A07:** Rigol requirements reject wrong model/capability/bounds and never enable
  output or contact instruments during mapping/activation.
- **RP-A08:** CLI and workbench expose the same lifecycle, cancellation, diagnostics,
  and privacy preview, including keyboard navigation and offline operation.

## 10. Future decisions

Consider signed publishers, a browsable community catalog, or remote downloads only
after local exchange is useful. Those require separate trust, update, moderation,
and revocation contracts. Do not make a recipe marketplace a prerequisite for sharing
the first three recipes.
