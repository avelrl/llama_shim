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
- verifies actual DOM state for the selected fixture scenarios

This proves the typed computer-loop contract can drive a real external
executor. It is intentionally more practical than `v3-local-runtimes-smoke`,
which stays JSON-only and CI-friendly.

The make target defaults to all deterministic fixture scenarios:

- `type`: click the search input and type `penguin`
- `keypress`: click the keyboard input, type `orca`, and press Enter
- `scroll`: scroll the page down by 520 pixels
- `drag`: drag the orange square into the drop zone

The script itself defaults to `COMPUTER_HARNESS_SCENARIOS=type` so manual real
upstream runs can stay short unless the caller opts into broader coverage.

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

Run a narrower or explicit scenario set:

```bash
COMPUTER_HARNESS_SCENARIOS=type,keypress \
make v3-computer-browser-harness-smoke
```

Run the script directly against a real upstream-backed shim, keeping the short
default `type` scenario:

```bash
SHIM_BASE_URL=http://127.0.0.1:18080 \
FIXTURE_BASE_URL=http://127.0.0.1:18081 \
MODEL=<model> \
SHIM_AUTH_HEADER='Authorization: Bearer <token>' \
bash ./scripts/v3-computer-browser-harness-smoke.sh
```

If the shim requires auth, pass the exact HTTP header:

```bash
SHIM_AUTH_HEADER='Authorization: Bearer <token>' \
make v3-computer-browser-harness-smoke
```

## Artifacts

Each run writes repo-local, ignored artifacts under:

```text
.tmp/v3-computer-browser-harness-runs/<model>_<timestamp>/
```

The directory contains:

- `summary.json`: overall status and per-scenario results
- `capabilities.json`: `/debug/capabilities` snapshot used by the run
- `requests/*.json`: `/v1/responses` request bodies
- `responses/*.json`: successful `/v1/responses` bodies
- `actions/*.json`: action arrays executed by Playwright
- `screenshots/*.png`: screenshots sent back as `computer_call_output`
- `states/*.json`: DOM state after each action batch
- `errors/*.json`: HTTP error bodies, when a request fails

Use `COMPUTER_HARNESS_ARTIFACT_DIR` to choose a different artifact root or
`COMPUTER_HARNESS_RUN_DIR` for an exact run directory.

The script writes Playwright daemon sockets to a short `/tmp`-based path by
default. If you override `PLAYWRIGHT_DAEMON_SOCKETS_DIR`, keep the resulting
Unix socket path short enough for macOS.

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
- `listen EADDRINUSE` during `playwright-cli open`: check for stale daemon
  processes first, then ensure `PLAYWRIGHT_DAEMON_SOCKETS_DIR` and
  `PLAYWRIGHT_SESSION` do not produce an overlong Unix socket path.
- `/debug/capabilities` failure: ensure `responses.computer.backend` is
  `chat_completions` and devstack was rebuilt after config changes.
- `shim-local computer planner did not return a supported computer action
  plan`: the model did not produce a usable computer planner object. The shim
  accepts the strict `decision/actions` planner JSON, fenced/embedded JSON, and
  single-action `action`/`args` JSON for known actions. `action_type` is
  accepted as a `type` alias. Chat-backed planner repair also accepts
  model-internal `{"type":"computer","action":"key","args":...}` and
  `{"type":"action","action":"key","args":...}` wrappers, plus
  function-call-like `{"name":"scroll","arguments":...}` objects, when they
  map cleanly to a known action. Namespaced function names such as
  `default_api::scroll` are reduced to the final action segment. For `scroll`,
  `dx/dy` and `delta_x/delta_y` aliases are normalized to `scroll_x/scroll_y`;
  a single `pixels` field is treated as vertical `scroll_y`. For `drag`, nested
  `params.from_x/from_y/to_x/to_y` endpoints are normalized to the canonical
  `path` form. The shim does not invent missing actions.
- DOM value mismatch: inspect the emitted `computer_call.actions` and the
  deterministic fixture coordinate at `/pages/computer-harness`. The prompt
  gives the fixture click coordinate explicitly so this smoke primarily checks
  the bridge and executor loop, not broad visual grounding quality.
- `type` action returned but DOM value is still empty or wrong: the model
  likely clicked outside the input or the browser executor lost focus. The
  harness fails immediately in this case instead of spending another upstream
  turn.
- `keypress`, `scroll`, or `drag` terminal action returned but state did not
  change: inspect `actions/*.json`, `states/*.json`, and the matching
  screenshot pair in the run artifact directory. That usually distinguishes a
  planner/action-shape problem from a browser executor problem.

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
