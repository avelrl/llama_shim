# Add A Provider/Model Alias

Last updated: May 10, 2026.

Status: operator guide for adding and testing a shim-owned `provider/model`
alias. This is not an OpenAI API parity claim.

## Goal

Use this guide when adding a new upstream model such as
`deepseek/deepseek-v4-flash`, `xiaomi/mimo-v2.5`, or a local runtime model.

The goal is to make the alias usable from every shim entrypoint without
manually swapping base URLs, raw model ids, or auth variables:

- OpenAI-shaped HTTP clients send the public `provider/model` alias.
- Codex eval and Codex config use the same public alias.
- The shim resolves provider base URL, provider token, and `upstream_model`.
- Operator evidence records the public alias first and raw upstream ids only as
  implementation detail.

## Terms

| Term | Meaning |
| --- | --- |
| Provider id | The left side of the public alias, configured in `llama.providers[].id`. |
| Public alias | The client-facing model id, always `provider/model`. |
| Provider model | The model slug configured under `llama.providers[].models[].model`. |
| Upstream model | The raw model id sent to the provider. Defaults to provider model unless `upstream_model` is set. |
| Provider token env | Server-side env var named by `llama.providers[].bearer_token_env`. The shim uses it when calling that provider. |
| Shim ingress auth | The API key clients use when calling the shim, such as `GW_API_KEY` for Codex eval. This is separate from provider tokens. |
| Codex metadata | Shim-owned model capability data under `responses.codex.model_metadata.models[]`. It is used by Codex metadata routes, not ordinary `/v1/models`. |

## Before Editing

Check the live provider catalog or provider docs first. Do not promote model ids
from memory or from old logs.

For OpenAI/Codex-facing wording, re-check:

- `openapi/llms.txt`
- OpenAI Codex config reference for `model`, `model_provider`,
  `model_providers.<id>.base_url`, `env_key`, and `supports_websockets`
- Responses WebSocket mode docs if the alias is expected to support Codex
  WebSocket mode

The official Codex config currently documents custom `model_providers.<id>`
with `base_url`, `env_key`, `requires_openai_auth`, stream retry settings, and
`supports_websockets`. The Responses WebSocket docs describe continuation with
`response.create`, `previous_response_id`, and incremental `input` items.

## Add The Alias

Add or extend a provider in `config.yaml`:

```yaml
llama:
  providers:
    - id: example
      base_url: https://example.invalid
      bearer_token_env: EXAMPLE_API_KEY
      models:
        - model: example-coder
          upstream_model: Example-Coder-2026
```

Rules:

- Use lowercase stable provider ids for public aliases.
- Use the shortest stable provider model slug for the alias.
- Set `upstream_model` only when the raw upstream id differs from the public
  model slug.
- Omit `bearer_token_env` only for unauthenticated local providers.
- Add matching placeholders to `.env.example`; never commit real secrets.

## Add Codex Metadata

If Codex will use the model, add `responses.codex.model_metadata.models[]` with
the public alias:

```yaml
responses:
  codex:
    model_metadata:
      models:
        - model: example/example-coder
          display_name: Example Coder
          context_window: 131072
          max_context_window: 131072
          shell_type: shell_command
          apply_patch_tool_type: freeform
          visibility: list
          supported_in_api: true
```

Keep this conservative. Wrong capability metadata is worse than missing
metadata because Codex may choose an unusable transport or tool shape.

## Add Compatibility Rules Only When Needed

Provider-specific cleanup hooks are internal shim behavior. They should adapt
requests sent upstream without changing the public OpenAI-shaped request the
shim accepts.

Common rule families:

- `chat_completions.upstream_compatibility.models[]`
- `responses.codex.upstream_input_compatibility.models[]`
- `responses.upstream_tool_compatibility.models[]`

When provider routing is enabled, rule patterns target the resolved
`upstream_model`, not the public alias. For example, a public
`svgun/qwen-3.6` alias with upstream model `Qwen3.6-35B-A3B` should match
`Qwen*`.

Do not add broad cleanup rules preemptively. Add them after a trace, smoke, or
Codex eval shows a concrete request-shape problem.

## Validate One Alias

Start with config and routing before Codex:

```bash
V4_PROVIDER_CONFIG_DOCTOR_FLAGS="-strict-env -require-matrix -strict-codex-metadata" \
  make v4-provider-config-doctor

V4_PROVIDER_MATRIX_MODELS="example/example-coder" \
  make v4-provider-matrix-smoke

V4_PROVIDER_MATRIX_CURATE_MODEL=example/example-coder \
  make v4-provider-matrix-curate

V4_PROVIDER_OPS_MODEL=example/example-coder \
  make v4-provider-ops-report
```

Then run Codex only for aliases that passed provider routing:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
  CODEX_PROVIDER=gateway-shim \
  CODEX_API_KEY_ENV=GW_API_KEY \
  CODEX_MODEL=example/example-coder \
  CODEX_EVAL_MODELS="example/example-coder" \
  make codex-eval-auto

CODEX_EVAL_CURATE_MODEL=example/example-coder make codex-eval-curate
V4_PROVIDER_OPS_MODEL=example/example-coder make v4-provider-ops-report
```

Use the external Responses compatibility tester against one stable alias, not
against every candidate model. That tester is for shim compatibility; Codex and
provider matrix runs are for model/provider behavior.

## Promotion Levels

| Level | Required evidence |
| --- | --- |
| Configured candidate | Alias exists in `config.yaml`; provider token env is documented. |
| Routing candidate | Provider matrix smoke passes for `/v1/responses`, `/v1/chat/completions`, token counting, compact, and fail-closed unknown model checks. |
| Codex candidate | Codex auto run has curated evidence for the public alias. Retry-dependent results are allowed but must stay visible. |
| Release gate | `make v4-provider-ops-report` returns a release-gate decision for the alias. |

## Local Models

For Ollama, LM Studio, llama.cpp, MLX, or another local runtime:

- Use a local provider id such as `local-ollama`, `local-lmstudio`, or
  `local-llamacpp`.
- Keep `base_url` loopback-only unless there is an explicit remote-runtime
  design.
- Start with provider-routing and preflight smokes.
- Run Codex eval only after basic tool-call discipline is proven.
- Do not infer vision, tool-call, or long-context support from the model family
  name; verify the exact served quant/model id.

## Documentation Checklist

Update these files when the alias changes status:

- `.env.example`
- `config.yaml`
- [Work Queue](../work-queue.md)
- [V4 Model/Provider Operational Matrix](../engineering/v4-model-provider-operational-matrix.md)
- [V4 Provider Ops Runbook](../v4-provider-ops-runbook.md), only if the workflow changes
- [Codex Upstream Model Matrix](../engineering/codex-upstream-model-matrix.md), after Codex evidence exists

Keep generated run artifacts out of committed docs unless they are sanitized
fixtures needed to justify a compatibility claim.

## Final Checks

For docs/config-only changes:

```bash
git diff --check
make lint
```

For code or behavior changes, also run the focused Go tests and `go test ./...`
as required by `AGENTS.md`.
