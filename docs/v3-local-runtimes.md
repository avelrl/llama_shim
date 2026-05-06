# V3 Richer Local-Only Runtimes

Status: partial. Browser/computer runtime hardening and the optional external
browser harness are implemented as the first focused runtime slice.

Last updated: May 5, 2026.

This document tracks V3 runtime-expansion work that is useful locally but does
not cleanly map to a complete hosted OpenAI surface.

The shim already has practical local subsets for several tool families. This
track is for future runtime loops that need more local machinery, stricter
capability reporting, and deterministic tests before they can be called
operationally useful.

## Current State

Existing local runtime slices include:

- local `computer` external loop over screenshots and action outputs
- local `code_interpreter` backed by shim-managed Docker containers
- local native coding tools for `shell` and `apply_patch`
- local `web_search`, `file_search`, and `image_generation` subsets when their
  backends are configured
- deterministic devstack fixture coverage for selected flows

These are not a single generic runtime framework yet. Each runtime has its own
config, capability reporting, and tests.

## Goals

- Add richer local runtime loops only when they have deterministic fixtures.
- Keep `/debug/capabilities` as the operator-visible runtime manifest.
- Keep public OpenAI-compatible request shapes separate from shim-local runtime
  settings.
- Prefer one focused runtime slice at a time over a large generic plugin layer.
- Preserve the frozen V2 compatibility story while adding local utility.

## Non-Goals

- Hosted-equivalent browser, shell, or multimodal environments.
- A broad plugin/runtime marketplace.
- A public OpenAI-surface backend selector.
- Treating planner prompts or model instructions as a security boundary.
- Moving opinionated memory or plugin architecture out of V4.

## Candidate Runtime Slices

### 1. Browser/Computer Runtime

Build on the current screenshot-first `computer` subset.

Implemented first slice:

- deterministic devstack coverage through `make v3-local-runtimes-smoke`
- optional real-browser executor coverage through
  `make v3-computer-browser-harness-smoke`; see
  [v3-computer-browser-harness.md](v3-computer-browser-harness.md)
- browser harness artifacts for triage: request/response JSON, action arrays,
  screenshots, DOM state snapshots, capabilities, and summary JSON under
  `.tmp/v3-computer-browser-harness-runs/`
- `/debug/capabilities.tools.computer` and
  `/debug/capabilities.probes.computer_runtime` coverage for the configured
  `chat_completions` runtime
- planner action allow-list and type normalization for the current documented
  action family: `screenshot`, `click`, `double_click`, `scroll`, `type`,
  `wait`, `keypress`, `drag`, and `move`
- current-turn `computer_call_output` screenshots are projected to the planner
  as multimodal context. Explicit `detail` is preserved on the Responses item;
  `original` is mapped to `high` for the Chat-backed planner projection.
- non-stream create, stream create, stored retrieve, retrieve-stream, and
  `/v1/responses/{id}/input_items` coverage for the screenshot-first loop
- deterministic real-browser fixture scenarios for `type`, `keypress`,
  `scroll`, and `drag`

Still out of scope:

- an in-process browser or VM runtime
- hosted-equivalent planner behavior
- hosted action execution, coordinate remapping, or screenshot downscaling
  ownership; the optional browser harness executes deterministic fixture flows
  with explicit task coordinates only
- exact hosted `response.computer_call.*` SSE choreography

Useful next tracks:

- visual-grounding eval profile: use the same fixture without giving the model
  coordinates, record pass/fail in model-matrix style output, and do not make
  it a required smoke
- optional coordinate remapping experiments for downscaled screenshots, guarded
  by explicit docs because OpenAI recommends `detail: "original"` for computer
  use
- additional deterministic fixture pages for multi-step form flows or richer
  DOM state transitions
- model-specific compatibility notes only when failures reduce to stable
  parser aliases or documented provider limitations

### 2. Multimodal Local Loop

Support richer local image/file/media input loops where the shim owns state and
the backend is OpenAI-compatible or explicitly local.

Potential work:

- artifact lifecycle tests
- input projection rules
- image/file lineage across `previous_response_id`
- capability-gated smoke coverage

### 3. Shell/App Runtime Extensions

Build on local `shell`, `apply_patch`, and Codex eval evidence.

Potential work:

- stricter runtime session lifecycle tests
- PTY and stdin continuation beyond the Codex eval profile
- app-server/manual feature reductions when they become deterministic

## Implementation Phases

### Phase 0: Runtime Inventory

Status: done for the first browser/computer slice.

- Inventory all current local runtime config keys and capability fields.
- Identify duplicated readiness and missing-dependency behavior.
- Pick one runtime slice for implementation.

### Phase 1: Fixture And Capability Gate

Status: done for the first browser/computer slice.

- Add or extend deterministic fixtures for the chosen runtime.
- Add `/debug/capabilities` tests for enabled, disabled, and unavailable
  states.
- Add smoke coverage that fails for environment reasons before runtime logic is
  exercised.

### Phase 2: Runtime Slice

Status: done for the first browser/computer slice.

- Implement the chosen runtime incrementally.
- Keep unsupported local-only behavior explicit.
- Add create, stream, retrieve, and input-items coverage where applicable.

### Phase 3: Regression Import

Status: not started.

- Convert manual failures into deterministic tasks only after they reduce to a
  stable fixture scenario.
- Keep flaky model behavior in model-matrix notes instead of hardcoding broad
  shim repairs.

## Done Criteria

The first richer local runtime slice is done when:

- it has deterministic fixture or devstack coverage: `make
  v3-local-runtimes-smoke`
- it has optional real-executor coverage: `make
  v3-computer-browser-harness-smoke`
- capability reporting exposes the configured computer runtime through
  `/debug/capabilities.tools.computer` and the readiness state through
  `/debug/capabilities.probes.computer_runtime`
- tests cover the relevant state and replay surfaces
- docs state the local-only boundary without hosted parity overclaim
