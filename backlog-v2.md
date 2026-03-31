# Backlog / roadmap toward v2

Актуализировано по состоянию на 31 марта 2026 на основе текущего состояния репозитория и актуальных OpenAI docs по `Responses API`, `Conversations API`, tool calling и `file_search`.

## Current baseline in repo

Сейчас в репозитории уже есть базовый stateful shim, поэтому это больше не “roadmap after v1”, а roadmap от текущего working baseline:

- `POST /v1/responses`
- `GET /v1/responses/{id}`
- `POST /v1/conversations`
- `POST /v1/responses` with `stream: true` over SSE
- `healthz` и базовый `/readyz`
- SQLite migrations, `WAL`, default `busy_timeout`
- request IDs и JSON request logs
- hybrid mode: локальная stateful логика + proxy/shadow-store через upstream `responses`
- локально поддерживаемые generation fields: `reasoning`, `temperature`, `top_p`, penalties, `stop`, `max_output_tokens`

Из этого следуют два важных вывода:

1. streaming уже не отдельная “будущая фича”, а тема для parity и hardening
2. следующий реальный шаг для v2 это не “ещё один endpoint”, а доведение совместимости `Conversations` + `Responses` до более честного OpenAI-compatible уровня

---

## Product direction

- `llama.cpp` остаётся stateless inference backend
- shim продолжает владеть state semantics
- где это разумно, выбираем OpenAI-compatible surface вместо bespoke API
- сначала закрываем episodic memory, response fidelity и conversations
- потом tools и richer items
- только потом retrieval/vector stores и multi-instance story

---

## Phase 1. Conversations and response fidelity

### 1.1. Conversations API parity

- `GET /v1/conversations/{id}`
- `GET /v1/conversations/{id}/items`
- `POST /v1/conversations/{id}/items`
- позже, если понадобится item mutation: `DELETE /v1/conversations/{id}/items/{item_id}`
- pagination / ordering contract for conversation items
- стабильная публичная shape for items, даже если внутри хранится больше служебных полей

### 1.2. Responses API parity around stored state

- `GET /v1/responses/{id}/input_items`
- `DELETE /v1/responses/{id}`
- позже, если появится background mode: `POST /v1/responses/{id}/cancel`
- чёткая retention semantics для standalone responses vs responses attached to conversation

### 1.3. Better response object fidelity

- `created_at`
- `status`
- `usage`
- `incomplete_details`
- `error`
- `metadata`
- максимально без потерь сохранять upstream response fields, если они уже пришли из backend-а

### 1.4. Request surface expansion

- request `metadata`
- `text.format` subset
- `include`
- `truncation`
- `parallel_tool_calls`
- более аккуратное решение для mixed supported/unsupported fields, чтобы local state не ломался из-за одного дополнительного поля

### 1.5. Richer input/output items

- full content arrays, а не только collapsed text
- `input_text`
- подготовка к `input_image`
- attachments / file refs
- annotations
- lossless item metadata

---

## Phase 2. Streaming parity and lifecycle hardening

Current state: basic SSE for `POST /v1/responses` уже есть и финальный response сохраняется. Следующий шаг это не “сделать streaming”, а сделать его более точным и надёжным.

### 2.1. Streaming contract fidelity

- event flow ближе к реальному `Responses API`
- support `stream_options`
- стабильные IDs между streamed и stored form
- явная стратегия: когда используем local SSE wrapper, а когда можно честно проксировать upstream `/v1/responses` stream

### 2.2. Lifecycle persistence

- `in_progress` / `completed` / `failed` / `cancelled`
- partial output buffer
- finalization timestamp
- понятная политика при client disconnect
- понятная политика при upstream error после partial deltas
- post-stream `usage` / accounting fields, когда это возможно

### 2.3. Runtime safety

- graceful shutdown с drain активных стримов
- streaming error taxonomy
- timeout policy for streams
- cancel path, если появится background generation

---

## Phase 3. Tools and richer conversation items

OpenAI Conversations хранят не только messages, но и tool calls / tool outputs. Для v2 это должен быть основной compatibility target.

### 3.1. Tool-call abstraction in the shim

- принимать tool definitions
- support `tool_choice`
- support `parallel_tool_calls`
- передавать tool config backend-у, если backend это умеет
- иначе запускать явный orchestration loop в shim

### 3.2. First supported tool subset

- function tools with JSON schema subset
- deterministic storage of tool call items
- deterministic storage of tool call output items
- canonical append of tool results into conversation items
- чистый bridge для follow-up turns через `call_id`

### 3.3. Guardrails

- max tool iterations
- per-tool timeout
- max total tool time
- audit trail
- allowlist / policy hooks для инструментов, которые могут менять внешний мир

---

## Phase 4. Retrieval and knowledge layer

Важно: это отдельный слой поверх episodic memory, а не замена `previous_response_id` и `conversation`.

### 4.1. Compatibility-first direction

Если цель проекта остаётся OpenAI-совместимость, retrieval лучше вести не через bespoke `/v1/knowledge/*`, а через совместимый subset вокруг:

- `vector_stores`
- `vector_stores/{id}/search`
- `file_search`-like tool contract inside `Responses API`

### 4.2. Storage choices

#### Single-node / local-first

- SQLite + `sqlite-vec`

Плюсы:

- low ops
- один локальный DB file
- быстрый путь для экспериментов

Минусы:

- слабее ecosystem
- выше риск миграции позже

#### Multi-instance / production

- Postgres + `pgvector`

Плюсы:

- лучше operational story
- проще shared state и backup flows
- естественный дом для episodic + semantic state

Минусы:

- выше infra cost
- выше цена миграции

### 4.3. Retrieval policy

- явно разделять system instructions, episodic memory, retrieved knowledge, current input
- логировать, какие chunks/files были подмешаны
- metadata filtering
- namespace / tenant isolation
- predictable citation shape for retrieved content

---

## Phase 5. Storage abstraction and multi-instance mode

### 5.1. Prepare for Postgres without abstraction zoo

- `ResponseStore`
- `ConversationStore`
- optional `VectorStore` / `KnowledgeStore`
- explicit SQL per backend, без лишней магии

### 5.2. Concurrency and idempotency

- optimistic locking for conversations via `version`
- transaction retries
- idempotency keys for create endpoints
- background jobs, безопасные для multi-instance режима

### 5.3. State ownership

- не держать DB transaction во время вызова llama backend
- deterministic append semantics for conversation items
- реалистичный migration path from SQLite to Postgres

---

## Phase 6. Security, tenancy, governance

### 6.1. Auth and tenanting

- API keys
- per-tenant quotas
- tenant scoping for responses / conversations / vector stores / files

### 6.2. Data governance

- retention policies
- hard delete vs soft delete
- redact sensitive fields in logs
- optional encryption at rest for selected columns

### 6.3. Abuse limits

- request size limits
- rate limits
- circuit breaker for failing llama backend
- safe defaults for tool execution

---

## Phase 7. Operations and DX

### 7.1. Observability

- request latency
- upstream latency
- DB latency
- error rates by class
- response chain depth
- conversation length distribution
- stream success / interruption metrics

### 7.2. Health and admin endpoints

- расширить `/readyz`, чтобы он проверял и DB, и llama backend
- inspect response lineage
- inspect conversation timeline
- dump normalized context for debugging
- optional replay to upstream for incident analysis

### 7.3. SQLite operations

- configurable busy timeout
- backup/restore commands
- retention cleanup job
- vacuum / optimize maintenance path

### 7.4. Developer experience

- Makefile
- dev script for shim + fake llama
- seed fixtures
- `.env.example`
- Dockerfile
- `docker-compose` for local dev

---

## Future API backlog, prioritized

### High priority

- `GET /v1/conversations/{id}`
- `GET /v1/conversations/{id}/items`
- `GET /v1/responses/{id}/input_items`
- richer response metadata: `created_at`, `status`, `usage`, `error`, `incomplete_details`, `metadata`
- `text.format` / JSON-mode subset
- better upstream error reporting
- retention management + `DELETE /v1/responses/{id}`

### Medium priority

- `POST /v1/conversations/{id}/items`
- tool calls / function calling
- streaming lifecycle parity
- vector-store / file-search compatible retrieval
- idempotency keys
- metadata filtering and citations for retrieval

### Lower priority

- Postgres multi-instance mode
- dashboards
- tenant admin UI
- broad multimodal parity
- full parity with every OpenAI field

---

## Technical debt watchlist

- local-vs-proxy decision logic не должна расползтись по handlers
- stream event shape и stored response shape не должны расходиться
- unsupported fields не должны тихо ломать local state reconstruction
- output parsing assumptions against upstream нужно закрывать тестами
- conversation append logic должна оставаться централизованной
- integration tests должны оставаться на deterministic fake backends, а не на реальных моделях

---

## Suggested next practical milestone

### v1.2

- `GET /v1/conversations/{id}`
- `GET /v1/conversations/{id}/items`
- `GET /v1/responses/{id}/input_items`
- richer response metadata
- `text.format` JSON-mode subset
- Dockerfile + Makefile + local dev script
- retention cleanup job
- `/readyz` backend probe

---

## Suggested v2.0 milestone

### v2.0

- Conversations API parity for read paths and canonical item storage
- response object fidelity good enough for common OpenAI clients
- tools/function-calling MVP with stored tool items
- streaming lifecycle parity beyond basic SSE
- retrieval via vector-store / file-search compatible subset
- clear migration path from SQLite single-node to Postgres multi-instance
