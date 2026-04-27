# Codex CLI

## What Works

Codex CLI can target `llama_shim` as an OpenAI-compatible Responses backend.

Use a named custom Codex provider for normal local development:

- `wire_api = "responses"`
- `base_url` points at the shim `/v1` base URL
- the environment variable named by `env_key` is set
- start with `supports_websockets = false` to prove HTTP first
- enable `supports_websockets = true` only when intentionally testing WS

HTTP `POST /v1/responses` and SSE streaming remain the baseline path.
WebSocket is an optional transport, not a replacement.

For staged manual validation of real Codex CLI against a model/upstream pair,
use [Codex Testing Plan](codex-testing-plan.md). It keeps tests small enough to
separate shim compatibility problems from upstream model quality problems.

## Shim Config

In the shim config, keep Responses WebSocket enabled:

```yaml
responses:
  mode: prefer_local
  websocket:
    enabled: true
  codex:
    enable_compatibility: true
    force_tool_choice_required: true
    # Keep the rewrite available for deterministic smokes, but disable it for
    # upstream models that answer normal Codex questions poorly when every
    # tool_choice=auto turn is forced to call a tool.
    force_tool_choice_required_disabled_models:
      - Kimi-*
    upstream_input_compatibility:
      models:
        - model: Kimi-*
          mode: stringify
```

WebSocket and Codex compatibility are enabled by default in
`config.yaml.example`; the `Kimi-*` exclusion is a provider-specific local
setting. The `upstream_input_compatibility` entry is also provider-specific: it
keeps the shim's OpenAI-facing request contract unchanged, but sends matching
upstream models a plain text transcript on the first try instead of waiting for
a structured-input validation `400` and retrying.
HTTP-first Codex config can still leave shim WebSocket enabled; it only tells
Codex not to choose WebSocket for that provider while debugging.

## Debug A Stuck Codex Turn

For a one-off local debugging session, raise shim logging to `debug`:

```yaml
log:
  level: debug
  file_path: ./.data/shim.log
```

Debug logs are intentionally structured so a stuck Codex turn can be read from
`.data/shim.log` without packet capture. For streamed `/v1/responses` turns,
look for these messages with the same `request_id` or Codex
`client_request_id`:

- `responses upstream request started`: model, `stream`, `tool_choice`, input
  shape, tool types, and body size before proxying upstream.
- `responses upstream response headers`: upstream status, content type,
  upstream request id, and time to headers.
- `responses stream first upstream line`: time to the first SSE line.
- `responses stream event`: selected Responses event summaries such as output
  text, function arguments, shell calls, apply_patch calls, MCP calls, and
  completion/failure events.
- `responses stream summary`: final event counters, whether any tool event was
  seen, output text length/preview, and whether `response.completed` arrived.
  `response.completed` summaries include `output_item_types` plus message,
  reasoning, and tool item counts, so an empty final answer shows whether the
  upstream returned only a reasoning/tool item or a real assistant message.
- `responses upstream request failed`: upstream connection/setup failures such
  as EOF before response headers.

When a model prints a preamble like “starting implementation” and then stops,
the key field is `saw_tool_event` in `responses stream summary`. If it is false,
Codex never received a tool call to execute; this points at model/upstream
tool-following or stream stability rather than local shell/apply_patch
execution.

`debug` logging can include request/response body previews from the generic HTTP
middleware. Use it only for local diagnostics, then return to `info` after the
capture.

## Codex Config

There are two practical ways to point Codex at the shim.

### Recommended Custom Provider, HTTP First

Use this for real interactive Codex sessions against a local shim:

```toml
model = "Kimi-K2.6"
model_provider = "gateway-shim"
approval_policy = "never"
sandbox_mode = "workspace-write"

[model_providers.gateway-shim]
name = "gateway-shim"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"
env_key = "GW_API_KEY"
supports_websockets = false
```

Run it with the same environment variable named in `env_key`:

```bash
GW_API_KEY=shim-dev-key \
codex \
  -m Kimi-K2.6 \
  -c 'model_provider="gateway-shim"' \
  -c 'model_reasoning_effort="high"' \
  -c 'model_reasoning_summary="auto"'
```

If you want to use `OPENAI_API_KEY` instead, change the provider block to:

```toml
env_key = "OPENAI_API_KEY"
```

Then launch with `OPENAI_API_KEY=...`.

If shim ingress auth is disabled, the key can be any non-empty value. If shim
ingress auth uses `static_bearer`, the key must match one of the configured
shim bearer tokens.

### Shim-Served Model Metadata

If Codex prints:

```text
Model metadata for `Kimi-K2.6` not found. Defaulting to fallback metadata
```

the request can still work, but Codex is using fallback local assumptions for
that model. Configure shim-owned Codex metadata instead of changing ordinary
OpenAI `GET /v1/models` output:

```yaml
responses:
  codex:
    model_metadata:
      models:
        - model: Kimi-K2.6
          display_name: Kimi K2.6
          description: OpenAI-compatible upstream routed through llama_shim.
          context_window: 128000
          max_context_window: 128000
          auto_compact_token_limit: 0
          effective_context_window_percent: 95
          default_reasoning_level: high
          supported_reasoning_levels: [low, medium, high]
          supports_reasoning_summaries: false
          default_reasoning_summary: none
          shell_type: shell_command
          apply_patch_tool_type: ""
          web_search_tool_type: text
          supports_parallel_tool_calls: false
          support_verbosity: false
          default_verbosity: ""
          supports_image_detail_original: false
          supports_search_tool: false
          input_modalities: [text]
          visibility: list
          supported_in_api: true
          priority: 100
          additional_speed_tiers: []
          experimental_supported_tools: []
          availability_nux_message: ""
          truncation_policy:
            mode: bytes
            limit: 10000
          base_instructions: ""
```

Codex sends `GET /v1/models?client_version=...`; for that Codex-specific query
shim returns a Codex model catalog (`{"models":[...]}`). A normal
OpenAI-compatible `GET /v1/models` without `client_version` remains proxied and
keeps the official `{"object":"list","data":[...]}` shape. The shim also serves
the same Codex catalog at `/api/codex/models` for provider configs that point
there.

Codex still supports its own local `model_catalog_json` config. Prefer the
shim-served metadata when the model is tied to this shim/upstream pair.
Codex caches fetched model metadata briefly in `$CODEX_HOME/models_cache.json`;
if the warning persists immediately after adding shim metadata, restart Codex or
remove that cache file.

The high-impact fields to tune first are:

- `context_window`, `max_context_window`, `auto_compact_token_limit`, and
  `effective_context_window_percent`: how much context Codex believes it can
  use before compacting.
- `shell_type`: which shell tool family Codex declares. Useful values are
  `shell_command`, `unified_exec`, `local`, `default`, and `disabled`.
- `apply_patch_tool_type`: `freeform`, `function`, or empty for Codex default
  selection.
- `input_modalities`, `supports_image_detail_original`, and
  `supports_search_tool`: whether Codex exposes image/search related surfaces.
- `support_verbosity`, `default_verbosity`, `supports_reasoning_summaries`, and
  `default_reasoning_summary`: which Responses parameters Codex will send for
  verbosity and reasoning summaries.
- `truncation_policy`: how Codex truncates long tool output in its local
  history. Use `bytes` or `tokens`.

#### Codex Model Metadata Fields

These fields live under
`responses.codex.model_metadata.models[]`. They are shim-owned YAML settings
that are rendered into the Codex `/models` catalog. They are not part of the
public OpenAI `GET /v1/models` schema.

| Field | Default | Allowed values | What it controls |
| --- | --- | --- | --- |
| `model` | required | any non-empty model slug | Codex model slug returned as `slug`. Use the same value that Codex launches with, for example `Kimi-K2.6`. |
| `display_name` | `model` | string | Human-facing model name in Codex UI/log surfaces. Does not affect upstream routing. |
| `description` | `OpenAI-compatible upstream routed through llama_shim.` | string | Human-facing description for model lists. Does not affect request shape. |
| `context_window` | omitted/null | non-negative integer | Context tokens Codex believes the model can accept. This drives context budgeting and compaction decisions. |
| `max_context_window` | omitted/null | non-negative integer | Maximum allowed context override. Codex clamps `model_context_window` overrides to this value when present. |
| `auto_compact_token_limit` | omitted/null | non-negative integer | Token threshold where Codex starts automatic history compaction. `0` means omit and let Codex derive it from context window. |
| `effective_context_window_percent` | `95` | `0..100`; `0` normalizes to `95` | Percent of the context window Codex treats as usable input budget after reserving headroom. Lower it if the upstream fails near its advertised limit. |
| `default_reasoning_level` | `high` | `none`, `minimal`, `low`, `medium`, `high`, `xhigh` | Reasoning effort Codex chooses when no explicit `model_reasoning_effort` override is set. |
| `supported_reasoning_levels` | `[low, medium, high]` | list of `none`, `minimal`, `low`, `medium`, `high`, `xhigh` | Reasoning effort choices Codex may expose/use for this model. Keep this aligned with what the upstream actually accepts. |
| `supports_reasoning_summaries` | `false` | boolean | Whether Codex may send/use reasoning summary controls for the model. |
| `default_reasoning_summary` | `none` | `auto`, `concise`, `detailed`, `none` | Default reasoning summary mode Codex uses when no explicit `model_reasoning_summary` override is set. |
| `shell_type` | `shell_command` | `shell_command`, `unified_exec`, `local`, `default`, `disabled` | Shell tool family Codex declares. `shell_command` is the current practical default for Codex local command execution. `unified_exec` exposes `exec_command`/`write_stdin`. `disabled` removes shell tools. |
| `apply_patch_tool_type` | empty/null | empty, `freeform`, `function` | Apply-patch tool shape Codex declares. Empty lets Codex pick its local default; `freeform` is closest to current Codex patch choreography. |
| `web_search_tool_type` | `text` | `text`, `text_and_image` | Web-search tool result shape Codex expects if search is enabled. This does not create a shim search backend by itself. |
| `supports_parallel_tool_calls` | `false` | boolean | Whether Codex advertises parallel tool-call support for the model. Keep `false` for upstreams that handle tool calls serially or unreliably. |
| `support_verbosity` | `false` | boolean | Whether Codex may send GPT-5-style `verbosity` controls. Enable only if the upstream accepts them. |
| `default_verbosity` | empty/null | empty, `low`, `medium`, `high` | Default verbosity Codex sends when `support_verbosity=true` and no explicit `model_verbosity` is set. |
| `supports_image_detail_original` | `false` | boolean | Whether Codex can request original-detail local image handling for image inputs. |
| `supports_search_tool` | `false` | boolean | Whether Codex may expose its tool-search surface when the corresponding Codex feature is enabled. This is tool discovery, not web search. |
| `input_modalities` | `[text]` | list of `text`, `image` | User input types Codex believes the model accepts. Adding `image` can cause Codex to expose image-related paths. |
| `visibility` | `list` | `list`, `hide`, `none` | Whether the model appears in Codex model lists. For direct `-m` usage, `list` is usually fine. |
| `supported_in_api` | `true` | boolean | Codex API support flag shown in model metadata. Keep `true` for shim-routed API models. |
| `priority` | `100` | integer | Sort priority in Codex model lists. Lower values appear earlier in the upstream Codex catalog. |
| `additional_speed_tiers` | `[]` | list of strings, commonly `fast` | Extra service-tier labels Codex may expose for this model. Do not add `fast` unless the upstream supports the corresponding request behavior. |
| `experimental_supported_tools` | `[]` | list of strings | Codex-internal experimental tool gates. Current known values include `list_dir` and `test_sync_tool`; leave empty unless intentionally testing Codex internals. |
| `availability_nux_message` | empty/null | string | Optional first-use/availability message rendered as Codex `availability_nux.message`. UI-only. |
| `truncation_policy.mode` | `bytes` | `bytes`, `tokens` | Unit Codex uses to truncate long tool output in local history. |
| `truncation_policy.limit` | `10000` | non-negative integer; `0` normalizes to `10000` | Tool-output truncation limit in the selected unit. This affects Codex local history, not upstream model max tokens. |
| `base_instructions` | empty string | string | Model-specific base instructions that Codex can use instead of its fallback base instructions. Leave empty unless you intentionally want to override Codex's model instructions. |

Practical starting point for weaker OpenAI-compatible upstreams:

```yaml
chat_completions:
  upstream_compatibility:
    models:
      # Use this only for DeepSeek-compatible gateways or similar upstreams
      # that reject OpenAI Chat developer/json_schema/thinking details.
      - model: deepseek-*
        remap_developer_role: true
        default_thinking: disabled
        json_schema_mode: json_object_instruction
responses:
  codex:
    model_metadata:
      models:
        - model: Kimi-K2.6
          display_name: Kimi K2.6
          context_window: 128000
          max_context_window: 128000
          default_reasoning_level: high
          supported_reasoning_levels: [low, medium, high]
          supports_reasoning_summaries: false
          default_reasoning_summary: none
          shell_type: shell_command
          apply_patch_tool_type: ""
          supports_parallel_tool_calls: false
          support_verbosity: false
          input_modalities: [text]
          truncation_policy:
            mode: bytes
            limit: 10000
```

For more capable GPT-5-like upstreams, the next settings to try are:

```yaml
supports_reasoning_summaries: true
support_verbosity: true
default_verbosity: medium
apply_patch_tool_type: freeform
supports_parallel_tool_calls: true
```

The shim intentionally exposes the simple, operational `ModelInfo` fields that
affect Codex behavior. Raw nested Codex backend fields such as `model_messages`
and `upgrade` are not exposed through YAML yet; add them only with dedicated
schema validation, because malformed values can make Codex reject the whole
model catalog.

### Enable WebSocket After HTTP Works

After the HTTP path works, enable Codex WebSocket mode explicitly:

```toml
supports_websockets = true
```

Expected shim log signal:

```text
GET /v1/responses status=101
```

An immediate WebSocket EOF after a warmup connection is not, by itself, a shim
auth failure. Codex prewarms WebSocket and can close that connection without a
request payload. Confirm the actual turn by checking for either:

- a later WebSocket turn on `/v1/responses`
- or HTTP fallback through `POST /v1/responses`

### Dev Stack Custom Provider

For the repo dev stack, use port `18080`:

```toml
model = "devstack-model"
model_provider = "llama_shim"
approval_policy = "never"
sandbox_mode = "workspace-write"

[model_providers.llama_shim]
name = "llama_shim devstack"
base_url = "http://127.0.0.1:18080/v1"
wire_api = "responses"
env_key = "OPENAI_API_KEY"
supports_websockets = false
```

### Compatibility Shortcut: Built-In OpenAI Provider Override

This is useful for repo smoke scripts and quick probes. Codex keeps using its
built-in OpenAI provider, but sends Responses requests to the shim URL:

```toml
model = "devstack-model"
approval_policy = "never"
sandbox_mode = "workspace-write"

openai_base_url = "http://127.0.0.1:18080/v1"
```

Equivalent one-off command-line override:

```bash
OPENAI_API_KEY=shim-dev-key \
codex exec \
  -m devstack-model \
  -c 'openai_base_url="http://127.0.0.1:18080/v1"' \
  'Use exec_command to run pwd, then reply READY.'
```

Do not use this as the first debugging path for a normal local session. It
still selects Codex's built-in `OpenAI` provider behavior, so Codex may attempt
OpenAI-specific side channels or WebSocket capability paths that make local
shim failures look like authorization or socket problems.

## Run Codex

### Interactive Session

With config saved in `~/.codex/config.toml`:

```bash
export GW_API_KEY=shim-dev-key
codex
```

For a normal shim on port `8080` without changing the default profile:

```bash
GW_API_KEY=shim-dev-key \
codex \
  -m "<model>" \
  -c 'model_provider="gateway-shim"'
```

For the dev stack, save the `llama_shim` provider block above and run:

```bash
OPENAI_API_KEY=shim-dev-key \
codex \
  -m devstack-model \
  -c 'model_provider="llama_shim"'
```

### One-Off Command

Use `codex exec` for non-interactive dev-stack checks. These examples use the
compatibility shortcut because the repo smoke scripts exercise that path:

```bash
OPENAI_API_KEY=shim-dev-key \
codex exec \
  -m devstack-model \
  -c 'openai_base_url="http://127.0.0.1:18080/v1"' \
  'Use exec_command to run pwd, then reply READY.'
```

For a small coding task:

```bash
OPENAI_API_KEY=shim-dev-key \
codex exec \
  --json \
  -C "$CODEX_TEST_DIR" \
  -m devstack-model \
  -c 'openai_base_url="http://127.0.0.1:18080/v1"' \
  -c 'approval_policy="never"' \
  -c 'sandbox_mode="workspace-write"' \
  'Make the requested small code change, run the relevant test, and summarize.'
```

### Codex Tool Modes

Current Codex CLI has two relevant local command-tool modes for this shim:

- By default, `[features].unified_exec` is enabled on non-Windows platforms.
  Codex then sends function tools such as `exec_command` and `write_stdin`.
- With `-c 'features.unified_exec=false'`, Codex can fall back to the default
  function tool named `shell`.

Both of those are Codex CLI function-tool declarations. They are different
from the official Responses native shell declaration, which is
`tools: [{"type":"shell", ...}]` and returns `shell_call` items. The native
Responses `shell` and `apply_patch` subset is covered by
`make v3-coding-tools-smoke`; real Codex CLI compatibility is covered by the
Codex smoke scripts below.

One-off fallback-shell check:

```bash
OPENAI_API_KEY=shim-dev-key \
codex exec \
  --json \
  -m devstack-model \
  -c 'openai_base_url="http://127.0.0.1:18080/v1"' \
  -c 'approval_policy="never"' \
  -c 'sandbox_mode="workspace-write"' \
  -c 'features.unified_exec=false' \
  'Use the shell tool to run pwd, then reply READY.'
```

### With Custom Provider

If `~/.codex/config.toml` contains the `[model_providers.llama_shim]` block
from above:

```bash
export OPENAI_API_KEY=shim-dev-key
codex --profile default
```

Or override explicitly:

```bash
export OPENAI_API_KEY=shim-dev-key
codex \
  -m devstack-model \
  -c 'model_provider="llama_shim"'
```

## Verify

Check that the shim advertises WebSocket support:

```bash
curl -fsS http://127.0.0.1:8080/debug/capabilities | jq '.surfaces.responses.websocket'
```

Expected shape:

```json
{
  "enabled": true,
  "support": "local_subset",
  "endpoint": "/v1/responses",
  "sequential": true,
  "multiplexing": false
}
```

Run the direct WebSocket smoke:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 make responses-websocket-smoke
```

For the dev stack:

```bash
make devstack-up
make devstack-ci-smoke
make codex-cli-devstack-smoke
make codex-cli-shell-tool-smoke
make codex-cli-coding-task-smoke
make codex-cli-task-matrix-smoke
```

`make devstack-ci-smoke` is safe for CI because it avoids the local Codex CLI
binary. `make devstack-full-smoke` is the local heavy gate that includes the
real Codex CLI smoke paths.

For a running local shim that proxies to a real upstream, use the real-upstream
smoke before asking Codex to work on this repository:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
CODEX_MODEL=Kimi-K2.6 \
CODEX_PROVIDER=gateway-shim \
CODEX_API_KEY_ENV=GW_API_KEY \
GW_API_KEY=shim-dev-key \
make codex-cli-real-upstream-smoke
```

The default case list is `boot,read,write,bugfix`. To debug a stuck model
incrementally:

```bash
CODEX_REAL_SMOKE_CASES=boot,read \
make codex-cli-real-upstream-smoke
```

This smoke writes an isolated temporary Codex config under
`.tmp/codex-real-upstream-smoke/codex-home`, disables apps/web search/memories
for the run, keeps HTTP-first by default, and validates local file/test results
after Codex exits. Use [Codex Testing Plan](codex-testing-plan.md) for the
manual phase-by-phase version of the same gate.

The Codex smoke scripts now fail if Codex hits HTTP 405 from
`ws://.../v1/responses`, because WebSocket support is expected for this shim
configuration.

`make codex-cli-shell-tool-smoke` runs the real `codex exec` binary with
`features.unified_exec=false` and verifies that the stored request used the
fallback Codex function tool named `shell`, without `exec_command` or
`write_stdin`.

`make codex-cli-task-matrix-smoke` runs the real `codex exec` binary through
the shim over `openai_base_url` and verifies four deterministic tasks:

- single-file patch
- tiny Go bugfix with `go test ./...`
- deterministic `PLAN.md` creation
- two-file workspace update

## Boundaries

Current status is `Broad subset`:

- sequential `response.create` messages over one socket
- existing Responses streaming event payloads as JSON text frames
- stateful continuation through `previous_response_id`
- local `shell`, `apply_patch`, `file_search`, `web_search`,
  `image_generation`, remote MCP, and hosted/server `tool_search` devstack
  smoke coverage

Not claimed:

- multiplexing
- upstream WebSocket proxying
- exact hosted close-code, quota, cache, or tool-choreography parity
- Realtime API WebSocket compatibility

## Troubleshooting

If Codex falls back or fails with HTTP 405 from `ws://.../v1/responses`:

- confirm the running shim was rebuilt after the WebSocket change
- confirm `responses.websocket.enabled=true`
- if using a custom provider, confirm `model_providers.<id>.base_url` points to
  the shim `/v1` base URL and `supports_websockets=true`
- if using the quick built-in provider override, confirm `openai_base_url`
  points to the shim `/v1` base URL
- to force HTTP while debugging, set the custom provider
  `supports_websockets=false`

If Codex logs `chatgpt.com/backend-api/.../connectors` with HTTP 403:

- this is Codex sideband app/tool discovery, not shim `/v1` auth
- prefer the custom provider path above before debugging shim auth

If the shim returns HTTP 401:

- check `shim.auth.mode` in the shim config
- check that the environment variable named by `env_key` is set
- if the provider says `env_key = "GW_API_KEY"`, `OPENAI_API_KEY` is ignored
- if `static_bearer` is enabled, check that the value matches a configured
  bearer token

If the upstream returns `Unsupported tool type` for an official/OpenAI-SDK
Responses tool, for example `image_generation` or SDK `NamespaceTool` with a
Kimi/LiteLLM model group:

- this is an upstream/model capability gap, not a local Codex `shell` or
  `apply_patch` failure
- keep `responses.image_generation.backend=disabled` unless a shim-local image
  backend is configured
- add a model-scoped compatibility rule in the shim config so passively declared
  unsupported tools are not proxied to that model:

```yaml
responses:
  upstream_tool_compatibility:
    models:
      - model: Kimi-*
        disabled_tools:
          - image_generation
          - namespace
```

This rule only filters passive tool declarations before upstream proxying. If a
request explicitly sets `tool_choice` to a disabled tool, the shim returns a
local validation error instead of pretending the upstream can execute it.
SDK `NamespaceTool` is serialized as `{"type":"namespace"}` on the wire;
`namespace_tool` is also accepted as a config alias.

If Codex runs a command for a plain question and then prints no final answer,
check the shim startup log for:

```text
responses_codex_force_tool_choice_required=true
```

For weaker or non-OpenAI upstreams, that compatibility rewrite can be too
aggressive. It turns Codex `tool_choice=auto` into `required` on the first
tool-capable turn, so the model must emit a tool call even when the user asked
for a direct answer. Keep the global setting enabled for deterministic tool
smokes, but exclude that model:

```yaml
responses:
  codex:
    force_tool_choice_required: true
    force_tool_choice_required_disabled_models:
      - Kimi-*
```

After changing this shim config, restart the shim and retry the same Codex
prompt.

If the shim log repeatedly shows a structured-input validation retry for the
same model:

```text
retrying responses request with stringified input after structured-input validation failure
```

move that behavior into config so the first upstream call is already in the
shape that backend accepts:

```yaml
responses:
  codex:
    upstream_input_compatibility:
      models:
        - model: Kimi-*
          mode: stringify
```

Allowed modes are:

- `auto`: preserve structured Responses input and keep the existing retry
  fallback for known upstream validation errors.
- `stringify`: rewrite structured `input` into a plain text transcript before
  the first upstream request for matching models.
- `structured`: explicitly keep structured input for the matching model's
  first upstream request; the generic validation-error retry fallback still
  applies.

For a coding-task smoke where the model must call tools, temporarily remove the
model from `force_tool_choice_required_disabled_models` or run a separate shim
profile without that exclusion. This makes Codex `tool_choice=auto` proxy as
`required`, which is useful for proving tool-call behavior but can make normal
chat turns worse on weaker upstreams.

You can also test another coding model by changing only the Codex model slug:

```bash
GW_API_KEY=shim-dev-key \
codex \
  -m YOUR-CODER-MODEL \
  -c 'model_provider="gateway-shim"' \
  -c 'model_reasoning_effort="high"' \
  -c 'model_reasoning_summary="auto"'
```

If the shim serves Codex model metadata, add a matching
`responses.codex.model_metadata.models[]` entry for that slug so Codex does not
fall back to generic model defaults.

If Codex was launched with `openai_base_url` and the failure looks like a mixed
OpenAI/shim issue:

- switch to a named custom provider
- set `wire_api = "responses"`
- start with `supports_websockets = false`
- retry the same task before changing shim code

If `previous_response_id` fails:

- prefer `store=true` for cross-socket continuation
- for `store=false`, continue from the most recent response on the same active
  WebSocket connection
- treat `previous_response_not_found` as a signal to start a new chain or resend
  the required context
