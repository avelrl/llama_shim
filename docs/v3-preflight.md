# V3 Preflight

Last updated: April 25, 2026.

This document fixes the next logical step after the V2 freeze and before the
project takes on new V3 backends or richer local-only runtime work.

V3 should not start by piling more capability onto a weak automation base.
The project already has a strong V2-compatible facade, broad integration
coverage, upstream SSE fixtures, and a growing external tester plan. The next
high-leverage move is to turn that into a repeatable development and regression
substrate.

## Goal

Ship the minimum shim-owned substrate that makes V3 work predictable:

- easier to inspect
- easier to run locally
- easier to automate in CI and nightly flows
- easier to connect to the external `openai-compatible-tester`

This is still V3 work because it mainly improves backend and runtime expansion
readiness. It does not change the frozen V2 compatibility promise.

## Why This Comes Before New V3 Tracks

Without a small preflight layer, every new V3 backend or runtime adds more
surface area than the project can observe or reproduce cleanly.

That creates the wrong development loop:

- feature works in one local setup
- feature fails in another setup for environment reasons
- tester failures mix real regressions with missing runtime prerequisites
- CI can only run shallow checks because the stack is not reproducible

The preflight layer fixes that by making runtime state explicit and
reproducible before new capability is added.

## Scope

The V3 preflight scope is intentionally narrow and practical.

It consists of four concrete deliverables:

1. a shim-owned capability manifest
2. runnable fake or deterministic local dev services
3. a reproducible local dev stack with `docker compose` and `make`
4. one real end-to-end smoke path on top of that stack

## Deliverables

### 1. Shim-owned capability manifest

Add a small shim-owned surface that reports what is actually available in the
current process and configuration.

This is now fixed as `GET /debug/capabilities`.

Keep `/readyz` as the terse public readiness probe; do not overload it with a
large operator payload.

The manifest should tell an operator, tester, or autonomous agent:

- which core surfaces are enabled locally
- which local tool runtimes are available
- which paths are proxy-only compatibility bridges
- which optional dependencies are configured but currently unavailable
- whether local persistence is expected to survive restart
- which retrieval and ranking backends are active

Suggested capability families:

- Responses state and retrieve support
- Conversations support
- stored Chat Completions support
- retrieval and vector-store support
- local `file_search`
- local `web_search`
- local `image_generation`
- local `computer`
- local `code_interpreter`
- remote MCP with `server_url`
- MCP `connector_id` proxy-only support
- hosted/server `tool_search`
- client `tool_search_output` passthrough behavior
- persistence and cleanup behavior

This is the bridge between the shim and future tester-side capability gating.

### 2. Runnable deterministic dev services

The repository already contains strong fake providers and test helpers, but
most of them only exist inside Go tests.

V3 preflight should promote the important ones into runnable local services or
small helper processes so they can be used outside `go test`.

Examples:

- deterministic MCP fixture server
- deterministic web-search fixture backend
- deterministic image-generation fixture backend
- deterministic computer-loop fixture inputs where useful
- deterministic retrieval seed assets

The goal is not to build production backends here.
The goal is to make development, smoke testing, and CI reproducible.

This now ships as a runnable repo-owned fixture service:

- `cmd/devstack-fixture`

The current fixture bundle covers the deterministic preflight path for:

- shim-local text generation through a `llama`-compatible surface
- shim-local hosted/server `tool_search` planning and stored
  `function_call_output` follow-up through deterministic chat-completions rules
- shim-local `web_search` through a `searxng`-compatible surface
- shim-local `image_generation` through an OpenAI-compatible
  `/v1/responses` surface
- shim-local remote `mcp` through deterministic `server_url` fixture transports
- fixed HTML pages linked from deterministic search results for targeted tests
  and debugging

### 3. Reproducible local dev stack

Add a small dev stack on top of the shim so the project can be started in a
known-good mode without bespoke manual setup.

This now ships as a small repo-owned stack with:

- `docker-compose.devstack.yml`
- `config.devstack.yaml`
- `make devstack-up`
- `make devstack-down`
- `make devstack-smoke`

The stack should be opinionated enough to be useful, but still small.

It should aim to give one predictable local environment for:

- stateful Responses and Conversations
- one retrieval-backed flow
- one or more local tool flows
- readiness and dependency probing

This now ships as:

- `config.devstack.yaml`
- `docker-compose.devstack.yml`
- `make devstack-up`
- `make devstack-down`
- `make devstack-smoke`
- `make devstack-ci-smoke`
- `make devstack-full-smoke`

### 4. One real end-to-end smoke path

Build one real, repo-owned smoke path that runs against the dev stack rather
than only against in-process test helpers.

This smoke path should validate a minimal but representative V2/V3 baseline:

- readiness succeeds
- a stateful `/v1/responses` flow succeeds
- one retrieval-backed or `file_search` flow succeeds
- one tool-backed flow succeeds
- the resulting output is stable enough to debug failures quickly

This should stay narrow.
It is not a replacement for the full integration suite or the external tester.
It is the first repeatable end-to-end loop the project can depend on while V3
grows.

This now ships as:

- `scripts/devstack-smoke.sh`
- `cmd/responses-websocket-smoke`
- `scripts/v3-coding-tools-smoke.sh`

The current smoke path verifies:

- fixture health
- shim readiness
- capability manifest
- stateful `previous_response_id`
- local `file_search`
- local `web_search`
- local `image_generation`
- local remote `mcp` via `server_url`
- cached remote `mcp` follow-up without repeating tools
- streamed generic replay for remote `mcp`
- hosted/server `tool_search` with namespace loading
- stored `tool_search` follow-up through `function_call_output`
- streamed generic replay for `tool_search`

`make devstack-ci-smoke` is the CI-compatible gate on top of the stack. It
combines the general devstack smoke, direct Responses WebSocket smoke, and V3
native coding-tools smoke. It deliberately avoids real Codex CLI checks because
CI runners should not need a locally installed `codex` binary.

`make devstack-full-smoke` is the local heavy gate. It includes
`make devstack-ci-smoke` plus real Codex CLI smoke paths that verify the
current `openai_base_url` bridge, the fallback Codex function tool named
`shell` when `features.unified_exec=false`, and the deterministic task matrix.

## Non-Goals

The preflight layer is not the place to:

- add novelty V3 backends just because they are easy to prototype
- reopen V2 compatibility wording
- claim exact hosted parity for more tool families
- move plugin or backend contract testing out of the main repository
- build a large deployment platform before the core dev loop is stable

## Relationship To The External Tester

The external `openai-compatible-tester` should stay focused on observable API
behavior.

This preflight layer exists partly to make that possible:

- the shim capability manifest gives the tester something explicit to gate on
- runnable dev services give the tester a deterministic environment
- the compose dev stack gives CI and autonomous agents a reproducible target
- the shim-owned smoke path gives a quick local sanity loop before broader runs

The boundary should stay clean:

- internal backend contracts still belong in `llama_shim` tests
- externally visible behavior belongs in the external tester and end-to-end
  shim smoke

## Success Criteria

Treat V3 preflight as complete when all of the following are true:

- a tester or operator can discover the current local capability set without
  reading config files by hand
- the repository can start a deterministic local stack for shim-focused smoke
  testing
- at least one real end-to-end smoke script or command validates that stack
- the external tester can later consume the same capability model instead of
  relying on guesswork
- new V3 backend or runtime work no longer has to invent ad hoc setup every
  time

As of April 25, 2026, the repository satisfies that preflight bar. The GitHub
Actions devstack job uses `make devstack-ci-smoke`; local release or merge
readiness can additionally run `make devstack-full-smoke` when the Codex CLI is
installed.

## Working Rule

Before adding a new V3 backend or a richer local-only runtime, first ask:

"Can the current shim describe, start, and smoke-test this capability
predictably?"

If the answer is "not yet", the missing piece probably belongs in V3 preflight
before the new capability itself.
