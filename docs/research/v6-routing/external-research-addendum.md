# V6 Routing Runtime - external research addendum

Дата: 2026-04-28  
Цель: найти идеи из GitHub, arXiv, runtime-frameworks, security/eval tooling и понять, что стоит добавить в наш V6 план.

Вердикт: **добавлять есть что, но аккуратно.** Лучшие находки не меняют главную архитектуру V6: один public OpenAI-shaped assistant, private workers, client-owned tool loop. Они усиливают план вокруг routing evals, durable event log, guardrails, security, observability и будущего adaptive routing. Иначе говоря: не ставим турбину на тележку Stage 0, но заранее прокладываем рельсы.

---

## Что было просмотрено

### Routing research and router repos

- RouteLLM: learned strong/weak model routing, OpenAI-compatible serving/eval framework.  
  Source: https://github.com/lm-sys/RouteLLM and https://arxiv.org/abs/2406.18665
- FrugalGPT: prompt adaptation, approximation, LLM cascades for cost/performance trade-off.  
  Source: https://arxiv.org/abs/2305.05176
- RouterBench: benchmark and dataset with 405k+ inference outcomes for multi-LLM routing.  
  Source: https://arxiv.org/abs/2403.12031 and https://github.com/withmartian/routerbench
- LLMRouterBench: 2026 benchmark, 400k+ instances, 21 datasets, 33 models, 10 baselines. Important warning: many routers do not reliably beat simple baselines under unified evaluation.  
  Source: https://arxiv.org/abs/2601.07206 and https://github.com/ynulihao/LLMRouterBench
- Dynamic Model Routing and Cascading survey: routing taxonomy: difficulty, preference, clustering, uncertainty, RL, multimodal, cascade.  
  Source: https://arxiv.org/html/2603.04445v2
- MasRouter: routing for multi-agent systems: collaboration mode, role allocation, LLM routing.  
  Source: https://github.com/yanweiyue/masrouter and https://arxiv.org/abs/2502.11133
- Arch-Router: preference-aligned routing via human-defined Domain-Action policies.  
  Source: https://ar5iv.labs.arxiv.org/html/2506.16655
- Router-R1: multi-round routing and aggregation via RL, with format, correctness and cost rewards.  
  Source: https://openreview.net/pdf?id=DWf4vroKWJ
- vLLM Semantic Router and Aurelio Semantic Router: fast semantic/intent routing, often positioned as a pre-router or gateway layer.  
  Sources: https://github.com/vllm-project/semantic-router and https://github.com/aurelio-labs/semantic-router
- LiteLLM and Portkey: gateway-level fallbacks, retries, budgets, observability.  
  Sources: https://docs.litellm.ai/docs/proxy/reliability and https://docs.portkey.ai/docs/product/ai-gateway/fallbacks

### Agent runtimes and orchestration

- OpenAI Agents SDK: orchestration, handoffs, guardrails, tracing, SDK-owned tools/state/approvals.  
  Sources: https://developers.openai.com/api/docs/guides/agents, https://openai.github.io/openai-agents-python/tracing/, https://openai.github.io/openai-agents-python/guardrails/
- LangGraph durable execution: persistence, deterministic replay, side-effect wrapping.  
  Source: https://docs.langchain.com/oss/python/langgraph/durable-execution
- AutoGen termination conditions: explicit stop predicates for multi-agent runs.  
  Source: https://microsoft.github.io/autogen/stable/user-guide/agentchat-user-guide/tutorial/termination.html
- OpenHands and OpenHands Software Agent SDK: event-sourced state, deterministic replay, typed event stream, sandbox/workspace abstractions, security analyzer, confirmation policy, pause/resume.  
  Sources: https://arxiv.org/html/2511.03690v1 and https://github.com/OpenHands/OpenHands/blob/main/openhands/runtime/README.md
- SWE-agent: Agent-Computer Interface design, linter-on-edit, concise file viewer/search commands.  
  Source: https://github.com/SWE-agent/SWE-agent/blob/main/docs/background/aci.md
- Claude Code agent teams: parallel specialized workers, lead synthesis, plan approval, hooks, warnings around coordination overhead and file conflicts.  
  Source: https://code.claude.com/docs/en/agent-teams

### Security, protocol, observability and evals

- MCP specification and MCP sampling: clients keep control over model access, selection and permissions; sampling should have human review and must be tied to an originating request.  
  Sources: https://modelcontextprotocol.io/specification/2025-06-18 and https://modelcontextprotocol.io/specification/draft/client/sampling
- MCP security best practices: confused deputy, token passthrough, SSRF, session hijacking, scope minimization.  
  Source: https://modelcontextprotocol.io/docs/tutorials/security/security_best_practices
- OWASP LLM Top 10 and OWASP Prompt Injection: prompt injection, insecure output handling, sensitive info disclosure, insecure plugin design, excessive agency, model DoS.  
  Sources: https://owasp.org/www-project-top-10-for-large-language-model-applications/ and https://genai.owasp.org/llmrisk/llm01-prompt-injection/
- OpenTelemetry GenAI semantic conventions: model/agent spans, events, token usage metrics.  
  Sources: https://opentelemetry.io/docs/specs/semconv/gen-ai/ and https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/
- OpenAI Evals, Inspect AI, Promptfoo: evals, agent/tool evals, sandboxed evals, red teaming and CI gates.  
  Sources: https://developers.openai.com/api/docs/guides/evals, https://inspect.aisi.org.uk/, https://www.promptfoo.dev/docs/red-team/

---

## Summary: what should change in our plan

### Add to Stage 0 or Stage 0-exit

1. **Model and worker capability registry.** Do not hard-code routing around model names only.
2. **Router eval harness.** Every router policy must beat deterministic baselines before it can become active.
3. **Durable private event log.** Private trace should become replayable, event-sourced state, not just a debug blob.
4. **Guardrail matrix.** Handoffs, private workers, hosted tools and public output mapping each need their own guardrail slot.
5. **Stuck detector and termination predicates.** Budget checks are not enough for multi-step private workers.
6. **Untrusted content envelopes and taint tracking.** Files, tool outputs and web/MCP content must be treated as data, not instructions.
7. **OTel-compatible private trace exporter.** Keep public surface clean, but make ops visibility standard-shaped.
8. **Contract and security evals in CI.** Golden snapshots, no-private-leak, public tool-loop ownership, prompt injection, budget exhaustion.

### Add to Stage 1/2

1. **Shadow semantic/router policies.** Semantic pre-routing is useful, but only as feature extraction or shadow scoring first.
2. **Private cascade policy.** Cheap worker first, escalate only on validated uncertainty/failure, with strict budget caps.
3. **Risk gates and human-in-loop hooks for future internal execution.** Stage 0 should not execute client tools, but future private tools need a review mechanism.
4. **MCP boundary plan.** MCP tools/resources/sampling/elicitation must not blur client-owned and shim-owned authority.
5. **Coder ACI improvements.** File viewer, compact repo search, linter before edit acceptance, explicit empty-output observations.

### Keep as Stage 3/research only

1. Active learned routing.
2. Multi-round RL router/aggregator.
3. Parallel private worker teams.
4. Semantic cache of final public outputs.
5. Auto-executed hosted or MCP tools.

---

## Recommendation R1 - Add `WorkerCapabilityRegistry`

**Priority:** P0/P1.  
**Why from research:** RouteLLM, Arch-Router, ECCOS, Router-R1 and vLLM Semantic Router all lean on the fact that routing needs model metadata: cost, latency, capability, modality, privacy, examples or policy descriptors. Hard-coded model names turn routing into a broom closet full of special cases.

**Что:** Add an internal registry describing every candidate worker and model.

```yaml
worker_pool_version: wpv_2026_04_28_001
workers:
  - worker_role: executive
    worker_model: gpt-main-exec
    capability_class: public_response_owner
    supports:
      tool_calling: true
      strict_json_schema: true
      vision_input: false
      patch_generation: false
      patch_validation: false
      streaming_private_deltas: false
    constraints:
      max_context_tokens: 128000
      max_output_tokens: 8192
      data_zone: standard
      may_call_private_tools: false
      may_emit_public_tool_calls: true
    economics:
      input_cost_per_mtok_usd: 0.0
      output_cost_per_mtok_usd: 0.0
      latency_slo_ms_p95: 6000
    routing_tags:
      domains: [general, planning, tool_decision]
      action_types: [final_answer, public_tool_call]

  - worker_role: coder
    worker_model: gpt-code-private
    capability_class: private_code_specialist
    supports:
      tool_calling: false
      strict_json_schema: true
      patch_generation: true
      patch_validation: true
      streaming_private_deltas: false
    constraints:
      max_context_tokens: 128000
      sandbox_required: true
      may_emit_public_tool_calls: false
      may_touch_public_state: false
    routing_tags:
      domains: [code, diff, repository]
      action_types: [draft_patch, explain_code]
```

**Почему:** A learned router, static router and eval harness all need the same truth source. If it is scattered through code, routing bugs become archaeology.

**Зачем:** We can add models, swap providers, run evals and enforce compatibility without changing public API code. This also makes rollback possible: `policy_state_version` can point to `worker_pool_version`.

**Implementation notes:**

- Store registry as immutable versioned config.
- Include model support for `Responses`, strict schema, tool calling, multimodal input, hosted/internal tools, context length and data retention zone.
- Do not expose this registry in public `/responses` objects.
- Record `worker_pool_version` on every private trace.

---

## Recommendation R2 - Add `RouterBaselineGate`

**Priority:** P0 for eval design, P1 for implementation.  
**Why from research:** LLMRouterBench reports that many routing methods, including recent and commercial routers, do not reliably beat a simple baseline under unified evaluation. RouterBench exists because router evaluation otherwise becomes hand-wavy fog.

**Что:** Before any non-static router becomes active, compare it against baselines on a replay dataset.

Required baselines:

```text
strongest_only: always use highest-capability eligible worker
cheapest_valid: use cheapest worker that supports all required request features
static_rules: current deterministic Stage 0 rules
random_valid: random eligible worker, fixed seed, sanity baseline
shadow_candidate: proposed router, scoring only
oracle_upper_bound: best observed candidate per task, offline only
```

Required pass gates:

```text
No public snapshot regressions: 0 tolerated for P0 compatibility corpus
No private trace leaks: 0 tolerated
No unsupported-request false accepts: 0 tolerated
No client-owned tool loop takeover: 0 tolerated
Cost or latency improvement: configured threshold vs static_rules
Quality: must not be worse than static_rules beyond agreed margin
Variance: fixed-seed repeatability for deterministic request classes
```

**Почему:** Learned routers are impressive until a trivial static rule quietly outperforms them. The baseline gate keeps us from buying a shiny compass that points to a vending machine.

**Зачем:** Enables safe Stage 2 routing experiments without destabilizing Stage 0 compatibility.

---

## Recommendation R3 - Add `RouterEvalDataset` generated from private traces

**Priority:** P1.  
**Why from research:** RouterBench and LLMRouterBench are useful public corpora, but our runtime has special constraints: public Responses compatibility, hidden workers, tool-loop ownership, no-private-leak, patch validation, idempotency.

**Что:** Define a private replay/eval row format.

```jsonc
{
  "eval_row_version": 1,
  "source_trace_id": "trace_...",
  "request_fingerprint": "sha256:...",
  "public_request_features": {
    "stream": false,
    "has_previous_response_id": true,
    "has_conversation": false,
    "has_public_tools": true,
    "tool_choice_mode": "auto",
    "strict_output_schema": false,
    "input_modalities": ["text"],
    "estimated_input_tokens_bucket": "medium"
  },
  "routing_context_features": {
    "requires_code_reasoning": true,
    "requires_patch": false,
    "requires_client_tool": true,
    "untrusted_file_content_present": false,
    "complexity_tier": 2
  },
  "constraints": {
    "max_private_calls": 3,
    "max_total_cost_usd": 0.10,
    "latency_slo_ms": 8000,
    "allowed_worker_roles": ["executive", "coder", "repair"]
  },
  "observed_outcome": {
    "public_snapshot_hash": "sha256:...",
    "status": "completed",
    "public_tool_call_count": 1,
    "total_usage_tokens": 4200,
    "latency_ms": 3470,
    "leak_check_passed": true,
    "schema_valid": true,
    "human_quality_label": null
  }
}
```

**Почему:** Generic routing quality is not enough. A route is bad if it returns a decent answer but breaks public item IDs, emits hidden worker content, or grabs a public tool from the client’s hands.

**Зачем:** Lets us replay policies offline and compare cost/latency/quality while protecting the public contract.

**Implementation notes:**

- Store only redacted inputs unless eval environment is approved for raw data.
- Keep `store=false` semantics separate from internal operational traces. If private eval data is retained, that needs operator policy and TTL.
- Allow synthetic rows for unsupported/edge requests, not only production traces.

---

## Recommendation R4 - Add private `CascadePolicy`

**Priority:** P1/P2.  
**Why from research:** FrugalGPT shows the value of LLM cascades, and the 2026 survey treats routing and cascading as complementary. For V6, cascade is most useful inside private workers, not on the public surface.

**Что:** Add a policy that can attempt a cheaper/faster private worker first, then escalate only if validators say it failed.

```ts
type CascadeStep = {
  step_id: string;
  worker_role: "executive" | "coder" | "repair" | "critic";
  worker_model: string;
  max_tokens: number;
  stop_if: string[];       // schema_valid, patch_valid, confidence_high
  escalate_if: string[];   // schema_invalid, patch_failed, uncertainty_high
};

type CascadePolicy = {
  policy_id: string;
  max_steps: number;
  max_total_cost_usd: number;
  max_latency_ms: number;
  steps: CascadeStep[];
};
```

**Почему:** “Always use strongest worker” wastes cost on easy tasks. “Always use cheap worker” burns quality on hard tasks. Cascade gives a staircase, not a trapdoor.

**Зачем:** Reduces private worker cost while preserving public behavior.

**Guardrails:**

- Cascade must never execute public client-owned tools.
- Escalation must be based on validators, not just model self-confidence.
- Public `usage` must include all internal calls according to our chosen usage semantics.
- Public output must be generated or approved by executive owner, not emitted directly from a lower-level worker.

---

## Recommendation R5 - Add optional semantic pre-router, but keep it non-authoritative first

**Priority:** P2.  
**Why from research:** vLLM Semantic Router and Aurelio Semantic Router use fast semantic/intent classification to avoid expensive LLM routing decisions. Useful, but unsafe as a sole source of truth for API compatibility.

**Что:** Add `SemanticFeatureExtractor` or `ShadowSemanticRouter` that emits features and scores but does not choose active worker in Stage 0/1.

```jsonc
{
  "semantic_router": {
    "enabled": true,
    "mode": "features_only",
    "route_scores": {
      "code_patch": 0.81,
      "general_answer": 0.14,
      "research": 0.05
    },
    "top_route": "code_patch",
    "threshold_met": true,
    "embedding_model_version": "emb_...",
    "index_version": "sridx_..."
  }
}
```

**Почему:** Semantic routing is fast, but it can be brittle on adversarial or ambiguous inputs. API compatibility must be decided by `AliasCompatibilityGate`, not embeddings.

**Зачем:** We get cheap features for future policy training without handing the keys to a semantic raccoon.

---

## Recommendation R6 - Upgrade private traces into a durable event log

**Priority:** P0/P1.  
**Why from research:** LangGraph durable execution emphasizes persistence, deterministic replay and wrapping side effects. OpenHands V1 emphasizes event-sourced state, immutable configuration and deterministic replay.

**Что:** Make private run state append-only and replayable.

```ts
type PrivateRuntimeEvent = {
  event_id: string;
  trace_id: string;
  parent_event_id?: string;
  sequence: number;
  created_at: string;
  event_type:
    | "compatibility_decided"
    | "public_state_loaded"
    | "routing_decided"
    | "worker_call_started"
    | "worker_call_completed"
    | "worker_call_failed"
    | "validator_result"
    | "public_mapper_emitted"
    | "waiting_for_public_tool_output"
    | "run_completed"
    | "run_failed"
    | "run_cancelled";
  config_refs: {
    runtime_version: string;
    worker_pool_version: string;
    prompt_template_version: string;
    policy_state_version?: string;
  };
  idempotency: {
    request_hash: string;
    idempotency_key?: string;
    replay_safe: boolean;
  };
  redaction: {
    raw_payload_blob_ref?: string;
    redacted_payload_blob_ref?: string;
    contains_secrets: boolean;
  };
};
```

**Почему:** Plain traces are great for looking backward. Durable event logs also let us resume, replay, audit, dedupe and run evals.

**Зачем:** Idempotency, retries, cancellations and future background mode become engineering problems, not séance sessions.

**Replay rules:**

- Never re-run a completed model call during replay if its result is already persisted.
- Never re-execute side effects during replay. In Stage 0, public tools are client-owned, so the runtime should only replay emitted public tool call items.
- Config versions must be stored with the event. Replaying with a different worker pool or prompt template is a separate eval, not operational recovery.
- Private events remain invisible to `/input_items` and public streaming.

---

## Recommendation R7 - Add a `GuardrailMatrix`

**Priority:** P0/P1.  
**Why from research:** OpenAI Agents SDK docs separate input, output and tool guardrails, and note that handoffs and hosted/built-in tools do not use the same guardrail pipeline as ordinary function tools. For V6, private delegation/handoffs are exactly where silent bypass can appear.

**Что:** Define where each guardrail runs and what it can block.

```yaml
guardrails:
  input:
    runs_before: compatibility_gate
    blocks: [malformed_request, known_abuse_pattern]
  compatibility:
    runs_before: private_worker_calls
    blocks: [unsupported_feature_combo]
  private_handoff:
    runs_before: delegate_worker
    blocks: [role_not_allowed, tool_ownership_violation, budget_violation]
  private_tool:
    runs_before: shim_owned_tool_execution
    blocks: [path_policy_violation, secret_exfiltration_risk, network_not_allowed]
  validator:
    runs_after: private_worker_output
    blocks: [schema_invalid, patch_invalid, private_trace_leak]
  public_mapper:
    runs_before: response_persist
    blocks: [unknown_public_item_type, private_event_leak, unstable_call_id]
  output:
    runs_before: public_response_return
    blocks: [schema_invalid, sensitive_disclosure, policy_violation]
```

**Почему:** “We have guardrails” is too vague. Guardrails need wiring points. Otherwise handoffs, hosted tools or private worker outputs slip through the side window.

**Зачем:** Compatibility and security become testable. Every guardrail has a before/after point, failure behavior and trace event.

---

## Recommendation R8 - Add `StuckDetector` and explicit termination predicates

**Priority:** P1.  
**Why from research:** AutoGen exposes termination conditions such as max messages, token usage, timeout, handoff, external termination and function-call termination. OpenHands also calls out stuck detection as an SDK feature area.

**Что:** Add a single stop engine used by all private worker loops.

```ts
type TerminationSignal =
  | "max_private_calls"
  | "max_tokens"
  | "max_latency_ms"
  | "budget_exhausted"
  | "external_cancelled"
  | "public_tool_call_emitted"
  | "no_progress_detected"
  | "repeated_same_worker_output"
  | "repeated_validation_failure"
  | "cyclic_delegation"
  | "unsupported_action_requested";
```

**Почему:** Multi-worker systems can loop without “errors”: same patch fails validation, repair repeats, executive delegates back and forth, or a model keeps asking for a tool it cannot use.

**Зачем:** Predictable failure states, cleaner traces, cheaper bad runs.

**Stage 0 predicates:**

- max internal calls per public response
- max total private tokens
- max wall-clock duration
- repeated validator failure count
- public tool call emitted, stop and return to client
- external cancellation

---

## Recommendation R9 - Add risk gates and human-in-loop hooks for future shim-owned execution

**Priority:** P1 design, P2 implementation.  
**Why from research:** OpenHands uses a SecurityAnalyzer and ConfirmationPolicy. LangChain HITL pauses tool execution for approve/edit/reject. Claude Code checkpoints, hooks and agent teams show the same theme: autonomous coding needs reversible checkpoints and approval seams.

**Что:** Add `ActionRiskGate` to the design now, but keep Stage 0 strict: client-owned public tools are never executed by the shim.

```ts
type ActionRisk = "low" | "medium" | "high" | "unknown";

type ActionRiskDecision = {
  risk: ActionRisk;
  requires_confirmation: boolean;
  allowed_in_sandbox_only: boolean;
  reasons: string[];
  blocked: boolean;
};
```

**Почему:** Today’s private workers may only draft/validate. Tomorrow’s internal coder might want to run tests or apply patches in a sandbox. Add the seam before tools arrive.

**Зачем:** Future hosted/internal execution can be added without punching holes through the public tool contract.

**Rules:**

- For Stage 0, public function calls remain client-owned. The shim may emit them, but not execute them.
- For future private tools, high/unknown risk requires sandbox or human approval.
- Risk decisions are private trace events, not public response items.
- Checkpoints/snapshots must exist before destructive private actions.

---

## Recommendation R10 - Add MCP boundary plan

**Priority:** P1 before any MCP support.  
**Why from research:** MCP sampling allows servers to request LLM completions through clients, but the spec emphasizes client control over model access/selection/permissions, human review, and request association. MCP security docs call out confused deputy, token passthrough, SSRF, session hijacking and scope minimization.

**Что:** Add an MCP compatibility matrix separate from regular function tools.

```yaml
mcp:
  stage0:
    tools: reject
    resources: reject
    prompts: reject
    sampling: reject
    elicitation: reject
  future:
    tools:
      ownership: client_or_explicit_shim_owned
      auth: per_server_per_user
      token_passthrough: forbidden
      audit: required
    sampling:
      allowed: false_by_default
      requires_originating_request: true
      human_review: required_by_policy
      model_selection_owner: client_or_runtime_policy
    resources:
      taint: untrusted_content
      max_bytes: configured
```

**Почему:** MCP is powerful because it blends context, tools and sometimes nested model calls. That is exactly why it can blur authority lines.

**Зачем:** Prevents “MCP server made the model do it” from becoming our incident report title.

---

## Recommendation R11 - Add `UntrustedContentEnvelope` and taint tracking

**Priority:** P0 if file/web/MCP/tool outputs enter private worker context, otherwise P1.  
**Why from research:** OWASP describes direct and indirect prompt injection, with indirect attacks coming from external sources such as websites or files. OWASP also calls out insecure output handling, sensitive disclosure, insecure plugin design and excessive agency.

**Что:** Any external content entering private context must be wrapped as data with provenance and permissions.

```jsonc
{
  "content_type": "untrusted_external_text",
  "source": {
    "kind": "uploaded_file",
    "file_id": "file_...",
    "filename": "README.md"
  },
  "trust": {
    "instruction_authority": "none",
    "may_override_system": false,
    "may_request_tools": false,
    "may_exfiltrate": false
  },
  "limits": {
    "max_quote_tokens": 2000,
    "redact_secrets": true
  },
  "content": "..."
}
```

**Почему:** “Ignore previous instructions” inside a README must remain README text, not a tiny pirate captain inside the context window.

**Зачем:** Prompt-injection mitigations become structural, not prompt poetry.

**Tests:**

- Uploaded file tries to reveal private worker prompt.
- Tool output tries to force a public function call.
- Web/MCP content tries to change routing policy.
- Coder draft contains hidden instructions for executive.
- Public output includes private trace IDs or worker names.

---

## Recommendation R12 - Add OTel-compatible private trace exporter

**Priority:** P1.  
**Why from research:** OpenAI Agents SDK tracing captures LLM generations, tool calls, handoffs, guardrails and custom events. OpenTelemetry GenAI conventions define model/agent spans, events and token usage metrics.

**Что:** Keep internal trace schema as the source of truth, but export redacted spans/metrics using OTel-compatible names and attributes.

Suggested spans:

```text
v6.response.create
  v6.compatibility_gate
  v6.public_state_load
  v6.routing.select
  gen_ai.agent.execute executive
    gen_ai.client.inference executive_model
  gen_ai.agent.execute coder
    gen_ai.client.inference coder_model
  v6.validator.schema
  v6.public_surface_mapper
  v6.response.persist
```

Suggested metrics:

```text
v6.routing.decisions_total{policy, worker_role, result}
v6.routing.rejections_total{error_code}
v6.private_worker.calls_total{worker_role, model, result}
v6.public_mapper.leak_blocks_total
v6.idempotency.replays_total
v6.usage.total_tokens{alias, worker_role}
v6.budget.exhaustions_total{budget_type}
```

**Почему:** Custom trace blobs are useful for developers; standardized telemetry is useful for operations.

**Зачем:** Easier dashboards, alerts, cost tracking and incident investigation without exposing private worker chatter to API clients.

**Privacy:**

- OTel export must default to redacted payloads.
- Raw prompts/outputs require explicit operator opt-in.
- ZDR or equivalent policies must disable external trace export if required.

---

## Recommendation R13 - Add CI eval suite with contract, quality and security lanes

**Priority:** P0/P1.  
**Why from research:** OpenAI Evals positions evals as essential for reliable LLM apps. Inspect supports coding, agentic tasks, tool calling, sandboxing and external agents. Promptfoo supports red teaming, vulnerability scanning and CI/CD.

**Что:** Make evals a merge gate, not a “later maybe” shelf ornament.

```text
contract_golden:
  - public response object exact snapshots
  - call_id stability
  - previous_response_id continuation
  - conversation lock/version behavior
  - unsupported feature rejection

security_redteam:
  - indirect prompt injection in file/tool output
  - private worker prompt leakage
  - secret redaction
  - public tool ownership takeover attempts
  - path traversal and unsafe patch attempts

routing_eval:
  - static_rules vs cheapest_valid vs strongest_only vs shadow_router
  - latency/cost distribution
  - no public snapshot regressions
  - no unsupported false accepts

fault_injection:
  - upstream model timeout
  - worker schema invalid
  - validator crash
  - storage write failure
  - cancellation mid-run
  - duplicate idempotency key

coding_agent_eval_future:
  - patch applies
  - lint/test validation
  - no same-file conflict from parallel workers
  - sandbox-only execution
```

**Почему:** Manual review will miss the small compatibility splinters. Evals are the metal detector.

**Зачем:** Safe refactors, safe model swaps, safe policy upgrades.

---

## Recommendation R14 - Improve private coder with Agent-Computer Interface rules

**Priority:** P2, or P1 if coder worker becomes central.  
**Why from research:** SWE-agent emphasizes ACI design: a linter on edits, custom file viewer, compact repository search, explicit “no output” observations. These small interface choices have large behavior impact.

**Что:** Do not feed coder raw repo dumps and hope. Give it a narrow, reliable interface.

Recommended coder context/interface:

```text
- file viewer: windowed view, line numbers, max 100-200 lines
- repo search: returns files and concise match summaries first, not huge blobs
- edit proposal format: structured patch only
- linter/checker: run before accepting patch proposal into private trace
- empty output rule: return explicit observation when command produced no output
- test command allowlist: configured per project or sandbox
```

**Почему:** Good model plus bad interface equals spaghetti with a keyboard.

**Зачем:** Better patch quality, less token waste, easier validation.

**Boundary:** Private coder proposals still do not become public `apply_patch_call` unless public apply-patch contract is explicitly implemented.

---

## Recommendation R15 - Define parallel worker conflict rules before enabling teams

**Priority:** P2.  
**Why from research:** Claude Code agent teams are useful for independent parallel exploration and code review, but the docs warn about coordination overhead, token cost and file conflicts. MasRouter also points toward multi-agent routing, but that is a later-stage optimization.

**Что:** Add a future `ParallelWorkerCoordinator` spec now, disabled by default.

```yaml
parallel_workers:
  enabled: false
  allowed_task_types:
    - independent_research
    - competing_hypotheses
    - code_review_only
  disallowed_task_types:
    - same_file_editing
    - public_tool_call_execution
    - non-idempotent_side_effects
  merge_policy:
    coordinator_role: executive
    require_non_conflicting_refs: true
    require_validator_pass: true
    max_parallel_workers: 3
```

**Почему:** Parallel workers can be brilliant, but without file locks and merge policy they become three chefs salting one soup from different rooms.

**Зачем:** Stage 3 can use parallelism safely, with known constraints.

---

## Recommendation R16 - Separate provider failover from capability routing

**Priority:** P1/P2.  
**Why from research:** LiteLLM and Portkey focus on retries/fallbacks/load balancing; Portkey explicitly warns that one request may invoke multiple LLMs and that fallback LLMs must be compatible with the use case.

**Что:** Add two different mechanisms:

```text
Capability routing: choose the best worker/model for the task.
Provider failover: retry the same capability class when provider/deployment fails.
```

```yaml
fallback_policy:
  mode: provider_failover_only
  max_attempts: 2
  compatible_classes:
    private_code_specialist:
      - gpt-code-private-primary
      - gpt-code-private-secondary
  forbidden:
    - fallback_to_model_without_strict_schema_when_schema_required
    - fallback_to_model_without_tool_calling_when_public_tool_call_possible
    - fallback_across_data_zone_without_policy
```

**Почему:** A fallback that changes capabilities is not a retry; it is a hidden route change.

**Зачем:** Better reliability without silently breaking schema, tool or privacy assumptions.

---

## Recommendation R17 - Add cache policy, but start with routing features only

**Priority:** P2.  
**Why from research:** vLLM Semantic Router and gateway stacks often combine routing with caching. Caching can cut cost, but public output caching collides with privacy, state and `store=false` semantics.

**Что:** Stage 1/2 may cache:

- compatibility decisions by normalized request shape
- routing feature extraction results
- cost/latency estimates per worker pool version
- eval candidate outputs in approved offline datasets

Do not cache by default:

- final public response text
- private worker raw prompts/outputs
- content from `store=false` requests unless operator policy explicitly allows transient operational storage

**Почему:** Cache is a dragon egg. Warm and useful, but it hatches teeth if privacy is vague.

**Зачем:** Gains from caching without accidental data retention leaks.

---

## Recommendation R18 - Keep multi-round router/aggregator as Stage 3 research

**Priority:** P3.  
**Why from research:** Router-R1 frames routing as sequential decisions with internal thinking, model calls and aggregation. This is close to our executive/private-worker architecture, but it is too dynamic for Stage 0.

**Что:** Add a research-only `MultiRoundAggregatorPolicy` placeholder.

```yaml
multi_round_aggregator:
  enabled: false
  stage: research
  max_rounds: 3
  allowed_actions: [think_private, route_private, aggregate_private]
  forbidden_actions: [execute_public_tool, emit_private_trace_publicly]
  rewards:
    - public_schema_valid
    - no_private_leak
    - cost_below_budget
    - latency_below_slo
    - task_quality_label
```

**Почему:** Sequential routing is powerful, but it can create nondeterminism and cost drift.

**Зачем:** We capture the idea without putting it in the Stage 0 cockpit.

---

## Stage plan patch

### Stage 0 additions

Add these before or during Stage 0 implementation:

```text
- WorkerCapabilityRegistry
- RouterBaselineGate skeleton
- DurablePrivateEventLog
- GuardrailMatrix
- StuckDetector
- UntrustedContentEnvelope
- OTel redacted exporter skeleton
- CI eval suite: contract + leak + unsupported feature rejection + idempotency
```

Stage 0 still uses:

```text
routing.policy = static_rules
adaptive.enabled = false
semantic_router.mode = disabled_or_features_only
private_cascade.enabled = false unless deterministic and validator-gated
parallel_workers.enabled = false
mcp.enabled = false
shim_owned_tool_execution.enabled = false
```

### Stage 1 additions

```text
- Streaming mapper over public events only
- RouterEvalDataset export from private traces
- Shadow semantic router scoring
- Provider failover within compatible capability class
- Expanded OTel spans and dashboards
```

### Stage 2 additions

```text
- Private CascadePolicy with validator-gated escalation
- Shadow learned router
- RiskGate and human-in-loop hooks for explicitly shim-owned private tools
- MCP compatibility prototype behind feature flags
- Coder ACI improvements if internal code worker is active
```

### Stage 3 additions

```text
- Active adaptive routing after replay gates
- Multi-round router/aggregator experiments
- Parallel private workers with conflict/merge controls
- Semantic caching under strict retention policy
```

---

## Do not import these patterns blindly

1. **Do not claim “OpenAI-compatible” because a repo has an OpenAI-shaped endpoint.** Our public Responses mapping has stricter item, status, continuation, tool-call and usage semantics.
2. **Do not use learned routing in Stage 0.** LLMRouterBench’s warning about simple baselines is enough reason to keep Stage 0 boring.
3. **Do not let semantic routing override compatibility gates.** Semantic intent is a feature, not law.
4. **Do not let private handoffs bypass guardrails.** Handoff/delegation is its own risk path.
5. **Do not implement MCP sampling without client control and request association.** Nested LLM calls are a permission boundary, not a convenience.
6. **Do not cache final public outputs by default.** State, privacy and `store=false` semantics come first.
7. **Do not route public function execution into private workers.** The client owns public tools unless a separate explicit shim-owned hosted tool contract exists.
8. **Do not use LLM-as-judge as the only reward.** Contract checks, leak checks, schema checks and deterministic validators must dominate.

---

## Minimal implementation backlog

```text
P0
  [ ] Add WorkerCapabilityRegistry schema and versioning.
  [ ] Add DurablePrivateEventLog event types.
  [ ] Add GuardrailMatrix and public mapper leak guard.
  [ ] Add UntrustedContentEnvelope for files/tool outputs.
  [ ] Add StuckDetector predicates to private loop.
  [ ] Add contract golden tests and no-private-leak tests.

P1
  [ ] Add RouterEvalDataset export.
  [ ] Add RouterBaselineGate runner.
  [ ] Add OTel redacted exporter.
  [ ] Add provider failover only within capability class.
  [ ] Add Promptfoo/Inspect-style red team lane for indirect injection.
  [ ] Add coder ACI interface if private coder is used heavily.

P2
  [ ] Add semantic feature extractor in shadow mode.
  [ ] Add validator-gated CascadePolicy.
  [ ] Add risk gate and human approval hooks for future private tools.
  [ ] Add MCP boundary plan and disabled-by-default implementation stubs.

P3
  [ ] Explore active adaptive routing.
  [ ] Explore multi-round router/aggregator.
  [ ] Explore parallel private worker teams.
```

---

## Final recommendation

Fold **R1, R2 skeleton, R6, R7, R8, R11, R12 skeleton and R13** into the main V6 plan now. These are not fancy routing glitter; they are the floorboards. Put **R4, R5, R9, R10, R14, R16** into Stage 1/2. Keep **R15, R17, R18** as future research.

The biggest architectural nudge from the research is this:

> Treat routing as an evaluated, versioned, observable subsystem, not as a clever `if/else` buried in request handling.

That single shift will make V6 safer to evolve, easier to debug and much less likely to become a beautiful maze with no exit signs.
