# Backlog / roadmap toward v2

Актуализировано по состоянию на 8 апреля 2026 на основе:

- текущего состояния репозитория, маршрутов и тестов
- текущего staged diff
- официального OpenAI API surface и docs:
  [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state),
  [Streaming responses](https://developers.openai.com/api/docs/guides/streaming-responses),
  [Function calling](https://developers.openai.com/api/docs/guides/function-calling),
  [Structured outputs](https://developers.openai.com/api/docs/guides/structured-outputs),
  [File search](https://developers.openai.com/api/docs/guides/tools-file-search),
  [Compaction](https://developers.openai.com/api/docs/guides/conversation-state#compaction),
  [Token counting](https://developers.openai.com/api/docs/guides/token-counting#api-reference)

## Текущий baseline в репе

Сейчас в коде уже есть не “заготовка под future”, а рабочий stateful shim с таким public surface:

- `POST /v1/responses`
- `GET /v1/responses/{id}`
- `GET /v1/responses/{id}/input_items`
- `POST /v1/conversations`
- `GET /v1/conversations/{id}/items`
- `POST /v1/chat/completions`
- `POST /v1/responses` with `stream: true` over SSE
- `/healthz`
- `/readyz` с проверкой SQLite readiness
- SQLite migrations, `WAL`, default `busy_timeout`
- hybrid mode: локальная stateful логика + proxy/shadow-store через upstream `responses`
- локально поддерживаемые generation fields: `reasoning`, `temperature`, `top_p`, penalties, `stop`, `max_output_tokens`

Из этого следуют два практических вывода:

1. `GET /v1/responses/{id}/input_items`, `GET /v1/conversations/{id}/items` и базовый `/readyz` больше не backlog items, а уже текущий baseline.
2. Следующий шаг для v2 это не “добавить еще один endpoint ради endpoint”, а довести совместимость surface до честного OpenAI-compatible уровня и оформить это нормальной спецификацией.

## Что сделали в текущей пачке

Последняя пачка уже закрыла несколько старых дыр, которые раньше висели в roadmap:

- добавлен `GET /v1/responses/{id}/input_items`
- добавлен `GET /v1/conversations/{id}/items` с ordering / pagination coverage в integration tests
- `/readyz` теперь реально проверяет SQLite, а не просто отвечает `200`
- `/v1/chat/completions` очищает provider-specific поля в обычном JSON и SSE потоке
- усилен bridge для custom tools и `tool_choice`: normalizing, contract tracking, fallback/retry для upstream-ов, которые принимают только `tool_choice=auto`
- улучшена canonical error mapping для response/tool-choice ошибок
- усилена SSE reconstruction / final response persistence для tool-call потоков
- добавлены тесты на store, middleware, stream proxy, chat sanitization и integration scenarios
- docs/config cleanup для публичной репы: английские комментарии в конфиге, отдельный русский README, ссылка на него из английского README

## Что делаем дальше

- [ ] - OpenAPI spec и docs для текущего surface shim ([детали](#task-openapi-docs))
- [ ] - `GET /v1/conversations/{id}` и честный read-model разговора ([детали](#task-get-conversation))
- [ ] - `POST /v1/conversations/{id}/items` и canonical append flow ([детали](#task-conversation-append))
- [ ] - `text.format` / JSON mode subset для Responses API ([детали](#task-structured-outputs))
- [ ] - response lifecycle parity: metadata, delete/cancel, retention semantics ([детали](#task-response-lifecycle))
- [ ] - streaming parity и `stream_options` ([детали](#task-streaming-parity))
- [ ] - compatibility для `/responses/compact` и `/responses/input_tokens` ([детали](#task-compaction-and-token-counting))
- [ ] - retrieval-compatible слой: vector stores + `file_search` ([детали](#task-retrieval-layer))
- [ ] - operational hardening: backend readiness, retention job, local DX ([детали](#task-ops-hardening))

## <a id="task-openapi-docs"></a>OpenAPI spec и docs для текущего surface shim

Почему это следующий шаг:

- репа собирается в публичный GitHub, но у shim до сих пор нет собственного versioned OpenAPI spec
- без spec сложно проверить, где мы реально OpenAI-compatible, а где у нас conscious subset
- backlog дальше будет только разрастаться, если не зафиксировать текущий contract

Что входит:

- `openapi/openapi.yaml` только для уже реализованных routes
- examples для `responses`, `responses/{id}`, `responses/{id}/input_items`, `conversations`, `conversations/{id}/items`, `chat/completions`, `healthz`, `readyz`
- описание error envelope и SSE streaming contract
- ссылка на spec из `README.md`

Definition of done:

- spec соответствует фактическим handlers и integration tests
- явно помечены `implemented`, `partial`, `proxy/fallback`
- нет выдуманных endpoints “на будущее”

Полезные reference:

- [Responses API](https://developers.openai.com/api/docs/api-reference/responses/create)
- [Responses streaming](https://developers.openai.com/api/docs/api-reference/responses-streaming)
- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)

## <a id="task-get-conversation"></a>`GET /v1/conversations/{id}` и честный read-model разговора

Почему это важно:

- официальный OpenAI surface включает `GET /conversations/{conversation_id}`
- сейчас у нас already есть list items, но нет верхнего conversation object read path
- без этого клиентам сложнее восстанавливать state и проверять существование conversation

Что входит:

- `GET /v1/conversations/{id}`
- стабильный conversation object shape
- нормальный `404` / validation contract
- при необходимости задел под `GET /v1/conversations/{id}/items/{item_id}`

Definition of done:

- conversation можно получить без list-items обходного пути
- response shape зафиксирован в OpenAPI spec и integration tests
- объект не течет внутренними storage-полями

Полезные reference:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)

## <a id="task-conversation-append"></a>`POST /v1/conversations/{id}/items` и canonical append flow

Почему это важно:

- официальный surface включает append path для conversation items
- это нужен не только для parity, но и для tool outputs / manual item injection / replay flows
- без append path conversation остается read-only abstraction поверх `responses`

Что входит:

- `POST /v1/conversations/{id}/items`
- canonical normalization для message, `function_call_output`, `custom_tool_call_output` и связанных item types
- append semantics без дублирования и без рассинхрона с response lineage
- задел под `DELETE /v1/conversations/{id}/items/{item_id}` как lower-priority follow-up

Definition of done:

- item append не ломает последующий `POST /v1/responses` с `conversation`
- list order и stored representation детерминированы
- integration tests закрывают manual append + follow-up response flows

Полезные reference:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Function calling](https://developers.openai.com/api/docs/guides/function-calling)

## <a id="task-structured-outputs"></a>`text.format` / JSON mode subset для Responses API

Почему это важно:

- OpenAI docs прямо указывают, что в `Responses API` structured outputs идут через `text.format`, а не через chat-style `response_format`
- это один из самых заметных пробелов в request surface по сравнению с official API
- без этого часть клиентов будет либо ломаться, либо откатываться на bespoke prompting

Что входит:

- `text.format: {type:"json_object"}` minimal JSON mode subset
- `text.format: {type:"json_schema", ...}` ограниченный subset, который мы реально можем поддерживать
- refusal / parse-failure semantics в response object
- явная ошибка, если клиент просит неподдерживаемый schema feature

Definition of done:

- happy-path examples проходят через shim и документированы в spec/README
- unsupported schema features не ломают local state silently
- streaming и non-streaming поведение согласованы

Полезные reference:

- [Structured outputs](https://developers.openai.com/api/docs/guides/structured-outputs)
- [Migrate to Responses: additional differences](https://developers.openai.com/api/docs/guides/migrate-to-responses#additional-differences)

## <a id="task-response-lifecycle"></a>Response lifecycle parity: metadata, delete/cancel, retention semantics

Почему это важно:

- response object у OpenAI богаче, чем наш текущий stored shape
- в official docs response objects по умолчанию хранятся 30 дней, а conversation items живут дольше
- пока у нас нет ясного ответа на delete/cancel/retention policy для public API surface

Что входит:

- richer response fields: `created_at`, `status`, `usage`, `error`, `incomplete_details`, `metadata`
- `DELETE /v1/responses/{id}`
- `POST /v1/responses/{id}/cancel` как endpoint под background / long-running режим, даже если сначала вернем explicit not-supported
- documented retention semantics для standalone responses vs conversation-attached items

Definition of done:

- response object не выглядит “обрезанным” для common OpenAI clients
- delete semantics понятны и покрыты тестами
- retention policy описана в README/OpenAPI и не конфликтует с storage implementation

Полезные reference:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [`/responses/{response_id}/cancel`](https://developers.openai.com/api/docs/api-reference/responses/cancel)

## <a id="task-streaming-parity"></a>Streaming parity и `stream_options`

Почему это важно:

- streaming уже есть, но это еще не full parity
- OpenAI streaming contract основан на typed semantic events, а не просто на “каких-то delta line”
- особенно критичны tool-call streams, lifecycle events и стабильная сборка stored form

Что входит:

- support `stream_options`
- event flow ближе к official `Responses` streaming API
- стабильные IDs между streamed и stored representation
- понятная политика при client disconnect, upstream error и partial tool-call stream

Definition of done:

- stream и post-stream `GET /v1/responses/{id}` не расходятся по смыслу
- tool/function/custom tool events собираются детерминированно
- есть отдельные tests на error path, interrupted stream и finalization

Полезные reference:

- [Streaming responses](https://developers.openai.com/api/docs/guides/streaming-responses)
- [Function calling: streaming](https://developers.openai.com/api/docs/guides/function-calling#streaming)
- [Structured outputs: streaming](https://developers.openai.com/api/docs/guides/structured-outputs#streaming)

## <a id="task-compaction-and-token-counting"></a>Compatibility для `/responses/compact` и `/responses/input_tokens`

Почему это важно:

- оба endpoint-а уже есть в официальном OpenAI surface
- compaction это естественное продолжение stateful shim story, а не отдельная “future fancy feature”
- token counting полезен и для DX, и для safe context management перед вызовом локального backend-а

Что входит:

- `POST /v1/responses/input_tokens`
- `POST /v1/responses/compact`
- documented policy: что считаем локально, что проксируем, что не поддерживаем
- связь compaction с `previous_response_id` / conversation flows

Definition of done:

- endpoint contracts зафиксированы в OpenAPI spec
- результат compaction пригоден для следующего request без ручной чистки
- token counting дает предсказуемый ответ хотя бы для поддерживаемого subset input items

Полезные reference:

- [Compaction](https://developers.openai.com/api/docs/guides/conversation-state#compaction)
- [Token counting](https://developers.openai.com/api/docs/guides/token-counting#api-reference)
- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)

## <a id="task-retrieval-layer"></a>Retrieval-compatible слой: vector stores + `file_search`

Почему это важно:

- если идти в retrieval, лучше делать это через OpenAI-compatible surface, а не через bespoke `/knowledge/*`
- официальный `file_search` завязан на `vector_stores`, files и annotations/citations
- это отдельный слой поверх episodic memory, а не замена conversation state

Что входит:

- минимальный roadmap для `vector_stores`
- `vector_stores/{id}/search`
- `file_search`-compatible tool contract внутри `responses`
- metadata filtering и citation shape

Definition of done:

- есть четко описанный MVP subset, а не “когда-нибудь сделаем retrieval”
- архитектурно понятно, где hosted-tool semantics эмулируем, а где честно говорим `not supported`
- storage choice для local-first (`sqlite-vec`) и later production (`pgvector`) описан заранее

Полезные reference:

- [File search](https://developers.openai.com/api/docs/guides/tools-file-search)
- [Retrieval guide](https://developers.openai.com/api/docs/guides/retrieval)

## <a id="task-ops-hardening"></a>Operational hardening: backend readiness, retention job, local DX

Почему это важно:

- `/readyz` уже проверяет SQLite, но еще не покрывает llama backend readiness
- публичная репа без нормального local DX и maintenance path быстро зарастает ручными шагами
- retention и vacuum/backup story нельзя оставлять “на потом”, если shim хранит state локально

Что входит:

- расширить `/readyz`: DB + llama backend
- retention cleanup job
- backup / restore / vacuum / optimize path
- `Makefile`, dev script, `Dockerfile`, `docker-compose` или их осознанный минимальный subset

Definition of done:

- локальный запуск и smoke path документированы
- оператору понятно, как проверить готовность и как чистить state
- maintenance story не размазана по ad-hoc shell snippets

## Более поздние milestone-пункты

Это не “делаем прямо сейчас”, но важно не потерять:

- Postgres / multi-instance mode без abstraction zoo
- auth, tenanting, quotas
- governance: redact logs, hard delete vs soft delete, optional encryption at rest
- metrics / dashboards / admin tooling
- full multimodal parity только после стабилизации core Responses/Conversations surface

## Technical debt watchlist

- local-vs-proxy decision logic не должна расползтись по handlers
- stream event shape и stored response shape не должны расходиться
- unsupported fields не должны тихо ломать local state reconstruction
- output parsing assumptions against upstream нужно закрывать тестами
- conversation append logic должна оставаться централизованной
- integration tests должны оставаться на deterministic fake backends, а не на реальных моделях
- spec-first discipline нужна до того, как surface вырастет еще на несколько endpoints
