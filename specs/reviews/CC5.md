# CC-5: instrument evidence and physical controls

Date: 2026-09-28. Software/simulator work complete. Real DG812, MHO954 and
Stream Deck+ acceptance remains pending; no instrument was operated for this work.

## Prism

Run `21c83dbbd5dde20a8130c3cea8ae84d3`, Gemini / gemini-3-flash-preview.
One low finding: `observation.Before` is assigned twice. Intentional: the first
assignment retains the state if setup fails or acquisition was already running;
the second records the authoritative check immediately before transfer. Removing
the first assignment would lose diagnosis on early failure. No unresolved defect.

## Implemented

- Stopped acquisition checks at initial entry and immediately before/after transfer.
  A bounded complete waveform survives a changed/unknown post-state as suspect.
  No auto-stop, trigger, output-enable, or retry was added.
- Typed per-step identity, firmware, observation time, preamble and readbacks. The
  bench workflow inspects the generator on both sides, including after a failed
  transfer when execution has not been cancelled. Desired values are captured
  separately; acquisition time stays unknown. Operators can annotate assertions.
- Native waveform series with explicit seconds/volts; suspect series cannot overlay,
  produce deltas, or become baselines through a failed run. Workbench tables and
  diagnostic observations refresh when an active run gains artifacts.
- Physical experiment confirmation consumes the exact retained preview. Baseline
  selection names the displayed run, so later capture completion cannot retarget it.
  Result controls show state, first measurement or run ID, and baseline membership.
- Source-selected exports include instrument observations. Defaults still exclude
  source context, device aliases, firmware and operator notes.

## Evidence

Full `make check` passed, including lint, unit/integration tests, race checks,
builds, plugin checks and 20 release tests. Chromium workbench regressions passed.
Added tests cover STOP/RUN/unknown/disconnected final scope state; bracketing query
order; exact scaling; no acquisition/output writes; immutable durable observations;
suspect artifact retention and baseline rejection; final generator inspection;
exact physical preparation identity; duplicate/stale approval rejection; baseline
selection after a newer run completes; and adapter transmission of displayed run ID.

## Physical gate

Pending: firmware versions, DG812 transport adapter/endpoint, authorized wiring and
load, channel/probe limits, front-panel/export comparison, disconnect behavior,
control latency and operator sign-off. Both STOP observations alone cannot prove
that a transient front-panel change did not occur between queries. This is a transfer
of a stopped trace, not a guaranteed fresh or atomic acquisition.
