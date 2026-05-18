# V3 Upstream Provider Routing

Status: `Implemented`.
Last updated: May 8, 2026.

This is a shim-owned V3 routing extension. It is not an OpenAI-hosted parity
claim.

## Summary

The implemented slice adds multi-provider upstream routing for model-bearing
requests on:

- `POST /v1/responses`
- `GET /v1/responses` WebSocket `response.create` messages
- `POST /v1/responses/input_tokens` when the request has `model` and is
  proxied upstream
- `POST /v1/responses/compact` when the request has `model` and is proxied
  upstream
- `POST /v1/chat/completions`
- `GET /v1/models` as a live-checked configured public-model catalog when
  `llama.providers` is enabled

Clients send `model` in `provider/model` form. The shim resolves the provider
and public model name through `llama.providers`, then calls the selected
OpenAI-compatible upstream with shim-owned credentials and an upstream model
name. Unknown provider/model mappings fail closed with `400`.

The legacy single-upstream `llama.base_url` behavior remains unchanged when
`llama.providers` is empty.

## Official Docs Check

Checked on May 8, 2026 against the local docs index at `openapi/llms.txt`,
OpenAI Docs MCP OpenAPI specs, and the current official pages:

- [Responses create](https://platform.openai.com/docs/api-reference/responses/create)
- [Chat Completions create](https://platform.openai.com/docs/api-reference/chat/create)
- [Models list](https://platform.openai.com/docs/api-reference/models/list)
- [Responses WebSocket mode](https://developers.openai.com/api/docs/guides/websocket-mode)
- [Counting tokens](https://developers.openai.com/api/docs/guides/token-counting)
- [Conversation state compaction note](https://developers.openai.com/api/docs/guides/conversation-state#compaction)
- [Codex configuration reference](https://developers.openai.com/codex/config-reference)

The official OpenAI API exposes `POST /v1/responses` and
`POST /v1/chat/completions` create endpoints with a normal string `model`
parameter and bearer-token authentication examples. It also exposes
`GET /v1/models` as a list endpoint with `id`, `object`, `created`, and
`owned_by` model objects. The WebSocket guide defines `response.create`
messages whose payload mirrors Responses create, including `generate:false`
warmups. The token-counting guide exposes `POST /v1/responses/input_tokens`,
and the conversation-state/compaction docs point explicit compaction control at
`/responses/compact`. The official contract does not define a public
`provider` field or `provider/model` routing syntax for these endpoints.

Therefore `provider/model` is a shim-owned request convention. The shim must
not document it as hosted OpenAI behavior, and OpenAPI wording must keep it
separate from the OpenAI-compatible base contract when the feature is
implemented.

The Codex configuration reference is useful as a nearby operator precedent for
provider IDs, provider `base_url`, and environment-backed provider keys. It is
not a source of truth for OpenAI API wire semantics.

## Config Shape

YAML shape:

```yaml
llama:
  base_url: http://127.0.0.1:8081
  providers:
    - id: qwen
      base_url: http://127.0.0.1:8081
      bearer_token_env: QWEN_API_KEY
      models:
        - model: qwen-coder
          upstream_model: qwen-coder
    - id: deepseek
      base_url: https://api.deepseek.example/v1
      bearer_token_env: DEEPSEEK_API_KEY
      models:
        - model: coder
          upstream_model: deepseek-coder
```

`.env` shape:

```bash
QWEN_API_KEY=...
DEEPSEEK_API_KEY=...
```

`llama.providers[].id` is the public provider prefix used by clients.
`llama.providers[].models[].model` is the public model suffix used after the
slash. `upstream_model` is optional; when it is omitted, the stripped model
suffix is forwarded upstream.

Provider tokens are referenced by environment-variable name and are never
stored directly in YAML examples, OpenAPI examples, fixtures, or logs. Omit
`bearer_token_env` for unauthenticated local upstreams such as local Ollama or
LM Studio adapters.

`base_url` may be either the upstream origin root or an OpenAI-SDK-style base
ending in `/v1`; the shim normalizes generation paths so `/v1` is not doubled.

`llama.base_url` remains the legacy behavior and fallback when
`llama.providers` is empty.

When `llama.providers` is non-empty, routing is explicit only: there is no
default provider, wildcard model, fuzzy alias, or fallback to
`llama.base_url` for malformed `provider/model` values. That avoids accidental
cross-provider calls when a client typo occurs.

## Runtime Behavior

Routing applies to request paths where the client explicitly selects a model:

- `POST /v1/responses`
- `GET /v1/responses` WebSocket `response.create`
- `POST /v1/responses/input_tokens` when the request includes `model` and the
  selected route proxies upstream
- `POST /v1/responses/compact` when the request includes `model` and the
  selected route proxies upstream
- `POST /v1/chat/completions`

The shim parses `model` by splitting on the first `/`. Both provider and model
suffix must be non-empty. The suffix may contain additional `/` characters; the
provider prefix is always the segment before the first slash. The pair must
match an explicit configured `llama.providers[].id` and `models[].model`.

Resolution rules:

1. `model: "qwen/qwen-coder"` resolves provider `qwen` and public model
   `qwen-coder`.
2. The outbound upstream request uses the selected provider `base_url`.
3. The outbound JSON body rewrites `model` to `upstream_model` when configured,
   otherwise to the stripped public model suffix.
4. Unknown providers, unknown model suffixes, empty provider/model parts, and
   ambiguous config fail with `400 invalid_request_error`.
5. Failed routing resolution must not make an upstream request.

`responses.upstream_transport=chat_completions` remains in scope: the public
Responses request can use `provider/model`, while the internal upstream
generation request to `/v1/chat/completions` uses the resolved provider,
credentials, and upstream model name.

WebSocket `response.create` uses the same model validation and provider
resolver as HTTP Responses create. This includes `generate:false` warmups:
they do not call upstream, but they still validate that `model` is a configured
public alias and store the public `provider/model` value in the warmup
response. Generated WebSocket turns bridge through the internal HTTP/SSE
Responses create path and route upstream calls through the resolved provider.

`/v1/responses/input_tokens` and `/v1/responses/compact` are derived Responses
endpoints. When a request has `model` and the route proxies upstream, the
upstream call uses the resolved provider URL, credentials, and `upstream_model`.
When a request omits `model`, the shim does not fall back to legacy
`llama.base_url` while provider routing is enabled. A supported local helper,
such as the deterministic `input_tokens` estimate, stays local. A local helper
that still requires model context, such as standalone compaction, returns a
local validation error instead of choosing a hidden provider.

`GET /v1/models` returns public IDs in `provider/model` form when provider
routing is enabled, but it first queries each configured provider's upstream
`/v1/models` with that provider's credentials. A configured public model is
listed only when its resolved `upstream_model` is present in the provider's
live model catalog. The route still does not expose or merge raw upstream model
IDs from providers; it only validates the configured public aliases. If no
configured public model can be live-confirmed, the route returns `503`.
Legacy single-upstream behavior remains unchanged when `llama.providers` is
empty.

Non-model resource routes are not provider-routed. `/v1/conversations`,
conversation items, files, vector stores, containers, stored response retrieval,
stored response input-items retrieval, and delete/list operations remain
resource/state routes. A response attached to a conversation routes through
`POST /v1/responses` because that request carries `model`; the conversation
resource itself does not imply a provider.

## Auth And Secret Handling

Provider credentials are shim-owned upstream credentials. The routed upstream
request must use the configured provider token and must not forward inbound
client upstream-auth headers such as `Authorization`, `Api-Key`, or
`X-Api-Key`.

This slice has only a client-authorization placeholder. Clients may send
requests without a client auth token until the separate ingress
authorization/tenant layer is designed and implemented.

`X-Client-Request-Id` and similar non-secret correlation headers may continue
to propagate when existing shim policy allows it.

Readiness and capabilities output must redact tokens and should report
provider status by provider ID only.

Provider readiness probes run concurrently and use a dedicated remote-provider
timeout budget so several configured remote providers do not consume one short
local-backend timeout sequentially. In provider-routing mode, `/readyz`
requires at least one configured provider to answer `/v1/models` and list at
least one configured upstream model. `/debug/capabilities.probes.providers`
reports per-provider readiness, and `backends.components[]` uses those
provider-specific probes instead of one aggregate llama probe for every
provider row.

## Operator Environment Roles

Provider token variables such as `DEEPSEEK_API_KEY`, `XIAOMI_API_KEY`, or
`SVGUN_API_KEY` are server-side shim credentials referenced by
`llama.providers[].bearer_token_env`. They are used only when the shim calls
configured upstream providers.

Codex eval variables are client-side harness settings. Keep
`CODEX_PROVIDER=gateway-shim`; set `CODEX_MODEL` or `CODEX_EVAL_MODELS` to
the public `provider/model` alias being tested; set `CODEX_API_KEY_ENV` and
the corresponding value such as `GW_API_KEY` to the shim ingress token. With
shim auth disabled, `GW_API_KEY` may be a placeholder.

Codex model metadata must use the same public aliases. Entries under
`responses.codex.model_metadata.models[].model` should be values such as
`svgun/qwen-3.6` or `deepseek/deepseek-v4-pro`, not hidden upstream names such
as `Qwen3.6-35B-A3B`. That keeps Codex's metadata probe, eval model IDs, and
the shim's provider resolver on the same public contract.

Model-specific upstream compatibility rules are the opposite: they are
upstream-facing because they run after provider resolution has rewritten the
request model to the resolved `upstream_model`. Keep entries such as
`chat_completions.upstream_compatibility.models[].model`,
`responses.upstream_tool_compatibility.models[].model`, and
`responses.codex.upstream_input_compatibility.models[].model` on upstream
patterns like `deepseek-*`, `Kimi-*`, or `Qwen*`. Do not use public aliases
there unless the public alias and upstream model are intentionally identical.

External Responses compatibility tester variables are also client-side. Point
`OPENAI_BASE_URL` at the shim, set `TESTER_MODEL` to one stable public
`provider/model` alias, and use `OPENAI_API_KEY` as shim ingress auth for the
tester. It should not be a provider upstream token unless the tester is
intentionally bypassing the shim.

`shimctl probe` is different: it is a direct single-upstream calibration tool
using `llama.base_url`, `SHIMCTL_PROBE_MODEL`, and
`SHIMCTL_PROBE_BEARER_TOKEN`. It does not use `llama.providers` routing in
this slice, so run it one direct upstream/model at a time.

## Public Model Semantics

The client-facing model remains the public `provider/model` value where the
shim owns the response shape:

- non-stream JSON responses
- shim-transformed SSE events
- stored local request/response metadata
- `/v1/responses/{id}` and stored Chat Completions reads when the shim owns
  the local record

The upstream model name is an internal transport detail. It may be visible only
on raw proxy paths where the shim does not transform the upstream response.

Compatibility matching, model-specific upstream sanitization, and
provider-specific request bridges run after provider resolution and use the
stripped or configured upstream model name.

Codex-facing model metadata keeps matching by the public model ID because that
is what the Codex client is configured to send. Upstream-compatibility
transforms, raw-markup repair prompts, and transport quirks run on the resolved
provider ID plus upstream model name. This keeps operator-facing model aliases
stable without duplicating compatibility rules for every public prefix.

## Implemented Slice

1. Add normalized config structs for `llama.providers`, provider model maps,
   token environment lookup, duplicate detection, and legacy `llama.base_url`
   fallback.
2. Add a provider resolver that accepts a public model string and returns the
   provider ID, base URL, upstream model, credential source, and public model.
3. Route `/v1/chat/completions` through the resolver before constructing the
   upstream request.
4. Route `/v1/responses` upstream generation through the same resolver,
   including the Chat-backed `responses.upstream_transport=chat_completions`
   path.
5. Route WebSocket `response.create` model validation through the same
   resolver for both generated turns and `generate:false` warmups.
6. Route `POST /v1/responses/input_tokens` and
   `POST /v1/responses/compact` through the resolver on upstream-proxy paths
   when the request includes `model`, while keeping model-less supported local
   requests local or failing them locally when model context is required.
7. Rewrite outbound request bodies so upstream sees the stripped model suffix
   or configured `upstream_model`.
8. Restore the public `provider/model` value in shim-owned JSON, SSE, and
   WebSocket response frames before returning data to the client.
9. Ensure stored local records keep the public model while internal logs and
   metrics can include provider ID and redacted routing class.
10. Add configured-public-model output for `GET /v1/models` when
   `llama.providers` is enabled.
11. Extend readiness/capabilities with redacted provider visibility. The first
   slice may check all configured providers for readiness; any future
   per-provider degraded mode needs a separate operator-policy design.
12. Update OpenAPI, config docs, examples, and compatibility-matrix wording
   in the same implementation change.

## Test Plan

Regression coverage:

- config normalization tests for duplicate providers, duplicate model mappings,
  empty IDs, optional unauthenticated providers, and legacy empty-provider
  fallback
- provider resolver coverage for valid routing, invalid `provider/model`
  shapes, unknown provider, unknown model, and `upstream_model` override
- Chat Completions integration tests for non-stream and stream routing
- Responses integration tests for upstream Responses routing and Chat-backed
  `responses.upstream_transport=chat_completions`
- Responses derived-endpoint tests for routed `input_tokens`/`compact` proxy
  calls and model-less derived requests with no hidden legacy upstream call
- WebSocket integration tests for `response.create` generated routing,
  `generate:false` alias validation, and public model restoration
- Models-list tests proving provider routing live-checks upstream catalogs,
  returns only configured public IDs whose `upstream_model` is available, uses
  provider auth, and keeps legacy single-upstream behavior unchanged without
  providers
- regression tests that inbound client `Authorization`, `Api-Key`, and
  `X-Api-Key` do not reach routed upstream requests
- tests that unknown mappings return `400` before any upstream call
- tests that shim-owned responses and stored records keep public
  `provider/model`
- tests that Codex model metadata can target the public model ID while
  upstream compatibility rules see provider/upstream routing context
- legacy tests proving current `llama.base_url` behavior stays unchanged when
  `llama.providers` is empty

Implementation verification should include `go test ./...`, `make lint`, and
`git diff --check`.

Live smoke coverage:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
UPSTREAM_PROVIDER_ROUTING_MODEL=deepseek/deepseek-v4-pro \
GW_API_KEY=shim-dev-key \
make upstream-provider-routing-smoke
```

The target wraps `scripts/upstream-provider-routing-smoke.sh` and is the
preferred operator check after editing `.env`, `config.yaml`, provider tokens,
or public model aliases. It writes artifacts under
`.tmp/upstream-provider-routing-smoke/<run-id>/`:

```text
summary.md
summary.json
run.env
healthz.response.json
readyz.response.json
capabilities.response.json
models.response.json
responses_create.request.json
responses_create.response.json
chat_completions_create.request.json
chat_completions_create.response.json
input_tokens.request.json
input_tokens.response.json
compact.request.json
compact.response.json
shim.log.slice
shim-log-diagnostics.md
```

Model selection is one public `provider/model` alias at a time. The script
checks `/debug/capabilities`, live `GET /v1/models`, routed Responses, routed
Chat Completions, derived Responses endpoints, fail-closed unknown model
behavior, and model-less derived request boundaries.

By default, `/readyz` is strict for the selected provider matrix only when no
configured provider is usable; unrelated provider degradation is captured in
`/debug/capabilities.probes.providers` and the backend registry. Derived
endpoint provider failures are warnings. This matches the practical split:
provider availability should be visible without making one unrelated transient
block every route, while third-party OpenAI-compatible providers may not
implement `/v1/responses/input_tokens` or `/v1/responses/compact`. To make
derived endpoints strict for providers that claim support:

```bash
UPSTREAM_PROVIDER_ROUTING_REQUIRE_DERIVED=1 make upstream-provider-routing-smoke
```

If a configured but unrelated provider is temporarily down and the operator
only wants to verify one alias route, use capture-only readiness:

```bash
UPSTREAM_PROVIDER_ROUTING_REQUIRE_READYZ=0 make upstream-provider-routing-smoke
```

## Assumptions And Non-Goals

- This is shim-owned V3 work, not OpenAI-hosted parity.
- Client authorization, tenant isolation, per-provider quotas, and end-user
  policy enforcement are separate follow-up layers.
- No wildcard matching, provider-only defaults, or fuzzy model aliases are part
  of this slice.
- Non-model resource routes are not routed by `provider/model` unless a later
  design defines how stored state maps back to providers.
- Upstream WebSocket proxying is not implemented. The shim terminates
  WebSocket locally and bridges generated turns through its internal HTTP/SSE
  Responses create path.
- No upstream fixtures are required for this planning document. Fixture needs
  must be re-evaluated when code changes affect streaming, replay, or exact
  proxy behavior.
