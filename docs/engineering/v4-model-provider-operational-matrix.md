# V4 Model/Provider Operational Matrix

Last updated: May 8, 2026.

Status: operator-maintained V4 matrix for configured upstream providers and
Codex-through-shim model choices. This is not a general model benchmark and
not an OpenAI API parity claim.

## Scope Boundary

The public OpenAI API uses normal string `model` fields on Responses and Chat
Completions requests, and `GET /v1/models` lists model objects for the
account. The shim's `provider/model` syntax, provider plugin descriptors,
Codex model metadata, `/debug/capabilities`, and `/debug/traces` are
shim-owned operator surfaces.

Use this matrix to decide which configured provider/model to test next and how
to interpret the result. Use
[Codex Upstream Model Matrix](codex-upstream-model-matrix.md) as the detailed
evidence ledger and [V3 Codex Eval Curation](../v3-codex-eval-curation.md) as
the promotion procedure.

Official docs checked on May 8, 2026:

- [Migrate to the Responses API](https://developers.openai.com/api/docs/guides/migrate-to-responses)
- [Codex configuration reference](https://developers.openai.com/codex/config-reference)

## Current Operator Matrix

| Public model id | Role | Routing smoke | Codex auto evidence | Codex metadata | Provider/config notes | Operator decision |
| --- | --- | --- | --- | --- | --- | --- |
| `deepseek/deepseek-v4-pro` | Primary broad gate | Passed: `deepseek-deepseek-v4-pro_20260507T212227Z` | Passed: `deepseek-deepseek-v4-pro_full_20260507T213350Z`; baseline 11/11 with 1 retry, expanded 18/18 with 1 retry, bench-lite 20/20 strict-clean | `1000000` context, `shell_command`, `apply_patch=freeform` | DeepSeek-compatible Chat cleanup can remap `developer`, disable provider thinking, and downgrade Chat `json_schema` to JSON mode plus schema instruction. Match cleanup rules against resolved `upstream_model`. | Default real-upstream gate when one broad provider is enough. Keep retries visible; do not call it strict-clean unless the fresh run has zero retries. |
| `xiaomi/mimo-v2.5-pro` | Strict chat-transport candidate | Passed: `xiaomi-mimo-v2.5-pro_20260507T212400Z` | Passed: `xiaomi-mimo-v2.5-pro_full_20260507T221756Z`; baseline 11/11 strict-clean, expanded 18/18 with 2 retries, bench-lite 20/20 with 1 retry. Older `mimo-v2.5-pro_full_20260507T070500Z` was strict-clean across all three profiles. | `1048576` context, `shell_command`, `apply_patch=freeform` | Use `responses.upstream_transport=chat_completions` for chat-only gateways. This validates the shim-owned Responses-over-Chat path, not native upstream Responses parity. | Best practical chat-only regression model. Good second gate after DeepSeek, especially for bridge/projection changes. |
| `svgun/kimi-k2.6` | Tuned-provider comparison | Passed: `svgun-kimi-k2.6_20260507T212604Z` | Latest detailed Codex evidence is still under `Kimi-K2.6`: baseline 11/11 strict-clean, expanded/bench-lite green but retry-dependent across May 7 runs. A provider-alias auto run should be promoted before treating `svgun/kimi-k2.6` as the canonical Codex id. | `262144` context, `shell_command`, `apply_patch=freeform` | Kimi/Moonshot cleanup can use JSON-mode downgrade, `default_max_tokens`, tool-schema type fill, Moonshot schema sanitization, omitted empty assistant tool content, invalid-tool-argument retry, and final-text fallback. | Useful third gate for provider-specific cleanup and larger diagnostics. Do not make it the only release gate until provider-alias Codex evidence is current. |
| `svgun/qwen-3.6` | Experimental/manual and raw-markup stress | Passed: `svgun-qwen-3.6_20260507T212719Z` | Latest full auto under `Qwen3.6-35B-A3B` is not promotable: baseline 10/11, expanded 16/18, bench-lite 18/20 with retries and model/tool-contract failures. | `262144` conservative tested context, `shell_command`, `apply_patch=freeform` | Qwen cleanup should avoid generic `thinking`; Qwen-style gateways can require JSON-mode downgrade. Raw-markup detector covers several Qwen pseudo-tool forms, but remaining failures are still model/tool discipline and task quality. | Do not use as unattended regression gate. Keep it for manual smoke, raw-markup detector coverage, and provider-behavior experiments. |

## Decision Order

Use this order for practical work:

1. **One broad compatibility gate:** run DeepSeek.
2. **Chat-only bridge or backend-projection change:** run MiMo after DeepSeek.
3. **Provider cleanup or Moonshot/Kimi-style schema change:** run Kimi as a
   second tuned-provider check.
4. **Raw markup or model-discipline hardening:** run Qwen manually or as a
   non-promoting diagnostic.

Do not compare model quality across old task counts. Promote only current
auto-run facts after curation.

## Commands

Provider-routing smoke for one public alias:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
  UPSTREAM_PROVIDER_ROUTING_MODEL=<provider>/<model> \
  make upstream-provider-routing-smoke
```

V4 preflight plus nested provider-routing smoke:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
  V4_PREFLIGHT_PROVIDER_MODEL=<provider>/<model> \
  make v4-preflight-smoke
```

Codex auto for one public alias:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
  CODEX_PROVIDER=gateway-shim \
  CODEX_API_KEY_ENV=GW_API_KEY \
  CODEX_MODEL=<provider>/<model> \
  CODEX_EVAL_MODELS=<provider>/<model> \
  make codex-eval-auto
```

Then curate:

```bash
make codex-eval-curate
```

## Promotion Rules

Promote a model/provider row only when:

- the public `provider/model` alias is configured in `llama.providers[]`
- provider secrets are referenced by env names and not committed as values
- live provider-routing smoke passes `/v1/models`, Responses, Chat
  Completions, helper endpoints, and fail-closed unknown-provider checks
- `V4_PREFLIGHT_PROVIDER_MODEL=<provider>/<model> make v4-preflight-smoke`
  passes with no plugin-contract errors
- Codex-specific model metadata uses the same public model id that Codex sends
- compatibility cleanup rules match the resolved `upstream_model`, not the
  public alias
- `make codex-eval-auto` has a green baseline, with retry-dependent tasks
  called out explicitly
- `make codex-eval-curate` agrees with the interpretation

`expanded` and `bench-lite` are diagnostic stability profiles. They should be
green before using a model as a broad gate, but a retry-dependent diagnostic
profile does not automatically block a baseline promotion when the notes call
out the risk.

## Adding A New Model

1. Add the provider under `llama.providers[]`.
2. Add the provider token env name to `.env.example`.
3. Add `responses.codex.model_metadata.models[]` using the same public
   `provider/model` alias that Codex will send.
4. Add scoped compatibility rules only when the upstream rejects an otherwise
   useful OpenAI-shaped projection. Keep those rules matched to
   `upstream_model`.
5. Run provider-routing smoke.
6. Run V4 preflight with `V4_PREFLIGHT_PROVIDER_MODEL`.
7. Run `make codex-config-doctor` if the model will be used from Codex.
8. Run `make codex-eval-auto`, then `make codex-eval-curate`.
9. Update this matrix and the historical Codex upstream model matrix with
   interpreted facts, not raw `.tmp` dumps.

## What Not To Do

- Do not use this matrix to widen OpenAI API compatibility labels.
- Do not expose provider-specific knobs as public request fields.
- Do not treat Codex metadata as ordinary OpenAI `/v1/models` output.
- Do not turn model raw text that looks like a command or patch into local
  execution; safe repair can only re-ask for structured tool calls or final
  plain text.
- Do not hide retry-dependent passes. They are useful, but they are not
  strict-clean evidence.
