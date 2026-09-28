# Shareable Recipes implementation plan

**Status:** RP-1/RP-2/RP-3 implemented; RP-4 next\
**Specification:** [Shareable Recipes](SPEC.md)\
**Dependencies:** CC-1 schemas; CC-2 for activation/runs; CC-3 for UI. CC-5 gates only the verified Rigol recipe.

## RP-1 — Portable format and inert inspection

**Outcome:** A recipe can be inspected and verified offline without changing runtime
state or executing any content.

**Depends on:** CC-1's experiment, layout, result, and capability schemas.

- [x] Define manifest/inventory, requirements, semantic definitions, layout references,
  license/version metadata, sample provenance, and canonical content digests.
- [x] Implement strict schema validation and compatibility checks. Reuse existing
  name, input, template, workflow and binding validators where their semantics match;
  keep imported policy constraints separate from trusted host configuration.
- [x] Implement directory copy and ZIP readers with finite budgets, no-follow path
  handling, collision detection, content verification, bounded concurrency/processing
  deadlines, and private staging cleanup.
- [x] Add offline `deckctl recipe inspect` in human and JSON modes. Explain commands,
  requirements, permissions and unresolved roles without loading providers.
- [x] Add malformed/adversarial fixtures and a minimal portable recipe that uses
  packaged CC sample results and the demo tool requirement.

**Verification:** Unknown/duplicate fields; unsupported schemas; nested workflow
references; traversal/absolute paths; Unicode/case collisions; symlinks; oversized
compression/expansion; malformed ZIP directory metadata; source-file replacement;
missing/unlisted content; forged digests; and import cancellation. Instrument the
test harness to prove zero subprocess, provider, network, and config mutations.

**Deliverables:** Recipe format guide, parser/verifier, inspect command, corpus and
fuzz targets for archive/manifest boundaries.

**Exit:** RP-F01/F02 and RP-A01/A02 pass; valid content has a stable digest and preview.

**Commit boundary:** Schema/digest; bounded importer; CLI and adversarial corpus.

**Evidence:** [RP-1 review and tests](../../reviews/RP1.md), [format guide](../../RECIPES.md).

## RP-2 — Local bindings and transactional activation

**Outcome:** A reviewed recipe becomes usable without overwriting host configuration
or changing the definitions of already admitted work.

**Depends on:** RP-1 and CC-2; existing atomic config-generation machinery.

- [x] Add private recipe content/binding/grant storage with installed/staged/version
  quotas and capability status. Keep portable files separate from local metadata.
- [x] Assign stable local installation IDs and editable display aliases; keep declared
  publisher IDs separate. Support same-name packages, unique aliases with proposed
  suffixes, unambiguous CLI selection and explicit update targeting.
- [x] Implement project/tool/action/device binding with provider identity, channel,
  unit and limit intersection checks. Resolve executable paths without running them.
- [x] Compose namespaced definitions with host config, fully validate overrides and
  nested workflows, and detect all action/parameter/control collisions.
- [x] Build daemon-derived previews covering exact effective commands, destinations,
  permissions, config changes and parameter resets; bind them to base and content
  digests, generation, local mappings, expiry, and durable management request IDs.
- [x] Implement activation journal/recovery and serialized publication of active disk
  manifest plus runtime generation. Document each crash point and authoritative state.
- [x] Add update semantic diffs, compatible binding/value retention, explicit reset
  choice, deactivation, removal, one-version rollback, and in-flight reference ownership.
- [x] Invalidate captures/agent proposals on deactivation or effective binding changes.
  Preserve durable evidence and block new unreviewed recipe invocations on host reload.
- [x] Extend CLI/API and operations docs; keep plain config behavior and plugin v1 intact.

**Verification:** Real daemon transactions with concurrent jobs/reloads; changed action
digests; namespace/binding collisions; stronger provider floors; invalid device limits;
expired/replayed approvals; disk full at each commit stage; daemon termination/restart;
base config changed offline; update/removal with pinned resources; and quota cleanup.
Include two unrelated recipes with the same declared ID and prove independent
activation, alias changes, updates, removal and saved-run provenance.

**Deliverables:** Lifecycle service, composition integration, management commands,
documented recovery procedure, and transaction/state-machine tests.

**Exit:** RP-F03 through RP-F07 and RP-A03/A04/A05/A07 pass through CLI/API. Activation
produces no action execution, provider request, hardware write, or automatic approval.

**Commit boundary:** Bindings/composition; previews/activation; updates/removal/recovery.

**Evidence:** [RP-2 review and tests](../../reviews/RP2.md), [lifecycle and recovery guide](../../RECIPES.md).

## RP-3 — Guided setup and portable export

**Outcome:** Users can exchange a setup, understand its effects, and activate it
without manually editing configuration files.

**Depends on:** RP-2 and CC-3 workbench/browser security boundary.

- [x] Add local import, sample preview, requirement mapping, exact-action preview,
  control assignment, activation, updates, disable/remove and rollback screens.
- [x] Separate sample data from measured local history. Show which roles are unresolved
  and which compatibility claims have real evidence. Keep optional missing roles visible.
- [x] Add bounded upload/download routes explicitly to the bridge allowlist; retain
  authentication, same-origin constraints, progress/cancellation and stale-session rules.
- [x] Build export selection and privacy preview for commands, portable defaults,
  documentation, license, and sample results. Require machine-specific fields to be
  converted to requirements or removed before export.
- [x] Produce deterministic ZIPs and re-import them through the same verifier; retain
  sample/measurement distinctions and content identity across round trips.
- [x] Verify keyboard operation, focus, error recovery, large manifests within quotas,
  no raw HTML execution, and no automatic loading when a project is opened or cloned.

**Verification:** Browser-to-real-daemon import/run/export round trip; planted secrets;
content/manifest mutation after preview; malicious documentation; expired sessions;
download naming; cancellation before/after activation commit; missing dependency flow;
two simultaneous management clients; offline use on a second installation.

**Deliverables:** Workbench recipe flow, export CLI/API, format fixtures, user guide,
and reproducible portable example.

**Exit:** RP-F08/F09 and RP-A06/A08 pass. A recipient can identify exactly what will
execute, supply their local mappings, and run a valid recipe.

**Commit boundary:** Guided import/setup; lifecycle UI; portable export and privacy.

**Evidence:** [RP-3 review and tests](../../reviews/RP3.md), [guided setup and export guide](../../RECIPES.md).

## RP-4 — Curated examples and release validation

**Outcome:** Three polished examples demonstrate the value and establish the recipe
authoring pattern without depending on a community marketplace.

**Depends on:** RP-3; CC-5 for a physically verified Rigol label only.

- [ ] Package Benchmark Playground, Project Checkup, and Rigol Capture & Compare with
  coherent controls, bounded defaults, sample results, descriptions, and licenses.
- [ ] Keep commands/project tools explicit in Project Checkup. Ship Rigol mappings as
  abstract roles, preserve manual acquisition/output steps, and show verification state.
- [ ] Add recipe files and notices to deterministic release inventories and verify all
  bundled manifests/examples against the actual packaged feature versions.
- [ ] Test clean installation, upgrade, deactivate/remove, rollback and stored-run
  readability without the recipe present. Document retention and export backup.
- [ ] Run two-person recipe exchanges on supported Macs. Record required help, missing
  mappings and confusing permission descriptions; fix first-use blockers.
- [ ] Complete Prism review, full release checks and a guide for authors with a minimal
  recipe plus schema/reference links. Record any remaining hardware/platform limits.

**Verification:** Full `make check`, browser checks, importer fuzz corpus, recipe
conformance, deterministic export/packaging, native packaged demo, and end-to-end
second-installation exchange. No network, equipment, or provider account is required
for the software recipe checks.

**Deliverables:** Curated packages, recipe authoring guide, exchange demo, release
evidence and Prism report.

**Exit:** RP-F10 and all RP acceptance scenarios have applicable evidence. Software
recipes can release while Rigol physical verification is still explicitly pending.

**Commit boundary:** Bundled recipes; release tooling; documentation and evidence.

## Acceptance traceability

| Requirements/scenarios | Owning phases |
| --- | --- |
| RP-F01/F02, RP-A01/A02 | RP-1 |
| RP-F03/F04/F05, RP-A03/A04/A07 | RP-2 |
| RP-F06/F07, RP-A05 | RP-2 |
| RP-F08, RP-A06 | RP-3 |
| RP-F09, RP-A08 | RP-3 |
| RP-F10 | RP-4 |

## Review and implementation discipline

Apply the [shared completion policy](../README.md) at each phase: relevant tests,
failure/cancellation coverage, race checks for shared state, updated docs, Prism
review with dispositions, and focused commits. Review importer and activation
contracts before UI polish because errors there can execute unexpected commands
or leave disk/runtime configuration inconsistent.

Do not add remote fetching, automatic dependency installation, a new plugin protocol,
or marketplace infrastructure while implementing these phases. If users need a new
capability, document the extension and its trust boundary before expanding the format.
