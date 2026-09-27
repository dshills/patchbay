# Deckd --- Formal Engineering Specification

**Document:** SPEC.md\
**Version:** 0.1\
**Status:** Implementation Specification\
**Date:** 2026-09-27\
**Language:** Go\
**Initial platform:** macOS\
**Initial physical client:** Elgato Stream Deck+

## 1. Purpose

Deckd is a local automation runtime connecting physical controls,
developer tools, AI agents, OS functions, and laboratory equipment
through a common event/action model. Stream Deck+ is the first physical
UI, but the daemon MUST remain device-independent.

> Physical controls invoke semantic actions; they do not encode
> implementation details.

A key invokes `project.validate`; it does not contain `go test ./...`.

## 2. Goals

Deckd SHALL provide a persistent local Go daemon; semantic actions
independent of clients; active project/context state; synchronous and
asynchronous execution; deterministic workflows; structured job
feedback; typed parameters for rotary controls; daemon-enforced safety;
and operation without Stream Deck hardware. It SHOULD prefer the Go
standard library and remain observable, testable, and easy to debug.

Future releases SHOULD support Codex/AI actions, reusable prompts, SCPI
instruments, dynamic displays, executable plugins, and opt-in remote
clients.

## 3. Non-Goals

V1 SHALL NOT implement cloud automation, multi-user operation,
distributed workflows, Kubernetes, a graphical workflow editor, a
general-purpose scripting language, remote exposure by default, direct
AI hardware control, a plugin marketplace, or Go `plugin` extensions.

## 4. Design Principles

-   Semantic actions: clients call stable action names.
-   Device independence: core packages MUST NOT import Stream
    Deck-specific code.
-   Local first: Unix domain socket by default; TCP is explicit opt-in.
-   Declarative configuration: projects/actions/workflows/bindings use
    YAML.
-   Structured results: stdout alone is not an API.
-   Safe by default: destructive actions require policy and
    confirmation.
-   Deterministic core: AI providers remain outside deterministic
    execution.
-   Boring infrastructure: favor explicit Go and ordinary protocols.

## 5. Architecture

``` text
                         Clients
        ┌──────────────────┼───────────────────┐
        ▼                  ▼                   ▼
   Stream Deck+         deckctl          Future Clients
        └──────────────────┼───────────────────┘
                           │
                     Local API / IPC
                           │
                    ┌──────▼──────┐
                    │    deckd    │
                    └──────┬──────┘
       ┌───────────────────┼────────────────────┐
       ▼                   ▼                    ▼
   Event Bus          Action Runtime       Runtime State
                           │                    │
                    Exec / Git /          Context/Project
                     Workflow              Parameters

Future: Codex/agent provider, SCPI provider, executable plugins.
```

`deckd` owns semantics, state, execution, policy, and feedback. Clients
own input/presentation. Providers own external integration details.

## 6. Repository Layout

``` text
cmd/deckd/          cmd/deckctl/
internal/action/    internal/api/
internal/config/    internal/context/
internal/event/     internal/feedback/
internal/job/       internal/parameter/
internal/permission/ internal/project/
internal/provider/  internal/workflow/
pkg/protocol/
configs/example.yaml
SPEC.md             README.md
```

Prefer `internal` until an external API is demonstrably stable.

## 7. Core Domain

Primary entities: `Event`, `Action`, `ActionRequest`, `Result`, `Job`,
`Workflow`, `WorkflowStep`, `RuntimeContext`, `Project`, `Parameter`,
`Binding`, `Feedback`, `Permission`, `Provider`, and `Device`.

## 8. Events

``` go
type Event struct {
    ID string `json:"id"`
    Type string `json:"type"`
    Source string `json:"source"`
    Timestamp time.Time `json:"timestamp"`
    Payload json.RawMessage `json:"payload,omitempty"`
}
```

Initial event types include `control.pressed`, `control.released`,
`control.rotated`, `context.changed`, `project.changed`,
`parameter.changed`, action/job lifecycle events, and `config.reloaded`.

The in-process bus SHALL be thread-safe, support multiple subscribers,
use bounded queues, honor `context.Context`, and prevent slow
subscribers from blocking publishers. No external broker in V1.

## 9. Actions

``` go
type Action interface {
    Name() string
    Execute(context.Context, ActionRequest) (Result, error)
}

type ActionRequest struct {
    Action string `json:"action"`
    Args map[string]any `json:"args,omitempty"`
    Context RuntimeContext `json:"context"`
}

type Result struct {
    Status string `json:"status"`
    Message string `json:"message,omitempty"`
    Data map[string]any `json:"data,omitempty"`
    Display *DisplayResult `json:"display,omitempty"`
}
```

The registry SHALL reject duplicate names.

V1 providers: - `exec`: binaries via `os/exec.CommandContext`; MUST NOT
implicitly invoke a shell. - `open`: open a file, directory, or URL. -
`git`: semantic status, diff, log, pull, push, branch, stash,
stash-pop. - `workflow`: invoke a named workflow.

Any future shell action MUST be explicit and more strongly
safety-classified.

## 10. Projects

``` yaml
projects:
  kicadai:
    name: KiCadAI
    path: ~/src/KiCadAI
    github: https://github.com/dshills/KiCadAI
    language: go
```

Required: `id`, `name`, `path`. Optional: `github`, `language`,
`engine`, `metadata`, `environment`, action overrides. Zero or one
project is active and selection SHOULD persist. Template expansion MUST
be constrained and MUST NOT enable arbitrary execution.

## 11. Runtime Context

``` go
type RuntimeContext struct {
    Project string `json:"project,omitempty"`
    Mode string `json:"mode,omitempty"`
    Values map[string]string `json:"values,omitempty"`
}
```

Expected modes include `development`, `electronics`, `meeting`, and
`consulting`. The same physical control MAY resolve differently by mode.

## 12. Bindings

``` yaml
bindings:
  - control: key-1
    when: {mode: development}
    press: {action: project.test}
  - control: dial-1
    when: {mode: electronics}
    rotate: {parameter: generator.frequency}
    press: {action: generator.toggle-step}
```

Resolution precedence: exact device+control+context; control+context;
device+control; generic control; no binding. Equally specific ambiguous
bindings SHALL fail validation.

## 13. Parameters

V1 types: integer, float, boolean, enum, string.

``` yaml
parameters:
  generator.frequency:
    type: float
    value: 1000
    min: 1
    max: 1000000
    step: 10
    unit: Hz
```

Numeric rotation uses `new = current + delta * step`, clamped to bounds.
Later versions MAY add logarithmic scaling, acceleration, selectable
steps, and provider synchronization.

## 14. Workflows

``` yaml
workflows:
  validate:
    stop_on_error: true
    steps:
      - {action: format}
      - {action: vet}
      - {action: lint}
      - {action: test}
```

V1 supports ordered execution, per-step results, stop-on-error,
cancellation, and aggregate results. V1 SHALL NOT add loops, branches,
arbitrary expressions, or embedded scripting.

## 15. Jobs

States: `queued`, `running`, `success`, `failed`, `cancelled`.

The job manager SHALL provide unique IDs, bounded concurrency, status,
cancellation, configurable recent history, lifecycle events,
goroutine-leak prevention, and panic isolation. Persistent history is
optional in V1.

## 16. Feedback

``` go
type DisplayResult struct {
    Title string `json:"title,omitempty"`
    Value string `json:"value,omitempty"`
    State string `json:"state,omitempty"`
    Progress *int `json:"progress,omitempty"`
}
```

States: `idle`, `running`, `success`, `warning`, `error`, `disabled`.
Rendering belongs to clients.

## 17. Safety

Every executable action SHALL be classified `safe`, `confirm`, or
`dangerous`. Dangerous actions are disabled by default. Confirmation
MUST be daemon-enforced, not merely UI-enforced. Clients may express
confirmation through long press, second press, or `deckctl --confirm`.
Commands use argument arrays rather than shell interpolation. Sensitive
values MUST NOT be logged.

## 18. Configuration

Default: `~/.config/deckd/config.yaml`.

``` yaml
version: 1
server:
  socket: ~/.deckd/deckd.sock
context:
  defaults: {mode: development}
security:
  allow_dangerous_actions: false
projects:
  kicadai:
    name: KiCadAI
    path: ~/src/KiCadAI
    github: https://github.com/dshills/KiCadAI
    language: go
actions:
  test:
    type: exec
    command: go
    args: ["test", "./..."]
    cwd: "{{ .project.path }}"
workflows:
  validate:
    stop_on_error: true
    steps: [{action: test}]
```

Configuration SHALL be fully validated before becoming active. Failed
reloads leave the previous valid configuration running.

## 19. Local API

Use HTTP semantics over a Unix domain socket with JSON.

``` text
GET    /v1/status
GET    /v1/context
PATCH  /v1/context
GET    /v1/projects
GET    /v1/projects/{id}
PUT    /v1/context/project
GET    /v1/actions
POST   /v1/actions/{name}
GET    /v1/workflows
POST   /v1/workflows/{name}
GET    /v1/jobs
GET    /v1/jobs/{id}
DELETE /v1/jobs/{id}
GET    /v1/parameters
GET    /v1/parameters/{name}
PUT    /v1/parameters/{name}
POST   /v1/events
POST   /v1/config/reload
```

No TCP listener by default. API errors use a stable
`{error:{code,message}}` structure.

## 20. deckctl

Required commands:

``` text
deckctl status
deckctl project list|current|use <id>
deckctl context show|set <key> <value>
deckctl action list|run <name>
deckctl workflow list|run <name>
deckctl job list|show <id>|cancel <id>
deckctl param list|get <name>|set <name> <value>
deckctl config validate|reload
```

Support human-readable and JSON output. `deckctl` is the primary V1
test/debug client.

## 21. Stream Deck+ Adapter

The adapter SHALL ultimately support key press/release, encoder
rotation/press, long press where practical, touch interactions where
available, dynamic labels, job feedback, parameter values, and context
changes. It translates device input to Deckd semantics and feedback to
presentation. It MUST NOT own business logic. V1 daemon development MUST
NOT block on hardware integration.

## 22. Providers and Plugins

External integrations use provider interfaces with lifecycle and health
semantics. Prefer small capability interfaces. Do NOT use Go's `plugin`
package. A later executable-plugin protocol SHOULD use subprocesses and
versioned JSON over stdin/stdout. Protocol stabilization is deferred
until core action/result semantics are proven.

## 23. AI / Codex --- Phase 2

AI is an action provider, not the runtime.

``` yaml
prompts:
  review: |
    Review the current implementation for correctness, architecture,
    edge cases, security, and unnecessary complexity.
actions:
  review:
    type: agent
    provider: codex
    prompt: review
    cwd: "{{ .project.path }}"
```

Agent jobs MUST expose status/output and obey cancellation/safety
policy. AI MUST NOT automatically gain unrestricted hardware-control
authority.

## 24. SCPI --- Phase 2

SCPI is another provider.

``` yaml
devices:
  generator:
    provider: scpi
    transport: tcp
    address: 192.168.1.120:5555
```

Expose semantic operations such as `generator.set_frequency`,
`generator.set_amplitude`, `generator.output`, and `scope.capture`; do
not leak raw SCPI to ordinary clients. Raw SCPI MAY be an advanced
stronger-permission action.

## 25. Persistence

V1 SHOULD persist active project, mode/context, and parameters marked
persistent. Use an atomic local state file unless requirements justify a
database. State writes SHALL use temp-file + rename semantics.

## 26. Logging and Observability

Use `log/slog`. Include component, event/action/job ID, duration,
outcome, and error. Never log secrets. `deckctl status` SHALL expose
daemon version, uptime, config path, active project, mode, running jobs,
and provider health.

## 27. Concurrency

All public operations accept `context.Context` where appropriate. Job
concurrency is bounded. Shared state is synchronized. Event subscribers
cannot indefinitely block publishers. Shutdown cancels jobs and waits
for a bounded grace period. CI SHOULD run `go test -race ./...`. Every
goroutine needs a documented owner and shutdown path.

## 28. Error Model

Stable categories: `invalid_config`, `invalid_request`, `not_found`,
`action_not_found`, `project_not_found`, `permission_denied`,
`confirmation_required`, `provider_unavailable`, `execution_failed`,
`cancelled`, `timeout`, `internal`.

## 29. Testing

Required: unit tests for configuration, bindings, parameters,
permissions, workflows, event bus, and state; integration tests for
API/jobs/cancellation/exec/Git/reload; CLI tests against an ephemeral
daemon; race tests; and failure-path tests. Tests MUST NOT require
Stream Deck hardware. Target core coverage is 80%+ where meaningful;
correctness outranks coverage gaming.

## 30. Security

Threats include malicious configuration, accidental destructive
commands, argument injection, unsafe network exposure, secret leakage,
and compromised plugins. Requirements: Unix socket default, restrictive
socket permissions, no implicit shell, dangerous-action opt-in,
daemon-side confirmation, constrained templates, no secrets in logs,
validated plugin messages, and authentication before supporting
non-loopback TCP.

## 31. Performance

Targets: negligible idle CPU; reasonable Go-daemon memory use; local
dispatch under 25 ms excluding action execution; perceptually immediate
rotary feedback; no unbounded queues/history; startup under one second
on normal development hardware.

## 32. Graceful Shutdown

On SIGINT/SIGTERM: stop accepting work, cancel jobs, stop providers,
flush persistent state, close listeners, and exit after a bounded grace
period.

## 33. Phased Delivery

### Phase 0 --- Foundation

Repository, config loader/validation, logging, version metadata, core
types.

### Phase 1 --- Headless Runtime

Event bus, context, projects, parameters, action registry, exec/open,
workflows, jobs, permissions, persistence, Unix-socket API.

**Exit:** all core behavior usable without hardware.

### Phase 2 --- deckctl

Reference CLI, JSON output, cancellation, config validation/reload.

**Exit:** useful developer automation entirely from CLI.

### Phase 3 --- Stream Deck+

Adapter, keys, encoders, dynamic feedback, context switching.

**Exit:** Stream Deck operates semantic actions without embedded shell
logic.

### Phase 4 --- Developer Integrations

Git refinement, project conventions, prompt library, Codex/agent
provider.

### Phase 5 --- Electronics

SCPI transport/provider, generator/scope semantic actions, parameter
synchronization, reusable bench workflows.

### Phase 6 --- Plugin Protocol

Executable plugin host, protocol versioning, capability discovery,
health/lifecycle, isolation.

## 34. V1 Acceptance Criteria

V1 is complete when: 1. `deckd` starts from valid YAML and clearly
rejects invalid configuration. 2. It listens on a protected Unix socket.
3. `deckctl` can inspect status, context, projects, actions, jobs, and
parameters. 4. Active project and mode can be changed and persisted. 5.
`exec`, `open`, Git, and workflow actions function. 6. Long-running
actions execute as cancellable jobs. 7. Workflow failure/cancellation
behavior is deterministic. 8. Parameters validate types/bounds and
publish changes. 9. Permission and confirmation rules are
daemon-enforced. 10. Invalid config reload cannot destroy the active
valid configuration. 11. Core tests pass under the race detector. 12. No
Stream Deck hardware is required to exercise core functionality.

## 35. Implementation Rules for Codex

When implementing this specification: - Review this specification before
each phase. - Keep packages cohesive and interfaces small. - Do not add
dependencies without clear justification. - Do not prematurely implement
later phases. - Prefer stdlib implementations where reasonable. - Add
tests with each feature, including failure paths. - Run formatting,
vetting, linting, tests, and race tests at natural checkpoints. - Review
completed phase work with Prism when available and address material
findings. - Commit at natural stopping points and phase completion with
focused commit messages. - Update documentation when implementation
decisions materially refine this specification. - Do not silently change
architectural boundaries; document proposed deviations first.

## 36. Definition of Done per Phase

A phase is done only when its scope is implemented, tests cover
normal/failure/cancellation behavior as applicable, validation is clean,
race tests pass for affected packages, Prism review has no unresolved
material findings when available, documentation is current, and work is
committed.

## 37. Future Direction

Deckd may eventually become a local physical automation platform where
the same semantic action can be invoked from Stream Deck, CLI, Raycast,
MCP, voice, or an AI agent, while the same runtime safely coordinates
development tools and laboratory equipment.

That future capability MUST emerge from stable core abstractions rather
than V1 speculative complexity.
