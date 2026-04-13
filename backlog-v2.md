# Backlog / roadmap toward v2

Актуализировано по состоянию на 12 апреля 2026 на основе:

- текущего состояния репозитория, маршрутов и тестов
- текущего staged diff
- локального индекса official docs: `openapi/llms.txt`
- OpenAI Docs MCP (`developers.openai.com` / `platform.openai.com`)
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
- `POST /v1/responses/input_tokens`
- `POST /v1/responses/compact`
- `GET /v1/responses/{id}`
- `DELETE /v1/responses/{id}`
- `GET /v1/responses/{id}/input_items`
- `POST /v1/responses/{id}/cancel`
- `POST /v1/conversations`
- `GET /v1/conversations/{id}`
- `GET /v1/conversations/{id}/items`
- `POST /v1/conversations/{id}/items`
- `GET /v1/conversations/{id}/items/{item_id}`
- `DELETE /v1/conversations/{id}/items/{item_id}`
- `POST /v1/chat/completions`
- `GET /v1/chat/completions`
- `GET /v1/chat/completions/{completion_id}`
- `GET /v1/chat/completions/{completion_id}/messages`
- `POST /v1/files`
- `GET /v1/files`
- `GET /v1/files/{file_id}`
- `GET /v1/files/{file_id}/content`
- `DELETE /v1/files/{file_id}`
- `POST /v1/vector_stores`
- `GET /v1/vector_stores`
- `GET /v1/vector_stores/{vector_store_id}`
- `DELETE /v1/vector_stores/{vector_store_id}`
- `POST /v1/vector_stores/{vector_store_id}/files`
- `GET /v1/vector_stores/{vector_store_id}/files`
- `GET /v1/vector_stores/{vector_store_id}/files/{file_id}`
- `DELETE /v1/vector_stores/{vector_store_id}/files/{file_id}`
- `POST /v1/vector_stores/{vector_store_id}/search`
- `POST /v1/responses` with `stream: true` over SSE
- `GET /v1/responses/{id}?stream=true` with local SSE replay
- `/healthz`
- `/readyz` с проверкой SQLite и upstream llama backend readiness
- SQLite migrations, `WAL`, default `busy_timeout`
- local-first `responses.mode=prefer_local` по умолчанию с controlled upstream fallback
- локально поддерживаемые response-level fields уже включают lifecycle/storage surface, `text.format` subset и stateful `input_items` snapshot
- retrieve/list handlers уже принимают documented compatibility query params (`include`, `after`, `limit`, `order`, `starting_after`, `include_obfuscation`, `stream`) там, где это реализовано shim-ом

Из этого следуют два практических вывода:

1. Основные Responses/Conversations CRUD-paths уже не backlog items, а текущий baseline.
2. Следующий шаг для v2 это не “добавить еще один endpoint ради endpoint”, а дожимать semantic parity: SSE event flow, `stream_options`, hosted tools, compaction/token counting и stored chat surface.

## Что сделали в текущей пачке

Последняя пачка уже закрыла несколько старых дыр, которые раньше висели в roadmap:

- `/v1/responses` теперь имеет lifecycle surface: `GET`, `DELETE`, `POST /cancel`, `GET /input_items`
- `Response` object подтянут до docs-shaped lifecycle subset с `created_at`, `status`, `completed_at`, `error`, `usage`, `metadata`, `conversation`, `background`, `store`
- retrieve stream replay умеет multi-item replay и default obfuscation для `response.output_text.delta`
- core streaming parity для shim-owned/local replay потоков теперь включает `response.in_progress`, `response.content_part.*`, `[DONE]`, `stream_options.include_obfuscation` и synthetic replay для `message` / `function_call` / `custom_tool_call`
- `/v1/responses/{id}/input_items` хранит и возвращает effective input snapshot, а не только current-turn input
- `/v1/responses/input_tokens` и `/v1/responses/compact` заведены: token counting дает детерминированный local estimate для shim-local stateful subset, а compaction возвращает shim-owned opaque item, который можно передать в следующий local `/v1/responses` call как есть
- Conversations surface теперь включает `GET /{id}`, `POST /{id}/items`, `GET /{id}/items/{item_id}`, `DELETE /{id}/items/{item_id}`
- `POST /v1/conversations` и `POST /v1/conversations/{id}/items` синхронизированы с official limits/shape (`items`, `metadata`, batch append)
- `text.format` поддерживает `text`, `json_object` и ограниченный `json_schema` subset
- `/readyz` теперь реально проверяет SQLite и upstream llama backend, а не просто отвечает `200`
- `/v1/chat/completions` очищает provider-specific поля в обычном JSON и SSE потоке
- успешные non-streaming `POST /v1/chat/completions` с explicit `store: true`
  теперь shadow-store-ятся локально и доступны через shim-owned
  `GET /v1/chat/completions`, `GET /v1/chat/completions/{completion_id}`,
  `GET /v1/chat/completions/{completion_id}/messages`
- локальный retrieval substrate заведен на OpenAI-shaped surface:
  `POST/GET/DELETE /v1/files`, `GET /v1/files/{id}/content`,
  `POST/GET/DELETE /v1/vector_stores`,
  `POST/GET/DELETE /v1/vector_stores/{id}/files`,
  `POST /v1/vector_stores/{id}/search`
- local `vector_stores` search уже usable end-to-end без upstream storage:
  UTF-8 text files chunk-ятся и индексируются локально, search поддерживает
  attribute filters и deterministic lexical ranking
- local `/v1/responses` теперь умеет shim-owned `file_search` execution over
  local `vector_stores` в pragmatic subset:
  один `file_search` tool, local lexical retrieval, stored/streaming
  `file_search_call` output item, optional
  `include=["file_search_call.results"]`, и follow-up turns не ломаются из-за
  stored tool items в локальном generation context
- local `/v1/responses` теперь умеет dev-only shim-local `code_interpreter`
  execution в pragmatic subset:
  один `code_interpreter` tool с `container.type=auto` или explicit
  `tools[].container="cntr_*"`, optional `container.file_ids` against
  shim-owned `/v1/files`, shim-managed `/v1/containers` and
  `/v1/containers/{container_id}/files`,
  backend-gated local executor (`disabled|unsafe_host|docker`) with hardened
  Docker as the primary boundary, non-streaming/streaming create, shim-owned
  `container_id` session reuse across stored `previous_response_id` lineage
  plus same-`cntr_*` restore after transient runtime loss, optional
  `include=["code_interpreter_call.outputs"]` with logs, same-origin image
  URLs for generated image artifacts, and shim-owned generated container-file
  descriptors for non-image artifacts, local final assistant
  `container_file_citation` subset plus replayed
  `response.output_text.annotation.added` over shim-added generated-file
  appendix, stored `code_interpreter_call` output item и follow-up turns не
  ломаются из-за stored tool items в локальном generation context; for
  self-hosted safety `input_file.file_url` is now disabled by default unless
  explicitly allowlisted/unsafe-enabled, and expired shim-managed containers
  are swept in the background while keeping `status=expired` metadata
- non-text/binary attachments не маскируются под успех: local
  `vector_store.file` честно возвращает `status=failed` и documented
  `last_error`
- усилен bridge для custom tools и `tool_choice`: normalizing, contract tracking, fallback/retry для upstream-ов, которые принимают только `tool_choice=auto`
- локальные constrained custom tools для supported `grammar` / `regex` subset заведены в local tool loop
- улучшена canonical error mapping для response/tool-choice ошибок
- добавлены тесты на store, middleware, stream proxy, chat sanitization и integration scenarios
- docs/config cleanup для публичной репы: английские комментарии в конфиге, отдельный русский README, ссылка на него из английского README

## Что делаем дальше

- [x] - local-first ownership для `/v1/responses` и Codex tool loop ([детали](#task-local-first-responses))
- [x] - shim-native constrained custom tools (`grammar`, `regex`) ([детали](#task-constrained-custom-tools))
- [x] - OpenAPI spec и docs для текущего surface shim ([детали](#task-openapi-docs))
- [x] - `GET /v1/conversations/{id}` и честный read-model разговора ([детали](#task-get-conversation))
- [x] - `POST /v1/conversations/{id}/items` и canonical append flow ([детали](#task-conversation-append))
- [x] - `GET /v1/conversations/{id}/items/{item_id}` и single-item read path ([детали](#task-conversation-get-item))
- [x] - `DELETE /v1/conversations/{id}/items/{item_id}` и delete flow ([детали](#task-conversation-delete-item))
- [x] - `text.format` / JSON mode subset для Responses API ([детали](#task-structured-outputs))
- [x] - response lifecycle parity: metadata, delete/cancel, retention semantics ([детали](#task-response-lifecycle))
- [x] - core streaming parity и `stream_options` ([детали](#task-streaming-parity))
- [x] - reasoning-specific SSE replay для stored `reasoning` items ([детали](#task-streaming-replay-reasoning))
- [x] - docs-backed hosted-tool replay safety subset для stored Responses items ([детали](#task-streaming-replay-hosted-safety))
- [x] - trace-backed `web_search_call` tool-specific SSE replay for stored Responses items ([детали](#task-streaming-replay-web-search))
- [x] - trace-backed `file_search_call` tool-specific SSE replay for stored Responses items ([детали](#task-streaming-replay-file-search))
- [x] - trace-backed `code_interpreter_call` tool-specific SSE replay for stored Responses items ([детали](#task-streaming-replay-code-interpreter))
- [x] - trace-backed `computer_call` generic SSE replay for stored Responses items ([детали](#task-streaming-replay-computer))
- [x] - trace-backed `image_generation_call` pre-final SSE replay for stored Responses items ([детали](#task-streaming-replay-image-generation))
- [x] - docs-backed `mcp_approval_request` generic SSE replay for stored Responses items ([детали](#task-streaming-replay-mcp-approval-request))
- [x] - docs-backed `mcp_list_tools` generic SSE replay for stored Responses items ([детали](#task-streaming-replay-mcp-list-tools))
- [ ] - hosted/native tool-specific SSE replay beyond core shim item families ([детали](#task-streaming-replay-hosted))
- [x] - compatibility для `/responses/compact` и `/responses/input_tokens` ([детали](#task-compaction-and-token-counting))
- [x] - local retrieval substrate: files + vector stores + lexical search ([детали](#task-retrieval-substrate-local))
- [x] - retrieval-compatible local `file_search` execution inside `/v1/responses` ([детали](#task-retrieval-layer))
- [x] - dev-only local `code_interpreter` execution inside `/v1/responses` ([детали](#task-local-code-interpreter-runtime))
- [ ] - true semantic/vector retrieval backend behind local `vector_stores` ([детали](#task-retrieval-semantic-backend))
- [ ] - parity для hosted/native Responses tools (`web_search`, `computer_use`, `code_interpreter`, `image_generation`, `remote MCP`, `tool_search`) ([детали](#task-hosted-tools-parity))
- [x] - local stored Chat Completions read surface for explicit `store=true` non-streaming proxy completions ([детали](#task-chat-stored-surface-local))
- [x] - `/readyz` checks SQLite and upstream llama backend readiness ([детали](#task-ops-hardening))
- [ ] - stored Chat Completions compatibility surface ([детали](#task-chat-stored-surface))
- [ ] - operational hardening: backend readiness, retention job, local DX ([детали](#task-ops-hardening))
- [ ] - true constrained decoder/runtime для `grammar` / `regex` custom tools ([детали](#task-true-constrained-runtime))

## <a id="task-local-first-responses"></a>Local-first ownership для `/v1/responses` и Codex tool loop

Почему это следующий шаг:

- исходная идея shim была в том, что upstream сегодня часто не умеет `Responses API` как контракт, а только эмулирует его поверх chat-like backend behavior
- текущий hybrid/proxy path помогает как fallback, но уже показал, что на реальных Codex payloads быстро расползается в цепочки специальных ретраев и backend-specific несовместимостей
- пока shim сам не станет владельцем `responses`-семантики, мы будем продолжать лечить симптомы вместо того, чтобы держать один детерминированный contract

Что входит:

- добавить `responses.mode`
- переключить default на `prefer_local`
- убрать обычный proxy `/v1/responses` для всех локально-поддерживаемых кейсов
- отдельно добить shim-native tool loop для Codex-сценариев

Definition of done:

- shim сам владеет state, item history, SSE lifecycle и tool-loop semantics для поддерживаемого subset `responses`
- upstream `/v1/responses` перестает быть primary execution path и остается только как controlled fallback там, где это явно разрешено конфигом
- поведение `prefer_local` и `local_only` зафиксировано в config/docs/tests и не зависит от случайной поддержки `responses` конкретным upstream-ом
- Codex-like tool flows не требуют native `custom` support от upstream и не разваливаются на `stringified input` / `custom->bridge` цепочках

## <a id="task-constrained-custom-tools"></a>Shim-native constrained custom tools (`grammar`, `regex`)

Когда делать:

- сразу после foundation из `task-local-first-responses`
- до стабилизации OpenAPI/docs для custom tools, до финальной `streaming parity` для custom-tool path и до расширения hosted/native parity

Почему это следующий шаг:

- `grammar` custom tools нельзя честно возить через chat/function bridge: при bridge теряется сам constraint, и shim начинает принимать syntactically-wrong input как будто всё прошло успешно
- полагаться на upstream `/v1/responses` здесь бессмысленно, если upstream этого surface всё равно не умеет
- без shim-native constrained path режим `local_only` не может честно поддерживать custom tools за пределами plain-text/freeform subset

Что входит:

- отдельный local execution path для `custom` tools с `format.type=grammar` и syntax `lark|regex`
- сохранить native `custom_tool_call` semantics без деградации в `function_call` bridge
- constrained generation/validation contract внутри shim: поддерживаемый subset грамматик проходит детерминированно, unsupported subset честно возвращает `not supported`
- корректный SSE path для `response.custom_tool_call_input.delta` / `response.custom_tool_call_input.done`
- config/docs/tests для `prefer_local`, `prefer_upstream` и `local_only` на grammar custom tools

Definition of done:

- grammar custom tool в локальном path не требует upstream `/v1/responses`
- shim не делает silent fallback `custom -> bridge` для grammar/regex tools
- input у `custom_tool_call` соответствует constraint, а не просто “похоже на нужный текст”
- docs/spec явно фиксируют поддерживаемый subset и ограничения grammar support

Полезные reference:

- [Function calling: Custom tools](https://developers.openai.com/api/docs/guides/function-calling#custom-tools)
- [Function calling: Context-free grammars](https://developers.openai.com/api/docs/guides/function-calling#context-free-grammars)

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
- conversation resource shape ближе к official API: `id`, `object`, `created_at`, `metadata`
- решить, остаются ли inline `items` shim-extension полем или уезжают в отдельные item-list/item-get endpoints
- нормальный `404` / validation contract

Definition of done:

- conversation можно получить без list-items обходного пути
- response shape зафиксирован в OpenAPI spec и integration tests
- объект не течет внутренними storage-полями

Полезные reference:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)

## <a id="task-conversation-get-item"></a>`GET /v1/conversations/{id}/items/{item_id}` и single-item read path

Почему это важно:

- в official OpenAI surface есть item-level read path для conversation items
- без него клиентам приходится делать list + client-side filtering даже для точечного доступа по `item_id`
- этот endpoint нужен и для parity, и для дебага tool-heavy разговоров с длинной историей

Что входит:

- `GET /v1/conversations/{id}/items/{item_id}`
- нормальный `404`, если conversation или item не существует
- canonical item payload без утечки storage/internal полей
- согласованность single-item shape с `GET /v1/conversations/{id}/items`

Definition of done:

- точечный item read не требует list-обходного пути
- single-item и list-item payload совпадают по форме
- OpenAPI spec и integration tests фиксируют happy path и 404 contract

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

Definition of done:

- item append не ломает последующий `POST /v1/responses` с `conversation`
- list order и stored representation детерминированы
- integration tests закрывают manual append + follow-up response flows

Полезные reference:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [Function calling](https://developers.openai.com/api/docs/guides/function-calling)

## <a id="task-conversation-delete-item"></a>`DELETE /v1/conversations/{id}/items/{item_id}` и delete flow

Почему это важно:

- официальный Conversations surface включает item-level delete path
- без delete endpoint conversation state нельзя аккуратно чинить или прореживать без полной пересборки истории
- delete нужно делать вместе со стабильным append-after-delete sequencing, иначе mid-list удаление ломает детерминированный порядок items

Что входит:

- `DELETE /v1/conversations/{id}/items/{item_id}`
- возврат top-level conversation resource после удаления
- `404` для missing conversation/item и `409` при version conflict
- стабильный `seq` allocation после удаления, чтобы последующий append не конфликтовал с уже занятыми sequence numbers

Definition of done:

- удаленный item больше не виден через single-item get и list-items
- append после удаления работает детерминированно даже если удаляли элемент из середины истории
- OpenAPI spec, integration tests и store tests фиксируют delete contract

Полезные reference:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)

## <a id="task-structured-outputs"></a>`text.format` / JSON mode subset для Responses API

Статус в репе:

- локально поддерживаются `text.format.type=text`, `json_object` и ограниченный `json_schema` subset
- `json_object` следует OpenAI JSON mode guardrail и требует строку `JSON` в request context
- `json_schema` ограничен subset-ом `object|array|string|number|integer|boolean|null` с `properties`, `required`, `additionalProperties`, `items`, `enum`
- top-level `response.text` возвращается и в sync, и в stream finalization path
- invalid request config отсекается до запуска локальной генерации

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

- [x] response object не выглядит “обрезанным” для common OpenAI clients
- [x] delete semantics понятны и покрыты тестами
- [x] retention policy описана в README/OpenAPI и не конфликтует с storage implementation

Статус:

- локальный `Response` теперь несет `created_at`, `status`, `completed_at`, `error`, `incomplete_details`, `usage`, `metadata`, `background`, `store`
- добавлены `DELETE /v1/responses/{id}` и `POST /v1/responses/{id}/cancel`
- shadow-stored upstream responses сохраняют canonical `response_json`, поэтому retrieve/cancel возвращают lifecycle-поля без shim-specific деградации
- retention semantics для standalone responses и conversation-attached items зафиксированы в OpenAPI

Полезные reference:

- [Conversation state](https://developers.openai.com/api/docs/guides/conversation-state)
- [`/responses/{response_id}/cancel`](https://developers.openai.com/api/docs/api-reference/responses/cancel)

## <a id="task-streaming-parity"></a>Core streaming parity и `stream_options`

Почему это важно:

- streaming уже есть, но это еще не full parity
- OpenAI streaming contract основан на typed semantic events, а не просто на “каких-то delta line”
- особенно критичны tool-call streams, lifecycle events и стабильная сборка stored form

Что входит:

- support `stream_options`
- event flow ближе к official `Responses` streaming API
- стабильные IDs между streamed и stored representation
- понятная политика при client disconnect, upstream error и partial tool-call stream
- docs-shaped replay для shim-supported output item types (`message`, `function_call`, `custom_tool_call`)

Definition of done:

- stream и post-stream `GET /v1/responses/{id}` не расходятся по смыслу
- tool/function/custom tool events собираются детерминированно
- retrieve replay не теряет event-level semantics для supported output item types
- есть отдельные tests на error path, interrupted stream и finalization

Статус:

- закрыто для shim-owned/local replay core flow
- residual по reasoning/hosted-tool specific SSE вынесен в отдельный backlog item ниже

Полезные reference:

- [Streaming responses](https://developers.openai.com/api/docs/guides/streaming-responses)
- [Function calling: streaming](https://developers.openai.com/api/docs/guides/function-calling#streaming)
- [Structured outputs: streaming](https://developers.openai.com/api/docs/guides/structured-outputs#streaming)

## <a id="task-streaming-replay-reasoning"></a>Reasoning-specific SSE replay для stored `reasoning` items

Почему это отдельно:

- core text/function/custom replay уже закрыт и не должен больше маскироваться одним большим open-item
- reasoning artifacts уже реально попадают в stored output, поэтому synthetic replay не должен деградировать их до одних только generic `output_item.*`

Что входит:

- reasoning-specific replay events, если stored response содержит reasoning artifacts

Definition of done:

- retrieve replay отдает `response.reasoning_text.*` для stored `reasoning` items вместо только generic `output_item.*`
- backlog и OpenAPI не обещают больше, чем реально поддержано

Статус:

- закрыто для shim-stored `reasoning` items с `reasoning_text` content parts
- docs-backed hosted-tool replay safety subset закрыт отдельно
- residual hosted/native tool-specific SSE parity вынесен ниже как
  отдельный open item

## <a id="task-streaming-replay-hosted-safety"></a>Docs-backed hosted-tool replay safety subset для stored Responses items

Почему это отдельно:

- это уже не тот же самый scope, что true tool-specific SSE parity
- по docs можно уверенно подтвердить hosted/native output item families, но
  не всегда их отдельные replay event families
- backlog должен показывать уже закрытый safety/proxy scope отдельно от
  оставшегося parity scope

Что входит:

- stored `mcp_call` и legacy stored `mcp_tool_call` retrieve replay с
  docs-backed `response.mcp_call_arguments.*`
- terminal `response.mcp_call.in_progress` и `response.mcp_call.failed` для
  stored MCP items
- conservative synthetic replay для stored `web_search_call`,
  `file_search_call`, `code_interpreter_call`, чтобы
  `response.output_item.added` не светил финальные `action`, `results` /
  `search_results` и `outputs` до `response.output_item.done`
- тот же non-leaking behavior для completed-only upstream normalization

Definition of done:

- stored replay и completed-only normalization не светят финальный hosted tool
  payload раньше terminal event-а
- MCP subset воспроизводится через docs-backed event names без догадок
- OpenAPI, tests и backlog wording не overclaim-ят true hosted tool-specific
  SSE parity

Статус на 10 апреля 2026:

- закрыто для stored `mcp_call` и legacy stored `mcp_tool_call` items:
  retrieve replay отдает `response.mcp_call_arguments.*`,
  `response.mcp_call.in_progress`, а для failed items еще и
  `response.mcp_call.failed`
- закрыто для stored `web_search_call`, `file_search_call`,
  `code_interpreter_call` safety subset: synthetic
  `response.output_item.added` больше не светит финальные `action`,
  `results` / `search_results` и `outputs` до `response.output_item.done`

## <a id="task-streaming-replay-web-search"></a>Trace-backed `web_search_call` tool-specific SSE replay for stored Responses items

Почему это отдельно:

- это уже больше, чем safety subset, но еще не весь hosted/native parity item
- для `search`, `open_page` и `find_in_page` теперь есть реальные upstream
  SSE traces, так что event family можно воспроизводить без догадок

Что входит:

- stored retrieve replay для `web_search_call` с final
  `action.type == search`, `open_page`, `find_in_page`
- completed-only upstream normalization для тех же случаев
- порядок synthetic events, совпадающий с captured upstream flow:
  `response.output_item.added` ->
  `response.web_search_call.in_progress` ->
  `response.web_search_call.searching` ->
  `response.web_search_call.completed` ->
  `response.output_item.done`
- `response.output_item.added` по-прежнему не светит финальный `action`
  раньше terminal events

Статус на 11 апреля 2026:

- закрыто для stored `web_search_call` с `action.type == search`,
  `open_page`, `find_in_page`:
  retrieve replay и completed-only normalization теперь отдают
  `response.web_search_call.in_progress`,
  `response.web_search_call.searching`,
  `response.web_search_call.completed`
- реализация завязана на live upstream trace в
  `internal/httpapi/testdata/upstream/web_search_call*.raw.sse` и parsed
  fixtures рядом
- coverage есть и на stored retrieve replay, и на completed-only proxy branch

Definition of done:

- stored `web_search_call` replay не деградирует до pure generic
  `response.output_item.*`
- replay sequence совпадает с реально снятым upstream order в пределах
  synthetic retrieve constraints
- backlog/OpenAPI wording не overclaim-ят parity для других hosted/native
  tool families

## <a id="task-streaming-replay-file-search"></a>Trace-backed `file_search_call` tool-specific SSE replay for stored Responses items

Почему это отдельно:

- official docs подтверждают `file_search_call` output item и `include=["file_search_call.results"]`, но exact SSE family через docs tooling отдельно не расписан
- real upstream traces нужны не только для event names, но и для того, чтобы не протечь финальными `queries` / `results` раньше `response.output_item.done`

Что входит:

- stored retrieve replay для `file_search_call` с default `results: null`
- stored retrieve replay для `file_search_call` с `include=["file_search_call.results"]`
- completed-only upstream normalization для тех же случаев
- порядок synthetic events, совпадающий с captured upstream flow:
  `response.output_item.added` ->
  `response.file_search_call.in_progress` ->
  `response.file_search_call.searching` ->
  `response.file_search_call.completed` ->
  `response.output_item.done`
- `response.output_item.added` не светит финальные `queries`,
  `results` и compatibility alias `search_results` до terminal event

Статус на 11 апреля 2026:

- закрыто для stored `file_search_call`:
  retrieve replay и completed-only normalization теперь отдают
  `response.file_search_call.in_progress`,
  `response.file_search_call.searching`,
  `response.file_search_call.completed`
- live upstream traces лежат в
  `internal/httpapi/testdata/upstream/file_search_call*.raw.sse` и parsed
  fixtures рядом
- shim сохраняет compatibility с уже хранимым `search_results` alias, хотя
  upstream trace-backed path использует canonical `results`
- coverage есть и на stored retrieve replay, и на completed-only proxy branch

Definition of done:

- stored `file_search_call` replay не деградирует до pure generic
  `response.output_item.*`
- replay sequence совпадает с реально снятым upstream order в пределах
  synthetic retrieve constraints
- backlog/OpenAPI wording не overclaim-ят parity для remaining hosted/native
  tool families

## <a id="task-streaming-replay-code-interpreter"></a>Trace-backed `code_interpreter_call` tool-specific SSE replay for stored Responses items

Почему это отдельно:

- official docs подтверждают `code_interpreter_call` как output item family и
  container-backed hosted tool, но exact SSE payload order через docs tooling
  отдельно не расписан
- real upstream traces нужны не только для event names, но и для того, чтобы
  не протечь финальными `code` / `outputs` раньше `response.output_item.done`

Что входит:

- stored retrieve replay для `code_interpreter_call` с canonical `code` field
- stored retrieve replay для `code_interpreter_call` с `outputs: null`
  и с `include=["code_interpreter_call.outputs"]` trace, где `outputs`
  приходят как список
- completed-only upstream normalization для тех же случаев
- порядок synthetic events, совпадающий с captured upstream flow:
  `response.output_item.added` ->
  `response.code_interpreter_call.in_progress` ->
  `response.code_interpreter_call_code.delta` ->
  `response.code_interpreter_call_code.done` ->
  `response.code_interpreter_call.interpreting` ->
  `response.code_interpreter_call.completed` ->
  `response.output_item.done`
- `response.output_item.added` держит `code` пустым и оставляет только
  placeholder для `outputs` (`[]` или `null`) до terminal event

Статус на 11 апреля 2026:

- закрыто для stored `code_interpreter_call`:
  retrieve replay и completed-only normalization теперь отдают
  `response.code_interpreter_call.in_progress`,
  `response.code_interpreter_call_code.delta`,
  `response.code_interpreter_call_code.done`,
  `response.code_interpreter_call.interpreting`,
  `response.code_interpreter_call.completed`
- live upstream traces лежат в
  `internal/httpapi/testdata/upstream/code_interpreter_call*.raw.sse` и
  parsed fixtures рядом
- coverage есть и на stored retrieve replay, и на completed-only proxy branch,
  включая оба trace-backed варианта `outputs`: список и `null`

Definition of done:

- stored `code_interpreter_call` replay не деградирует до pure generic
  `response.output_item.*`
- replay sequence совпадает с реально снятым upstream order в пределах
  synthetic retrieve constraints
- backlog/OpenAPI wording не overclaim-ят parity для remaining hosted/native
  tool families

## <a id="task-streaming-replay-computer"></a>Trace-backed `computer_call` generic SSE replay for stored Responses items

Почему это отдельно:

- official docs подтверждают built-in `computer` tool и request/response loop
  через `computer_call` и `computer_call_output`, но не описывают отдельную
  `response.computer_call.*` SSE family в Responses streaming reference
- real upstream traces нужны, чтобы не выдумать unsupported events и не
  протечь финальными `actions[]` раньше `response.output_item.done`

Что входит:

- stored retrieve replay для screenshot-first turn и для follow-up turn с
  `computer_call_output`
- completed-only upstream normalization для тех же случаев
- generic synthetic sequence, совпадающая с captured upstream flow:
  `response.created` ->
  `response.output_item.added` ->
  `response.output_item.done` ->
  `response.completed`
- synthetic `response.output_item.added` для `computer_call` omits final
  `actions[]` until `response.output_item.done`

Статус на 12 апреля 2026:

- закрыто для stored `computer_call`: retrieve replay и completed-only
  normalization теперь воспроизводят generic `response.output_item.*`
  sequence без synthetic `response.computer_call.*`
- live upstream traces лежат в
  `internal/httpapi/testdata/upstream/computer_call*.raw.sse` и parsed
  fixtures рядом
- captured upstream flow для обоих traces содержит только
  `response.created`, `response.in_progress`, `response.output_item.added`,
  `response.output_item.done`, `response.completed`
- coverage есть и на stored retrieve replay, и на completed-only proxy branch

Definition of done:

- synthetic `response.output_item.added` не протекает финальными `actions[]`
- shim не invent-ит unsupported `response.computer_call.*` events
- backlog/OpenAPI wording честно фиксируют, что текущая parity для
  `computer_call` generic-only по trace

## <a id="task-streaming-replay-image-generation"></a>Trace-backed `image_generation_call` pre-final SSE replay for stored Responses items

Почему это отдельно:

- official image generation docs явно описывают `image_generation_call` как
  Responses output item with base64 `result`, `revised_prompt`, and optional
  `action`
- those same docs also describe a dedicated streaming event
  `response.image_generation_call.partial_image`, but the stored Response
  object does not retain the intermediate partial image bytes needed to replay
  that event faithfully
- live upstream captures now confirm dedicated
  `response.image_generation_call.in_progress` and
  `response.image_generation_call.generating` events before the terminal item,
  but they still do not make the intermediate partial image bytes recoverable
  from a stored Response object
- official Responses streaming reference now also documents
  `response.image_generation_call.completed`, so stored replay can safely
  synthesize the pre-final lifecycle around the final stored item while
  leaving `partial_image` explicitly open

Что входит:

- stored retrieve replay для documented `image_generation_call` item shape
- completed-only upstream normalization для того же item family
- trace-backed synthetic sequence через `response.output_item.added`,
  `response.image_generation_call.in_progress`,
  `response.image_generation_call.generating`,
  `response.image_generation_call.completed`, and
  `response.output_item.done`
- synthetic `response.output_item.added` follows current upstream captures and
  is reduced to the minimal in-progress item shape instead of exposing stable
  image metadata too early

Статус на 12 апреля 2026:

- закрыто для stored `image_generation_call`: retrieve replay и
  completed-only normalization теперь synthesize
  `response.image_generation_call.in_progress`,
  `response.image_generation_call.generating`,
  `response.image_generation_call.completed`, and terminal
  `response.output_item.done`
- docs source: image generation guide and Responses streaming reference
  explicitly document `image_generation_call` result shape plus
  `response.image_generation_call.in_progress`,
  `response.image_generation_call.generating`,
  `response.image_generation_call.completed`, and
  `response.image_generation_call.partial_image`
- shim intentionally keeps `response.image_generation_call.partial_image`
  open for stored replay because the stored Response object does not retain the
  intermediate `partial_image_b64` payloads needed for faithful replay
- explicit follow-up for full closure: persist irrecoverable
  `response.image_generation_call.partial_image` artifacts during create-time
  streaming, then attach them to stored responses so retrieve replay can emit
  the original `partial_image_b64` sequence without synthesizing bytes
- coverage есть и на stored retrieve replay, и на completed-only proxy branch

Definition of done:

- stored `image_generation_call` больше не протекает final `result`,
  `revised_prompt`, `action`, or even stable image metadata in synthetic
  `response.output_item.added`
- stored replay now emits the docs-backed pre-final lifecycle through
  `response.image_generation_call.in_progress`,
  `response.image_generation_call.generating`, and
  `response.image_generation_call.completed`
- final stored item shape is still preserved in `response.output_item.done`
- backlog/OpenAPI wording честно фиксируют, что
  `response.image_generation_call.partial_image` remains explicitly open for
  stored replay

## <a id="task-streaming-replay-mcp-approval-request"></a>Docs-backed `mcp_approval_request` generic SSE replay for stored Responses items

Почему это отдельно:

- official MCP docs явно описывают `mcp_approval_request` как Responses
  output item со shape `id`, `type`, `arguments`, `name`, `server_label`
- при этом docs не описывают отдельную
  `response.mcp_approval_request.*` SSE family, поэтому replay нельзя
  закрывать через invented tool-specific events

Что входит:

- stored retrieve replay для documented `mcp_approval_request` item shape
- completed-only upstream normalization для того же item family
- generic synthetic sequence через `response.output_item.added` и
  `response.output_item.done`
- synthetic replay сохраняет documented item shape и не добавляет
  synthetic `status`, если его нет в stored item

Статус на 12 апреля 2026:

- закрыто для stored `mcp_approval_request`: retrieve replay и completed-only
  normalization теперь synthesize generic `response.output_item.*`
- docs source: MCP and Connectors approvals section now explicitly documents
  `mcp_approval_request` output items and follow-up `mcp_approval_response`
- shim intentionally does not invent unsupported
  `response.mcp_approval_request.*` events
- coverage есть и на stored retrieve replay, и на completed-only proxy branch

Definition of done:

- stored `mcp_approval_request` больше не теряется при synthetic retrieve
  replay
- synthetic replay не invent-ит dedicated SSE family без docs/trace support
- backlog/OpenAPI wording честно фиксируют docs-backed generic-only scope

## <a id="task-streaming-replay-mcp-list-tools"></a>Docs-backed `mcp_list_tools` generic SSE replay for stored Responses items

Почему это отдельно:

- official MCP docs явно описывают `mcp_list_tools` как Responses output item,
  включая `server_label` и imported `tools` list
- при этом public Responses docs не описывают отдельную
  `response.mcp_list_tools.*` SSE family для stored/retrieve replay, поэтому
  shim не должен invent-ить tool-specific events без trace support

Что входит:

- stored retrieve replay для documented `mcp_list_tools` item shape
- completed-only upstream normalization для того же item family
- generic synthetic sequence через `response.output_item.added` и
  `response.output_item.done`
- synthetic replay preserves documented `tools` list и не добавляет
  synthetic `status`, если его нет в stored item

Статус на 12 апреля 2026:

- закрыто для stored `mcp_list_tools`: retrieve replay и completed-only
  normalization теперь synthesize generic `response.output_item.*`
- docs source: MCP and Connectors guide explicitly documents
  `mcp_list_tools` output items as part of Responses API flow
- shim intentionally does not invent unsupported
  `response.mcp_list_tools.*` events for stored replay without trace support
- coverage есть и на stored retrieve replay, и на completed-only proxy branch

Definition of done:

- stored `mcp_list_tools` больше не теряется при synthetic retrieve replay
- synthetic replay не invent-ит dedicated SSE family без docs/trace support
- backlog/OpenAPI wording честно фиксируют docs-backed generic-only scope

## <a id="task-streaming-replay-hosted"></a>Hosted/native tool-specific SSE replay beyond core shim item families

Почему это отдельно:

- для hosted/native tools official docs сейчас явно перечисляют event families, но exact payload schema через docs tooling доступна фрагментарно
- без source event log легко “додумать” synthetic payload неправильно и начать overclaim-ить совместимость

Статус на 12 апреля 2026:

- docs-backed MCP replay и hosted replay safety subset вынесены в закрытый
  item выше
- trace-backed replay для stored `web_search_call` и `file_search_call`
  вынесен в закрытые item выше
- trace-backed replay для stored `code_interpreter_call` вынесен в закрытый
  item выше
- trace-backed replay для stored `computer_call` вынесен в закрытый item
  выше; captured upstream flow для него generic-only и не содержит
  `response.computer_call.*`
- trace-backed pre-final replay для stored `image_generation_call` вынесен в
  закрытый item выше; dedicated stored replay for
  `response.image_generation_call.partial_image` is not claimed without a
  recoverable partial-image policy
- docs-backed generic replay для stored `mcp_approval_request` вынесен в
  закрытый item выше; dedicated `response.mcp_approval_request.*` family не
  заявляется без trace/reference support
- docs-backed generic replay для stored `mcp_list_tools` вынесен в закрытый
  item выше; dedicated `response.mcp_list_tools.*` family не заявляется без
  trace/reference support
- для remaining hosted/native families без live source event log replay все
  еще может деградировать до generic `response.output_item.*`

Что осталось:

- tool-specific retrieve replay только для remaining hosted/native families,
  где upstream действительно exposes dedicated SSE families, и для item
  families, которые еще не закрыты отдельным docs-backed или trace-backed
  item
- максимальное сближение synthetic replay с реальным upstream event log там,
  где сам stored object не хранит исходные deltas

Definition of done:

- retrieve replay не деградирует remaining hosted/native tool outputs до
  generic `output_item.*` только потому, что stream synthetic
- residual event families либо реально воспроизводятся, либо явно исключены
  из supported shim surface

## <a id="task-compaction-and-token-counting"></a>Compatibility для `/responses/compact` и `/responses/input_tokens`

Статус на 10 апреля 2026:

- endpoint-ы заведены в public surface
- локальный subset честно задокументирован в OpenAPI и tests
- `/responses/input_tokens` локально считает детерминированный estimate по effective local context snapshot
- `/responses/compact` локально возвращает shim-owned opaque compaction item, который shim умеет развернуть в synthetic summary message на следующем local `/responses` call
- exact backend-native tokenization и OpenAI-equivalent encrypted compaction state не заявляются как поддержанные в shim-local path

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

## <a id="task-retrieval-substrate-local"></a>Local retrieval substrate: files + vector stores + lexical search

Что уже закрыто:

- shim-owned file substrate:
  `POST /v1/files`, `GET /v1/files`, `GET /v1/files/{id}`,
  `GET /v1/files/{id}/content`, `DELETE /v1/files/{id}`
- local `vector_stores` CRUD subset:
  `POST /v1/vector_stores`, `GET /v1/vector_stores`,
  `GET /v1/vector_stores/{id}`, `DELETE /v1/vector_stores/{id}`
- local `vector_store.file` subset:
  `POST /v1/vector_stores/{id}/files`,
  `GET /v1/vector_stores/{id}/files`,
  `GET /v1/vector_stores/{id}/files/{file_id}`,
  `DELETE /v1/vector_stores/{id}/files/{file_id}`
- `POST /v1/vector_stores/{id}/search`
- current search semantics are explicit and docs-consistent for a pragmatic MVP:
  deterministic lexical chunk search over valid UTF-8 text content with
  attribute filtering and score-threshold filtering
- binary/non-text files are surfaced as failed `vector_store.file` attachments,
  not silently treated as searchable

Definition of done:

- local file/vector-store/search surface is usable end-to-end without upstream
  OpenAI storage
- OpenAPI/backlog wording clearly labels this as local retrieval-compatible
  subset, not hosted semantic-search parity
- tests cover text happy path and failed binary indexing path

## <a id="task-retrieval-layer"></a>Retrieval-compatible слой: vector stores + `file_search`

Почему это важно:

- если идти в retrieval, лучше делать это через OpenAI-compatible surface, а не через bespoke `/knowledge/*`
- официальный `file_search` завязан на `vector_stores`, files и annotations/citations
- это отдельный слой поверх episodic memory, а не замена conversation state

Что закрыто в pragmatic subset:

- local retrieval substrate подключен к `file_search` execution path внутри
  `/v1/responses`
- shim-local path поддерживает один `file_search` tool с
  `vector_store_ids`, `filters`, `max_num_results`,
  `ranking_options.score_threshold` и compatibility validation для
  ranker/tool_choice subset
- non-streaming и streaming local `/v1/responses` requests возвращают
  `file_search_call` + assistant `message`, а streaming replay использует уже
  существующую tool-specific SSE family
- `include=["file_search_call.results"]` теперь реально меняет stored/local
  response payload, а не принимается как no-op
- follow-up local turns по `previous_response_id` после stored
  `file_search_call` не ломаются из-за tool items в generation context

Что осталось открытым:

- hosted citations/annotations parity: local subset не синтезирует
  OpenAI-shaped `file_citation` annotations в final assistant `message`
- hosted ranking parity: локальный path делает deterministic lexical search и
  возвращает normalized snippets, а не managed semantic ranking/embedding
  results OpenAI
- расширить beyond UTF-8 text MVP там, где это реально нужно, не притворяясь
  hosted embeddings parity
- отдельно решить, нужен ли позже semantic ranking/embeddings backend behind
  this contract, или lexical MVP достаточно для `prefer_local`

Definition of done:

- `file_search` tool contract inside `/v1/responses` реально исполняется на
  local retrieval substrate
- backlog/OpenAPI wording честно отличают retrieval-compatible local execution
  от hosted OpenAI semantic-search parity
- архитектурно понятно, где hosted-tool semantics эмулируем, а где честно
  говорим `not supported`

Полезные reference:

- [File search](https://developers.openai.com/api/docs/guides/tools-file-search)
- [Retrieval guide](https://developers.openai.com/api/docs/guides/retrieval)

## <a id="task-retrieval-semantic-backend"></a>True semantic/vector retrieval backend behind local `vector_stores`

Почему это отдельный task, а не хвост текущего:

- текущий local retrieval layer уже полезен и закрывает shim-owned contract
  path для `files`, `vector_stores` и local `file_search`
- переход от lexical MVP к настоящему semantic/vector backend меняет не только
  scoring, но и ingestion/indexing/runtime architecture
- это уже не “дожать пару полей”, а отдельная backend-capability milestone

Что должно появиться:

- настоящая embedding/vector indexing pipeline за local `vector_stores`
- retrieval path, который ищет по embeddings similarity, а не только по
  tokenized lexical chunks
- optional reranking layer поверх dense/sparse retrieval, если она окупается
- migration path, при котором внешний OpenAI-shaped surface не меняется:
  `files`, `vector_stores`, `vector_store.search`, `file_search`
  остаются теми же, меняется только engine под ними

Что уже зафиксировано как сознательное ограничение текущего state:

- local `vector_stores` сейчас contract-shaped, но не являются настоящим
  managed vector database
- local `file_search` использует deterministic lexical retrieval over
  normalized UTF-8 chunks
- final assistant `message` не претендует на hosted `file_citation` parity

Definition of done:

- local `vector_stores` используют реальный semantic/vector search backend
- backlog/OpenAPI wording можно ужесточить с “lexical MVP” до
  “semantic retrieval subset” без overclaim про full OpenAI hosted parity
- migration не ломает уже существующий external shim contract

Практический decomposition, когда вернемся:

- phase 1: embeddings generation + local chunk/index schema
- phase 2: search path switch from lexical-only to dense or hybrid retrieval
- phase 3: optional reranking and better result shaping for `file_search_call`
- phase 4: revisit citations/annotations parity for final assistant messages

## <a id="task-local-code-interpreter-runtime"></a>Dev-only local `code_interpreter` execution inside `/v1/responses`

Почему это отдельный pragmatic subset:

- trace-backed replay для stored `code_interpreter_call` уже был закрыт, но это
  не делало shim реально usable как local runtime
- hosted Code Interpreter у OpenAI это sandboxed container/VM tool, и притворяться
  ему равным через silent host exec на сервере было бы небезопасно и нечестно
- полезный следующий шаг это explicit opt-in local subset, а не overclaim про
  hosted parity
- docs rechecked on April 13, 2026 against the official Code Interpreter guide
  and `/v1/responses` reference before closing this subset

Что закрыто в pragmatic subset:

- local `/v1/responses` path теперь умеет ровно один `tools[]` entry с
  `type=code_interpreter`, `container.type=auto` или explicit
  `tools[].container = "cntr_*"`, и optional `container.file_ids`
- local execution теперь включается через explicit backend gate
  `responses.code_interpreter.backend=unsafe_host|docker`
- legacy `responses.code_interpreter.enable_unsafe_host_executor=true`
  остаётся compatibility alias для `backend=unsafe_host`, чтобы не ломать
  существующие dev-конфиги
- при включенном gate shim делает двухшаговый flow:
  planner JSON -> local sandbox/backend execution -> final assistant answer
- non-streaming и streaming local create возвращают stored
  `code_interpreter_call` item и используют уже существующий trace-backed SSE
  replay family
- поддержан `include=["code_interpreter_call.outputs"]` для logs output
- follow-up local turns по `previous_response_id` после stored
  `code_interpreter_call` не ломаются из-за tool items в generation context
- для `backend=docker` execution больше не идет напрямую на host:
  shim запускает и переиспользует жестко ограниченный session container
  (`network=none`, `read_only`, tmpfs workspace, non-root,
  `cap_drop=ALL`, `no-new-privileges`, memory/cpu/pids limits)
- `container.type=auto` теперь reuse-ит активный shim-owned session из
  последнего stored `code_interpreter_call` в lineage того же backend
- explicit `/v1/containers` subset теперь реализован поверх того же
  shim-owned session store:
  `POST/GET/LIST/DELETE /v1/containers`,
  `POST/GET/LIST/DELETE /v1/containers/{container_id}/files`,
  `GET /v1/containers/{container_id}/files/{file_id}/content`
- `container.type=auto` и explicit `cntr_*` mode теперь умеют восстановить
  тот же shim-owned container после transient runtime loss:
  hardened Docker session пересоздается, persisted container files
  restage-ятся, а `container_id` не меняется
- `container.file_ids` теперь поддержан для shim-owned `/v1/files`:
  перед execution файлы stage-ятся в текущий session workspace под
  sanitized filenames, planner видит доступные filenames и может читать их
  через guarded workspace-relative `open()`
- current-turn `input_file` model content parts теперь автоматически
  stage-ятся в local sandbox workspace для pragmatic subset:
  поддержаны `input_file.file_id`, inline `input_file.file_data`
  (`filename` required) и HTTP(S) `input_file.file_url`
  (server-side fetch with a local 50 MiB cap), так что shim-local
  `code_interpreter` может читать model-uploaded files без отдельного
  `container.file_ids`
- `input_file.file_url` fetches теперь честно gated для self-hosted shim:
  по умолчанию они выключены, а для opt-in subset нужен либо
  `responses.code_interpreter.input_file_url_policy=allowlist` c exact-host /
  wildcard-suffix allowlist, либо явный
  `unsafe_allow_http_https`
- generated file artifacts теперь сохраняются как bounded shim-owned
  `/v1/files`, зеркалятся в shim-owned container files, и попадают в local
  `code_interpreter_call.outputs` как same-origin `type=image` URLs для
  generated image artifacts и как shim-local `type=file` descriptors с
  `file_id` (`cfile_*`), `filename`, `bytes`, `backing_file_id` для других
  generated files; final assistant turn видит этот список в local generation
  context
- local final assistant message теперь получает pragmatic
  `container_file_citation` subset: shim добавляет короткий
  `Generated files:` appendix и annotates filenames с
  `container_id`, `file_id` (`cfile_*`), `filename`, `start_index`,
  `end_index`
- stored/local streaming replay теперь synthesize-ит generic
  `response.output_text.annotation.added` events для final assistant
  annotations, включая shim-local `container_file_citation` annotations над
  generated-file appendix
- появился background cleanup sweep для expired shim-managed containers:
  session runtime уничтожается, local container-file access убирается,
  metadata snapshot остаётся видимым как `status=expired`
- runtime execution failures в shim-local subset теперь не валятся только в
  transport/request error path: create и retrieve/stream replay возвращают
  stored `response.status=failed` с top-level `response.error.code/message`,
  failed `code_interpreter_call` output item и terminal `response.failed`
  event; это pragmatic shim-local subset, а не claim про exact hosted
  failure parity

Что осталось открытым:

- это не hosted Code Interpreter parity; по умолчанию backend выключен
- `unsafe_host` остаётся явно небезопасным fallback/dev path и не должен
  считаться production-grade boundary
- нет полного hosted container/file/artifact parity:
  richer hosted container lifecycle (`skills`, `network_policy`,
  exact hosted status/failure surface) и exact non-image
  `code_interpreter_call.outputs` semantics
- нет hosted citation placement parity:
  annotations теперь replay-ятся отдельными
  `response.output_text.annotation.added` events, но для local generated files
  они всё ещё висят на shim-added `Generated files:` appendix, а не на
  hosted model-chosen spans
- нет hosted failure/artifact semantics parity beyond logs, local image URLs,
  и local generated file subset
- stronger isolation backends (`gVisor`, microVM) не заведены; текущий
  production-minded шаг это hardened Docker, а не VM parity
- нет full hosted expiration/cleanup parity:
  local subset теперь sweep-ит expired containers в фоне и хранит shim
  snapshot metadata, но не воспроизводит весь hosted lifecycle/retention
  surface и не очищает mirrored backing `/v1/files` до hosted-grade parity

Definition of done:

- local shim может реально исполнить базовый `code_interpreter` request внутри
  `/v1/responses` без upstream hosted runtime
- config/OpenAPI/backlog явно фиксируют security boundary:
  explicit backend selection, hardened Docker subset, disabled by default
- stored replay, integration tests и follow-up semantics не расходятся с новым
  runtime path

## <a id="task-hosted-tools-parity"></a>Parity для hosted/native Responses tools

Почему это важно:

- по официальным OpenAI docs Responses API это не только `message` и `function_call`, а полноценный agentic surface с built-in tools и typed items
- без отдельного плана по hosted/native tools shim легко “застрянет” в text + function subset и будет выглядеть совместимым только частично
- часть этой поверхности уже пересекается с retrieval, но `web_search`, `computer_use`, `code_interpreter`, `image_generation`, `remote MCP` и `tool_search` требуют отдельной архитектурной рамки

Что входит:

- описать поддерживаемый MVP subset для `web_search`, `computer_use`, `code_interpreter`, `image_generation` и `remote MCP`
- current state уже включает pragmatic local subsets для `file_search` и
  dev-only unsafe-host `code_interpreter`; remaining work здесь это не
  “впервые сделать tool”, а довести boundaries/runtime parity осознанно
- решить по каждому tool type, где shim эмулирует hosted semantics локально, а где честно возвращает `not supported`
- добавить `tool_search`-совместимый контракт и output item types `tool_search_call` / `tool_search_output`
- зафиксировать parity по reasoning items / reasoning summaries для tool-heavy flows, где это влияет на качество follow-up шагов
- описать границы между local-first shim tools, passthrough/proxy режимом и controlled fallback policy

Definition of done:

- в backlog/spec явно перечислено, какие response-native tools shim поддерживает, какие проксирует, а какие пока не реализует
- tool/item surface не ограничивается только `message`, `function_call` и `function_call_output`
- hosted/native tools не “просачиваются” в код через ad-hoc special cases, а идут через осознанную модель item/tool semantics
- docs/config/tests позволяют проверить поведение `prefer_local`, `prefer_upstream` и `local_only` для каждого поддерживаемого tool family

Полезные reference:

- [Migrate to Responses: About the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses#about-the-responses-api)
- [Migrate to Responses: Responses benefits](https://developers.openai.com/api/docs/guides/migrate-to-responses#responses-benefits)
- [Migrate to Responses: Messages vs. Items](https://developers.openai.com/api/docs/guides/migrate-to-responses#messages-vs-items)
- [Hosted tool search](https://developers.openai.com/api/docs/guides/tools-tool-search#hosted-tool-search)

## <a id="task-chat-stored-surface-local"></a>Local stored Chat Completions read surface for explicit `store=true` non-streaming proxy completions

Почему это отдельно:

- official OpenAI surface у `chat/completions` включает stored-resource routes,
  но полный parity здесь сильно шире, чем просто “добавить три GET handler-а”
- прагматичный следующий шаг это не эмулировать весь upstream history, а дать
  честный local read model для тех Chat Completions, которые реально прошли
  через shim и были explicitly stored
- такой partial ownership уже полезен клиентам и не требует выдумывать
  account-level default storage semantics или reconstruction из streamed chunks

Что входит:

- local shadow-store только для успешных non-streaming JSON
  `POST /v1/chat/completions` c explicit `store: true`
- `GET /v1/chat/completions` с filters/pagination subset:
  `model`, `metadata[key]=value`, `after`, `limit`, `order`
- `GET /v1/chat/completions/{completion_id}`
- `GET /v1/chat/completions/{completion_id}/messages`, где message list
  реконструируется из исходного request `messages[]`
- OpenAPI/docs wording, которая прямо фиксирует границы этого local subset

Статус на 12 апреля 2026:

- закрыто для local shim-owned subset:
  successful non-streaming explicit `store: true` chat completions now land in
  SQLite and are readable through list/get/messages handlers
- `messages` read surface возвращает reconstructed request messages with stable
  synthetic ids when the original message object had no `id`
- filtering/pagination покрыты integration tests и store tests
- OpenAPI wording explicitly says, что это local shadow-store subset, а не
  full official stored-chat parity

Definition of done:

- local list/get/messages contract реализован и покрыт integration tests
- OpenAPI/backlog не overclaim-ят upstream-owned history, default storage при
  omitted `store`, или streamed-chat reconstruction
- `go test ./...` проходит на этом scope

Полезные reference:

- [List stored Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/list)
- [Retrieve a stored Chat Completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/retrieve)
- [List messages for a stored Chat Completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/subresources/messages/methods/list)

## <a id="task-chat-stored-surface"></a>Stored Chat Completions compatibility surface

Почему это важно:

- в official OpenAI API у `chat/completions` есть не только `POST`, но и stored-resource surface: list/get/messages
- сейчас shim уже дает local shadow-store subset для explicit `store: true`
  non-streaming proxy completions, но это еще не полная OpenAI-compatible read
  model для stored chat completions
- это один из заметных gaps между “минимальный shim для chat proxy” и “честный OpenAI-compatible facade”

Что входит:

- upstream-aware policy: что shim хранит локально при `store=true`, а что
  честно оставляет upstream-only
- решение по omitted `store`: считать ли account-level default storage частью
  shim contract или оставлять это explicit gap
- streamed chat completions: reconstruct/shadow-store from chunks или честно
  не включать в local stored surface
- update/delete endpoints для stored chat completions, если и когда shim
  начнет владеть ими

Definition of done:

- по remaining stored chat endpoints/semantics есть осознанный contract:
  implemented, proxy-only или explicit not-supported
- docs/OpenAPI не создают ложного впечатления, что `POST /v1/chat/completions` автоматически означает полную parity со всем official chat surface
- local shadow-store subset и remaining gaps разделены явно
- если расширенный local support выбран, shape и pagination покрыты
  integration tests

Полезные reference:

- [List stored Chat Completions](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/list)
- [Retrieve a stored Chat Completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/retrieve)
- [List messages for a stored Chat Completion](https://developers.openai.com/api/reference/resources/chat/subresources/completions/subresources/messages/methods/list)

## <a id="task-ops-hardening"></a>Operational hardening: backend readiness, retention job, local DX

Почему это важно:

- `/readyz` уже проверяет SQLite и upstream llama backend readiness, но это
  только первый кусок operational hardening
- публичная репа без нормального local DX и maintenance path быстро зарастает ручными шагами
- retention и vacuum/backup story нельзя оставлять “на потом”, если shim хранит state локально

Что входит:

- retention cleanup job
- backup / restore / vacuum / optimize path
- `Makefile`, dev script, `Dockerfile`, `docker-compose` или их осознанный минимальный subset

Статус на 12 апреля 2026:

- закрыт минимальный readiness scope:
  `/readyz` теперь возвращает `200` только когда жива SQLite и configured
  upstream llama-compatible backend успешно отвечает на `GET /v1/models`
- failure path тоже покрыт:
  readiness падает в `503 service_unavailable`, если upstream backend недоступен
  или не отдает валидный model-list payload
- OpenAPI wording синхронизирован с этим contract

Что остается здесь open:

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

## <a id="task-true-constrained-runtime"></a>True constrained decoder/runtime для `grammar` / `regex` custom tools

Когда делать:

- после текущего shim-native subset и до заявлений о полной OpenAI-compatible grammar parity
- только если появится либо backend-native constrained generation hook, либо отдельный локальный runtime с доступом к sampling/decoding

Почему это отдельная задача:

- OpenAI docs для custom tools и CFG описывают настоящий constrained generation/runtime, а не prompt+validate и не retry/repair loop
- текущий shim умеет локально валидировать и чинить supported subset, но это compatibility layer, а не spec-equivalent decoding
- без true runtime нельзя честно обещать строгую parity для сложных grammar/regex сценариев

Что входит:

- либо backend-native constrained generation path для `/v1/chat/completions`, либо отдельный local decoder/runtime внутри shim
- убрать зависимость grammar/regex input generation от repair prompts
- сделать constraint enforcement частью самого generation path, а не пост-валидации результата
- зафиксировать в docs/spec, какой путь выбран и какие гарантии он даёт

Варианты реализации:

- backend-native constrained generation
  shim продолжает владеть `responses` semantics, но для raw `custom_tool_call.input` вызывает upstream `/v1/chat/completions` с backend-specific constrained decoding hook
  это лучший путь, если backend умеет per-request grammar / regex / schema constraints без изменения общей архитектуры stateless shim
- embedded decoder/runtime library
  shim подключает OSS-библиотеку constrained decoding и сам управляет runtime для supported grammars
  этот путь снимает зависимость от конкретного backend, но требует отдельной интеграции tokenizer/sampling contract и заметно усложняет Go runtime
- low-level sampler/logits integration
  shim или новый backend-adapter опускается ниже HTTP-уровня и управляет decoding на уровне inference loop
  это самый мощный, но и самый дорогой вариант; он уже противоречит текущей идее “unchanged stateless backend”

Куда это нужно встраивать:

- `internal/llama/client.go`
  здесь нужен либо новый backend request path для constrained generation, либо capability-aware adapter поверх текущего `/v1/chat/completions`
- `internal/httpapi/local_tool_loop.go`
  текущий repair loop должен быть заменён на вызов настоящего constrained runtime для `custom_tool_call.input`
- `internal/httpapi/local_tool_loop_request.go`
  compiler/validator остаётся как preflight для supported subset и как safety-check, но перестаёт быть primary enforcement path
- `internal/config/config.go`
  возможно понадобится capability flag или backend mode, если constrained generation зависит от конкретного upstream

Что проверить до реализации:

- умеет ли выбранный upstream принимать per-request constrained generation для `chat/completions`
- можно ли смэппить наш supported subset `lark|regex` в backend-native формат без потери semantics
- не ломает ли выбранный путь streaming, `previous_response_id` replay и `custom_tool_call_input.delta/done` lifecycle

Definition of done:

- constrained custom tool input генерируется через реальный runtime constraint, а не через prompt + validate + repair
- grammar/regex happy path не требует repair loop для соблюдения constraint
- docs/spec не вводят в заблуждение и не обещают parity там, где её нет

Полезные reference:

- [Function calling: Context-free grammars](https://developers.openai.com/api/docs/guides/function-calling#context-free-grammars)
- [Constrain model outputs](https://developers.openai.com/api/docs/guides/latest-model#constraining-outputs)
