# V3 Computer Browser Harness

Status: implemented as an optional local smoke harness.

Last updated: May 5, 2026.

This document covers the real browser executor used to validate the
shim-local `computer` loop beyond JSON-only fixtures.

The OpenAI Computer Use guide describes an external loop: the model returns a
`computer_call`, the application executes actions, captures a new screenshot,
and sends it back as `computer_call_output` with `detail: "original"` preferred
for click accuracy. The shim keeps that boundary. It owns Responses state,
planner routing, typed item storage, and replay. It does not own a hosted
browser runtime.

## What It Tests

`make v3-computer-browser-harness-smoke` runs a deterministic end-to-end flow:

- opens the devstack fixture page in a real Playwright browser
- creates a `/v1/responses` request with `tools: [{"type":"computer"}]`
- verifies the shim returns a screenshot-first `computer_call`
- captures a real browser screenshot and sends it back as
  `computer_call_output`
- executes returned actions in the browser across a bounded computer loop
- verifies the actual DOM input value is `penguin`

This proves the typed computer-loop contract can drive a real external
executor. It is intentionally more practical than `v3-local-runtimes-smoke`,
which stays JSON-only and CI-friendly.

## Requirements

- devstack running with the default fixture and shim ports
- `playwright-cli` available on `PATH`
- an installed Playwright browser; the default script value is `chrome`
- local permission to open a browser process

Default endpoints:

- shim: `http://127.0.0.1:18080`
- fixture: `http://127.0.0.1:18081`

## Command

```bash
make devstack-up
make v3-computer-browser-harness-smoke
```

Useful overrides:

```bash
SHIM_BASE_URL=http://127.0.0.1:18080 \
FIXTURE_BASE_URL=http://127.0.0.1:18081 \
MODEL=devstack-model \
PLAYWRIGHT_BROWSER=chrome \
make v3-computer-browser-harness-smoke
```

If the shim requires auth, pass the exact HTTP header:

```bash
SHIM_AUTH_HEADER='Authorization: Bearer <token>' \
make v3-computer-browser-harness-smoke
```

## CI Boundary

This harness is not part of `make devstack-ci-smoke`. It depends on local
browser availability and GUI/browser sandboxing, which makes it a developer
or release-candidate check rather than a portable CI gate.

The CI-compatible local-computer gate remains:

```bash
make v3-local-runtimes-smoke
```

## Failure Triage

- `missing required command: playwright-cli`: install or expose the local
  Playwright CLI.
- browser launch failure: install the selected browser or set
  `PLAYWRIGHT_BROWSER` to one that is available locally.
- `/debug/capabilities` failure: ensure `responses.computer.backend` is
  `chat_completions` and devstack was rebuilt after config changes.
- `shim-local computer planner did not return a supported computer action
  plan`: the model did not produce a usable computer planner object. The shim
  accepts the strict `decision/actions` planner JSON, fenced/embedded JSON, and
  single-action `action`/`args` JSON for known actions. `action_type` is
  accepted as a `type` alias. The shim does not invent missing actions.
- DOM value mismatch: inspect the emitted `computer_call.actions` and the
  deterministic fixture coordinate at `/pages/computer-harness`. The prompt
  gives the fixture click coordinate explicitly so this smoke primarily checks
  the bridge and executor loop, not broad visual grounding quality.
- `type` action returned but DOM value is still empty or wrong: the model
  likely clicked outside the input or the browser executor lost focus. The
  harness fails immediately in this case instead of spending another upstream
  turn.

## Boundaries

- The harness executes the current local action family supported by the shim:
  `screenshot`, `click`, `double_click`, `scroll`, `type`, `wait`,
  `keypress`, `drag`, and `move`. `drag` accepts either a `path` array of
  points or `x`/`y` plus `end_x`/`end_y`.
- It does not validate exact hosted `response.computer_call.*` SSE
  choreography.
- It does not provide a security boundary for arbitrary browsing. It is a
  deterministic devstack smoke over a local fixture page.
- `computer_call_output.output.detail: "original"` is preserved on the
  Responses item. For the Chat-backed planner projection, `original` is mapped
  to `high` because Chat-compatible image inputs commonly accept only
  `auto`, `low`, or `high`.
