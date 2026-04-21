# Runtime Hardening

## What It Is

This guide is the place for shim-internal reliability and performance changes
that are worth documenting but do not belong in the `v*` scope ledgers and do
not justify new request preflight behavior.

Use it for operator-facing or engineering-facing notes about how the shim
stays stable when the upstream backend is slow, saturated, or slightly
inconsistent.

This guide is explicitly not:

- a new compatibility claim
- a replacement for the compatibility matrix
- a backlog ledger
- a reason to add new public validation on OpenAI-compatible routes

## What Belongs Here

- upstream backpressure, bounded queueing, and concurrency gating
- HTTP transport and connection reuse tuning
- shim-local continuation, replay, and state-reconstruction hardening
- internal-only storage or replay bounds that must not change the public API
- observability, metrics, and error-classification improvements
- pragmatic fast paths for shim-owned subsets that already fit the documented
  surface

## What Does Not Belong Here

- new API routes
- widening V2, V3, or V4 scope claims
- undocumented request validation added to public OpenAI-compatible endpoints
- parity claims that still need fixture-backed or docs-backed validation
- work that belongs in the compatibility matrix or OpenAPI

## Safe Patterns

- add bounded internal admission control before slow upstream model calls
- tune keep-alive, idle connections, and transport behavior to reduce avoidable
  connection churn
- keep retries to idempotent read paths such as retrieve, list, or input-item
  lookup
- preserve shim-local state ownership for `previous_response_id` and
  `conversation`
- trim or compact local context only when the request semantics stay intact
- improve logging and metrics so upstream timeouts are distinguishable from
  contract failures

## Patterns To Avoid

- adding preflight rejection on hot create paths just because an upstream
  dependency is currently slow
- retrying side-effecting `POST /v1/responses` requests after timeout
- introducing undocumented public caps on otherwise valid OpenAI-compatible
  payloads
- silently degrading tool semantics while still claiming compatibility parity
- moving a runtime problem into a public API behavior change because that is
  easier to implement

## Current Notes

As of 2026-04-21:

- `responses.mode=prefer_local` means the shim owns routing, local state, and
  supported local tool/runtime subsets first; it does not guarantee fully local
  inference latency
- the common text-generation path may still call the configured upstream text
  backend even when the shim owns the outer `/v1/responses` surface
- under concurrency, a slow upstream is more likely to show up first as p95/p99
  latency growth and timeout storms than as deterministic contract bugs
- for that class of issue, internal backpressure and transport tuning are
  preferable to new public preflight or undocumented API limits

## Working Notes

These notes are allowed to document near-term hardening work before it is
implemented, but they must stay conservative:

- describe the symptom and intended invariant clearly
- avoid implying that a planned mechanism has already shipped
- avoid turning this guide into a scope ledger or feature roadmap

### 2026-04-21: Upstream admission control and transport tuning

- Status: implemented
- Symptom: when several agents or concurrent compatibility scenarios hit the
  shim at once, the upstream text backend may slow down sharply, pushing p95
  and p99 latency high enough to trigger timeout cascades
- Root cause: the shim currently has local concurrency gates for some
  shim-owned runtimes such as retrieval and code interpreter, but not for the
  main upstream model-call hot path; it also uses default HTTP client behavior
  without explicit upstream transport tuning
- Invariant: valid `POST /v1/responses` traffic must not gain a new
  undocumented compatibility preflight or a shim-only contract regression just
  to make overload easier to manage
- Change:
  add bounded internal admission control before slow upstream model calls and
  hold the slot until the proxied or streamed upstream body is closed; tune the
  upstream HTTP transport for explicit connection reuse and host-level caps
- Operator surface:
  new internal knobs live under `llama.max_concurrent_requests`,
  `llama.max_queue_wait`, and `llama.http.*` in the shim config; these are
  shim-only runtime controls, not OpenAI-surface request fields
- Observability:
  emit `shim_upstream_admission_total`, `shim_upstream_queue_wait_ms`,
  `shim_inflight`, and `shim_queued` metrics, and log slow or failed admission
  waits
- Non-goals:
  do not retry side-effecting create requests after timeout, do not add public
  undocumented payload caps, and do not reframe a runtime protection as a new
  OpenAI API behavior
- Verification used:
  targeted client tests for serialized admission, queue timeout, and proxy-body
  slot ownership; config parsing tests for the new runtime knobs; metrics
  integration coverage; `go test ./...`; `go vet ./...`; `git diff --check`

### 2026-04-21: Planned startup calibration and recommendation mode

- Status: planned
- Symptom: operators can see that the upstream is alive via `/readyz`, but that
  does not tell them whether the hot path is merely alive or already too slow
  for the intended agent concurrency
- Root cause: the current readiness path checks basic backend reachability, but
  it does not measure representative startup latency for the common upstream
  text-generation path
- Invariant: startup probing must not become a new public compatibility
  preflight for normal `POST /v1/responses` traffic, and it must not mutate
  OpenAI-visible request or response semantics
- Intended change:
  add an internal startup calibration mode that performs a small number of
  short deterministic upstream probes, records observed latency, and emits
  conservative runtime recommendations through logs and shim-owned operational
  surfaces such as `/debug/capabilities`
- First stage:
  recommendation-only mode; measure and report, but do not automatically change
  behavior
- Possible later stage:
  optional internal auto-tuning of shim-only upstream admission limits or queue
  budgets, with explicit opt-in and hard min/max clamps
- Non-goals:
  do not reject valid create traffic because calibration looks bad, do not
  auto-tune undocumented public API limits, and do not use calibration as a
  reason to add side-effecting request retries
- Verification plan:
  confirm that startup calibration stays informational in recommendation mode,
  confirms the difference between simple readiness and real hot-path latency,
  and produces stable enough guidance to help operators size safe upstream
  concurrency without changing the external API contract

## Change Note Template

When a hardening change lands, add a short note with:

- symptom
- root cause
- invariant that must not change
- internal mechanism added
- verification used

Example:

```md
### 2026-04-21: Upstream admission control

- Symptom: concurrent agent turns caused timeout storms against a slow upstream.
- Root cause: the shim allowed unbounded concurrent model calls on the hot path.
- Invariant: valid `POST /v1/responses` traffic must not gain a new undocumented
  preflight rejection mode.
- Change: add bounded internal admission control and expose queue/in-flight
  metrics.
- Verification: targeted concurrent runs, full compatibility reruns, and
  contract regression tests.
```

## Related Docs

- [Operations](operations.md)
- [Responses](responses.md)
- [Tools Overview](tools.md)
- [Compatibility Matrix](../compatibility-matrix.md)
- [V2 Scope](../v2-scope.md)
