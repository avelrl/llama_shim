# V3 Constrained Decoding

Last updated: April 19, 2026.

This document fixes the design starting point for the V3 constrained decoding
track before implementation begins.

It does not change the frozen V2 contract.
It does not change OpenAPI.
It does not claim new OpenAI-surface parity before code, tests, and
capabilities exist.

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

This design note was re-checked on April 19, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [Function calling](https://developers.openai.com/api/docs/guides/function-calling#context-free-grammars)
  - [Function calling best practices](https://developers.openai.com/api/docs/guides/function-calling#key-ideas-and-best-practices)
  - [Using GPT-5.4: constraining outputs](https://developers.openai.com/api/docs/guides/latest-model#constraining-outputs)

The practical takeaway from the current official docs is:

- custom tools support `grammar` with `lark` and `regex`
- OpenAI's native implementation constrains sampling during generation
- grammars are expected to stay simple, explicit, and bounded

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

## First Implementation Target

The first V3 target is `llama.cpp`.

Reasoning:

- it is a realistic local deployment target for this repository
- it is expected to be used often enough to justify an explicit adapter
- it is a better first target than an opaque remote upstream because the shim
  can treat it as an operator-controlled runtime instead of a black-box chat
  proxy

This document intentionally does not assume that every backend will support the
same native constrained feature set.

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

The first rollout should stay narrow:

- `llama.cpp` is the first `grammar_native` target
- other backends remain on the current V2 fallback path until they have an
  explicit adapter and capability coverage
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

## `/debug/capabilities` Direction

Before or along with implementation, the capability manifest should grow
constrained-decoding-specific detail rather than a single vague boolean.

The manifest should be able to answer:

- whether constrained custom tools are available at all
- whether the current process is using shim-local validate and repair only
- whether native constrained decoding is available
- which capability class is active: `none`, `json_schema_native`, or
  `grammar_native`
- which backend is providing the native path when one exists

This keeps V3 work observable for operators, tests, and future automation.

## Test Expectations

Before any native constrained path is called done, coverage should include:

- unit tests for backend capability detection and adapter request shaping
- integration tests for `prefer_local`, `prefer_upstream`, and `local_only`
- request-level tests showing native path vs shim-local fallback behavior
- devstack or fixture-backed smoke coverage where the runtime is reproducible

The current shim-local validate and repair tests remain part of the baseline.

## Non-Goals For The First Cut

The first V3 constrained decoding slice should not try to do all of the
following at once:

- support every inference backend
- claim exact hosted parity for every grammar edge case
- replace the existing shim-local subset before the native path is proven
- widen the public OpenAI compatibility wording before the runtime behavior is
  implemented and tested

## Initial Rollout Shape

The expected first rollout is:

1. keep the V2 subset and wording intact
2. add a shared constrained-runtime abstraction
3. add a `llama.cpp` adapter as the first `grammar_native` backend
4. expose the new capability in `/debug/capabilities`
5. add integration and smoke coverage before any broader claims are made

That is the narrowest practical path from the current V2 subset to a real V3
runtime-expansion track.
