# V3 Alternative Image Backends

Status: partial; deterministic fixture backend implemented.

Last updated: May 5, 2026.

This document tracks the V3 plan for adding additional shim-local image
generation backends without widening the OpenAI compatibility promise.

The official Responses API supports `image_generation` as a built-in tool and
allows image inputs and outputs to remain in Responses context. The shim may
delegate local image generation to operator-owned providers, but those
providers are backend diversity work, not hosted OpenAI parity.

## Current State

The current shim-local image path is a practical subset:

- `image_generation` can run through a configured Responses-compatible image
  backend
- `image_generation` can also run through a deterministic `fixture` backend
  for devstack and regression coverage
- the shim owns the outer `/v1/responses` state, storage, and replay behavior
- current-turn image inputs and edit lineage are projected into the image
  backend request
- partial-image artifacts are stored for replay when the backend emits them

There is no separate Stable Diffusion, ComfyUI, or provider-specific production
pipeline selected yet. The first V3 expansion slice intentionally uses the
fixture backend so the adapter/replay contract is testable before a production
backend is added.

## Goals

- Add backend diversity for local image generation.
- Keep one stable shim-owned `image_generation` contract at the Responses
  boundary.
- Expose active image runtime capability through `/debug/capabilities`.
- Keep artifacts, partials, and replay behavior deterministic enough for
  devstack and regression tests.
- Reject unsupported local-only image requests explicitly instead of pretending
  to match hosted behavior.

## Non-Goals

- Exact hosted image-generation planner parity.
- Exact hosted partial-image timing or failure choreography.
- A public request field that lets clients select arbitrary local backends.
- Provider-specific behavior documented as OpenAI API behavior.
- Adding novelty backends without a reproducible smoke path.

## Candidate Backend Slices

### 1. ComfyUI

Useful when an operator already runs a ComfyUI graph service.

Required before implementation:

- fixed request template and output artifact contract
- startup/readiness probe
- bounded artifact download/storage behavior
- deterministic fixture or mock server for CI
- capability reporting for backend name, readiness, and supported modes

### 2. Stable Diffusion HTTP API

Useful when an operator runs an A1111-compatible or similar HTTP backend.

Required before implementation:

- explicit config namespace for URL, timeout, and artifact limits
- request/response adapter tests with sanitized fixtures
- clear generate/edit support boundary
- no hidden OpenAI-surface request limits

### 3. Fixture-Only Backend

Useful as a first regression substrate before production backends are stable
enough for CI.

Status: implemented.

- deterministic image placeholder artifacts
- stable partial-image event simulation
- devstack smoke coverage
- explicit docs that this is not a production image backend

## Implementation Phases

### Phase 0: Backend Selection

Status: implemented for the fixture backend.

- The selected first backend is `responses.image_generation.backend=fixture`.
- It has no native external API; it returns deterministic
  `image_generation_call` items and deterministic partial-image SSE events.
- Config examples keep image generation disabled by default, while devstack
  enables `fixture`.

### Phase 1: Adapter Boundary

Status: implemented for the fixture backend.

- The existing small image backend interface behind the local
  `image_generation` path now supports `responses` and `fixture`.
- Keep the outer Responses item and replay shape shim-owned.
- Fixture adapter request-shaping and streaming tests cover the deterministic
  first slice.

### Phase 2: Deterministic Smoke

Status: implemented.

- `make v3-image-backends-smoke` verifies fixture capabilities, non-stream
  create, stream partial images, and retrieve-stream replay.
- Verify `/debug/capabilities` reports backend readiness and supported modes.
- Verify stored artifacts and retrieve-stream replay do not depend on live
  backend availability.

### Phase 3: First Production Backend

Status: not started.

- Add the first real backend adapter only after Phase 2 is stable.
- Add timeout, artifact-size, and storage behavior tests.
- Keep unsupported edit/input combinations explicit and local-only.

## Done Criteria

This track should only move out of planned status when:

- one backend slice has a design, config, implementation, and smoke path
- unsupported local-only behavior is explicitly rejected or documented
- `/debug/capabilities` reports the active image backend accurately
- generated artifacts are bounded and replayable
- docs avoid hosted parity claims beyond the implemented subset

The fixture slice satisfies those criteria for deterministic regression
coverage. The remaining open work is the first production backend adapter,
which should stay blocked until its native request/response shape, artifact
limits, and fixture server are documented.
