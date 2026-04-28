# V6 Routing Runtime - Mahoraga addendum

Review target: `https://github.com/pockanoodles/Mahoraga`  
Date: April 28, 2026  
Verdict: **useful as Stage 2 inspiration, not as Stage 0 architecture.**

Mahoraga is interesting because it treats routing as a learnable policy rather than a fixed rules table. For our `model=Auto` / V6 runtime, that is valuable, but only after the public Responses-compatible contract is stable. Stage 0 should remain boring and deterministic. Let the dragon sleep in a metrics jar before letting it steer the carriage.

---

## Executive summary

Mahoraga should influence the plan in three places:

1. Add an internal `RoutingPolicy` abstraction now, even if Stage 0 uses only deterministic routing.
2. Expand private trace schema so future adaptive routing has enough training/eval data.
3. Add Stage 2 evaluation gates before enabling any learned/bandit policy in production.

Do **not** copy Mahoraga as-is. It is a standalone orchestrator with its own UI/MCP shape, global-ish state, heuristic scoring, and worker execution model. Our runtime has a stricter goal: expose one public OpenAI-shaped assistant while private workers remain invisible.

---

## Comment M1 - Add `RoutingPolicy` as an internal extension point

**Priority:** P1 now, implementation can stay static until Stage 2.

**Что:** Add a policy interface between the executive/runtime and private worker selection.

```ts
type RoutingCandidate = {
  worker_role: "executive" | "coder" | "researcher" | "critic" | "repair";
  worker_model: string;
  capabilities: string[];
  estimated_cost?: number;
  estimated_latency_ms?: number;
};

type RoutingDecision = {
  policy: "static" | "rules" | "linucb" | "shadow";
  selected: RoutingCandidate;
  candidates: RoutingCandidate[];
  features?: Record<string, number | string | boolean>;
  scores?: Record<string, number>;
  decision_trace_id: string;
};

interface RoutingPolicy {
  select(ctx: PrivateRoutingContext, candidates: RoutingCandidate[]): RoutingDecision;
  observe?(decision: RoutingDecision, outcome: PrivateWorkerOutcome): void;
}
```

**Почему:** Mahoraga’s main useful idea is not “use LinUCB immediately”; it is “make routing policy swappable and observable.” Without an interface, adaptive routing later will worm its way through request handling, traces, retries, and accounting.

**Зачем:** Stage 0 can ship with `StaticRoutingPolicy`, while Stage 2 can test `ShadowBanditPolicy` without changing the public API surface.

Recommended Stage 0 behavior:

```text
model=Auto request
  -> CompatibilityGate
  -> PublicStateLoader
  -> StaticRoutingPolicy.select(...)
  -> PrivateWorkerRuntime
  -> PublicSurfaceMapper
```

The policy must never decide public tool execution ownership. It may choose private model workers only.

---

## Comment M2 - Add routing telemetry to private traces now

**Priority:** P0/P1, because trace shape is painful to retrofit.

**Что:** Extend the private trace schema with routing-specific fields.

```jsonc
{
  "routing": {
    "policy": "static",
    "decision_id": "rdec_...",
    "worker_role": "coder",
    "worker_model": "gpt-...",
    "candidate_count": 3,
    "features": {
      "input_tokens_bucket": "medium",
      "has_files": true,
      "requires_patch": true,
      "requires_client_tool": false,
      "complexity_tier": 2
    },
    "scores": null,
    "selected_reason": "rules: requires_patch -> coder"
  },
  "outcome": {
    "success": true,
    "latency_ms": 1840,
    "usage": { "input_tokens": 1200, "output_tokens": 340 },
    "quality_signals": {
      "schema_valid": true,
      "patch_applies": true,
      "public_leak_check_passed": true
    }
  }
}
```

**Почему:** Mahoraga’s learning loop needs context vector, selected agent, outcome, reward, and decision log. Even if we do not use online learning, those fields are gold dust for debugging bad routes, cost explosions, private-worker loops, and regressions.

**Зачем:** Enables later offline replay: “Would a smarter policy have chosen a cheaper/faster worker?” without exposing private traces to the public `/input_items` surface.

Important boundary: these fields stay private/operator-visible only. They must not become public Responses items unless explicitly mapped by `PublicSurfaceMapper`.

---

## Comment M3 - Use Mahoraga-style evaluation, but not Mahoraga-style quality as truth

**Priority:** P1 for eval harness, P2 for adaptive routing.

**Что:** Add an evaluation harness comparing routing strategies on golden tasks:

- `static_baseline`
- `rules_baseline`
- `shadow_bandit`, read-only scoring, no production steering
- later: `adaptive_bandit`, gated by feature flag

Each eval row should include request shape, public output snapshot hash, private worker chain, total usage, total latency, public-tool-call count, retry count, leak-check result, and human/automated quality labels.

**Почему:** Mahoraga reports strategy comparisons and uses benchmarks to warm-start routing. The useful part is the discipline of measuring routing policies independently from the runtime contract.

**Зачем:** We can improve cost/latency without accidentally changing the public API behavior. Public compatibility remains the boss; learned routing is only the gearbox.

Do not use heuristic quality as the only reward. For V6, quality reward must be assembled from contract-safe signals:

```text
reward candidates:
  + final output schema valid
  + no private trace leakage
  + public tool loop remained client-owned
  + patch proposal validates/applies in sandbox, if patch mode exists
  + lower normalized latency
  + lower normalized cost
  - hidden retry exhaustion
  - public error/status mismatch
  - cancellation ignored
  - unsupported request accidentally accepted
```

---

## Comment M4 - Keep adaptive routing behind a shadow mode first

**Priority:** P2.

**Что:** Add a Stage 2 mode where the learned router scores candidates but cannot select them.

```yaml
routing:
  policy: static
  shadow_policy:
    enabled: true
    type: linucb
    observe_public_success: false
    observe_private_outcomes: true
    emit_trace_scores: true
```

**Почему:** Mahoraga learns online from every task. That is powerful, but in an OpenAI-compatible shim it can silently alter behavior across requests. Hidden non-determinism makes golden tests flaky and compatibility bugs slippery.

**Зачем:** Shadow mode lets us collect evidence before changing behavior. Flip to active only after it beats the static baseline under fixed seeds and replay tests.

Stage 2 activation gate:

```text
Adaptive policy may become active only if:
  - no public snapshot regressions across golden corpus
  - no private trace leakage
  - no increase in unsupported-request false accepts
  - cost/latency improves by agreed threshold
  - policy state is versioned, seedable, exportable, and rollbackable
```

---

## Comment M5 - Add `PolicyState` versioning and rollback

**Priority:** P2, but design now.

**Что:** Treat learned policy state as versioned operational data, not application logic.

```jsonc
{
  "policy_state_version": 1,
  "policy_type": "linucb",
  "created_at": "2026-04-28T00:00:00Z",
  "updated_at": "2026-04-28T00:00:00Z",
  "model_alias": "Auto",
  "worker_pool_version": "wpv_...",
  "feature_schema_version": "fsv_...",
  "state_blob_ref": "blob://...",
  "training_window": {
    "from_trace_id": "trace_...",
    "to_trace_id": "trace_..."
  }
}
```

**Почему:** Mahoraga persists bandit state locally. That is fine for a local orchestrator, but our runtime needs reproducibility, rollout, and rollback.

**Зачем:** If a policy starts routing badly, operators can disable it, roll back, or replay the exact decisions that caused the drift.

---

## Comment M6 - Do not copy these Mahoraga choices into Stage 0

**Priority:** P0 guardrail.

**Что:** Explicitly reject these design imports for Stage 0:

| Mahoraga idea | Why not Stage 0 | Safe alternative |
| --- | --- | --- |
| Keyword classifier as main router | Too brittle for Responses compatibility and tool semantics | Use deterministic compatibility gate + executive schema |
| Online learning immediately | Non-deterministic public behavior | Shadow-only policy first |
| Global bandit state | Bad for multi-tenant or per-conversation guarantees | Namespace by tenant/model alias/worker pool/version |
| Heuristic output quality as truth | Can reward plausible junk | Use contract-validity signals first, quality labels later |
| Standalone SSE chat stream | Not equivalent to OpenAI Responses stream events | Keep Stage 1 streaming mapper separate |
| Worker tool execution as normal orchestration | Our client-owned tools must stay public/client-owned | Private workers may propose, never secretly execute public tools |

**Почему:** Mahoraga optimizes for local-first agent orchestration. V6 optimizes for API compatibility and hidden private collaboration. Those are cousin systems, not twins.

**Зачем:** Avoid architecture drift. Borrow the compass, not the whole ship.

---

## Recommended insertion into the reviewed V6 plan

Add this under a new **P1 - Routing policy abstraction** section:

> Add an internal `RoutingPolicy` interface. Stage 0 must use deterministic/static policy only, but every private worker call must log candidate set, selected worker, feature schema version, and outcome fields. Learned policies are disallowed from affecting production routing until Stage 2 shadow-mode evaluation passes compatibility, leak, cost, latency, and rollback gates.

Add this under **Stage 2**:

> Optional adaptive routing experiment: implement `ShadowBanditPolicy` using private trace outcomes. It may compute scores and write private diagnostics, but cannot affect public output or worker choice until golden replay proves no public contract regression.

Add this to the test matrix:

```text
Adaptive routing tests:
  - fixed-seed replay produces identical decisions
  - policy state rollback restores previous decisions
  - shadow policy cannot affect public response snapshots
  - no private features/scores leak into public /input_items
  - learned policy disabled when worker_pool_version or feature_schema_version mismatches
```

---

## Bottom line

Mahoraga is worth harvesting for **observability + future adaptive routing**, not for the initial runtime contract. The correct move is:

```text
Stage 0: deterministic routing + trace fields
Stage 1: public streaming mapper, still deterministic
Stage 2: shadow adaptive policy
Stage 3: active adaptive policy only after replay/eval gates
```

That keeps V6 boring where it must be boring, and clever where cleverness can be caged, measured, and rolled back.
