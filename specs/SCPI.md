# Electronics and SCPI

## Phase 5 design decision

Recorded before implementation, 2026-09-27. The first profiles target Rigol
DG812 generator and MHO954 oscilloscope over explicitly configured TCP
endpoints. Model identity is checked on every connection; firmware can be pinned.
Transport framing and profile commands remain separate. No raw-command action,
network discovery, agent delegation, or automatic output restoration is added.

Each operation owns a fresh connection and a per-device serialization slot.
Cancellation closes the connection; dial, I/O, total execution and response sizes
are bounded. An uncertain write is never retried. Device configuration changes
require restart so old jobs cannot bypass new limits or create competing locks.

Instrument parameters hold desired values. CLI edits and encoder rotation only
stage them. Explicit semantic actions apply a pinned desired value under normal
job permissions, then read back the instrument. Observed values, synchronization
state and observation time are separate, transient fields. Reload/restart never
claims restored values were applied and never sends them automatically.

Frequency/amplitude changes require both outputs off. Enabling output requires the
daemon's dangerous-action opt-in and confirmation, plus a checked sine-wave
configuration with no offset, modulation, sweep or burst and values within the
configured limits. A dedicated disable operation remains available with ordinary
confirmation. Scope capture reads a stopped acquisition as a bounded BYTE
waveform with scaling metadata; it does not trigger or resume acquisition.

Shutdown defaults to preserving instrument state, explicitly without an output-
off guarantee. An opt-in `output_off` policy attempts bounded disable on both
channels of a generator used by this process. Abrupt exit or lost connectivity
can leave output enabled; software is not an equipment interlock. Normal startup
does not contact equipment. Hardware validation is separate from simulator tests.

## Primary references

- [DG800 programming guide, PGB11100-1110, July 2018](https://beyondmeasure.rigoltech.com/acton/attachment/1579/f-08a6/1/-/-/-/-/DG800_ProgrammingGuide_EN.pdf), documenting firmware 00.00.01 and the DG812's 10 MHz limit.
- [MHO900 programming guide, 2026 revision](https://www.rigol.com/dam/global/downloads/brochures/en/program-guide/oscilloscopes/MHO900-ProgrammingGuide.pdf), including MHO954 and 1,000-point normal-mode reads.

Profiles are based on these command contracts. No physical instrument or firmware
has yet been validated in this workspace.

The DG812 requires its supported USB-LAN adapter for this TCP integration. Direct
USB/VISA is not implemented. Configure the actual SCPI socket address and port;
the example's loopback endpoints are placeholders. Do not assume that a LAN
VISA/VXI-11 resource is a raw TCP socket. The operator must establish exclusive
instrument access: another client or front-panel changes can invalidate readback
or alter settings between safety checks and a command.

## Configuration and capabilities

Start from [`configs/bench.yaml`](../configs/bench.yaml). It is safe to validate
without equipment and uses loopback placeholders. Replace both endpoints with
literal IP addresses and actual SCPI ports. The DG800 guide documents
`:SYST:COMM:LAN:CONT?` returning port 5555 when socket communication is supported,
or 0 otherwise; verify the installed adapter and firmware before use.

`devices` maps at most 16 names to these fields:

| Field | Contract |
| --- | --- |
| `provider`, `transport` | `scpi`, `tcp` |
| `address` | Literal unicast IP and port; no hostname, discovery or duplicate endpoints |
| `profile`, `model` | `rigol-dg800` / `DG812`, or `rigol-mho900` / `MHO954` |
| `firmware` | Optional exact `*IDN?` firmware match; omission accepts the model's reported version without claiming validation |
| `dial_timeout`, `io_timeout` | Default 2s each; 1ms–30s |
| `max_response_bytes` | Default 65536; 1024–1048576. Lines also cap at 4096 bytes; capture at 1000 samples |
| `shutdown` | `preserve` (default); `output_off` for the generator only |
| `limits` | Required generator frequency min/max in Hz and amplitude min/max in Vpp; absent for scope |

Generator limits must stay within 0.000001–10000000 Hz and 0.002–10 Vpp.
These are profile ceilings, not safe limits for every connected circuit. The
example narrows them to 1–1000000 Hz and 0.01–1 Vpp. Scope channels are 1–4;
generator channels are 1–2. Device definitions, including limits, require restart.
Other action/parameter definitions can use ordinary atomic reload.

Every operation has a 30s provider cap including time waiting for the device;
SCPI actions default to 15s and normal job/action/request deadlines may shorten
this. Operations on different instruments can run concurrently within job limits.
`status` exposes device descriptors and `scpi:<name>` health. Health is the last
operation's result, initially `not_checked`; it is not a background connectivity
probe. A failed device does not prevent unrelated actions or captures.

| Semantic operation | Input | Minimum permission and behavior |
| --- | --- | --- |
| `generator.inspect` | None | Confirm; reads frequency, Vpp amplitude and output state |
| `generator.set_frequency` | Float `value`, Hz | Confirm; both outputs off; readback required |
| `generator.set_amplitude` | Float `value`, Vpp | Confirm; both outputs off; selects Vpp then verifies readback |
| `generator.output` | Boolean `value` | Dangerous opt-in plus confirm, including `false`; `true` adds the enable checks below |
| `generator.disable` | None | Confirm; requests OFF before checking errors; verifies OFF |
| `scope.capture` | None | Confirm; reads an already stopped acquisition |

Actions specify `type: scpi`, `device`, `operation` and optional `channel`
(default 1). Numeric/bool input schemas are generated from the operation and
configured limits; callers cannot supply strings or raw commands. Alternatively,
an action specifies `parameter` bound to an instrument, without separate routing
fields. Configured safety can raise but never lower these floors.

Setting frequency/amplitude or enabling output requires tracking and all channel
coupling disabled. Output enable additionally requires SIN, VPP, high impedance,
zero offset, disabled modulation/sweep/burst/summing/harmonics, a clear instrument
error queue, and frequency/amplitude within configured limits. These checks reject
unsupported front-panel state; they do not silently reset the instrument.
`generator.disable` remains available when dangerous actions are disabled or the
instrument has a queued error. An error can still mean OFF was sent but its final
state could not be verified. Check physical output after any uncertain result.

## Desired values and observations

An instrument parameter adds:

```yaml
instrument: {device: generator, channel: 1, operation: generator.set_frequency}
```

Its type, unit and bounds must match the operation; only one parameter may bind
each device/channel/property. Bindings support frequency, amplitude and output.
Output parameters must start `false` and cannot persist. Persistent numeric
parameters restore desired values only.

`value` remains the desired value for API compatibility. The additive
`synchronization` object contains `desired`, optional `observed`, `observed_at`
and `error_code`, plus one of these states:

- `unobserved`: startup, reload, or a late operation from an older generation.
- `pending`: the desired value changed; no command has been sent by that edit.
- `matched`: the latest successful readback equals the current desired value.
- `different`: a successful readback differs from the current desired value.
- `error`: the latest operation failed; any retained observation is historical.

Readback never overwrites desired values. A rounded/rejected apply fails the job
and exposes the returned value when available. Equality is exact; no tolerance
is silently applied. Every action pins its value at admission. If desired changes
while that job runs, the final readback is compared with the new desired value.
Reload clears observations; an old job may still execute its admitted command,
but its completion cannot publish old observations into new bindings and instead
invalidates affected synchronization state. Values are not automatically applied
on startup, reload, reconnect, edits, or encoder rotation. Pending control
confirmation is invalidated when desired changes.

CLI parameter output labels desired and observed values explicitly. Stream Deck
instrument dials show `Want` and `Read`, with a warning for unsynchronized values;
a previous successful job cannot hide that warning. `parameter.changed` events
include synchronization data for instrument parameters. An observation is a
snapshot, not proof the front panel or another client has left settings unchanged.

## CLI bench sequence

After configuring endpoints and suitable circuit limits, validate and start:

```sh
./bin/deckctl config validate --config configs/bench.yaml
./bin/deckd --config configs/bench.yaml
```

In a second terminal (use the same socket override if you changed the example):

```sh
./bin/deckctl --socket ~/.deckd/bench.sock status
./bin/deckctl --socket ~/.deckd/bench.sock param set bench.frequency 2000
./bin/deckctl --socket ~/.deckd/bench.sock param set bench.amplitude 0.1
./bin/deckctl --socket ~/.deckd/bench.sock workflow run bench.prepare --confirm
./bin/deckctl --socket ~/.deckd/bench.sock param get bench.frequency
```

`bench.prepare` disables both outputs, applies frequency and amplitude, and stops
on failure. It does not enable output. After inspecting the actual connections
and explicitly configuring `security.allow_dangerous_actions: true`, enable with:

```sh
./bin/deckctl --socket ~/.deckd/bench.sock action run generator.output --arg value=true --confirm
./bin/deckctl --socket ~/.deckd/bench.sock action run generator.off --confirm
```

Stop the MHO954 acquisition at its front panel, then capture:

```sh
./bin/deckctl --socket ~/.deckd/bench.sock workflow run bench.capture --confirm --json
```

Capture selects the requested analog channel and changes only waveform transfer
settings: NORM, BYTE and up to 1000 points. It checks the preamble and definite
binary block, then returns `samples_v`, `points`, `channel`, `x_increment_s`,
`x_origin_s`, `x_reference`, and Y scaling metadata. Sample i's time is
`(i - x_reference) * x_increment_s + x_origin_s`; voltage is
`(byte - y_origin - y_reference) * y_increment_v`. JSON waveform size counts
against the shared job output budget. Human CLI output shows a compact summary.

Cancellation closes the socket, including an in-progress read. It cannot undo a
write that the instrument already accepted. Reconnect occurs only for the next
explicit operation, validates identity again, and never replays the failed write.
Shutdown `output_off` only attempts devices used by this process, tries both
channels within the remaining daemon shutdown deadline, and reports failures.
The default `preserve` policy performs no shutdown instrument I/O.

## Controlled hardware verification (pending)

No physical equipment was contacted. Simulator identity strings are fixtures,
not evidence of firmware support. Record each real model, adapter/transport,
firmware, date and outcome before claiming physical validation:

1. DG812: confirm USB-LAN raw socket support, endpoint, identity and optional
   firmware pin with outputs disconnected/off; check a mismatched pin refuses I/O
   beyond identification.
2. With a suitable load and measurement setup, verify OFF on both channels, the
   example's low frequency/amplitude settings, exact/rounded readback and units.
   Confirm coupling/tracking and unsafe waveform states reject changes/enable.
3. Explicitly authorize a low-level output, measure it on the MHO954, then disable
   and verify the actual output. Exercise both channels individually.
4. Stop scope acquisition and compare capture samples/scaling with the displayed
   waveform on channels 1–4. Check running acquisition refusal.
5. Disconnect during a read and a setting write. Confirm bounded failure, no replay,
   honest desired/observed state, and continued unrelated CLI operation.
6. Check preserve and opt-in output-off shutdown with both outputs; record that
   lost connectivity or abrupt process termination prevents any OFF guarantee.

The simulator suite covers these software paths without bench equipment. Physical
DG812/MHO954 and the earlier Stream Deck+ smoke gates remain open.

## Durable stopped-trace captures

`configs/bench.yaml` now declares `bench.trace`. Capture it from the workbench or
`deckctl experiment run bench.trace --confirm --socket ~/.deckd/bench.sock`.
The capture workflow inspects DG812 channel 1, transfers MHO954 channel 1, and
inspects the generator again. It continues to the final inspection after a failed
transfer, but cancellation or a recording failure can still prevent later steps.
All step outcomes remain explicit.

Use the existing sequence: disable both outputs, explicitly apply desired frequency
and amplitude, separately authorize output enable, acquire and stop using the scope,
then capture. The capture action never enables outputs, stops/starts acquisition,
or triggers a new trace. Generator settings are observations at two different times,
not an atomic snapshot. Changing a workbench input changes the desired value only.

The scope is checked for STOP before setup and immediately before and after waveform
transfer. Unknown/changed post-transfer state fails the operation. A complete bounded
trace is retained with `quality: suspect` for diagnosis; comparisons and baseline
selection reject it. STOP on both sides cannot prove that the front panel was not
changed between queries. Acquisition time remains `unknown`; an operator can record
an acquisition assertion in the saved run's note.

Run outcomes retain model, firmware, channel, observation times, generator readbacks,
pre/post acquisition state, and the waveform preamble. Serial numbers and endpoint
addresses are excluded from these observations. The series uses volts and seconds,
with x = (index − x_reference) × x_increment + x_origin and the existing voltage
scaling. Selecting source context during report preview includes these observations;
default reports omit them. The workbench shows desired/readback disagreement and
changed readbacks and offers complete trace tables.

Simulator evidence is in [CC-5](reviews/CC5.md). Physical verification of the DG812,
MHO954 firmware/adapter and Stream Deck+ remains pending. No equipment was operated
by the implementation tests, and shutdown does not guarantee outputs are off.
