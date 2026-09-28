# CC-1 review and verification

Date: 2026-09-28. Scope: experiment schemas/preparation, bounded durable evidence,
read and annotation/baseline APIs, CLI inspection. Capture execution follows in CC-2.

Prism provider: Gemini, model `gemini-3-flash-preview`.
Review ID: `a8683b9a537d2b202d6cc4174c13907b`.

| Finding | Disposition |
| --- | --- |
| Medium: artifact strings amplify memory during JSON encoding | Changed the response to a typed byte payload encoded as base64, bounded by the 4 MiB artifact limit. |
| Low: storage-open error hidden by generic diagnostics | Included the local initialization error in storage diagnostics. |

Additional inspection fixes: reject initial manifests above the configured per-run
limit; invalidate physical-control previews when the baseline changes.

Validation: `make check` passed (format, vet, lint, tests, race tests, all builds,
plugin conformance, 20 release-tool tests). Added storage fault injection at write,
file-sync, rename, and directory-sync boundaries; restart/deduplication, concurrent
admission, annotation compare-and-swap, references/pins/baselines, tombstones,
corruption/symlink, request-window/clock rollback, immutable snapshot, experiment
schema/override/sensitivity, preparation privacy/invalidation, and API fixtures.
Affected package/race checks were repeated after the corrective changes.

Migration policy: no automatic migration; unknown/corrupt versions remain in place
and disable store mutations. Unrelated ordinary actions remain available.

Follow-up review ID: `86ac981f7cc2206076ffed9108b1074e`.

- Claimed missing `SetBaseline` lock: not reproduced; the method starts with
  `r.mu.Lock()` / `defer r.mu.Unlock()`. The short diff omitted that unchanged context.
- Claimed artifact API break: this is a new, unreleased route within CC-1; its first
  committed contract uses `data_base64`. No existing consumer used the interim field.
- Manifest/run limits: these intentionally govern different resources. Combined the
  admission check using their minimum, and reused the encoded bytes for the write.
- Marshal failure classification: now returns `internal`; quota failures retain
  `storage_full`.
- Suggested punctuation change: existing message explicitly includes a separating
  space; no correctness issue or change needed.
- Redundant marshal: removed along with the combined limit check.

No unresolved actionable findings remain. Review findings were checked against the
full source rather than accepted solely from short diff context.
