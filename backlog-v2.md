# Backlog / roadmap toward v2

Актуализировано по состоянию на 10 апреля 2026 на основе:

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
- `POST /v1/responses` with `stream: true` over SSE
- `GET /v1/responses/{id}?stream=true` with local SSE replay
- `/healthz`
- `/readyz` с проверкой SQLite readiness
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
- `/readyz` теперь реально проверяет SQLite, а не просто отвечает `200`
- `/v1/chat/completions` очищает provider-specific поля в обычном JSON и SSE потоке
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
- [ ] - hosted/native tool-specific SSE replay beyond core shim item families ([детали](#task-streaming-replay-hosted))
- [x] - compatibility для `/responses/compact` и `/responses/input_tokens` ([детали](#task-compaction-and-token-counting))
- [ ] - retrieval-compatible слой: vector stores + `file_search` ([детали](#task-retrieval-layer))
- [ ] - parity для hosted/native Responses tools (`web_search`, `computer_use`, `code_interpreter`, `image_generation`, `remote MCP`, `tool_search`) ([детали](#task-hosted-tools-parity))
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
- hosted/native tool-specific SSE parity вынесен ниже как отдельный open item

## <a id="task-streaming-replay-hosted"></a>Hosted/native tool-specific SSE replay beyond core shim item families

Почему это отдельно:

- для hosted/native tools official docs сейчас явно перечисляют event families, но exact payload schema через docs tooling доступна фрагментарно
- без source event log легко “додумать” synthetic payload неправильно и начать overclaim-ить совместимость

Что осталось:

- hosted/native tool-specific SSE replay beyond current shim-supported item families
- максимальное сближение synthetic replay с реальным upstream event log там, где сам stored object не хранит исходные deltas

Definition of done:

- retrieve replay не деградирует hosted/native tool outputs до generic `output_item.*` только потому, что stream synthetic
- residual event families либо реально воспроизводятся, либо явно исключены из supported shim surface

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

## <a id="task-hosted-tools-parity"></a>Parity для hosted/native Responses tools

Почему это важно:

- по официальным OpenAI docs Responses API это не только `message` и `function_call`, а полноценный agentic surface с built-in tools и typed items
- без отдельного плана по hosted/native tools shim легко “застрянет” в text + function subset и будет выглядеть совместимым только частично
- часть этой поверхности уже пересекается с retrieval, но `web_search`, `computer_use`, `code_interpreter`, `image_generation`, `remote MCP` и `tool_search` требуют отдельной архитектурной рамки

Что входит:

- описать поддерживаемый MVP subset для `web_search`, `computer_use`, `code_interpreter`, `image_generation` и `remote MCP`
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

## <a id="task-chat-stored-surface"></a>Stored Chat Completions compatibility surface

Почему это важно:

- в official OpenAI API у `chat/completions` есть не только `POST`, но и stored-resource surface: list/get/messages
- сейчас shim владеет только `POST /v1/chat/completions` как validate+proxy path и не дает OpenAI-compatible read model для stored chat completions
- это один из заметных gaps между “минимальный shim для chat proxy” и “честный OpenAI-compatible facade”

Что входит:

- `GET /v1/chat/completions`
- `GET /v1/chat/completions/{completion_id}`
- `GET /v1/chat/completions/{completion_id}/messages`
- policy: что shim хранит локально при `store=true`, а что честно оставляет upstream-only
- явное решение, связываем ли мы stored chat completion state с existing SQLite store или оставляем этот surface как explicit not-supported

Definition of done:

- по каждому из stored chat endpoints есть осознанный contract: implemented, proxy-only или explicit not-supported
- docs/OpenAPI не создают ложного впечатления, что `POST /v1/chat/completions` автоматически означает полную parity со всем official chat surface
- если local support выбран, shape и pagination покрыты integration tests

Полезные reference:

- [Chat Completions API spec](https://api.openai.com/v1/chat/completions)

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
