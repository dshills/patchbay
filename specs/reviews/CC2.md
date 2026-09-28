# CC-2 review and verification

Date: 2026-09-28. Scope: durable admission, collection, sample/demo workload,
comparison, export previews and offline reports, semantic experiment actions.

Provider: Prism with Gemini `gemini-3-flash-preview`. The initial whole-diff request
timed out. Two bounded reviews succeeded:

- Capture/scheduler: `eae8486f0da82fe4afac12db590d191d`.
- Comparison/export/CLI: `e0aaa63934ff42fe7220dbf7d3ed9416`.

| Finding | Disposition |
| --- | --- |
| High: prepared plans could share mutable collector callbacks | Admission already consumes each preparation under the runtime lock. Refactored anyway: prepared plans stay immutable and each execution receives its own collector callback. |
| High: parallel workflow leaves could race on a run | Current workflows execute sequentially. Added an execution-local collector mutex and a race test with two independent concurrent captures to make ownership explicit. |
| Low: comparison/export lacked request cancellation | Added request context propagation and checks around bounded artifact retrieval and rendering; cancelled exports retain their approval token. |
| Low: JSON text in reports showed escaped Unicode | Disabled the JSON encoder's HTML escapes inside the text helper; `html/template` still escapes the resulting string in its HTML context. No trusted-HTML bypass is used. |

Validation: full `make check` passed, including vet/lint, tests, race tests, builds,
plugin verification, and 20 release-tool tests. Additional race tests passed after
review corrections. Evidence covers concurrent identical retries, distinct capture
ownership, stale/expired preparation, queued cancellation, private input pinning,
required/optional malformed collectors, truncation, unit mismatch, zero/overflow
comparison cases, exact series grids, export privacy/reference holds/token expiry,
escaped HTML, cancelled exports, and source changes during a run.

The real integration fixture builds `deckdemo`, captures twice, compares measured
and sample results, exports HTML, restarts the daemon, and verifies terminal evidence.
Injected disk failures prove that failed admission dispatches nothing and failed
recording after an external operation never replays it. Hardware is not involved.

A follow-up diff review failed during Prism's JSON repair request (provider timeout).
A smaller review succeeded: `ae5138ca5bdf6421b8b1216d0e58936b`.

- Claimed unpersisted source-end observation: verified that `capture.go` already
  refreshes artifact inventory and calls `r.runs.Update(local)` after source-end
  calculation. The source-change integration test covers that saved record.
- Collector failure could obscure the execution outcome: the run already retains
  each original step outcome. Added explicit `execution_outcome` and
  `collection_error` fields to the job's leaf result as well, preserving command
  success/failure while the experiment itself fails required collection.
- Suggested continuing after a storage failure: deliberately retained fail-closed
  behavior. A failed durable write makes the store read-only and stops further
  effects in that capture. Optional malformed measurements still remain nonfatal.

No unresolved actionable findings remain. The final recording-failure regression
checks that the original successful subprocess outcome remains visible in the job.
