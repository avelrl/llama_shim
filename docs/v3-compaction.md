# V3 Compaction Runtime

Last updated: April 23, 2026.

This document fixes the design starting point for the V3 compaction track
before implementation begins.

It does not change the frozen V2 contract.
It does not change OpenAPI.
It does not claim new OpenAI-surface parity before code, tests, and
capabilities exist.

## Why This Exists

The shim already supports compaction as a useful V2 subset:

- standalone `/v1/responses/compact`
- shim-local automatic compaction via
  `context_management=[{"type":"compaction","compact_threshold":N}]`
- local follow-up reuse through `previous_response_id`, `conversation`, and
  `/v1/responses/{id}/input_items`

That is valuable, but the current local implementation is still a coarse,
heuristic summary path.

The current V2 subset is intentionally lossy.
Lossiness is not the problem by itself.
The problem is that the current loss is too blunt for long, tool-heavy, or
stateful workflows where context quality matters more than transcript fidelity.

The V3 goal is higher-fidelity context compression:

- keep compaction explicitly lossy
- preserve more task-relevant state than the current deterministic synopsis
- keep the external OpenAI-facing surface honest and opaque

This is a runtime-expansion and quality track, not a V2 compatibility
requirement.

## Official References Reviewed

This design note was re-checked on April 21, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [Compaction](https://developers.openai.com/api/docs/guides/compaction)
  - [Prompt guidance: preserve behavior in long sessions](https://developers.openai.com/api/docs/guides/prompt-guidance#preserve-behavior-in-long-sessions)
  - [Compact a response](https://developers.openai.com/api/reference/resources/responses/methods/compact)

The practical takeaway from the current official docs is:

- compaction reduces context size while preserving state needed for subsequent
  turns
- compaction items are opaque and not intended to be human-interpretable
- server-side compaction via `context_management.compact_threshold` prunes
  context during normal `/responses` generation
- standalone `/responses/compact` returns the canonical next context window,
  not just an isolated compaction blob
- the returned compacted window may include retained items from the previous
  window in addition to the compaction item

## Current V2 Baseline

The frozen V2 truth remains:

- `/v1/responses/compact` is a `Broad subset` in
  [compatibility-matrix.md](compatibility-matrix.md)
- automatic server-side compaction via `context_management.compact_threshold`
  is also a `Broad subset`
- V2 does not claim exact hosted encrypted payload fidelity
- V2 does not claim exact hosted stream choreography for compaction

This document does not reopen those claims.

## Working Inference From The Docs

The official docs intentionally describe compaction as opaque state.
They do not document the internal algorithm.

The following is an engineering inference from the public behavior, not a claim
about OpenAI's private implementation:

- the hosted system likely performs loss-aware state compression, not just a
  single readable summary string
- the hosted output likely preserves some raw structure or retained items in
  addition to compressed state
- the compaction artifact exists so the server can evolve the internal state
  format without exposing its internals as a public contract

This is enough to justify a better local compaction runtime without pretending
to know or copy the hosted internal algorithm exactly.

## V3 Design Goal

The first V3 target is not "perfect parity."
The first V3 target is "normal compaction quality" for long local sessions:

- older context should compress into a more useful opaque state artifact
- recent context should often remain verbatim when it is still cheap and useful
- the next turn should recover key facts, constraints, open loops, and recent
  tool state more reliably than the current heuristic path

## First Implementation Target

The first rollout should stay narrow and operator-friendly:

- keep the current heuristic compactor as the baseline fallback
- use the shared `internal/compactor` abstraction above the current synthetic
  item format
- enable a `model_assisted_text` backend driven by a small fast local instruct
  model chosen by the operator
- bias the first model-assisted slice toward text and tool metadata before
  broader multimodal state

This intentionally does not lock V3 to one model family.
A Gemma-family local model is a reasonable candidate, but the design should
stay backend-agnostic.

The initial implementation is configured under `responses.compaction`:

- `backend: heuristic` keeps the deterministic V2-compatible fallback
- `backend: model_assisted_text` calls an OpenAI-compatible
  `/v1/chat/completions` backend for internal compaction only
- `model`, `base_url`, `timeout`, `max_output_tokens`, `retained_items`, and
  `max_input_chars` tune the internal compactor without changing the public
  `/v1/responses` or `/v1/responses/compact` contract
- if the model-assisted call fails, times out, or returns invalid JSON, the
  shim falls back to `heuristic` instead of failing the main client path

## Output Model

V3 compaction should preserve an OpenAI-compatible external shape while making
the internal state richer.

Externally:

- keep returning an opaque `compaction` item
- keep the item format shim-owned unless exact hosted parity is actually
  implemented and tested
- keep `/v1/responses/compact` output usable as the next input window without
  client-side pruning

Internally:

- stop treating compaction as only a readable free-text synopsis
- allow the opaque payload to hold structured local state
- support retained raw items alongside compressed state when that improves
  continuation quality

## Capability Classes

V3 compaction should be framed in capability classes rather than in one hard
implementation.

### `heuristic`

The current deterministic summary-based subset.

Behavior:

- keep the existing V2-compatible fallback
- continue to support the current local state paths where no better runtime is
  configured

### `model_assisted_text`

A local model-assisted compactor for text and tool metadata.

Behavior:

- retain a bounded recent tail of raw items
- compress older state with a small local model into structured opaque state
- reconstruct local generation context from the retained tail plus the compacted
  state

### `tool_aware_stateful`

A richer local compactor that understands outstanding and recent tool state.

Behavior:

- preserve unresolved tool loops and recent tool boundaries more explicitly
- distinguish durable state from transient transcript detail
- stay conservative where exact hosted semantics are unknown

## Routing Policy

The existing `responses.mode` contract stays in force.
V3 should refine runtime behavior without rewriting the public mode model.

### `prefer_local`

- use the highest local compactor capability available
- fall back to the current heuristic compactor when no model-assisted runtime
  is configured
- keep local state reuse explicit for `previous_response_id`, `conversation`,
  and `/v1/responses/{id}/input_items`

### `prefer_upstream`

- remain proxy-first unless the shim already owns the active local state chain
- do not silently claim hosted parity when the shim is still using a
  shim-owned compaction artifact
- keep proxy/fallback behavior explicit in capability and docs wording

### `local_only`

- require a local compactor path
- fall back to the heuristic local subset when no better compactor is
  configured
- reject only on boundaries that already require rejection, not because a
  higher-quality compactor is absent

## Canonical Window Direction

The first V3 compaction slice should move the local standalone path closer to
the official semantics:

- `/v1/responses/compact` should be shaped as the canonical next context window
- local output should be allowed to include retained items, not just one
  compaction item
- create, create-stream, retrieve-stream, and follow-up local state rebuild
  should all consume the same effective compaction window model

This is a practical quality improvement even before exact hosted parity.

## Suggested Rollout

The narrowest practical rollout is:

1. keep the V2 subset and wording intact
2. add a shared compactor abstraction with `heuristic` as the default
3. add retained-tail window shaping for standalone and automatic compaction
4. add one local `model_assisted_text` backend
5. add capability visibility for the active compactor mode
6. expand carefully into `tool_aware_stateful` behavior only after the text path
   is stable

## Test Expectations

Before any V3 compaction upgrade is called done, coverage should include:

- unit tests for compactor selection, retained-window shaping, and opaque state
  serialization
- integration tests for non-stream create, create-stream, retrieve-stream,
  `previous_response_id`, `conversation`, and
  `/v1/responses/{id}/input_items`
- regression tests that long sessions preserve key facts, constraints, and open
  loops better than the current heuristic path
- routing tests for `prefer_local`, `prefer_upstream`, and `local_only`
- conservative fixture-backed work only where exact hosted choreography is
  claimed or required

The current heuristic subset tests remain part of the baseline.

## Non-Goals For The First Cut

The first V3 compaction slice should not try to do all of the following at
once:

- claim exact hosted encrypted payload parity
- claim exact hosted SSE choreography parity
- solve every multimodal or artifact-heavy state case
- remove the heuristic fallback before the better runtime is proven
- widen the public compatibility wording before the implementation and tests
  exist

## Working Rule

If a change mainly improves the quality of local context compression while
keeping the OpenAI-facing contract conservative and explicit, it belongs in
this V3 track.

If a change starts to depend on exact hosted encrypted state or exact hosted
wire choreography, it needs a tighter parity plan, and may need fixtures before
the wording can change.
