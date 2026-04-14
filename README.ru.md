# llama_shim

Русская версия README. Английская версия: [README.md](README.md).

`llama_shim` это небольшой HTTP-сервис на Go 1.26, который дает минимальный OpenAI-совместимый слой для Responses + Conversations и при этом использует `llama.cpp` как неизменяемый stateless backend.

v1 поддерживает:

- `POST /v1/responses`
- `GET /v1/responses/{id}`
- `POST /v1/conversations`
- `POST /v1/responses` с `stream: true` через SSE
- SQLite-backed восстановление состояния для `previous_response_id`
- SQLite-backed историю диалогов для `conversation`
- fallback proxying для остальных маршрутов напрямую в upstream backend

## Совместимость и roadmap

- цель V2: broad compatibility facade поверх текущего official OpenAI surface,
  который shim уже экспонирует
- matrix по surface-ам: [docs/compatibility-matrix.md](docs/compatibility-matrix.md)
- подробный backlog по V2: [backlog-v2.md](backlog-v2.md)
- parking lot для post-V2 expansion: [docs/v3-scope.md](docs/v3-scope.md)

## Архитектура

- `cmd/shim`: bootstrap процесса и запуск HTTP-сервера
- `internal/httpapi`: тонкие handlers, JSON error mapping, request ID и middleware для логирования запросов
- `internal/service`: orchestration для генерации ответов и создания conversations
- `internal/storage/sqlite`: явный SQL, migrations, WAL mode, foreign keys, busy timeout
- `internal/llama`: адаптер для `llama.cpp` `POST /v1/chat/completions`
- `internal/domain`: normalизация входа, восстановление контекста, генерация ID, normalизация response

Ключевые решения:

- `llama.cpp` остается stateless, всей state-семантикой владеет shim
- в v1 единственное постоянное хранилище это SQLite
- write transactions короткие; сервис не держит DB transaction открытой во время генерации
- response и conversation используют компактную и стабильную JSON-форму
- не-shim маршруты upstream можно стримить через shim через SSE passthrough

## Требования

- Go 1.26+
- запущенный `llama.cpp` сервер с `POST /v1/chat/completions`

## Локальный запуск llama.cpp

Один из возможных вариантов:

```bash
./llama-server \
  -m /path/to/model.gguf \
  --host 127.0.0.1 \
  --port 8081
```

Этот README предполагает, что `llama.cpp` уже запущен отдельно и доступен по:

```text
POST /v1/chat/completions
```

## Запуск shim

Сервис можно запускать через переменные окружения, YAML-конфиг или их комбинацию. Переменные окружения имеют приоритет над YAML.

```bash
LLAMA_BASE_URL=http://127.0.0.1:8081 \
SQLITE_PATH=./data/shim.db \
SHIM_ADDR=:8080 \
go run ./cmd/shim
```

### YAML-конфиг

Пример лежит в [config.yaml.example](config.yaml.example).

```yaml
shim:
  addr: ":8080"
  read_timeout: 15s
  write_timeout: 90s
  idle_timeout: 60s

sqlite:
  path: ./data/shim.db

llama:
  base_url: http://127.0.0.1:8081
  timeout: 60s

log:
  level: info
  file_path: ./.data/shim.log

responses:
  mode: prefer_local
  custom_tools:
    mode: auto
  codex:
    enable_compatibility: true
    force_tool_choice_required: true
```

Запуск с явным конфигом:

```bash
go run ./cmd/shim -config ./config.yaml
```

Или через переменную окружения:

```bash
SHIM_CONFIG=./config.yaml go run ./cmd/shim
```

Если `-config` и `SHIM_CONFIG` не заданы, сервис также попробует автоматически загрузить `./config.yaml` или `./config.yml`, если файл существует.

Поддерживаемые environment overrides:

- `LLAMA_TIMEOUT`, значение по умолчанию `60s`
- `SHIM_READ_TIMEOUT`, значение по умолчанию `15s`
- `SHIM_WRITE_TIMEOUT`, значение по умолчанию `90s`
- `SHIM_IDLE_TIMEOUT`, значение по умолчанию `60s`
- `LOG_LEVEL`, значение по умолчанию `info`; `debug` добавляет отдельную debug-запись с телами request/response
- `LOG_FILE_PATH` переопределяет `log.file_path`; если задан, логи пишутся и в stdout, и в файл
- `LLAMA_BASE_URL` переопределяет `llama.base_url`
- `SQLITE_PATH` переопределяет `sqlite.path`
- `SHIM_ADDR` переопределяет `shim.addr`
- `RESPONSES_MODE` переопределяет `responses.mode`; поддерживаются `prefer_local`, `prefer_upstream`, `local_only`
  `prefer_local` теперь используется по умолчанию: shim сам ведет `/v1/responses` для локально-поддерживаемого subset и обращается к upstream `/v1/responses` только для неподдерживаемых фич.
- `RESPONSES_CUSTOM_TOOLS_MODE` переопределяет `responses.custom_tools.mode`; поддерживаются `bridge`, `auto`, `passthrough`
  Для дефолтного режима лучше `auto`: он сохраняет bridge для обычных text custom tools и не ломает grammar-constrained инструменты, пропуская их passthrough.
- `RESPONSES_CODEX_ENABLE_COMPATIBILITY` переопределяет `responses.codex.enable_compatibility`; если выключено, shim перестает добавлять Codex-specific instructions/context и пропускает Codex-specific normalизацию response
- `RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED` переопределяет `responses.codex.force_tool_choice_required`; если включено, Codex-like запросы с `tool_choice: "auto"` переписываются в `required`

Заметки по retention для responses:

- standalone-объекты `/v1/responses` следуют outward `store` contract, который возвращается в самом response object
- conversation-attached items живут по lifecycle разговора, а не по retention standalone response
- shim может хранить внутренние hidden response rows для локального `previous_response_id` replay даже когда outward response сообщает `store=false`

## Примеры curl

### POST `/v1/responses`

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"test-model","store":true,"input":"Say OK and nothing else"}'
```

### GET `/v1/responses/{id}`

```bash
curl -s http://127.0.0.1:8080/v1/responses/resp_your_id_here
```

### POST `/v1/conversations`

```bash
curl -s http://127.0.0.1:8080/v1/conversations \
  -H 'Content-Type: application/json' \
  -d '{
    "items": [
      {"type":"message","role":"system","content":"You are a test assistant."},
      {"type":"message","role":"user","content":"Remember: code=777. Reply OK."}
    ]
  }'
```

### POST `/v1/responses` с `previous_response_id`

Первый запрос:

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{"model":"test-model","store":true,"input":"Remember: my code = 123. Reply OK"}'
```

Продолжение с использованием возвращенного response ID:

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model":"test-model",
    "store":true,
    "previous_response_id":"resp_previous_id_here",
    "input":"What was my code? Reply with just the number."
  }'
```

### POST `/v1/responses` с `conversation`

После создания conversation:

```bash
curl -s http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model":"test-model",
    "store":true,
    "conversation":"conv_your_id_here",
    "input":"What is the code? Reply with just the number."
  }'
```

### POST `/v1/responses` с `stream: true`

```bash
curl -N http://127.0.0.1:8080/v1/responses \
  -H 'Content-Type: application/json' \
  -d '{
    "model":"test-model",
    "store":true,
    "stream":true,
    "input":"Say OK and nothing else"
  }'
```

Shim отправляет SSE events, включая:

- `response.created`
- `response.output_item.added`
- `response.output_text.delta`
- `response.output_text.done`
- `response.output_item.done`
- `response.completed`

## Примечания по API

- versioned OpenAPI spec для текущего surface shim лежит в [openapi/openapi.yaml](openapi/openapi.yaml)
- в spec операции помечены через `x-shim-status: implemented|partial|proxy`, чтобы было видно, где контрактом владеет сам shim, а где он только проксирует upstream
- `previous_response_id` и `conversation` взаимоисключающие
- все API-ошибки возвращаются в JSON
- `output_text` всегда присутствует в успешных ответах
- при создании conversation текстовый контент нормализуется в канонические `input_text` items

## Тесты

```bash
go test ./...
```

Интеграционные тесты используют:

- временную SQLite-базу
- фейковый `llama.cpp` сервер на `httptest.Server`

Покрытые acceptance-сценарии:

- store + GET
- восстановление цепочки `previous_response_id`
- восстановление состояния через `conversation`
- 404 для отсутствующих response и conversation
- 4xx-валидация для взаимоисключающих state fields
