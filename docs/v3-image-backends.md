# V3 Alternative Image Backends

Status: planned design stub.

Last updated: May 4, 2026.

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
- the shim owns the outer `/v1/responses` state, storage, and replay behavior
- current-turn image inputs and edit lineage are projected into the image
  backend request
- partial-image artifacts are stored for replay when the backend emits them

There is no separate Stable Diffusion, ComfyUI, or provider-specific pipeline
selected as the first V3 expansion slice yet.

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

Useful as a first regression substrate if production backends are not stable
enough for CI.

Required before implementation:

- deterministic image placeholder artifacts
- stable partial-image event simulation
- devstack smoke coverage
- explicit docs that this is not a production image backend

## Implementation Phases

### Phase 0: Backend Selection

Status: not started.

- Pick the first backend based on reproducibility, local availability, and
  smoke-testability.
- Document the selected provider's native request and response shape.
- Add config examples with disabled-by-default behavior.

### Phase 1: Adapter Boundary

Status: not started.

- Extract a small image backend interface behind the existing local
  `image_generation` path.
- Keep the outer Responses item and replay shape shim-owned.
- Add adapter request-shaping tests.

### Phase 2: Deterministic Smoke

Status: not started.

- Add a devstack or fixture-backed smoke.
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
