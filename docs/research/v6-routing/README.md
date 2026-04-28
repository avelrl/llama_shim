# V6 Routing Research Notes

This directory keeps exploratory V6 routing notes out of the canonical runtime
contract while preserving the source material for later stages.

Documents:

- [external-research-addendum.md](external-research-addendum.md): broad survey
  of routing papers, agent runtimes, security guidance, observability, and eval
  tooling.
- [mahoraga-addendum.md](mahoraga-addendum.md): focused review of Mahoraga-style
  adaptive routing and what V6 should borrow or reject.

Incorporation status:

- Stage 0 in [../../v6-routing-runtime.md](../../v6-routing-runtime.md)
  incorporates only the stable foundations: worker capability registry, static
  routing policy seam, durable private event log, guardrail matrix, stuck
  detector, untrusted-content envelopes, routing telemetry, and eval skeletons.
- Stage 1 incorporates replay/eval dataset export, baseline gates, redacted
  observability, and compatible provider failover.
- Stage 2 keeps cascade, shadow learned routing, policy state rollback, MCP
  prototypes, and risk gates behind explicit gates.
- Stage 3 keeps active adaptive routing, multi-round aggregation, parallel
  worker teams, and caching as research-only until separately reviewed.

These notes are not compatibility claims. The canonical public-contract wording
lives in [../../v6-routing-runtime.md](../../v6-routing-runtime.md) and the
reviewed Stage 0 hardening addendum lives in
[../../v6-routing-runtime-review.md](../../v6-routing-runtime-review.md).
