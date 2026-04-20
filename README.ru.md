# llama_shim

Русская версия README. Английская версия: [README.md](README.md).

`llama_shim` — небольшой сетевой сервис на Go 1.26. Он дает совместимую с
OpenAI прослойку поверх `llama.cpp`, оставляя внешний сервер без собственного
состояния и забирая всю логику хранения состояния на свою сторону.

В текущем состоянии сервис поддерживает:

- `POST /v1/responses`
- `GET /v1/responses/{id}`
- `POST /v1/conversations`
- `POST /v1/responses` с полем `stream: true` через `SSE`
- локальное восстановление состояния по `previous_response_id`
- локальную историю диалога по `conversation`
- локально хранимую поверхность сохраненных `Chat Completions`:
  `GET /v1/chat/completions`,
  `GET/POST/DELETE /v1/chat/completions/{completion_id}`,
  `GET /v1/chat/completions/{completion_id}/messages`
- прямую пересылку всех остальных маршрутов, которыми сервис сам не владеет,
  во внешний сервер

## Совместимость и план релиза

- V2 оформлен как широкий совместимый фасад для тех поверхностей OpenAI,
  которые сервис уже предоставляет
- текущее состояние по поверхностям: [docs/compatibility-matrix.md](docs/compatibility-matrix.md)
- зафиксированные рамки V2: [docs/v2-scope.md](docs/v2-scope.md)
- заметки к релизу V2: [docs/release-notes-v2.md](docs/release-notes-v2.md)
- завершенный подготовительный слой V3: [docs/v3-preflight.md](docs/v3-preflight.md)
- задачи, вынесенные за V2: [docs/v3-scope.md](docs/v3-scope.md)
- направления расширения и модель подключаемых модулей после ядра
  совместимости: [docs/v4-scope.md](docs/v4-scope.md)

## Документация

- практические руководства: [docs/guides/README.md](docs/guides/README.md)
- заметки по runtime hardening: [docs/guides/runtime-hardening.md](docs/guides/runtime-hardening.md)
- контракт API и границы совместимости: [docs/compatibility-matrix.md](docs/compatibility-matrix.md)
- рамки релиза V2: [docs/v2-scope.md](docs/v2-scope.md)
- завершенный подготовительный слой V3: [docs/v3-preflight.md](docs/v3-preflight.md)
- детерминированный локальный стек и быстрая проверка: [docs/guides/devstack.md](docs/guides/devstack.md)
- задачи, вынесенные за V2: [docs/v3-scope.md](docs/v3-scope.md)
- направления расширения и подключаемые модули V4: [docs/v4-scope.md](docs/v4-scope.md)
- спецификация OpenAPI: [openapi/openapi.yaml](openapi/openapi.yaml)

## Устройство проекта

- `cmd/shim`: запуск процесса и сетевого сервера
- `internal/httpapi`: обработчики, преобразование ошибок в JSON, `request_id`
  и промежуточные обработчики для журналирования запросов
- `internal/service`: оркестрация генерации ответов и создания conversations
- `internal/storage/sqlite`: явный SQL, миграции, `WAL`, внешние ключи и
  `busy_timeout`
- `internal/llama`: адаптер к `llama.cpp` для `POST /v1/chat/completions`
- `internal/domain`: нормализация входа, восстановление контекста, генерация
  идентификаторов и приведение ответов к каноническому виду

Ключевые решения:

- `llama.cpp` остается без собственного состояния, а состояние разговора
  контролирует сам сервис
- основное постоянное хранилище — `SQLite`
- транзакции записи короткие; сервис не держит транзакцию открытой во время
  генерации
- объекты response и conversation имеют компактную и устойчивую JSON-форму
- маршруты, которыми сервис не владеет, можно прозрачно передавать во внешний
  сервер, в том числе через `SSE`

## Требования

- Go 1.26+
- запущенный `llama.cpp` с маршрутом `POST /v1/chat/completions`

## Локальный запуск `llama.cpp`

Один из возможных вариантов:

```bash
./llama-server \
  -m /path/to/model.gguf \
  --host 127.0.0.1 \
  --port 8081
```

Ниже предполагается, что `llama.cpp` уже запущен отдельно и доступен по
`POST /v1/chat/completions`.

## Запуск сервиса

Сервис можно запускать через переменные окружения, через файл конфигурации
`YAML` или в смешанном режиме. Переменные окружения имеют приоритет над
`YAML`.

```bash
LLAMA_BASE_URL=http://127.0.0.1:8081 \
SQLITE_PATH=./data/shim.db \
SHIM_ADDR=:8080 \
go run ./cmd/shim
```

### Файл конфигурации YAML

Пример лежит в [config.yaml.example](config.yaml.example).

```yaml
shim:
  addr: ":8080"
  read_timeout: 15s
  write_timeout: 90s
  idle_timeout: 60s

sqlite:
  path: ./data/shim.db
  maintenance:
    cleanup_interval: 15m

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

Запуск с явным файлом конфигурации:

```bash
go run ./cmd/shim -config ./config.yaml
```

Запуск через переменную окружения:

```bash
SHIM_CONFIG=./config.yaml go run ./cmd/shim
```

Если `-config` и `SHIM_CONFIG` не заданы, сервис попробует автоматически
загрузить `./config.yaml` или `./config.yml`, если такой файл существует.

### Поддерживаемые переопределения через переменные окружения

- `LLAMA_TIMEOUT`, по умолчанию `60s`
- `SHIM_READ_TIMEOUT`, по умолчанию `15s`
- `SHIM_WRITE_TIMEOUT`, по умолчанию `90s`
- `SHIM_IDLE_TIMEOUT`, по умолчанию `60s`
- `LOG_LEVEL`, по умолчанию `info`; значение `debug` включает дополнительную
  отладочную запись с телами запроса и ответа
- `LOG_FILE_PATH` переопределяет `log.file_path`; если задано, журналы пишутся
  и в `stdout`, и в файл
- `LLAMA_BASE_URL` переопределяет `llama.base_url`
- `SQLITE_PATH` переопределяет `sqlite.path`
- `SQLITE_MAINTENANCE_CLEANUP_INTERVAL` переопределяет
  `sqlite.maintenance.cleanup_interval`
- `SHIM_ADDR` переопределяет `shim.addr`
- `RESPONSES_MODE` переопределяет `responses.mode`; поддерживаются
  `prefer_local`, `prefer_upstream`, `local_only`
  `prefer_local` используется по умолчанию: сервис сам обрабатывает
  `/v1/responses` для поддерживаемого локального подмножества и обращается к
  внешнему `/v1/responses` только для неподдерживаемых возможностей
- `RESPONSES_CUSTOM_TOOLS_MODE` переопределяет `responses.custom_tools.mode`;
  поддерживаются `bridge`, `auto`, `passthrough`
  Для обычного режима рекомендуется `auto`: он сохраняет bridge-путь для
  простых custom tools и не ломает grammar-constrained инструменты
- `RESPONSES_CODEX_ENABLE_COMPATIBILITY` переопределяет
  `responses.codex.enable_compatibility`; если выключить, сервис перестанет
  добавлять Codex-специфический контекст и нормализацию
- `RESPONSES_CODEX_FORCE_TOOL_CHOICE_REQUIRED` переопределяет
  `responses.codex.force_tool_choice_required`; если включить, Codex-подобные
  запросы с `tool_choice: "auto"` будут переписываться в `required`

### Замечания по хранению `Responses`

- отдельные объекты `/v1/responses` следуют внешнему контракту `store`, который
  возвращается в самом объекте response
- элементы, привязанные к conversation, живут по жизненному циклу разговора, а
  не по правилам хранения отдельного response
- сервис может сохранять скрытые внутренние записи response, если они нужны
  для локального воспроизведения `previous_response_id`

## Обслуживание

В проекте есть минимальный набор средств для эксплуатации:

- фоновая очистка SQLite через `sqlite.maintenance.cleanup_interval`
- разовые команды обслуживания через `./cmd/shimctl`
- локальная упаковка через `Makefile`, `Dockerfile` и `docker-compose.yml`

`sqlite.maintenance.cleanup_interval` сейчас чистит только локальные ресурсы с
явным `expires_at`:

- просроченные `/v1/files`
- просроченные `/v1/vector_stores`

Срок жизни контейнеров `code_interpreter` настраивается отдельно через
`responses.code_interpreter.cleanup_interval`.

Примеры:

```bash
go run ./cmd/shimctl -config ./config.yaml cleanup
go run ./cmd/shimctl -config ./config.yaml optimize
go run ./cmd/shimctl -config ./config.yaml vacuum
go run ./cmd/shimctl -config ./config.yaml backup -out ./.data/shim-backup.db
go run ./cmd/shimctl -config ./config.yaml restore -from ./.data/shim-backup.db
```

Восстановление намеренно рассчитано на автономный режим: перед заменой файла
`SQLite` лучше остановить работающий сервис.

## Локальная разработка

В репозитории уже есть минимальная обвязка для локальной работы:

- `make run`, `make test`, `make build`
- `make maint-cleanup`, `make maint-optimize`, `make maint-vacuum`,
  `make maint-backup`
- `docker build -t llama-shim:local .`
- `docker compose up --build`
- `make devstack-up`, `make devstack-smoke`, `make devstack-down`

`docker-compose.yml` монтирует `./config.yaml` в контейнер и хранит данные
SQLite в `./.data`.

Для детерминированного локального стека из двух процессов и встроенной
быстрой проверки см. [docs/guides/devstack.md](docs/guides/devstack.md).

## Примеры `curl`

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

Продолжение с использованием возвращенного `response.id`:

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

### POST `/v1/responses` с полем `stream: true`

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

Сервис отправляет `SSE`-события, включая:

- `response.created`
- `response.output_item.added`
- `response.output_text.delta`
- `response.output_text.done`
- `response.output_item.done`
- `response.completed`

## Примечания по API

- версия спецификации `OpenAPI` для текущей поверхности сервиса лежит в
  [openapi/openapi.yaml](openapi/openapi.yaml)
- в спецификации операции помечены через
  `x-shim-status: implemented|partial|proxy`, чтобы было видно, где контрактом
  владеет сам сервис, а где он только пересылает запрос во внешний сервер
- `previous_response_id` и `conversation` взаимоисключающие
- все ошибки `API` возвращаются в `JSON`
- `output_text` всегда присутствует в успешных ответах
- при создании conversation текст нормализуется в канонические элементы
  `input_text`

## Тесты

```bash
go test ./...
```

Интеграционные тесты используют:

- временную базу `SQLite`
- поддельный `llama.cpp` сервер на `httptest.Server`

Покрытые сценарии:

- хранение + получение через `GET`
- восстановление цепочки `previous_response_id`
- восстановление состояния через `conversation`
- `404` для отсутствующих response и conversation
- валидацию `4xx` для взаимоисключающих полей состояния
