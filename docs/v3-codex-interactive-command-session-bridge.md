# V3 Codex Interactive Command Session Bridge

Last updated: May 1, 2026.

Task id: `v3-codex-interactive-command-session-bridge`

Status: initial shim bridge coverage implemented; dedicated profile passes on
devstack.

This task isolates the `exec_command -> session_id -> write_stdin` problem from
the broader eval-harness and benchmark-lite work. The failure mode is not that
Codex lacks interactive command support; it is that the shim's current
Chat-backed Responses bridge does not yet prove stable live-session state and
model-visible session identity across a tool-output follow-up.

## References Checked

Checked on May 1, 2026:

- local official-docs index: [openapi/llms.txt](../openapi/llms.txt)
- OpenAI docs:
  - [Codex configuration reference](https://developers.openai.com/codex/config-reference)
  - [Codex app-server API overview](https://developers.openai.com/codex/app-server)
  - [Evaluation best practices](https://developers.openai.com/api/docs/guides/evaluation-best-practices)
- local `openai/codex` source checkout at commit
  `9121132c8f5412ae99c36363409759baa7e004f9`

Relevant constraints:

- Codex `features.unified_exec` exposes the unified exec tool path.
- Current Codex app-server APIs include command execution, stdin write,
  resizing, termination, and output-delta notifications for live command
  sessions.
- This shim tests Codex through `codex exec --json`; app-server APIs are a
  reference for runtime concepts, not the wire contract used by the harness.

## Problem Statement

`write_stdin_pty` is intentionally outside default `codex-core` and stays in
`codex-core-interactive` / `codex-compat` until the bridge is proven stable.

The compatibility work must verify all of these:

1. `exec_command` preserves live session state in the shim/bridge.
2. Tool output clearly and consistently reaches the model with the live
   `session_id`.
3. A later `write_stdin` tool call matches that `session_id` to the correct
   running process.
4. HTTP Responses and Chat-backed Responses paths are tested separately because
   native Responses upstream behavior can differ from the shim's Chat bridge.

## Required Implementation Work

- Keep `write_stdin_pty` in `codex-core-interactive` and `codex-compat`, not
  default `codex-core`.
- Add focused shim tests for Responses-over-Chat:
  `exec_command -> session_id -> write_stdin`. Done for the shim-owned bridge:
  the test asserts that a Codex `function_call_output` containing real unified
  exec text such as `Process running with session ID N` is preserved as Chat
  `tool` content with the same `tool_call_id`, and that the next Chat-backed
  response can return a structured `write_stdin` call with that `session_id`.
- Keep runtime session negatives in the eval profile rather than shim unit
  tests. In the Codex CLI path, the shim does not own the live PTY process or
  its session registry; Codex executes `exec_command`, returns tool output to
  the shim, and later executes `write_stdin`. The shim-owned contract is to
  preserve model-visible session identity and structured tool-call shape, not
  to validate unknown or expired local CLI sessions directly.
- Add explicit shim logging around the Chat-backed local tool-loop bridge.
  Done at debug level for generated Chat payloads:
  - tool names;
  - assistant tool-call count;
  - model-visible tool-output call ids;
  - detected interactive `session_id` values.
- Inspect `.data/shim.log` or devstack container logs from failed interactive
  runs to confirm whether the loss is in state preservation, model-visible
  output, or lookup/routing. The May 1, 2026 failure was in the devstack
  fixture: it parsed only JSON `"session_id"` values, while real Codex unified
  exec emits model-visible text `Process running with session ID N`.
- After implementation, run the profile:

```bash
CODEX_EVAL_SUITE=codex-core-interactive bash ./scripts/codex-eval-runner.sh
```

Validation on May 1, 2026:

- focused fixture and bridge tests passed
- `go test ./...`
- `make lint`
- `git diff --check`
- `OPENAI_API_KEY=shim-dev-key CODEX_EVAL_SUITE=codex-core-interactive bash ./scripts/codex-eval-runner.sh`

## Acceptance Criteria

- The Chat-backed Responses bridge exposes a stable `session_id` in
  model-visible `exec_command` output for live commands.
- `write_stdin` reaches the intended process and the eval observes the expected
  output after stdin.
- Unknown or expired sessions fail deterministically with a useful diagnostic
  in the Codex CLI/eval runtime that owns the process session.
- The Chat-backed path has focused shim tests for context preservation. Native
  HTTP Responses upstream remains profile-tested because the session registry is
  still client-side, not shim-side.
- `codex-core-interactive` passes locally before any promotion discussion.
- Default `codex-core` remains unchanged until the profile is stable.

## Non-Goals

- Do not promote `write_stdin_pty` into the default core gate as part of the
  first fix.
- Do not use benchmark-lite tasks to validate this bridge; this is profile
  compatibility, not benchmark breadth.
- Do not infer success from model final text alone. The checker must verify the
  command/session behavior.
- Do not claim exact hosted OpenAI or Codex parity from this profile.
