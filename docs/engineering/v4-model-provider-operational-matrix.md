# V4 Model/Provider Operational Matrix

Last updated: May 20, 2026.

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
the historical Codex curation reference. Use
[V4 Provider Ops Runbook](../v4-provider-ops-runbook.md) as the compact
end-to-end provider/model operating procedure.

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
| `gpu/qwen3-coder-30b` | Fast local candidate | Not promoted to provider matrix gate yet | Certification evidence: recent baseline passes; expanded `cert-20260519T144415Z` failed 17/18 on `multi_file`; bench-lite `cert-20260519T162448Z` is 19/20 with repeated `patch_after_context` miss. `command_recovery` stays fixed. | `32768` context, `shell_command`, `apply_patch=freeform` | Local llama.cpp-compatible GPU alias resolves to upstream runtime id `coder30b`. Chat cleanup downgrades JSON Schema to JSON mode plus schema instruction for sampler compatibility. Raw tool markup now surfaces as classified `502 malformed_backend_response`, not hidden `500`. | Usable as a supervised local coding assistant. Do not use as unattended release gate until bench-lite is clean or an operator explicitly accepts the repeated partial-edit risk. |
| `gpu/qwen3-coder30b-q5km` | Stronger local Codex candidate | Not promoted to provider matrix gate yet | Model certification `cert-20260519T220117Z` passed all focused Codex profiles: baseline 11/11, expanded 18/18, bench-lite 20/20, for 49/49 final tasks. Verdict is `codex_retry_dependent`: 6 failed attempts were recovered by retries. | `32768` context, `shell_command`, `apply_patch=freeform` | Local GPU alias resolves to upstream runtime id `coder30b-q5km`. Chat exactness has a weak `chat.basic` result in the external API tester, but Responses/Codex paths are usable. Retry evidence includes raw-tool-markup repair, malformed backend responses, one transport failure, and one timeout. | Best current local Codex candidate. Usable for supervised coding and larger local diagnostics; do not call it strict-clean or unattended release-gate clean until retry rate is lower. |
| `gpu/gemma4-e4b` | Local API/chat candidate | Not promoted to provider matrix gate yet | API certification `cert-20260520T071938Z` passed all external tester rows. Codex baseline `cert-20260520T072422Z` failed 10/11: `bugfix_go` printed a correct apply-patch body as final text instead of calling the edit tool. | `32768` context, `shell_command`, `apply_patch=freeform` | Local GPU alias resolves to upstream runtime id `gemma4-e4b`. The Codex run also showed llama.cpp/Gemma transcript fragility: upstream Chat returned parser errors on histories containing raw `<|tool_call>...`, `assistant{selection:assistant}`, and similar assistant/tool markers. | Keep as a chat/API model, not a Codex candidate. Revisit only if a future Gemma-specific transcript cleanup/stringify mode is worth testing; do not promote as an unattended coding gate. |
| `gpu/omnicoder-9b` | Local small-coder experiment | Not promoted to provider matrix gate yet | External tester partial run `llama_shim_omnicoder_9b_20260520_142219` showed core Responses paths working, but `previous_response_id` follow-ups hit upstream `context canceled` and returned `502 transport_error`. Codex not run. | `32768` context, `shell_command`, `apply_patch=freeform` | Local GPU alias resolves to upstream runtime id `omnicoder-9b`. The May 20 shim log shows `store=true` and conversations working independently, so the current blocker is continuation through upstream Chat transport, not basic storage/conversation surface. | Keep experimental. Do not run broader Codex profiles or promote until a focused API certification shows stable `previous_response_id`; spending more time is low priority for a 9B model while stronger local candidates exist. |
| `gpu/glm47-flash-opus-reasoning` | Local reasoning/API candidate | Not promoted to provider matrix gate yet | API certification `cert-20260520T115344Z` was 27/28: only native Chat streaming returned `H` instead of `HELLO`, while Responses stream, structured output, tools, custom tools, `store=true`, conversations, and `previous_response_id` paths passed. Codex baseline `cert-20260520T120358Z` failed 8/11: `basic_patch` checker diff plus `bugfix_go`/`bugfix_mixed` malformed `apply_patch` responses. | `32768` context, `shell_command`, `apply_patch=freeform` | Local GPU alias resolves to upstream runtime id `glm47-flash-opus-reasoning`. API behavior is promising for reasoning/content cleanup diagnostics, but Codex failures show patch-contract instability rather than broad transport failure. | Keep experimental. Useful for API/chat diagnostics and reasoning-content stress; do not use as a Codex gate until baseline patch/tool-call behavior is clean. |
| `gpu/qwen35-27b-opus-reasoning` | Local reasoning/API candidate | Not checked yet | Evidence pending. | `32768` context, `shell_command`, `apply_patch=freeform` | Local GPU alias resolves to upstream runtime id `qwen35-27b-opus-reasoning`. Use it as a larger Qwen-family reasoning candidate, not as a known Codex gate. | Treat as experimental; run only focused certification before any broader Codex profile. |
| `gpu/gpt-oss-20b` | Fast local API/assistant candidate | API certification: `cert-20260519T182616Z` failed only native Chat stream; later manual tester evidence confirmed Responses-ready behavior. | Codex baseline `cert-20260519T193742Z` failed 6/11: `basic_patch`, `bugfix_go`, `bugfix_mixed`, `command_recovery`, and `multi_file` were not clean. | `32768` context, `shell_command`, `apply_patch=freeform` | Uses GPT-OSS Chat cleanup for `developer` role and default thinking. The 20B runtime can emit native Chat stream chunks with no final text, while the shim-owned Responses stream remains usable. Codex failures are mostly model/tool-loop behavior: planning text, missing file edits, timeouts, and incomplete workspace state. | Keep for API/Responses smoke and lightweight assistant use. Do not use as a Codex gate or unattended coding model while stronger local coder candidates exist. |
| `gpu/gpt-oss-120b` | Larger local API candidate, Codex experimental | API certification `cert-20260519T201957Z` passed: Chat and Responses both ready, including Chat stream. | Codex baseline `cert-20260519T202427Z` failed 7/11: `bugfix_go`, `bugfix_mixed`, `command_recovery`, and `multi_file` remained red. | `32768` context, `shell_command`, `apply_patch=freeform` | Better than 20B on API and Codex baseline, but still has transport/context-canceled noise and incomplete edit/tool-loop behavior. Failures include no file change, missing final markers, and files described as updated before they actually match. | Keep as an experimental local assistant/model-quality comparison point. Do not promote as an unattended Codex gate until baseline is clean. |

## Decision Order

Use this order for practical work:

1. **One broad compatibility gate:** run DeepSeek.
2. **Chat-only bridge or backend-projection change:** run MiMo after DeepSeek.
3. **Provider cleanup or Moonshot/Kimi-style schema change:** run Kimi as a
   second tuned-provider check.
4. **Raw markup or model-discipline hardening:** run Qwen manually or as a
   non-promoting diagnostic.
5. **Fast local drafting:** prefer `gpu/qwen3-coder30b-q5km` for supervised
   local coding. Use `gpu/qwen3-coder-30b` only with diff/test supervision;
   neither local coder variant is a strict-clean release gate yet.
6. **Local chat/API only:** keep `gpu/gemma4-e4b` for chat/API experiments,
   not Codex tool-loop work.
7. **New local candidates:** skip broader Codex time on `gpu/omnicoder-9b`
   unless a focused API run proves stable `previous_response_id`. Keep
   `gpu/glm47-flash-opus-reasoning` as an API/chat diagnostic rather than a
   Codex gate. Use `gpu/qwen35-27b-opus-reasoning` as the next
   diagnostic/reasoning-content check until it has curated evidence.

Do not compare model quality across old task counts. Promote only current
auto-run facts after curation.

## Commands

Fast operator check for every current matrix row:

```bash
V4_PROVIDER_CONFIG_DOCTOR_FLAGS="-strict-env -require-matrix -strict-codex-metadata" \
  make v4-provider-config-doctor

SHIM_BASE_URL=http://127.0.0.1:8080 \
  make v4-provider-matrix-smoke
```

Then curate the local artifacts into a short operator verdict:

```bash
make v4-provider-matrix-curate
make v4-provider-ops-report
```

Limit it to one or more aliases when iterating:

```bash
SHIM_BASE_URL=http://127.0.0.1:8080 \
  V4_PROVIDER_MATRIX_MODELS="deepseek/deepseek-v4-pro xiaomi/mimo-v2.5-pro" \
  make v4-provider-matrix-smoke
```

Focus the curation report on one model:

```bash
V4_PROVIDER_MATRIX_CURATE_MODEL=deepseek/deepseek-v4-pro \
  make v4-provider-matrix-curate
V4_PROVIDER_OPS_MODEL=deepseek/deepseek-v4-pro \
  make v4-provider-ops-report
```

Set `V4_PROVIDER_MATRIX_RUN_CODEX_DOCTOR=1` when the same pass should also
run the isolated Codex config doctor for each model. The matrix smoke writes a
single report under `.tmp/v4-provider-matrix-smoke/` and nests the per-model
provider-routing and V4-preflight artifacts below it. The curation report reads
those artifacts without calling upstream providers and writes
`.tmp/v4-provider-matrix-curation/<curation-id>/summary.md` plus `summary.json`.
The ops report reads provider doctor, provider matrix curation, and Codex eval
curation artifacts without calling upstream providers. It writes
`.tmp/v4-provider-ops/<ops-id>/summary.md` plus `summary.json` and is the
preferred final read-only operator view before updating this document.

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
- `make v4-provider-config-doctor` passes for the intended operator matrix, or
  the only remaining issues are explicitly accepted non-gating warnings
- `make v4-provider-matrix-smoke` passes for the row, or a deliberately scoped
  single-model matrix run passes for that row
- `make v4-provider-matrix-curate` classifies the latest row as
  `release_gate_ok`
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
- `make v4-provider-ops-report` has no config, matrix, or required-Codex drift
  for the promoted alias

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
