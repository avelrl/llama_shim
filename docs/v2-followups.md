# V2 Follow-Up Work

Last updated: April 15, 2026.

This document tracks narrowly scoped follow-up work that may still land under
the V2 umbrella without rewriting the frozen V2 release ledger in
[v2-scope.md](v2-scope.md).

The source of truth for the current shipped status remains
[compatibility-matrix.md](compatibility-matrix.md). A checkbox in this file is
planning state, not a compatibility claim.

## Official References Rechecked

The items in this document were re-checked on April 15, 2026 against:

- local official-docs index: `openapi/llms.txt`
- [Compaction](https://developers.openai.com/api/docs/guides/compaction)
- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- official API reference for `/v1/responses`
- official API reference for `/v1/responses/compact`

## Candidate: Automatic Server-Side Compaction

Goal: add a docs-aligned local subset of automatic server-side compaction for
`POST /v1/responses` via `context_management.compact_threshold`.

The intended V2 framing is conservative:

- support a useful shim-local subset
- keep standalone `/v1/responses/compact` as the underlying primitive
- do not claim exact hosted encrypted payload parity
- do not claim exact hosted SSE choreography unless docs or fixtures pin it down

### Already Shipped

- [x] Standalone `POST /v1/responses/compact` local subset
- [x] Reuse of a returned compaction item in a later local response
- [x] Generic local state expansion of shim-owned synthetic compaction items

### Decomposed Work

- [x] Request contract and OpenAPI
  Accept `context_management.compact_threshold` on `POST /v1/responses`,
  validate the supported local subset, and keep unsupported fields explicit.
- [x] Local threshold decision
  Evaluate the effective local context before generation and trigger automatic
  compaction when the threshold is crossed.
- [x] Non-stream create path
  Run automatic compaction before local generation, rebuild the effective
  context, and produce the final assistant output from the compacted state.
- [x] Stateful follow-up behavior
  Persist enough derived state so `previous_response_id` and `conversation`
  follow-up turns continue from the compacted context rather than the pre-cut
  window.
- [x] Create-stream behavior
  Emit a generic compaction output item before the final assistant output and
  keep the choreography honest as a shim-local subset.
- [x] Retrieve-stream replay
  Replay the stored compaction item and final assistant output consistently.
- [x] Mode behavior
  Re-check `prefer_local`, `prefer_upstream`, and `local_only` behavior so the
  automatic path does not silently change routing promises.
- [x] Integration coverage
  Cover non-stream happy path, stream happy path, `previous_response_id`
  follow-up, conversation follow-up, and main mode/validation edges.
- [x] Docs and matrix update
  Update the compatibility matrix, practical docs, release notes, and OpenAPI
  wording only after the implementation and tests are aligned.

### Conscious Boundaries While This Is In Flight

- exact hosted encrypted compaction payload format is not required for this
  candidate
- exact hosted SSE event choreography is not required unless fixture-backed
- the compatibility matrix should describe only the shipped core local
  text/stateful subset and keep exact hosted choreography out of scope
