# V3 Compaction Runtime

Last updated: April 25, 2026.

Status: closed as a `Broad subset` in
[compatibility-matrix.md](compatibility-matrix.md).

This is a runtime-quality track for shim-owned local compaction. It does not
change the frozen V2 contract and does not claim exact hosted encrypted state,
exact hosted compaction algorithms, or exact hosted SSE choreography.

## Official References Reviewed

Re-checked on April 25, 2026 against:

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [Compaction](https://developers.openai.com/api/docs/guides/compaction)
  - [Standalone compact endpoint](https://developers.openai.com/api/docs/guides/compaction#standalone-compact-endpoint)
  - [Server-side compaction](https://developers.openai.com/api/docs/guides/compaction#server-side-compaction)
  - [Compact a response](https://developers.openai.com/api/docs/api-reference/responses/compact)

The docs-backed contract used here is:

- standalone `/responses/compact` returns a canonical next context window
- that window includes an opaque encrypted compaction item and may include
  retained items from the previous window
- clients should pass `/responses/compact` output into the next `/responses`
  call as-is
- server-side compaction is enabled with
  `context_management=[{"type":"compaction","compact_threshold":N}]`
- compaction items are opaque and not intended to be human-interpretable

## Implemented Scope

The shim now supports:

- standalone `POST /v1/responses/compact`
- server-side compaction through `context_management.compact_threshold`
- local follow-up reuse through `previous_response_id`, `conversation`, and
  `/v1/responses/{id}/input_items`
- non-stream create, create-stream, and retrieve-stream replay for the local
  compaction item lifecycle
- `heuristic` compaction as the deterministic fallback
- `model_assisted_text` compaction through a configured OpenAI-compatible
  `/v1/chat/completions` backend
- bounded retained-tail state inside shim-owned opaque compaction payloads for
  automatic compaction
- canonical standalone compact output for `model_assisted_text`: retained
  recent items followed by a shim-owned opaque `compaction` item
- `/debug/capabilities` visibility for active compaction backend,
  capability class, model presence, retained item limit, and max input chars
- devstack coverage using the deterministic fixture as the compaction backend

The default remains `heuristic`, so existing local deployments keep the stable
V2-compatible behavior unless `responses.compaction.backend` is changed.

## Runtime Configuration

Configured under `responses.compaction`:

- `backend: heuristic` keeps the deterministic fallback
- `backend: model_assisted_text` calls an OpenAI-compatible
  `/v1/chat/completions` backend for internal compaction only
- `base_url` defaults to `llama.base_url` when omitted with
  `model_assisted_text`
- `model`, `timeout`, `max_output_tokens`, `retained_items`, and
  `max_input_chars` tune the internal compactor

If the model-assisted call fails, times out, or returns invalid JSON, the shim
falls back to `heuristic` instead of failing the main client path. A canceled
request still returns the request cancellation.

## Output Model

Standalone `/v1/responses/compact` is treated as the canonical next input
window:

- `heuristic` usually returns a single shim-owned `compaction` item
- `model_assisted_text` returns up to `retained_items` recent raw items followed
  by a shim-owned `compaction` item
- callers should pass the returned `output` array as-is into the next local
  `/v1/responses` call

Automatic server-side compaction keeps the response output compact:

- when the threshold is crossed, prior state is compacted before generation
- the response output is prefixed with one `compaction` item
- the compaction item carries enough shim-owned local state to rebuild the
  effective context on later local follow-ups

## Capability Classes

### `heuristic`

Deterministic summary compaction. This is the stable fallback and the baseline
local subset.

### `model_assisted_text`

Text and tool-metadata compaction through an operator-selected local or
OpenAI-compatible Chat Completions backend. It extracts structured state
(`summary`, `key_facts`, `constraints`, `open_loops`, `recent_tool_state`) and
retains a bounded recent tail.

### Deferred

`tool_aware_stateful`, multimodal-aware state carry-forward, exact hosted
encrypted payload parity, and exact hosted stream choreography remain outside
this closed V3 slice. If they are implemented later, they need their own docs,
tests, and fixture-backed claims where exact wire behavior matters.

## Verification

Repo-owned closure is covered by:

- compactor unit tests for config normalization, fallback, model-assisted
  structured state, and retained-window output
- response service tests for automatic compaction and standalone canonical
  output
- HTTP integration tests for standalone compaction, automatic compaction,
  create-stream, retrieve-stream, `previous_response_id`, `conversation`, and
  `/input_items`
- `/debug/capabilities` integration coverage
- `scripts/devstack-smoke.sh` coverage for model-assisted standalone compaction
  and server-side `context_management` compaction
- `openapi/openapi.yaml` capability and compaction-output wording aligned to
  the local subset

Real upstream fixtures are not required for this closed label because this
slice does not claim exact hosted encrypted payload bytes or exact hosted SSE
choreography. Add upstream fixtures before making either of those claims.
