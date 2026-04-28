# V6 Model Routing Runtime

Last updated: April 27, 2026.

This document stages a future shim-owned runtime for routing work across
multiple internal model roles behind one public OpenAI-compatible request.

This is a design and scope document. It does not change the current V2/V3
compatibility matrix, does not claim OpenAI hosted parity, and does not change
the current `/v1/responses`, `/v1/conversations`, or Codex CLI behavior until
code, tests, OpenAPI wording, and compatibility docs are updated together.

Official references checked for this plan:

- [Function calling](https://developers.openai.com/api/docs/guides/function-calling)
- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Compaction](https://developers.openai.com/api/docs/guides/compaction)
- [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses)
- [Apply Patch](https://developers.openai.com/api/docs/guides/tools-apply-patch)
- [Orchestration and handoffs](https://developers.openai.com/api/docs/guides/agents/orchestration)
- [Codex configuration reference](https://developers.openai.com/codex/config-reference)
- [Codex App Server API overview](https://developers.openai.com/codex/app-server#api-overview)

## Why V6 Exists

The current shim can already expose a broad OpenAI-compatible facade, local
Responses state, local tool subsets, compaction, and Codex compatibility
bridges. The next useful step is not another public endpoint. It is an
internal runtime that can make one client-visible assistant behave as a small
set of specialized model roles:

- an executive model decides the next public action
- a coder model drafts focused code edits from bounded file context
- a repair model fixes malformed structured output
- a summary model compacts state for long-running work

The external client should still see one OpenAI-shaped assistant. Internal
roles are implementation detail unless explicitly exposed through shim-owned
debug or trace surfaces.

## Compatibility Framing

V6 routing is a shim-owned extension. It must not be described as an official
OpenAI model-routing contract.

The public contract stays conservative:

- ordinary model names continue through the existing routing behavior
- configured routing aliases such as `Auto` opt into the V6 runtime
- public response objects, tool calls, tool outputs, `previous_response_id`,
  `conversation`, and `/input_items` remain OpenAI-shaped
- private worker attempts and routing decisions are stored in a private event
  log, not invented as new public Responses item types
- exact hosted SSE choreography is not claimed unless backed by official docs
  or upstream fixtures

One important security boundary: for Codex CLI-style client tools, the shim
must not silently take over client-side filesystem or shell execution. If the
client owns approvals, sandboxing, and local tool execution, V6 should preserve
that public tool-call loop. The shim may internally execute only shim-owned
local runtimes that are explicitly configured and already within the current
security model.

## Stage 0: Feasibility Probe

Goal: prove that routing one configured model alias through an executive,
coder, and summary role improves coding-task throughput or quality without
breaking the existing OpenAI-compatible surface.

This stage should be intentionally narrow.

### Scope

- one configured alias, for example `Auto`
- non-streaming `POST /v1/responses` first
- existing `previous_response_id` and `conversation` state paths
- existing compaction backend for `summary_model`
- one internal coder role for patch drafting
- one repair role for structured-output repair
- private trace rows for internal events
- public final response shaped like a normal single-assistant response

### Non-Goals

- no general-purpose model ensemble
- no hidden filesystem or shell execution for client-owned Codex tools
- no exact hosted SSE replay work
- no fourth model judging every decision
- no public `worker_attempt` Responses item
- no automatic use on ordinary model names

### Minimal Config Shape

Use explicit alias configuration so the feature cannot accidentally change
existing model behavior.

```yaml
responses:
  routing:
    enabled: true
    aliases:
      - model: Auto
        executive_model: Kimi-K2.6
        code_model: qwen-coder
        repair_model: small-json-repair
        summary_model: local-compact
        max_steps: 24
        max_worker_attempts: 3
        max_context_pack_chars: 120000
        stream: false
```

The exact model names are operator-owned. The important part is that the alias
is distinct from the underlying provider model names.

### Runtime Shape

```text
client request model=Auto
  |
  v
route alias to V6 runtime
  |
  v
executive_model decides next public action
  |
  +--> public tool call to client or configured shim-local tool
  |
  +--> private delegate_code_edit call
          |
          v
        code_model drafts structured patch proposal
          |
          v
        runtime validates proposal
          |
          v
        executive_model chooses next action or final answer
```

The executive owns public action selection. The coder answers only: "Given this
bounded code context and edit objective, what patch should be proposed?"

### Private Event Log

Do not store the V6 trace as one growing JSON array in memory. Use append-only
rows or JSONL-style records with bounded payload references.

Candidate fields:

- `run_id`
- `response_id`
- `turn_id`
- `sequence`
- `phase`
- `role`
- `model`
- `parent_event_id`
- `public_item_id`
- `tool_call_id`
- `input_ref`
- `output_ref`
- `status`
- `usage_json`
- `error_json`
- `created_at`

Large model inputs, outputs, file snapshots, and tool logs should be stored as
bounded blobs or content-addressed references with internal size limits. Listing
or replay paths must not require full materialization of every blob.

### Context Packs

Each role gets a purpose-built context pack, not the whole transcript.

Executive pack:

- user goal and current turn input
- public conversation state
- recent public tool results
- active constraints and approvals state
- latest worker summary, not raw worker chatter
- current phase and remaining budgets

Coder pack:

- concrete edit objective
- relevant file excerpts or snapshots
- applicable style and safety constraints
- prior patch failure, if any
- test or lint failure excerpt, if any
- strict output schema

Repair pack:

- malformed output
- expected schema
- parser or validator error
- minimal task reminder

Summary pack:

- completed public actions
- durable assumptions
- unresolved blockers
- active file and patch state
- next concrete goal

### Stage 0 Phase Machine

Start with a small deterministic phase machine:

| Phase | Owner | Purpose |
| --- | --- | --- |
| `executive_decide` | executive model | choose public tool call, private delegation, or final |
| `await_public_tool_output` | client or configured local tool runtime | wait for tool result |
| `code_draft` | coder model | produce patch proposal from bounded context |
| `patch_validate` | runtime | validate schema, paths, and patch format |
| `repair_structured_output` | repair model | fix malformed JSON or patch schema |
| `test_repair` | coder model | revise after test/lint failure |
| `finalize` | executive model | produce final public response |

The runtime should prefer explicit executive delegation over fragile heuristics
such as "a file was read, therefore call the coder." A private internal command
like `delegate_code_edit` makes phase transitions easier to test.

### Public Tool Boundary

V6 must preserve the official function-calling model: the model can request a
tool call, the application executes it, and tool output is passed back. In a
Codex CLI deployment, Codex is often the application that owns local tool
execution and approval prompts. The shim should not bypass that ownership.

Allowed in Stage 0:

- executive emits ordinary public function/tool calls
- client executes public tool calls and sends outputs back
- shim-local runtimes execute only tools already configured as shim-local
- coder produces private patch proposals

Not allowed in Stage 0:

- shim secretly reads or writes the user's filesystem for a client-owned tool
- private worker output appears as a new public Responses item type
- local approval or sandbox semantics are weakened to make routing easier

### `responses.mode` Behavior

The alias must have explicit mode behavior.

Recommended Stage 0 semantics:

| Mode | `model=Auto` behavior |
| --- | --- |
| `prefer_local` | route through V6 runtime; use configured underlying models |
| `local_only` | route through V6 runtime; reject unsupported fields instead of proxying |
| `prefer_upstream` | do not raw-proxy `Auto` unless an explicit alias-to-upstream fallback model is configured |

If fallback is supported, proxy with the configured `executive_model`, not the
alias string, unless the upstream is known to support that alias.

### Storage And Continuation

Stage 0 should keep the existing public state behavior intact:

- `previous_response_id` and `conversation` remain mutually exclusive
- public effective input and output are stored through the existing response
  service paths
- private event log is keyed to the public response id
- continuation uses both public state and private V6 trace summaries
- `/input_items` stays public and OpenAI-shaped

If private worker output materially affects the next public action, persist the
resulting public action or public final response, not the worker chatter.

### Implementation Steps

1. Add routing config types, defaults, normalization, and tests.
2. Add a `ModelAliasRouter` that detects configured aliases and resolves role
   models.
3. Add a `RoleInvoker` wrapper over the existing model client.
4. Add private event-log storage and tests for bounded append/list behavior.
5. Add context-pack builders for executive, coder, repair, and summary roles.
6. Add strict coder output schema and validator.
7. Add the Stage 0 phase machine for non-streaming create.
8. Route `model=Auto` through the runtime in `prefer_local` and `local_only`.
9. Preserve existing paths for all non-alias models.
10. Add integration tests with fake upstreams asserting which role model was
    called and which public response shape was returned.
11. Add Codex-style tool-loop tests where public tool calls remain client-owned.
12. Document the new shim-owned capability and keep compatibility wording
    conservative.

### Stage 0 Exit Criteria

- non-alias Responses behavior is unchanged
- alias requests produce valid OpenAI-shaped response objects
- public tool calls still round-trip through the client/tool-output flow
- private worker attempts are visible only in shim-owned trace/debug surfaces
- `previous_response_id` continuation works for alias responses
- `conversation` continuation works for alias responses
- the runtime stops on budget, loop, validation, and model errors with useful
  public error behavior
- focused tests pass
- `go test ./...`, `make lint`, and `git diff --check` pass before merge

## Stage 1: Production Routing Runtime

Goal: turn the probe into a reusable role runtime with real operator controls,
observability, and streaming support for the shim-owned subset.

Stage 1 starts only if Stage 0 shows practical gains on representative coding
tasks without degrading client compatibility.

### Generalized Role Registry

Move from one hard-coded alias to a registry:

```yaml
responses:
  routing:
    enabled: true
    aliases:
      - model: Auto
        strategy: coding_agent
        roles:
          executive:
            model: Kimi-K2.6
            provider: primary
          coder:
            model: qwen-coder
            provider: coding
          repair:
            model: small-json-repair
            provider: primary
          summary:
            model: local-compact
            provider: compaction
        limits:
          max_steps: 32
          max_model_calls: 16
          max_tool_calls: 12
          max_worker_attempts: 4
          max_trace_bytes: 8388608
```

The provider layer should be explicit if roles can call different upstreams.
The current single `llama.base_url` path is enough for Stage 0 if all role
models live behind the same gateway; Stage 1 should make provider routing a
first-class internal abstraction.

### Runtime Interfaces

Candidate interfaces:

```go
type Role string

const (
	RoleExecutive Role = "executive"
	RoleCoder     Role = "coder"
	RoleRepair    Role = "repair"
	RoleSummary   Role = "summary"
)

type RoleInvoker interface {
	Generate(ctx context.Context, req RoleRequest) (RoleResult, error)
	GenerateStream(ctx context.Context, req RoleRequest, onDelta func(RoleDelta) error) (RoleResult, error)
}

type ContextPackBuilder interface {
	Build(ctx context.Context, req ContextPackRequest) (ContextPack, error)
}

type RoutingEventStore interface {
	Append(ctx context.Context, event RoutingEvent) error
	List(ctx context.Context, runID string, page RoutingEventPage) ([]RoutingEvent, error)
}
```

Keep these internal. Public OpenAI request and response schemas should not grow
role-specific fields.

### Streaming Support

Stage 1 can add streaming for the shim-owned alias path, but it must stay
conservative:

- emit standard Responses lifecycle events
- stream public executive text only when it is truly public
- do not leak private coder tokens
- emit public tool-call events only for public tool calls
- store replay artifacts for retrieve-stream parity within the shim-owned
  subset
- document that exact hosted routing choreography is not claimed

If exact event order matters for a hosted/native tool family, capture upstream
fixtures before strengthening any compatibility claim.

### Observability

Add operator-visible tracing without logging sensitive data by default:

- route selected
- role model selected
- phase transitions
- token estimates and usage where available
- context-pack sizes
- retry and repair counts
- public tool-call counts
- private worker attempts
- budget termination reason
- final status

Debug endpoints or trace exports should require explicit enablement and should
redact large payloads, secrets, and file contents by default.

### Evaluation Harness

Stage 1 needs evals before becoming default for any serious workflow.

Candidate eval dimensions:

- task completion rate
- number of public tool calls
- patch apply success rate
- test pass rate after first patch
- total tokens
- latency p50/p95
- cost per solved task
- final explanation quality
- regression rate versus plain executive-only model

Useful test sets:

- small deterministic repo bug fixes
- multi-file refactors
- failed-test repair loops
- malformed JSON repair cases
- Codex CLI public tool-loop smokes
- `previous_response_id` and `conversation` continuation cases

### Stage 1 Exit Criteria

- multiple aliases can be configured independently
- role providers can differ without changing public request shape
- streaming works for the shim-owned subset
- private trace storage is bounded, pageable, and redacted by default
- context packs are deterministic enough to reproduce failures
- evals show a measurable gain on at least one target workflow
- `/debug/capabilities` reports the routing runtime as shim-owned
- docs, OpenAPI, compatibility matrix, and choreography atlas stay aligned

## Stage 2: Advanced Routing Runtime

This is the "go further" stage. It is deliberately not part of the current V2
compatibility facade. It should happen only after Stage 1 proves that the
runtime is useful, observable, and safe.

### 1. Strategy Plugins

Make routing strategies pluggable:

- `coding_agent`
- `research_agent`
- `tool_heavy_agent`
- `local_first_agent`
- `cost_optimized_agent`
- `latency_optimized_agent`

Each strategy owns:

- role graph
- phase machine
- context-pack policy
- model selection policy
- budget policy
- repair policy
- eval profile

### 2. Dynamic Role Selection

Replace static "always use coder after delegation" with measured routing:

- classify task type
- estimate context and output size
- choose role model by task, budget, and observed reliability
- skip coder for tiny edits
- escalate to stronger coder after repeated validation failures
- select repair model by schema family
- choose summary model by trace size and state sensitivity

This should be code-driven and eval-backed, not an extra model making every
decision by default.

### 3. Parallel Workers And Ensembles

Use parallelism only where it has a concrete value:

- two coder candidates for risky patch generation
- cheap reviewer pass over a proposed patch
- test-failure diagnosis in parallel with patch repair
- independent file-slice edits with disjoint write scopes

The runtime must define merge rules before adding parallel workers:

- disjoint file ownership
- conflict detection
- deterministic winner selection
- budget-aware cancellation
- no hidden overwrite of user or client changes

### 4. Validation Pipeline

Turn patch validation into a real pipeline:

- schema validation
- path policy validation
- patch grammar validation
- apply dry-run
- formatting check
- focused tests
- full tests where configured
- semantic guardrails for dangerous changes
- regression evidence attached to private trace

The runtime should distinguish:

- invalid worker output
- valid patch that does not apply
- patch applies but tests fail
- tests pass but public final response still needs executive synthesis

### 5. Long-Running State And Memory

Integrate with future memory work without confusing memory with public OpenAI
state:

- private run summaries
- per-project task memory
- durable coding preferences
- recurring failure signatures
- successful patch patterns
- tool reliability history

This belongs to shim-owned extension state. It should not be exposed as an
OpenAI-compatible memory endpoint unless a real official surface exists and is
implemented separately.

### 6. Trace Replay And Debug UI

Make routing failures inspectable:

- replay a run from public input plus private trace refs
- diff context packs between runs
- show phase timeline
- show model/provider decisions
- show budget use
- show validation failures
- redact payloads by default
- export sanitized bug bundles

The goal is reproducibility. Without this, dynamic routing will be too hard to
trust.

### 7. Cost And Latency Optimizer

Add an optimizer after enough traces exist:

- per-role token budgets
- prompt-cache-friendly context pack ordering
- model downgrade for easy phases
- model upgrade after repeated failures
- concurrent speculative worker with cancellation
- max-cost-per-run
- p95 latency guardrails

This should optimize from measured traces, not static guesses.

### 8. Policy-Aware Tool Routing

Make permissions and approvals first-class routing inputs:

- client-owned tools stay client-owned
- shim-owned local tools declare runtime and approval boundaries
- destructive tools require explicit public tool-call flow or configured local
  policy
- private workers cannot directly trigger side effects
- executive remains responsible for public side effects

This is the line that keeps model routing from becoming a hidden security
regression.

### 9. Multi-Transport Support

After HTTP non-stream and SSE are stable, extend the same runtime to:

- Responses WebSocket mode for the shim-owned subset
- retrieve-stream replay of routed responses
- background mode only if lifecycle semantics are explicitly implemented
- upstream proxy fallback only when mode semantics are documented

Do not mix this with Realtime API compatibility. Realtime remains a separate
surface.

### 10. Role Marketplace

Eventually, roles can become operator-installed profiles:

- role prompt packages
- model/provider presets
- eval packs
- strategy templates
- local policy bundles
- capability requirements

This should use repo/plugin conventions instead of hard-coding every role into
the core runtime.

### Stage 2 Exit Criteria

- routing strategy plugins are stable internal APIs
- dynamic routing beats static Stage 1 routing in evals
- parallel workers improve success rate or latency enough to justify cost
- validation pipeline prevents malformed or unsafe private outputs from
  becoming public actions
- trace replay can reproduce and debug failures
- policy boundaries are tested for client-owned and shim-owned tools
- docs clearly distinguish shim-owned routing from OpenAI compatibility

## Open Questions

- Should `response.model` for alias requests remain the requested alias, or
  expose the executive model? Stage 0 should prefer the requested alias and keep
  underlying role models in private trace/debug surfaces.
- Should alias routing be available in `prefer_upstream`, or should aliases be
  local-runtime-only? Stage 0 should avoid raw-proxying aliases unless an
  explicit fallback model is configured.
- How much private trace should be retained by default, and for how long?
- Should the coder produce native `apply_patch_call` items, or private patch
  proposals that the executive later turns into public tool calls? Stage 0
  should prefer private proposals and executive-owned public actions.
- Which eval set is the first gate: Codex CLI scratch tasks, repo-owned Go
  bugfix tasks, or generic Responses tool-loop tests?

## Promotion Rule

Promote work from this document into implementation only when all are true:

- the target stage has a measurable success criterion
- public OpenAI-compatible behavior remains unchanged or is explicitly updated
- `responses.mode` behavior is documented before merge
- tool ownership and approval boundaries are clear
- private trace storage is bounded
- tests cover happy path, adversarial path, and continuation path
- compatibility matrix and choreography atlas are updated if implementation
  changes current behavior or public claims
