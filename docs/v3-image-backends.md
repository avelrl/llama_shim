# V3 Alternative Image Backends

Status: implemented for fixture, Responses-compatible proxy, and generic
ComfyUI text-to-image workflow backend.

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
- `image_generation` can also run through a generic ComfyUI workflow backend
  or a deterministic `fixture` backend for devstack and regression coverage
- the shim owns the outer `/v1/responses` state, storage, and replay behavior
- current-turn image inputs and edit lineage are projected into the image
  backend request
- partial-image artifacts are stored for replay when the backend emits them

The first production backend slice is ComfyUI. It is intentionally a generic
text-to-image workflow adapter, not a hosted-image parity layer and not a
universal ComfyUI graph compiler.

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

Status: implemented.

- configuration supports inline workflow maps or `workflow_path`
- workflow templates support `{{prompt}}`, `{{negative_prompt}}`, `{{width}}`,
  `{{height}}`, `{{seed}}`, and `{{filename_prefix}}`
- readiness probes `GET /system_stats`
- generation submits `POST /prompt`, polls `GET /history/{prompt_id}`, and
  downloads the selected `GET /view` image
- artifact download is bounded by `comfyui.max_image_bytes`
- devstack exposes a ComfyUI-compatible mock route set for CI smoke coverage
- generic edit/image-input support is explicitly rejected because ComfyUI image
  inputs are workflow-specific

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

Status: implemented for fixture and ComfyUI-backed devstack.

- Devstack now uses `responses.image_generation.backend=comfyui` against the
  fixture service's ComfyUI-compatible mock endpoints.
- The fixture backend remains available for deterministic partial-image SSE
  event simulation.
- Config examples keep image generation disabled by default, while devstack
  enables `comfyui`.

### Phase 1: Adapter Boundary

Status: implemented.

- The existing small image backend interface behind the local
  `image_generation` path now supports `responses`, `comfyui`, and `fixture`.
- Keep the outer Responses item and replay shape shim-owned.
- ComfyUI adapter tests cover workflow rendering, queue/poll/download flow, and
  final `image_generation_call` shape.

### Phase 2: Deterministic Smoke

Status: implemented.

- `make v3-image-backends-smoke` verifies active backend capabilities,
  non-stream create, stream create, and retrieve-stream replay.
- When the active backend is `fixture`, the smoke also verifies deterministic
  partial-image events. ComfyUI does not synthesize partial-image events.
- Verify `/debug/capabilities` reports backend readiness and supported modes.
- Verify stored artifacts and retrieve-stream replay do not depend on live
  backend availability.

### Phase 3: First Production Backend

Status: implemented for generic ComfyUI text-to-image.

- `comfyui` is configured with `responses.image_generation.base_url` plus
  `responses.image_generation.comfyui.*`.
- It supports text prompt, size placeholders, bounded image fetch, readiness,
  unit coverage, and devstack smoke coverage.
- Unsupported edit/input combinations remain explicit local errors.

## Done Criteria

This track should only move out of planned status when:

- one backend slice has a design, config, implementation, and smoke path
- unsupported local-only behavior is explicitly rejected or documented
- `/debug/capabilities` reports the active image backend accurately
- generated artifacts are bounded and replayable
- docs avoid hosted parity claims beyond the implemented subset

The fixture and ComfyUI slices satisfy those criteria for deterministic
regression coverage and the first operator-owned production adapter. Additional
backends can be added through the same image provider interface, but each new
backend still needs its own config namespace, bounded artifact handling, tests,
and smoke path before it should be documented as supported.

## Possible Future Extensions

These are intentionally not committed roadmap items. They are useful extension
points to revisit when there is a concrete operator workflow to support:

- ComfyUI workflow profiles with explicit node-input mapping for prompt,
  negative prompt, size, seed, sampler, steps, CFG, and checkpoint override.
- Workflow-specific image input adapters for image-to-image, edit, upscale,
  ControlNet, reference-image, or inpainting graphs.
- Additional provider adapters behind the same `imagegen.Provider` boundary,
  such as A1111/Forge, local Diffusers, Replicate, fal, or Stability.
- Backend-specific smoke profiles that run only when the corresponding real
  service is configured, while devstack keeps using deterministic fixtures.
