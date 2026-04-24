# Codex CLI

## What Works

Codex CLI can target `llama_shim` through the OpenAI provider path.

Use WebSocket mode when:

- the shim exposes `responses.websocket.enabled=true`
- Codex is configured with `openai_base_url` pointing at the shim `/v1`
- or the selected custom Codex provider has `supports_websockets=true`

HTTP `POST /v1/responses` and SSE streaming remain available. WebSocket is an
additional transport, not a replacement.

## Shim Config

In the shim config, keep Responses WebSocket enabled:

```yaml
responses:
  mode: prefer_local
  websocket:
    enabled: true
```

This is the default in `config.yaml.example`.

## Codex Config

There are two practical ways to point Codex at the shim.

### Quick Built-In OpenAI Provider Override

This is the path used by the repo smoke scripts. Codex keeps using its built-in
OpenAI provider, but sends requests to the shim URL:

```toml
model = "devstack-model"
approval_policy = "never"
sandbox_mode = "workspace-write"

openai_base_url = "http://127.0.0.1:8080/v1"
```

For the dev stack, use port `18080`:

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

### Explicit Custom Provider

Use this when you want a named provider entry in Codex config:

```toml
model = "devstack-model"
model_provider = "llama_shim"
approval_policy = "never"
sandbox_mode = "workspace-write"

[model_providers.llama_shim]
name = "llama_shim"
base_url = "http://127.0.0.1:8080/v1"
wire_api = "responses"
env_key = "OPENAI_API_KEY"
supports_websockets = true
```

For the dev stack, use port `18080` instead:

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
supports_websockets = true
```

If shim ingress auth is disabled, `OPENAI_API_KEY` can be any non-empty value.
If shim ingress auth uses `static_bearer`, set `OPENAI_API_KEY` to one of the
configured shim bearer tokens.

## Run Codex

### Interactive Session

With config saved in `~/.codex/config.toml`:

```bash
export OPENAI_API_KEY=shim-dev-key
codex
```

For the dev stack without editing `~/.codex/config.toml`:

```bash
export OPENAI_API_KEY=shim-dev-key
codex \
  -m devstack-model \
  -c 'openai_base_url="http://127.0.0.1:18080/v1"'
```

For a normal shim on port `8080`:

```bash
export OPENAI_API_KEY=shim-dev-key
codex \
  -m "<model>" \
  -c 'openai_base_url="http://127.0.0.1:8080/v1"'
```

### One-Off Command

Use `codex exec` for non-interactive checks:

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
  -C /path/to/workspace \
  -m devstack-model \
  -c 'openai_base_url="http://127.0.0.1:18080/v1"' \
  -c 'approval_policy="never"' \
  -c 'sandbox_mode="workspace-write"' \
  'Make the requested small code change, run the relevant test, and summarize.'
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
make responses-websocket-smoke
make codex-cli-devstack-smoke
make codex-cli-coding-task-smoke
```

The Codex smoke scripts now fail if Codex hits HTTP 405 from
`ws://.../v1/responses`, because WebSocket support is expected for this shim
configuration.

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
- if using the quick built-in provider override, confirm `openai_base_url`
  points to the shim `/v1` base URL
- if using a custom provider, confirm `model_providers.<id>.base_url` points to
  the shim `/v1` base URL and `supports_websockets=true`

If `previous_response_id` fails:

- prefer `store=true` for cross-socket continuation
- for `store=false`, continue from the most recent response on the same active
  WebSocket connection
- treat `previous_response_not_found` as a signal to start a new chain or resend
  the required context
