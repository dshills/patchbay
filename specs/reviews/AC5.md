# AC-5 review — bounded patch application and recovery

## Prism disposition

Source, tests and documentation were reviewed with Gemini `gemini-3-flash-preview`.
No private project context or production runtime data was submitted.

1. `30f0cf928a7155c465a9d8f3ba624d46`:
   - Medium, directory inode/device portability: retained intentionally. This
     implementation targets the existing macOS/Linux filesystem contract. Moving,
     replacing or recloning a project directory invalidates the old operation;
     canonical paths alone would weaken that precondition. No Windows support is claimed.
   - Medium, index-derived staging names: the ordered file slice is immutable and
     persisted with the plan. No map conversion or sort reorders it. Recovery rejects
     changed order instead of retargeting a stage file. No change required.
   - Low, repeated Git subprocesses: capped at ten files and a one-second deadline
     per read. The checks immediately before each replacement deliberately recheck
     tracked status/HEAD; a long-lived Git process is unnecessary here.
2. `1161c7aa1db4b2398a53cdc8e037e266`:
   - High, alleged missing runtime mutex: false positive. `PrepareAgentProposal`
     already locks `r.mu` and defers its unlock before touching preparations or calling
     the private patch helper. A twelve-client concurrent preparation regression
     passes under the race detector and returns twelve distinct complete previews.
   - Medium, repeated marshaling for quota accounting: confirmed and fixed. The
     patch store now tracks committed bytes per record and updates totals after
     rename and deletion; startup reconstructs totals from bounded files. The reported
     path was inaccurate; the implementation is `internal/patching/store.go`.

3. `5ef33e7cde26f28abb2b3f049e3b9449` reviewed the quota fix and final
   selection change. Its sole medium finding alleged a leaked `syncRoot` directory
   handle; the helper already defers `f.Close()` immediately after the open/error
   check. No change required. No high findings were returned.

4. `ae572562f1f325159ebf446e1be19502` reviewed the final execution/metadata lock
   split and cancellation regression. No high findings were returned.
   - Medium, suggested promoting `journal.pending` during recovery: declined.
     Rename followed by directory sync is the metadata commit boundary. Scratch
     bytes do not establish a committed intent or successful operation. Committed
     preimages already support reconciliation from actual current files after a
     replacement; recovery reports interrupted/applied/unapplied without replay.
     A regression verifies that valid uncommitted metadata is discarded, both
     before and after a replacement, and cannot introduce a new operation. It
     passes under the race detector. The reported `journal.go` path does not exist;
     this code is in `internal/patching/store.go`.
   - Low, omitted Git stderr: accepted diagnostic limitation. The helper returns
     the process error, while callers provide operation-specific failures. Raw Git
     stderr can contain private paths and is deliberately excluded from persisted
     proposal/run errors; local Git commands remain available for diagnosis.

A final cross-feature inspection found that clearing selection reset the deck's
open-review counter. Preserving its monotonic value makes requests for successive
proposals distinct. The browser scopes the observed counter to the daemon instance,
and a regression verifies repeated review requests across different selections.
Focused race tests, lint and all four browser scenarios passed after this correction.

The final lock review also found that patch execution held the journal metadata
mutex throughout staging and replacement. A history poll could then hold the runtime
mutex while waiting for the journal, delaying cancellation. Execution now uses its
own cancellable semaphore, with brief metadata transactions and cloned records.
The persisted `staging` claim prevents queued finalization or deletion from racing
an active write. A regression pauses immediately before replacement, polls history,
cancels the job, and verifies that the original file remains unchanged.

## Verification

Full `make check` passed after the cancellation fix (vet, lint, ordinary/race tests,
builds, plugin conformance and 20 Python release tests).
All four real-browser scenarios passed on the same code. The patch screenshot was inspected: complete
plain-text diff and project/hash metadata remain available with keyboard review controls.

Patch tests cover exact/invalid hunks, LF/CRLF/Unicode/missing final newline, empty
existing files, forbidden/untracked/symlink paths, changed preimages/HEAD/grants,
concurrent preparation and duplicate approvals, audit failure, cancellation, staging
replacement, conflicting external edits, cross-store project locking, restoration
without automatic execution, and killed subprocesses after stage creation, staging,
ready commit and replacement. A real daemon crash after replacement retains an
interrupted CC run linked to the separately reconciled applied-file record; no
provider request, replay or revert occurs.

The native offline smoke removes the key, uses a private temporary Git project and
bundled binaries, and verifies one generation, one configured action proposal,
three measured helper runs, a locally supplied untrusted patch, separately approved
validation/comparison and separately reviewed restoration. No recipe is required.
Both macOS architectures pass static archive checks; arm64 passes the full native
archive gate. Final unsigned local candidates are version `0.2.0-rc7`, built at
`2026-09-28T18:06:13Z` from base commit `63dac69` plus the reviewed changes
(`source_dirty:true`). They are verification artifacts, not published releases.
They include the final runtime cancellation fix; the additional scratch-recovery
regression and final review-report updates followed packaging without changing binaries.

| Architecture | SHA-256 |
| --- | --- |
| darwin-arm64 | `c88d9729cd17df894d9469aa8425413a05f5e9273407ec7268cf6294f18c2b48` |
| darwin-amd64 | `83c918d632ce06102424ca11509c91ab07540c97d37581ce2436a68472504014` |

## Scope and remaining external gates

Patch capability is opt-in, requires an exact local grant and explicit file allowlist,
and is disabled in shipped example configs. Ordinary provider actions remain text-only.
Restoration records are private and bounded; preimages also count toward CC artifact
quotas. Existing run evidence is immutable; recovery never overwrites a terminal run.
A failed partial stage that cannot be identified safely remains for manual inspection.
External editors can still race the final read/rename boundary; multiple renames are
not one filesystem transaction. The user guide describes these limits.

Human/user-project observation, live-provider generation, physical Stream Deck/Rigol,
native Intel, clean-account upgrade, signing/notarization and Gatekeeper gates remain
pending. Deterministic fixtures do not claim those results. No release was published.
