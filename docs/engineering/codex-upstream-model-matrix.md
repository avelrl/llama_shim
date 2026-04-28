# Codex Upstream Model Matrix

Last updated: April 28, 2026.

Status: practical Codex-through-shim model notes. This is not a general model
benchmark and not an OpenAI API parity claim. Scores below reflect only the
observed shim/Codex smoke and external-tester behavior captured in this repo.

## Source References

Official and implementation references used for this matrix:

- [OpenAI Codex](https://github.com/openai/codex): client behavior, Responses
  provider config, model metadata, and local tool execution.
- [Kimi CLI](https://github.com/MoonshotAI/kimi-cli): Kimi/Moonshot request
  shaping, tool schema handling, thinking behavior, and coding-session defaults.
- [Qwen Code](https://github.com/QwenLM/qwen-code): Qwen/DashScope provider
  shape, `extra_body.enable_thinking`, and background thinking behavior.
- [OpenCode](https://github.com/anomalyco/opencode): provider-specific defaults
  used as a second implementation reference for Qwen-like and other
  OpenAI-compatible providers.
- [Responses Compatibility External Tester](responses-compatibility-external-tester.md):
  real-upstream API-surface ledger for DeepSeek, Kimi, and Qwen.
- [Codex Testing Plan](../guides/codex-testing-plan.md): manual and automated
  Codex smoke procedure.

Provider documentation:

- DeepSeek:
  [First API Call](https://api-docs.deepseek.com/),
  [Chat Completion](https://api-docs.deepseek.com/api/create-chat-completion/),
  [Tool Calls](https://api-docs.deepseek.com/guides/tool_calls),
  [Thinking Mode](https://api-docs.deepseek.com/guides/thinking_mode),
  [JSON Output](https://api-docs.deepseek.com/guides/json_mode),
  [Models and Pricing](https://api-docs.deepseek.com/quick_start/pricing).
- Kimi/Moonshot:
  [API Overview](https://platform.kimi.ai/docs/overview),
  [Chat Completion](https://platform.kimi.ai/docs/api/chat),
  [Kimi K2.6 Quickstart](https://platform.kimi.ai/docs/guide/kimi-k2-6-quickstart),
  [Thinking Model Guide](https://platform.kimi.ai/docs/guide/use-kimi-k2-thinking-model),
  [Tool Calls](https://platform.kimi.ai/docs/guide/use-kimi-api-to-complete-tool-calls),
  [Agent Support](https://platform.kimi.ai/docs/guide/agent-support).
- Qwen:
  [Qwen Code Architecture](https://qwenlm.github.io/qwen-code-docs/en/developers/architecture/),
  [Qwen Code Model Providers](https://qwenlm.github.io/qwen-code-docs/en/users/configuration/model-providers/),
  [Qwen Code Configuration](https://qwenlm.github.io/qwen-code-docs/en/users/configuration/).

## Rating Key

Ratings are intentionally coarse:

- `5`: reliable in the current repo-owned checks.
- `4`: good enough for practical use, with known warnings or retries.
- `3`: useful, but requires provider-specific config and manual smoke before
  larger tasks.
- `2`: narrow or flaky; use only for targeted diagnosis.
- `1`: not recommended for this path yet.

## Current Matrix

| Model / upstream | Codex context metadata | API compatibility through shim | Codex coding smoke | Tool-call discipline | Config complexity | Best current use | Main risks |
| --- | --- | ---: | ---: | ---: | --- | --- | --- |
| DeepSeek V4 Pro | `1000000` | 5 | 4 | 4 | Medium | External compatibility gate, structured API checks, Codex smoke baseline after config. | Reasoning/tool-choice interactions can fail on some variants; Codex can still print reasoning-delta warnings. |
| Qwen3.6-35B-A3B | `262144` conservative tested default | 4 | 4 | 3 | Low | Practical Codex coding smoke and manual Codex task testing. | Needs Chat `json_schema` downgrade for shim-local helpers; can make Unix/macOS command-shape mistakes such as GNU-only flags. |
| Kimi K2.6 | `262144` | 4 | 3 | 3 | High | Long-context Codex experiments after model-specific config; useful for agent behavior comparison. | Most provider-specific workarounds: schema sanitization, larger output budget, invalid-tool-argument retry/final-text fallback, and careful thinking handling. |

## Interpretation

DeepSeek V4 Pro is the strongest current API compatibility gate. It passed the
strict external tester profile after the Chat compatibility fixes, so use it
when the question is whether the shim's broad OpenAI-compatible surface still
works.

Qwen3.6-35B-A3B is currently a good practical Codex smoke model. It passed
boot/read/write/bugfix through `/v1/responses`, including local file changes
and Go test repair. Its failures are more often model-command quality or
Codex-side reasoning warnings than shim transport failures.

Kimi K2.6 works, but it is the least plug-and-play of the three for Codex task
loops. It benefits from the richest compatibility block and should be treated
as a tuned-provider path, not as the default first model for manual smoke.

Context metadata is Codex-facing budgeting data served by the shim model catalog,
not a new OpenAI API claim. DeepSeek is set to the current 1M context line,
Kimi K2.6 to 262144 tokens, and Qwen3.6 to the conservative 262144-token path
that has been smoked through this gateway. Raise Qwen only after the exact
upstream deployment proves a larger long-context setting end to end.

## Recommended Order

Use this order when qualifying a new shim change:

1. Run deterministic repo tests and devstack checks.
2. Run the strict external tester against DeepSeek V4 Pro or the current
   strongest API-compatibility upstream.
3. Run `make codex-cli-real-upstream-smoke` against Qwen3.6-35B-A3B for a
   practical Codex coding loop.
4. Run Kimi K2.6 only after the same change is green on the simpler model path,
   or when the change is specifically about Kimi/Moonshot behavior.
5. Move to manual Codex testing only after the automated smoke is green.

## Manual Smoke Scope

Manual smoke is now the right next step for model quality. Keep the tasks small:

- ask for one plain answer;
- read one file;
- create one tiny file;
- patch one known one-line bug;
- run one bounded test command;
- stop and inspect logs after the first ambiguous failure.

Do not use manual smoke to upgrade compatibility labels by itself. It is an
operator-confidence check. Compatibility labels still require docs-backed,
test-backed, and, where needed, fixture-backed evidence.
