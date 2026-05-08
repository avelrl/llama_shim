# V4 State And Session Memory

Last updated: May 8, 2026.

This is a shim-owned V4 extension. It is not an OpenAI API parity claim and it
does not add a new public OpenAI-shaped memory endpoint.

## Official Boundary

The public OpenAI surfaces checked for this work are:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Text generation: message roles and instruction following](https://developers.openai.com/api/docs/guides/text#message-roles-and-instruction-following)

The relevant boundary is:

- `previous_response_id` and the Conversations API are conversation-state
  mechanisms.
- Request `instructions` apply only to the current response.
- Durable application memory is application-owned state, not a generic public
  OpenAI memory API contract.

The shim therefore implements memory as local extension behavior behind
ordinary `/v1/responses` requests.

## What Exists

`responses.memory.backend=local` enables a storage-backed memory note store.
The store supports:

- `global` notes shared across sessions
- `session` notes scoped by `llama_shim.memory.session_id`
- bounded note capture from request metadata
- bounded hidden developer-context injection for local Responses generation
- SQLite and Postgres storage
- governance purge coverage with the rest of local durable state
- `/debug/capabilities.runtime.memory` and `runtime.memory` plugin descriptors

By default the backend is disabled.

## Who Calls It

The shim does not ask the model to manage memory and Codex does not use this
extension automatically.

Memory is controlled by the client that calls `/v1/responses`:

- operators enable the backend in shim config
- clients write notes by sending ordinary `metadata` keys on a successful
  `/v1/responses` request
- clients select the session by sending `llama_shim.memory.session_id`
- injection happens inside the shim when the same session is used later

For Codex CLI this means memory is only active if the Codex-facing bridge or
wrapper deliberately sends these metadata keys. A stock Codex request that only
sends `model`, `input`, tools, and conversation state will not create memory
notes.

## When To Use

Use this extension for explicit, durable facts that are small enough to carry as
state and important enough to keep out of retrieval guesswork:

- project preferences, for example the expected release-gate command
- session-specific working facts, for example "this investigation is about
  provider routing"
- operator rules, for example "after V4 preflight changes, run
  `make v4-preflight-smoke`"
- stable user or team constraints when the calling app has consent and owns
  that state

Do not use it for large documents, source material, or fuzzy search. Those
belong in retrieval. Do not use it as automatic user profiling yet; this slice
has no consent, PII, conflict-resolution, or promotion policy.

## Configuration

```yaml
responses:
  memory:
    backend: local
    inject: true
    max_notes: 8
    max_note_bytes: 2KiB
    max_context_bytes: 8KiB
    metadata_namespace: llama_shim.memory
```

Environment overrides use the normal key mapping:

```bash
RESPONSES_MEMORY_BACKEND=local
RESPONSES_MEMORY_INJECT=true
RESPONSES_MEMORY_MAX_NOTES=8
RESPONSES_MEMORY_MAX_NOTE_BYTES=2KiB
RESPONSES_MEMORY_MAX_CONTEXT_BYTES=8KiB
RESPONSES_MEMORY_METADATA_NAMESPACE=llama_shim.memory
```

## Request Metadata

The default namespace is `llama_shim.memory`.

| Key | Meaning |
| --- | --- |
| `llama_shim.memory.session_id` | Session memory scope for this request. |
| `llama_shim.memory.remember` | Explicit note text to persist after a successful response. |
| `llama_shim.memory.note` | Alias for `remember`. |
| `llama_shim.memory.scope` | `session` or `global`; default is `session` when `session_id` exists. |
| `llama_shim.memory.inject` | Per-request injection override: `true` or `false`. |

`max_note_bytes` bounds the internal accepted note size. Requests still pass
through normal `metadata` validation first, so metadata keys and values remain
subject to the existing shim metadata limits.

Example:

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "provider/model",
    "input": "Reply OK",
    "metadata": {
      "llama_shim.memory.session_id": "project-a",
      "llama_shim.memory.remember": "Preferred test command is make v4-preflight-smoke"
    }
  }'
```

Follow-up:

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "provider/model",
    "input": "What test command should I run?",
    "metadata": {
      "llama_shim.memory.session_id": "project-a"
    }
  }'
```

## Injection Semantics

Memory injection is intentionally hidden shim context:

- It is rendered as one `developer` message for local generation.
- It is not persisted into public stored `input_items`.
- It is skipped for pure proxy/shadow-store paths where the shim is only
  recording upstream behavior.
- It is not used by `/v1/responses/compact` or
  `/v1/responses/input_tokens`; those derived endpoints remain based on the
  explicit request/context input.

This keeps the OpenAI-shaped response and replay state from silently growing
new public fields while still allowing practical local memory behavior.

## Operational Checks

Check configuration:

```bash
make v4-provider-config-doctor
```

Check live capabilities:

```bash
curl http://127.0.0.1:8080/debug/capabilities | jq '.runtime.memory, .plugins.plugins[] | select(.id=="runtime.memory")'
```

Run the normal V4 gate:

```bash
V4_PREFLIGHT_PROVIDER_MODEL=<provider>/<model> make v4-preflight-smoke
```

Run focused tests:

```bash
go test ./internal/service ./internal/storage/sqlite ./internal/httpapi -run 'Memory|CapabilitiesEndpointReportsConfiguredRuntime'
```

## Current Non-Goals

- no automatic extraction or promotion from arbitrary model text
- no user profile schema
- no conflict-resolution engine
- no PII classifier or consent workflow
- no external Redis/vector/managed memory backend
- no public memory CRUD HTTP route

Those are valid future V4 tracks, but the current slice is deliberately
explicit, local, bounded, and easy to purge.
