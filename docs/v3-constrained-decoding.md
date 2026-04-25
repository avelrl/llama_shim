# V3 Constrained Decoding

Last updated: April 25, 2026.

This document fixes the design and current implementation status for the V3
constrained decoding track.

It does not change the frozen V2 contract.
It does not claim exact hosted/native OpenAI constrained-sampling parity.

## Why This Exists

The shim already supports constrained custom tools as a useful V2 subset:

- request parsing for `grammar` and `regex`
- shim-local validation
- shim-local repair and retry behavior
- explicit fallback behavior in `prefer_local`, `prefer_upstream`, and
  `local_only`

That is valuable, but it is still "generate text, then validate or repair."

The V3 goal is deeper constrained decoding:

- use backend-native constrained generation where a runtime actually supports it
- keep the current shim-local validate and repair path as the compatibility
  fallback where native support does not exist

This is a runtime-expansion track, not a V2 compatibility requirement.

## Official References Reviewed

This design note was re-checked on April 25, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [Function calling](https://developers.openai.com/api/docs/guides/function-calling#context-free-grammars)
  - [Function calling best practices](https://developers.openai.com/api/docs/guides/function-calling#key-ideas-and-best-practices)
  - [Structured model outputs](https://developers.openai.com/api/docs/guides/structured-outputs)
  - [Using GPT-5.5](https://developers.openai.com/api/docs/guides/latest-model)

The practical takeaway from the current official docs is:

- custom tools support `grammar` with `lark` and `regex`
- OpenAI's native implementation constrains sampling during generation
- grammars are expected to stay simple, explicit, and bounded
- Structured Outputs are a schema-constrained API feature, while JSON mode
  only guarantees valid JSON and still requires application-side validation

That supports a V3 direction toward native constrained decoding where the shim
can actually control or select such a runtime.

## Current V2 Baseline

The frozen V2 truth remains:

- constrained custom tools are a `Broad subset` in
  [compatibility-matrix.md](compatibility-matrix.md)
- V2 does not claim true backend-native constrained decoding parity
- current request, validation, repair, and fallback behavior stays valid until
  a native path is implemented and tested

This document does not reopen those claims.

## Working Assumptions

The constrained decoding track starts from the following assumptions:

- arbitrary remote upstreams may be chat-only or may not expose native grammar
  controls to the shim
- the shim must not assume that a generic upstream can be forced into true
  constrained decoding from outside
- a backend-specific adapter is acceptable when the backend is operator-owned or
  locally deployed
- the shim should keep one shared abstraction above thin backend adapters rather
  than inventing a separate feature implementation per backend

## Implemented V3 Slice

The implemented V3 slice is deliberately conservative:

- a shared shim-local constrained custom tool runtime abstraction now owns the
  direct constrained generation path
- that runtime shapes a Chat Completions request with an OpenAI-compatible
  `response_format: {type: "json_schema"}` hint plus a llama.cpp-compatible
  top-level `json_schema` hint
- the hint is not treated as proof of native enforcement
- the final `custom_tool_call.input` is accepted only after shim-local regex
  validation against the compiled `grammar.regex` or supported Lark subset
- invalid, timed-out, or upstream-error runtime attempts still flow through the
  existing shim-local repair/fallback path

The current active capability class is therefore `none`, not
`json_schema_native` or `grammar_native`.

`llama.cpp` remains the likely first backend-specific native target, but no
`grammar_native` adapter is claimed until the shim can prove and test a concrete
backend-native grammar enforcement path.

## Capability Classes

V3 constrained decoding should be framed in capability classes rather than in
backend names.

### `none`

The backend has no native constrained decoding path available to the shim.

Behavior:

- keep the current shim-local validate and repair subset

Typical examples:

- generic remote upstreams
- opaque chat-only providers

### `json_schema_native`

The backend can natively constrain a structured subset, but not the full custom
tool `grammar` surface.

Behavior:

- use the native structured path only for the supported subset
- keep shim-local validate and repair for `grammar` and any unsupported
  constrained formats

### `grammar_native`

The backend can natively constrain custom tool input for the `grammar`
surface used by the shim.

Behavior:

- route the supported constrained request through the native path
- fall back explicitly when the request uses grammar features or modes outside
  the adapter's supported subset

## Backend Policy

The implemented rollout stays narrow:

- generic upstreams remain on the current validate/repair path
- the Chat Completions runtime receives structured-output hints where useful,
  but those hints are not advertised as native constrained decoding
- `llama.cpp` or another backend can move to `json_schema_native` or
  `grammar_native` only after an explicit adapter and capability coverage exist
- unknown or opaque upstreams should continue to behave as `none`

This avoids a misleading "native constrained decoding everywhere" story.

## Routing Policy

The existing `responses.mode` contract stays in force.
V3 should refine runtime routing without rewriting the public mode model.

### `prefer_local`

- use a local or operator-owned native constrained runtime when available
- otherwise use the existing shim-local validate and repair subset
- only fall back upstream on the same boundaries already accepted by the V2
  contract

### `prefer_upstream`

- remain proxy-first
- do not silently claim that a generic upstream now supports native constrained
  decoding
- if a future upstream-specific adapter exists, capability and routing should
  say so explicitly

### `local_only`

- require either a local native constrained runtime or the current shim-local
  subset
- reject unsupported requests explicitly rather than pretending the runtime can
  enforce constraints it cannot enforce

## `/debug/capabilities` Status

The capability manifest now exposes constrained-decoding-specific detail rather
than a single vague boolean.

`runtime.constrained_decoding` answers:

- whether constrained custom tools are available at all
- whether the current process is using shim-local validate and repair only
- whether native constrained decoding is available
- which capability class is active: `none`, `json_schema_native`, or
  `grammar_native`
- which backend is providing the native path when one exists
- which grammar formats and operational limits are active
- which structured-output validation subset is exposed by the shim

This keeps V3 work observable for operators, tests, and future automation.

Current default values intentionally report:

- `support: "shim_validate_repair"`
- `runtime: "chat_completions_json_schema_hint"`
- `capability_class: "none"`
- `native_available: false`
- `native_backend: "none"`

## Test Expectations

Before any native constrained path is called done, coverage still must include:

- unit tests for backend capability detection and adapter request shaping
- integration tests for `prefer_local`, `prefer_upstream`, and `local_only`
- request-level tests showing native path vs shim-local fallback behavior
- devstack or fixture-backed smoke coverage where the runtime is reproducible

The current shim-local validate and repair tests remain part of the baseline.

Implemented V3-slice coverage includes:

- unit coverage for constrained custom tool runtime request shaping and final
  validation
- integration coverage for direct and planner-selected grammar custom tools,
  invalid-output repair, local-only behavior, and stream replay
- `/debug/capabilities` coverage for the constrained runtime flags
- devstack smoke coverage through `make v3-constrained-decoding-smoke`

## Non-Goals For The First Cut

The first V3 constrained decoding slice should not try to do all of the
following at once:

- support every inference backend
- claim exact hosted parity for every grammar edge case
- replace the existing shim-local subset before the native path is proven
- widen the public OpenAI compatibility wording before the runtime behavior is
  implemented and tested

## Implemented Rollout Shape

The implemented first rollout is:

1. V2 subset and wording remain intact.
2. A shared constrained-runtime abstraction owns the direct constrained custom
   tool path.
3. The current runtime provides a structured-generation hint path, but no
   `grammar_native` adapter is claimed.
4. `/debug/capabilities` exposes the active constrained-decoding support,
   runtime, capability class, native availability, formats, limits, and routing.
5. Integration and devstack smoke coverage exist before any broader claims are
   made.

The next valid status upgrade would require a fixture-backed or backend-owned
adapter that can prove `json_schema_native` or `grammar_native` enforcement
instead of only accepting a hint and validating after generation.
