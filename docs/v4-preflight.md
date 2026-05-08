# V4 Preflight: Backend Capability And Tool Routing Architecture

Last updated: May 8, 2026.

This document captures the practical architecture work that should happen
before V4 grows new extension and plugin behavior.

V2 is the broad compatibility facade.
V3 is practical backend, runtime, transport, and automation expansion.
V4 is where the shim can add opinionated extensions and plugin architecture
without pretending they are first-party OpenAI API contracts.
V5 is for exact hosted-parity work that needs upstream fixtures.

The preflight goal is narrow:

- make backend capability explicit
- keep public OpenAI-shaped semantics separate from backend projections
- make tool routing inspectable and testable
- make Codex CLI support a named compatibility profile, not a vague
  "any model works" claim
- create implementation seams for V4 plugins without changing the public
  OpenAI-compatible contract

## Current Implementation Slice

The current implementation slices are complete:

- `internal/backendcap` defines a normalized backend capability registry,
  capability-class vocabulary, deterministic component sorting, and validation
  for missing ids, duplicate ids, missing namespaces, unknown classes, and
  disabled-but-ready contradictions.
- `GET /debug/capabilities` now includes `backends.schema_version:
  v4.backend_capabilities.v1` and a `backends.components[]` registry generated
  from current config plus runtime probes.
- `GET /debug/capabilities` also includes `plugins.schema_version:
  v4.plugin_contracts.v1` and a `plugins.plugins[]` registry for the current
  backend/runtime/plugin contract slice. Every advertised backend component is
  cross-linked from `backends.components[].plugin_id`.
- The registry covers the current practical backend/plugin boundaries:
  primary storage, retrieval index, retrieval embedder, compaction,
  constrained decoding, Responses WebSocket transport, upstream model
  providers, web search, image generation, computer, code interpreter, native
  local `shell`, native local `apply_patch`, and the Codex client profile.
- Provider-routed model backends expose only public model aliases and secret
  env names. Actual bearer tokens are not emitted. Provider routes also carry
  plugin id/version metadata into debug traces.
- Existing `tools.*` capability entries now include a `disposition` summary
  using the V4 classifier vocabulary such as `local_execute`, `proxy_only`,
  and `client_round_trip`.
- `POST /v1/responses` now runs a request-time tool classifier over `tools[]`
  before selecting the local/proxy route. In `responses.mode=local_only`, the
  classifier fails closed for explicitly non-local families such as
  `mcp.connector_id`, client-executed `tool_search`, unknown future tool types,
  and upstream-only aliases. Local tool-family parser/runtime errors remain
  owned by the existing local handlers so their diagnostics stay specific.
- Create-stream, retrieve-stream, and Responses WebSocket warmup now share a
  `responseReplayEmitter` path with explicit replay classes and capability
  labels. This keeps event writing incremental, records whether a path is
  `generic_replay` or typed local-tool replay, and preserves the existing
  public SSE/WebSocket payloads.
- Backend failures now pass through a shared V4 policy classifier. It
  distinguishes auth, permission, quota, retryable rate limit, model
  unavailable, unsupported tool/parameter, timeout, malformed response,
  capability mismatch, local runtime unavailable, and generic transport/server
  errors. The classifier drives shim error mapping, fallback eligibility for
  `prefer_upstream`, log fields, and `/debug/capabilities.runtime.ops`.
- `make v4-preflight-smoke` is the operator-facing aggregate smoke for this
  V4 layer. It captures health/readiness, `/v1/models`,
  `/debug/capabilities`, one Responses request plus its `/debug/traces`
  metadata, optional provider-routing smoke, optional `shimctl codex doctor`,
  and a JSON/Markdown artifact summary under `.tmp/v4-preflight-smoke`.
- `make v4-provider-matrix-smoke` runs provider-routing smoke plus V4
  preflight across the current operator model/provider matrix and writes one
  aggregate report under `.tmp/v4-provider-matrix-smoke`.
- `make v4-provider-matrix-curate` reads those aggregate reports locally and
  classifies the latest rows into release-gate, rerun-candidate, routing,
  preflight, Codex doctor, readiness, and transport buckets.

These slices intentionally do not create a new OpenAI public surface. They
centralize what is already true so future V4 memory/plugin work has one
operator-visible contract instead of one-off backend claims.

## Official References Reviewed

This wording was checked on May 6, 2026. The request-time classifier,
stream/replay, and plugin-contract/debug-trace slices were spot-checked again
on May 8, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI Docs MCP on `developers.openai.com` / `platform.openai.com`
- current official API endpoint list, including `/v1/responses`,
  `/v1/responses/{response_id}`, `/v1/responses/{response_id}/input_items`,
  `/v1/responses/compact`, `/v1/conversations`,
  `/v1/chat/completions`, `/v1/files`, and `/v1/vector_stores`
- absence of `/debug/capabilities`, `/debug/traces`, and plugin-contract
  endpoints from the official OpenAI endpoint list; these remain shim-owned
  operator surfaces
- [Responses streaming events API reference](https://platform.openai.com/docs/api-reference/responses-streaming)
- [Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Streaming API responses](https://developers.openai.com/api/docs/guides/streaming-responses)
- [Using tools](https://developers.openai.com/api/docs/guides/tools)
- [Function calling](https://developers.openai.com/api/docs/guides/function-calling)
- [MCP and Connectors](https://developers.openai.com/api/docs/guides/tools-connectors-mcp)
- [Tool search](https://developers.openai.com/api/docs/guides/tools-tool-search)
- [Codex config reference](https://developers.openai.com/codex/config-reference#configtoml)
- current `openai/codex` source as an implementation reference for Codex CLI
  request shape, provider config, model-provider metadata, tool modes, and
  transport behavior

The OpenAI docs and API reference remain the source of truth for OpenAI
wire-contract claims. Codex source is useful for Codex CLI behavior, but it is
not a substitute for official API docs.

## External Research Input

An external OmniRoute source review on May 6, 2026 found useful engineering
patterns, but not a trustworthy OpenAI parity implementation to copy.

Useful patterns observed:

- a central provider registry with provider metadata, formats, auth, executor
  choice, model aliases, fallback policy, and operational knobs
- explicit source-format and target-format detection before request dispatch
- provider-specific executor hooks for request cleanup and backend-specific
  behavior
- a dedicated Codex path instead of treating Codex as ordinary chat traffic
- a stream transformer component with explicit state
- fallback, cooldown, quota, and health concepts around providers
- config generation for Codex-style custom providers
- a broad inventory of practical smoke scenarios

Boundaries observed:

- its `/v1/responses` support is a partial `POST` adapter, not the full
  official Responses and Conversations surface
- its generic Responses-to-Chat projection is lossy for state, items, tools,
  `include`, `store`, and `reasoning`
- hosted/native tools are not generically convertible to Chat function tools
- its Codex pass-through is narrow and provider-specific
- its MCP server is a control-plane gateway, not OpenAI Responses `mcp` tool
  parity
- its stream output is synthetic and should not be treated as exact hosted SSE
  choreography
- its useful pieces are architecture hints, not evidence for compatibility
  matrix upgrades

That research should influence V4 architecture. It must not weaken the
repo-wide rule that OpenAI compatibility claims require official-docs checks,
Codex source checks where applicable, and upstream fixtures where observable
wire behavior is ambiguous.

## Why This Is V4 Preflight

This work is not a new OpenAI API surface. It is the substrate that lets future
extension and plugin work avoid three failure modes:

1. provider-specific quirks leaking into public OpenAI-compatible handlers
2. chat-only backend projections silently erasing Responses state and tool
   semantics
3. compatibility docs claiming "supported" when the implementation only has a
   best-effort bridge

Most of the implementation belongs under existing internal ownership:

- `internal/config` for normalized backend and plugin config
- `internal/service` for orchestration boundaries
- `internal/domain` for canonical OpenAI-shaped objects
- `internal/upstreamcompat` for backend-specific compatibility projections
- `internal/httpapi` for capability manifest exposure and request validation
- `internal/ssetrace` for streaming and replay evidence
- `internal/codexeval` for Codex compatibility harness integration

Use different package names if implementation pressure points elsewhere, but
keep the ownership boundary clear: public contract validation is not the same
as backend request cleanup.

## Deliverables

### 1. Backend Capability Registry

Classification: V4 preflight platform work.

Goal:
Create a normalized registry describing what each configured backend can
actually do, how the shim may route to it, and what evidence supports that
claim.

The registry should distinguish at least these capability classes:

- `native`: the backend accepts the OpenAI-shaped request natively enough for
  the claimed subset
- `local_subset`: the shim owns the behavior locally and only uses the backend
  for model generation or a narrow runtime call
- `proxy_only`: the shim can pass the request upstream but does not implement
  the behavior locally
- `chat_projection`: the shim can project a Responses request into Chat
  Completions for model text/tool generation while preserving public state
  locally
- `repair_or_validate`: the shim can locally validate, repair, or post-check a
  backend result, but does not have backend-native parity
- `unsupported`: the backend must not receive this feature unless a later
  classifier deliberately rejects or degrades it

The first registry schema should include:

- provider id and display name
- backend kind, for example OpenAI-compatible, local llama, hosted proxy,
  deterministic fixture, or Codex profile
- base URL and auth strategy, with secrets referenced by env/config names only
- request wire modes supported by the backend:
  - `responses_native`
  - `responses_over_chat`
  - `chat_completions`
  - `raw_proxy`
  - `websocket_responses`
- endpoint ownership:
  - `responses.create`
  - `responses.retrieve`
  - `responses.delete`
  - `responses.cancel`
  - `responses.input_items`
  - `responses.input_tokens`
  - `responses.compact`
  - `conversations.create`
  - `conversations.retrieve`
  - `conversations.items`
  - stored Chat Completions routes
- state ownership:
  - shim-owned store
  - upstream-owned store
  - mixed shadow store
  - stateless backend with shim reconstruction
- continuation support:
  - `previous_response_id`
  - `conversation`
  - manually supplied input history
  - WebSocket repeated `response.create`
- streaming support:
  - create-stream
  - retrieve-stream
  - typed tool event families
  - generic replay only
  - WebSocket support
  - upstream WebSocket proxy support
- tool support by family:
  - function tools
  - custom tools
  - constrained custom tools
  - local `file_search`
  - hosted/proxy `file_search`
  - local `web_search`
  - hosted/proxy `web_search`
  - local `image_generation`
  - hosted/proxy `image_generation`
  - local `computer`
  - hosted/proxy `computer`
  - local `code_interpreter`
  - hosted/proxy `code_interpreter`
  - remote MCP with `server_url`
  - connector MCP with `connector_id`
  - hosted/server `tool_search`
  - client-executed `tool_search_output`
  - native local `shell`
  - native local `apply_patch`
- unsupported-field policy:
  - reject
  - accept as compatibility no-op
  - preserve locally but do not send upstream
  - proxy unchanged
- routing-mode behavior:
  - `prefer_local`
  - `prefer_upstream`
  - `local_only`
- operational state:
  - readiness
  - last health error
  - cooldown state
  - quota exhaustion state
  - retryability
  - fallback eligibility
- evidence metadata:
  - official docs checked date
  - test names
  - fixture names
  - smoke run names
  - manual verification notes
  - compatibility matrix label supported by this evidence

Acceptance criteria:

- `/debug/capabilities` can be generated from the registry without duplicating
  feature knowledge in HTTP handlers
- startup validation catches contradictory backend config
- docs can explain why a feature is `native`, `local_subset`, `proxy_only`, or
  `unsupported`
- tests prove that missing capability does not silently turn into a lossy
  backend request

### 2. Canonical Request And Response Pipeline

Classification: V4 preflight platform work.

Goal:
Make the internal request path preserve Responses semantics even when the
selected backend is chat-only.

The shape should be:

```text
client request
  -> public OpenAI-surface validation
  -> canonical shim request
  -> routing and tool classification
  -> backend projection
  -> backend response integration
  -> canonical shim response/events
  -> public OpenAI-surface response
```

The canonical layer should preserve:

- typed Responses input and output items
- response id, item ids, output indexes, content-part indexes, and statuses
- `instructions`, `input`, `previous_response_id`, and `conversation`
- `store` and local persistence decisions
- `include` handling as implemented, no-op, proxy-only, or unsupported
- `reasoning` metadata boundaries
- annotations, citations, artifacts, and tool-output placement where supported
- tool calls and tool outputs as typed items, not only chat messages
- usage accounting and incomplete/error statuses
- create-stream and retrieve-stream replay evidence

Backend projections may be lossy only inside a declared backend bridge. The
public object stored and returned by the shim must remain aligned with the
claimed OpenAI-compatible subset.

Specific rule:
Do not build V4 on a generic "Responses equals Chat plus wrapper" adapter.
Chat projections are allowed for model generation, but state, item history,
retrieval, replay, and public response shape stay owned by the shim.

Acceptance criteria:

- a table-driven test proves that each public Responses field is preserved,
  rejected, no-op'd, or projected deliberately
- chat-only backend tests show that the public response still has Responses
  item semantics
- retrieve and input-items tests use the canonical stored state, not backend
  chat history

### 3. Tool Routing Classifier

Classification: V4 preflight platform work.

Goal:
Replace vague "tool conversion" with an explicit classifier that decides what
happens to every requested tool family under the current backend, route, and
`responses.mode`.

The classifier should return a disposition:

- `local_execute`: shim owns and executes the tool family locally
- `upstream_passthrough`: backend receives the original OpenAI-shaped tool
- `proxy_only`: request is allowed only in proxy-first modes
- `chat_projection`: tool can be projected to Chat Completions function shape
  for model planning while shim owns public item/state semantics
- `function_repair`: tool can be represented as a function with a repair layer,
  but must not be called native parity
- `client_round_trip`: client is expected to execute and return tool output
- `accept_noop`: accepted for compatibility but not semantically implemented
- `reject`: fail early with a documented error

The classifier must keep these pairs separate:

- OpenAI Responses remote MCP with `server_url` vs connector-style MCP with
  `connector_id`
- hosted/server `tool_search` vs client-executed `tool_search_output`
- function tools vs custom tools with raw string input
- constrained custom tools with shim validation/repair vs true backend-native
  grammar or regex decoding
- hosted/proxy `code_interpreter` vs shim-local runtime execution
- native local `shell` vs Codex CLI function-tool bridge paths
- native local `apply_patch` vs Codex CLI file-change behavior

The classifier output should be visible in debug traces and, at a summarized
level, in `/debug/capabilities`.

Specific rules:

- never silently drop a requested tool family
- never coerce hosted tools into plain functions without recording the
  compatibility class
- never delete `include`, `store`, `reasoning`, or tool-specific fields as a
  generic cleanup step
- never treat Codex task success as evidence that the CLI emitted native
  Responses tool declarations
- local-only mode must reject unsupported tool families rather than falling
  back upstream

Acceptance criteria:

- each tool family in the compatibility matrix has classifier coverage
- each routing mode has tests for supported, unsupported, and proxy-only tools
- classifier decisions are included in failed-request diagnostics without
  leaking secrets or raw sensitive payloads

### 4. Codex CLI Compatibility Profile

Classification: V3/V4 bridge work, with the architecture hooks parked here.

Goal:
Treat Codex CLI as a concrete client profile with its own request shapes, tool
modes, and config expectations.

The first profile should capture:

- current Codex config format: `~/.codex/config.toml` or trusted
  `.codex/config.toml`
- custom provider fields:
  - `model_provider`
  - `model_providers.<id>.base_url`
  - `model_providers.<id>.env_key`
  - `model_providers.<id>.wire_api = "responses"`
  - `model_providers.<id>.supports_websockets`
  - retry and stream timeout knobs where useful
- model metadata overrides needed by Codex:
  - context window
  - reasoning effort and summaries
  - verbosity
  - tool-mode feature toggles
- Codex request-shape fixtures:
  - simple text turn
  - streaming text turn
  - `previous_response_id` continuation
  - `conversation` or prompt-cache key behavior where observable
  - `/responses/compact`
  - unified exec tool mode
  - fallback shell function mode
  - apply-patch or file-change task shape
  - MCP configured in Codex
  - web search and tool search toggles where visible
- generated isolated `CODEX_HOME` for smoke tests
- redacted Codex JSON/event logs for classifier and replay assertions

Specific rules:

- "Codex works with this model" must mean a named task profile passed, not
  only that `codex exec` printed a final answer once
- "Codex native Responses compatibility" must not be inferred from a
  Responses-over-Chat backend bridge
- Chat-only upstreams may be useful, but they are a shim-owned projection path,
  not Codex provider wire API parity
- Codex local tools remain client-owned; the shim should not silently take over
  filesystem or shell authority

Acceptance criteria:

- config generation is current with official Codex docs
- smoke tests fail clearly for config, auth, transport, tool-mode, model-output,
  and workspace-checker problems
- docs and matrix labels distinguish boot, read-only tools, write tools,
  interactive tools, and real coding task success

### 5. Provider-Specific Request Cleanup Hooks

Classification: V4 preflight platform work.
Status: implemented for model-provider alias/auth cleanup and configured Chat
Completions backend projection cleanup.

Goal:
Allow backend-specific request adaptation without mutating the public
OpenAI-compatible contract.

Suggested hook points:

- `ValidatePublicRequest`
- `NormalizePublicRequest`
- `ClassifyTools`
- `BeforeBackendRequest`
- `AfterBackendHeaders`
- `MapBackendStreamEvent`
- `AfterBackendResponse`
- `BeforeClientResponse`
- `RecordPersistence`
- `RecordReplay`

Rules:

- public validation runs before backend cleanup
- cleanup is scoped to a backend/profile and must be named in debug traces
- cleanup may remove fields only from the backend projection, not from the
  canonical stored public request
- cleanup may not invent undocumented public limits
- cleanup may not make a valid OpenAI-shaped request fail only because an
  optional local side effect would be too large
- cleanup must be covered by tests that compare public input, backend
  projection, and stored replay state

Implemented hook contract:

- plugin descriptors expose supported backend request hooks through
  `/debug/capabilities.plugins.plugins[].request_cleanup_hooks`
- debug traces record applied hooks as
  `transforms[]` entries with `stage=request_cleanup`,
  `class=backend_projection`, `hook=<name>`, and metadata-only field labels
- current provider hooks cover public model alias rewrite and provider auth
  header override
- current Chat Completions projection hooks cover configured developer-role
  remap, default thinking disable, default `max_tokens`, JSON Schema to
  JSON-object instruction downgrade, tool-parameter type repair, Moonshot tool
  schema sanitization, and empty assistant tool-call content omission

Useful cleanup examples:

- remove backend-unsupported fields from a chat-only projection after the
  public contract has been preserved locally
- rewrite model aliases before the backend call
- add provider-required headers or query parameters
- lower unsupported reasoning-effort settings only for a named backend bridge
- map provider-specific quota or unsupported-tool errors into typed shim
  diagnostics

Non-useful cleanup examples:

- deleting `store` from the canonical request
- deleting `include` without recording no-op/proxy/unsupported behavior
- turning native hosted tools into functions without classifier evidence
- stripping `previous_response_id` instead of reconstructing context in the
  shim-owned state path

### 6. Stream And Replay Transformer Interface

Classification: V4 preflight platform work.
Status: implemented for the V4 preflight platform scope.

Goal:
Centralize create-stream and retrieve-stream event writing without forcing
exact hosted choreography where the shim only claims a broad subset.

Implemented interface support:

- streaming backend event normalization through the existing upstream stream
  proxy
- typed Responses event emission
- generic replay emission for stored responses
- tool-family-specific replay where docs or fixtures support it
- event sequence tracking where the current shim uses it
- persisted replay artifacts that can be compared in tests on artifact-backed
  paths
- incremental writing without prebuilding the full event list
- clear split between create-stream and retrieve-stream behavior
- WebSocket reuse where the event payload is the same and the transport differs

The interface exposes these capability levels:

- `typed_text`
- `typed_function_call`
- `typed_tool_family`
- `generic_replay`
- `fixture_backed_hosted_replay`
- `unsupported`

Rules:

- do not append Chat Completions-specific sentinels to Responses streams unless
  the public contract explicitly requires them
- do not prebuild full replay event slices on hot paths
- do not claim hosted tool choreography without docs or upstream fixtures
- store enough replay metadata to explain why retrieve-stream differs from
  create-stream when it does

Implemented checks:

- `emitResponseReplayEvents` owns sequence numbering, `starting_after`
  filtering, replay summary metadata, and transport-agnostic event emission.
- `writeCompletedResponseAsSSE`, `writeResponseReplayAsSSE`, and
  `writeCompletedResponseAsWebSocket` use the same emitter path.
- focused tests cover replay classes, capability labels, `starting_after`
  numbering, WebSocket/SSE-compatible sequence payloads, and short-circuiting
  on emitter errors so the replay walker does not need to prebuild a full
  event list before writing.

Boundary:

- exact hosted tool-family SSE choreography is still V5 fixture-backed parity
  work, not a V4 preflight claim.

### 7. Fallback, Cooldown, Quota, And Health Policy

Classification: V4 preflight platform work.
Status: implemented for the V4 preflight platform scope.

Goal:
Make multi-backend routing reliable without hiding compatibility failures.

The policy classifies backend failures:

- auth failure
- permission failure
- quota exhausted
- rate limit retryable
- model unavailable
- unsupported tool or parameter
- transport timeout
- stream idle timeout
- malformed backend response
- backend capability mismatch
- local runtime unavailable
- transport/server error

For each class it defines:

- whether it is retryable
- whether it triggers cooldown
- whether fallback is allowed
- the client-visible OpenAI-shaped status/type/code/message used by the shim
- how it appears in `/debug/capabilities` and logs

Rules:

- `local_only` never falls back upstream
- `prefer_upstream` remains proxy-first and must not silently become a local
  hosted-tool emulation path after upstream rejects a request
- `prefer_local` may fall back only when the compatibility matrix and tool
  classifier allow it
- fallback decisions must be visible in private debug traces
- quota and cooldown state must not be exposed as fake OpenAI response fields

Implemented checks:

- deterministic tests cover HTTP-status classes, timeout and malformed-response
  classes, and the operator policy table
- `MapError` uses the policy for upstream auth, permission, quota, retryable
  rate-limit, timeout, malformed response, capability mismatch, local runtime,
  and transport failures while preserving the existing generic `502` behavior
  for unknown upstream errors
- `prefer_upstream` local-state fallback now consults the policy instead of
  treating every upstream failure as fallback-safe
- upstream stream/proxy logs include `failure_class`, `retryable`,
  `cooldown`, `cooldown_hint_seconds`, and `fallback_allowed`
- `/debug/capabilities.runtime.ops.backend_failure_policy` exposes the static
  operator decision table without secrets or live quota state

Boundary:

- V4 exposes cooldown decisions and hints, not a fake OpenAI quota object.
- Active provider quarantine across requests is still intentionally absent
  because provider-routed model ids are explicit `provider/model` choices; the
  shim should not silently satisfy that request through a different provider.

### 8. Codex Config Generator And Probe

Classification: V4 preflight developer tooling.
Status: implemented for config generation and direct preflight doctor.

Goal:
Make Codex-through-shim setup reproducible and current with Codex docs.

Implemented command shape:

```text
shimctl codex config -provider gateway-shim -base-url http://127.0.0.1:8080/v1 -model <provider/model>
shimctl codex doctor -provider gateway-shim -base-url http://127.0.0.1:8080/v1 -model <provider/model>
make codex-config
make codex-config-doctor
```

The generated config should include:

- `model`
- `model_provider`
- `[model_providers.<id>]`
- `name`
- `base_url`
- `env_key`
- `wire_api = "responses"`
- `supports_websockets`
- optional retry and timeout settings
- optional `model_context_window`
- optional reasoning and verbosity settings

Implemented generator behavior:

- prints a standalone current Codex `config.toml` to stdout by default
- writes `config.toml` to `-out` or `-codex-home <dir>/config.toml` when
  requested
- defaults to `CODEX_MODEL`/`MODEL`, `CODEX_PROVIDER`, `CODEX_BASE_URL` or
  `SHIM_BASE_URL + /v1`, and `CODEX_API_KEY_ENV`
- never writes bearer token values into generated TOML

The doctor verifies:

- Codex binary is present and version captured
- generated config can be written to an isolated artifact `CODEX_HOME`
- provider auth env var is present
- `/healthz` and `/readyz` are reachable
- `/debug/capabilities` advertises the Responses surface and, for
  `provider/model` aliases, the routed model
- `/v1/models` works and lists the target model
- a direct `/v1/responses` smoke works before invoking Codex
- generated artifacts include `summary.json`, `summary.md`, `env.sh`, and an
  isolated `codex-home/config.toml`

Boundary:

- `shimctl codex doctor` does not run a full Codex task by default; the eval
  harness remains responsible for tool-mode and task-success evidence.
- This is shim-owned developer tooling. It does not widen the OpenAI API
  compatibility claim.

### 9. Smoke Scenario Inventory

Classification: V4 preflight test planning.
Status: implemented for the V4 preflight smoke aggregator and scenario
inventory. Deeper domain-specific smokes remain in their existing V3/Codex
targets.

Goal:
Preserve useful external-project scenario coverage while deriving expected
behavior from this repo's docs, tests, official docs, and fixtures.

For day-to-day command usage, artifact reading order, failure triage, and
scope boundaries, see the
[V4 preflight runbook](guides/operations.md#v4-preflight-runbook). This
section stays architecture-oriented.

The default aggregate operator smoke is:

```bash
make v4-preflight-smoke
```

It verifies the V4 platform substrate, not model quality:

- `/healthz`, `/readyz`, and `/v1/models` are reachable
- `/debug/capabilities` exposes valid
  `v4.backend_capabilities.v1` and `v4.plugin_contracts.v1` registries
- backend components that advertise `plugin_id` cross-link to plugin
  descriptors with the same id/version
- plugin descriptors keep `request_cleanup_hooks` as named strings when hooks
  are present
- debug traces are enabled in capabilities unless
  `V4_PREFLIGHT_REQUIRE_DEBUG_TRACE=0` makes trace checks advisory
- a direct `POST /v1/responses` request creates a retrievable metadata-only
  `/debug/traces/{request_id}` record
- that trace contains V4 tool-classifier metadata for at least one function
  tool

For a routed provider alias, run:

```bash
V4_PREFLIGHT_PROVIDER_MODEL=deepseek/deepseek-v4-pro make v4-preflight-smoke
```

That nests the existing provider-routing smoke inside the same artifact tree.
The nested smoke verifies live `provider/model` routing through
`/debug/capabilities`, `/v1/models`, `/v1/responses`,
`/v1/chat/completions`, derived Responses helper endpoints, and fail-closed
unknown-provider behavior.

Optional Codex config preflight can be added to the same run:

```bash
V4_PREFLIGHT_RUN_CODEX_DOCTOR=1 \
  V4_PREFLIGHT_PROVIDER_MODEL=deepseek/deepseek-v4-pro \
  make v4-preflight-smoke
```

This keeps `shimctl codex doctor` as a direct config/probe check, while
long-running Codex task success remains owned by the Codex eval harness.

The aggregate smoke writes:

- `summary.json` and `summary.md`
- request/response/status/header files for each probe
- a bounded `shim.log.slice`
- nested provider-routing and Codex doctor artifacts when enabled

Environment controls:

- `SHIM_BASE_URL` chooses the running shim, default
  `http://127.0.0.1:8080`
- `SHIM_AUTH_HEADER` or `SHIM_API_KEY`/`GW_API_KEY`/`OPENAI_API_KEY` supplies
  shim bearer auth
- `V4_PREFLIGHT_MODEL` selects the direct Responses trace model; otherwise the
  smoke uses `MODEL`, `CODEX_MODEL`, `TESTER_MODEL`, the first `/v1/models`
  entry, then `devstack-model`
- `V4_PREFLIGHT_REQUIRE_READYZ=0` records readiness as advisory for partial
  local debugging
- `V4_PREFLIGHT_REQUIRE_DEBUG_TRACE=0` records trace retrieval as advisory
  when debug traces are intentionally disabled
- `V4_PREFLIGHT_RUN_PROVIDER_ROUTING=0|1|auto` controls nested provider
  routing
- `V4_PREFLIGHT_RUN_CODEX_DOCTOR=1` enables the Codex config doctor
- `V4_PREFLIGHT_TIMEOUT_SECONDS` controls per-probe timeout and health wait
- `V4_PREFLIGHT_ARTIFACT_DIR` changes the artifact root

Candidate scenario families:

- plain `/v1/responses` text
- streamed text
- retrieve stored response
- retrieve input items
- `previous_response_id`
- `conversation`
- `store=false`
- `include` no-op/proxy/subset behavior
- function call and function output follow-up
- custom tool raw-input flow
- constrained custom tool validate/repair
- local `file_search`
- local `web_search`
- local `image_generation`
- local `computer`
- local `code_interpreter`
- remote MCP `server_url`
- connector MCP `connector_id` proxy-only bridge
- hosted/server `tool_search`
- client `tool_search_output`
- native local `shell`
- native local `apply_patch`
- `/responses/compact`
- create-stream replay
- retrieve-stream replay
- WebSocket `response.create`
- Codex boot
- Codex read-only command task
- Codex write-file task
- Codex tiny bugfix task
- Codex interactive or multi-agent request-shape diagnostics
- auth failure
- quota failure
- unsupported tool failure
- backend malformed response
- fallback success and fallback rejection

Each scenario should specify:

- required capability flags
- route and mode under test
- expected public status
- expected stored state
- expected stream/replay class
- expected logs or debug trace fields
- whether upstream fixtures are mandatory
- whether it belongs in CI, full local smoke, manual runbook, or external
  tester profile

### 10. Observability And Debug Trace Contract

Classification: V4 preflight platform work.

Status: implemented for bounded, metadata-only request traces exposed through
shim-owned debug endpoints.

Goal:
Make routing, backend projection, classifier decisions, and fallback visible to
operators without leaking user data or secrets.

The private trace should capture:

- request id
- public route and method
- source format
- canonical surface
- selected backend/profile
- routing mode
- tool classifier decisions
- backend projection class
- removed or transformed backend-only fields
- unsupported field decisions
- fallback attempts
- cooldown and quota decisions
- persistence decision
- replay class
- stream transformer class
- final public status

The trace must not expose:

- API keys or bearer tokens
- raw user prompts by default
- full tool outputs by default
- raw file contents
- decrypted opaque state
- private provider headers

Operator observability is exposed through `/debug/capabilities`, logs,
metrics, and the shim-owned `/debug/traces` / `/debug/traces/{request_id}`
endpoints. The trace store is in-memory, bounded by
`shim.debug_traces.max_entries`, and metadata-only. It must not add fake
fields to OpenAI-shaped responses.

### 11. Plugin Boundary Preparation

Classification: V4 plugin platform work.

Status: implemented for the current backend/runtime plugin rollout.

Goal:
Prepare extension/plugin interfaces after the capability and routing substrate
exists.

Candidate plugin contracts:

- `ModelBackend`
- `ChatProjectionBackend`
- `ResponsesBackend`
- `ToolRuntime`
- `MCPRuntime`
- `SearchBackend`
- `ImageBackend`
- `ComputerRuntime`
- `CodeInterpreterRuntime`
- `CapabilityProbe`
- `StreamEventMapper`
- `ReplayWriter`
- `FallbackPolicy`

Each plugin should declare:

- stable id and version
- config namespace
- required secrets by env name
- readiness probe
- capability report
- supported public surfaces
- supported backend projections
- limits and timeouts
- error taxonomy
- whether it is safe for CI fixtures
- whether it is production intended or dev-only

Plugin rules:

- plugin-specific knobs stay in plugin config, not in fake OpenAI request
  fields
- plugins cannot widen compatibility matrix claims by registration alone
- plugin capabilities must be visible in `/debug/capabilities`
- plugins must fail closed when configured for `local_only` but unavailable
- plugin errors must map into the shared error taxonomy

Implemented rollout:

- `internal/plugincontract` defines `v4.plugin_contracts.v1` descriptors,
  backend projection declarations, required secret references, readiness probe
  links, public surfaces, timeout/error-class metadata, validation, and a
  `CapabilityPlugin` interface.
- Primary storage, retrieval index/embedder, compaction, constrained decoding,
  Responses WebSocket transport, web search, image generation, computer,
  code interpreter, native local `shell`, native local `apply_patch`, Codex
  client profile, the default `model.llama` backend, and configured
  `llama.providers[]` entries are exposed as plugins. Their descriptors live
  in `/debug/capabilities.plugins.plugins[]`, while their backend capability
  components are cross-linked through `plugin_id`, `plugin_version`, and
  `plugin_contract_version`.
- The plugin registry validates that every descriptor points at an existing
  backend component and that backend component plugin metadata points back at
  an advertised descriptor.
- Provider-routed requests record the selected plugin contract in
  `/debug/traces/{request_id}`.
- Plugin descriptors expose only config namespaces and required env secret
  names. They do not emit bearer-token values and do not add any public
  `/v1/*` request or response fields.

### 12. Security And Regression Guardrails

Classification: V4 preflight hardening.

Goal:
Carry the repo's current security guardrails into the new architecture.

Do not import:

- MITM or TLS certificate spoofing machinery
- private backend scraping as a default capability
- undocumented public request limits
- global Responses-to-Chat wrappers that erase state
- in-memory caches as persistence substitutes
- hidden field deletion as compatibility behavior
- fake language-level sandboxes as security boundaries

Required checks for implementation work in this area:

- no full request, stream, replay, or list materialization on hot paths unless
  bounded by contract and tests
- no hidden OpenAI-surface regressions introduced to simplify routing
- sibling paths checked together: create-stream and retrieve-stream, local and
  proxy, stored and synthetic replay
- capability limits centralized in config or service limit structs
- debug traces scrub secrets and large payloads
- `go test ./...`
- `make lint`
- `git diff --check`

## Implementation Sequence

### Phase 0: Documentation And Schema Sketch

Status: implemented.

- land this preflight document
- add a small capability schema sketch or ADR
- align wording in `v4-scope.md`, `compatibility-matrix.md`, and relevant
  guides before code starts
- list the first provider profiles to support

### Phase 1: Capability Registry

Status: implemented.

- define normalized capability structs
- load capabilities from config plus runtime probes
- generate `/debug/capabilities` from the registry
- add unit tests for capability normalization and contradiction detection
- keep the existing public routes behavior unchanged

Implemented details:

- registry schema version: `v4.backend_capabilities.v1`
- capability classes: `native`, `local_subset`, `proxy_only`,
  `chat_projection`, `repair_or_validate`, and `unsupported`
- component dimensions: stable id, category, kind, config namespace, backend,
  capability class, enabled/ready state, readiness probe, auth summary,
  secret refs, state ownership, wire modes, public surfaces, tools, model ids,
  routing modes, evidence, and notes
- `/debug/capabilities.ready` stays tied to existing dependency probes plus
  registry validation errors; the route still returns `200` and reports
  degraded state through its body

### Phase 2: Tool Classifier And Canonical Pipeline

Status: implemented for the request-time classifier gate and canonical
Responses request preservation on proxy paths. Broader stream/replay
classifier evidence belongs to Phase 3.

- added classifier result types and disposition vocabulary in
  `internal/httpapi/tool_classifier.go`
- route current Responses `tools[]` families through the classifier before
  create-route selection
- local-only mode rejects `proxy_only`, `upstream_passthrough`,
  `client_round_trip`, and `reject` classifications with a `tools` validation
  error
- focused tests cover the known tool families, local-only rejection, and
  prefer-upstream preservation of an MCP connector request body
- canonical storage and local Chat projection still use the existing
  Responses-owned state pipeline; this slice does not add a new stored-state
  object model

### Phase 3: Stream And Replay Interface

Status: implemented for the shared stream/replay emitter scope.

- factor event emission behind a shared interface
- preserve existing SSE behavior while making capability class explicit
- add create-stream and retrieve-stream coverage for classifier decisions
- add large replay tests to prevent full event materialization

### Phase 4: Codex Profile And Generator

Status: implemented for config generation and direct doctor preflight.

- `shimctl codex config` generates current Codex TOML for a shim custom
  provider
- `shimctl codex doctor` writes an isolated Codex home plus JSON/Markdown/env
  artifacts and checks Codex binary, auth env, shim health, capabilities,
  `/v1/models`, and direct `/v1/responses`
- Codex task success remains separate from hosted Responses parity claims and
  stays covered by the Codex eval harness

### Phase 5: Provider Hooks And Fallback Policy

Status: implemented for named provider/request cleanup hooks, the shared backend
failure policy, and debug trace visibility.

- add named backend cleanup hooks
- map backend errors into the shared taxonomy
- record fallback decisions in debug traces
- expose degraded backend state in `/debug/capabilities`

### Phase 5.5: Observability And Debug Trace Contract

Status: implemented for bounded request traces and capability advertising.

- `/debug/traces` lists the latest retained traces and
  `/debug/traces/{request_id}` retrieves one trace by `X-Request-Id`
- traces capture request metadata, response route/status, model/provider route,
  Responses create-route selection, tool classifier decisions, Chat projection
  transforms, named request cleanup hooks, backend failure policy decisions,
  rate-limit decisions, and final status
- traces are metadata-only and exclude raw prompts, bearer tokens, private
  headers, tool outputs, and file contents
- `shim.debug_traces.enabled` and `shim.debug_traces.max_entries` configure
  the operator trace store
- `/debug/capabilities.runtime.ops.debug_traces` reports the active retention
  and endpoint contract

### Phase 6: Plugin Contracts

Status: implemented for the current backend/runtime plugin rollout.

- introduce the first stable plugin interface only after the registry,
  classifier, and traces are in place
- migrate one existing backend/runtime behind the contract as proof
- document plugin capability reporting and failure behavior

Implemented details:

- `internal/plugincontract` owns the stable descriptor schema.
- current backend/runtime families are exposed through plugin descriptors:
  storage, retrieval, compaction, constrained decoding, Responses WebSocket,
  tool runtimes, Codex profile, and model backends/providers.
- `/debug/capabilities.plugins` reports plugin descriptors, while
  `/debug/capabilities.backends.components[]` remains the operational
  readiness/capability registry.
- plugin/component cross-links are validated so the two registries cannot drift
  silently.
- `/debug/traces` records selected provider plugin metadata for model-routed
  requests.

## Non-Goals

- no V4 claim of exact hosted OpenAI parity
- no compatibility matrix upgrade from OmniRoute research alone
- no public `/v1/*` route changes solely for plugin convenience
- no generic "any tool converts to function" layer
- no replacement of upstream fixtures with synthetic third-party behavior
- no hidden fallback from `local_only`
- no weakening of V2/V3 documented boundaries

## Definition Of Done For V4 Preflight

The preflight is complete when:

- backend capabilities are centralized and observable
- tool routing decisions are explicit, testable, and visible in debug traces
- chat-only backend projections preserve shim-owned Responses state and replay
- Codex CLI has a named profile with config generation and smoke evidence
- fallback, cooldown, quota, and unsupported-feature handling are consistent
- stream and replay paths share an interface without full event materialization
- plugin contracts can be added without leaking provider-specific fields into
  the public OpenAI-compatible surface
- documentation states conservative boundaries before implementation claims are
  widened

Until then, V4 extension and plugin work should stay scoped to designs that do
not depend on vague provider capabilities or universal tool conversion.
