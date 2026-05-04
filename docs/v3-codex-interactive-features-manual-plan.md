# V3 Codex Interactive Features Manual Plan

Last updated: May 4, 2026.

Task id: `v3-codex-interactive-features-manual-plan`

Status: planned manual exploration track. This is not an automated gate.

This document keeps the manual Codex interactive feature work separate from the
deterministic eval harness. The harness should remain the default regression
signal; this plan is for features that require the TUI, app-server protocol, or
human interaction before they can be reduced into deterministic tasks.

Automated compatibility pieces that already have deterministic reductions are
tracked in [V3 Codex Eval Harness](v3-codex-eval-harness.md) and
[V3 Codex Shim-Native Coverage](v3-codex-shim-native-coverage.md). Run
`make codex-eval-automated-profiles` for those non-manual gates. This document
is only for the remaining interactive feature work.

## References Checked

- local official-docs index: `openapi/llms.txt`
- OpenAI docs:
  - [Codex interactive mode](https://developers.openai.com/codex/cli/features#running-in-interactive-mode)
  - [Codex configuration reference](https://developers.openai.com/codex/config-reference)
- implementation reference:
  - [OpenAI Codex](https://github.com/openai/codex)
- repo-local tracking:
  - [V3 Codex Eval Harness](v3-codex-eval-harness.md)
  - [V3 Codex Interactive Command Session Bridge](v3-codex-interactive-command-session-bridge.md)
  - [V3 Codex Shim-Native Coverage](v3-codex-shim-native-coverage.md)

The Codex source reference is used for CLI feature discovery and request-shape
expectations only. OpenAI API wire-contract claims still need the official docs
and fixture evidence required by `AGENTS.md`.

## Purpose

Use this track to answer three questions before automating a feature:

1. Does the interactive Codex feature work through the shim at all?
2. If it fails, is the failure in shim transport/request shape, Codex CLI
   behavior, user interaction plumbing, or upstream model behavior?
3. Can the failure be reduced into an automated `codex-eval-runner` task or a
   shim-native profile?

Manual results should not be copied into the model matrix as pass/fail
benchmarks. Convert useful failures into deterministic tasks, then let the
runner own pass/fail.

## Non-Goals

- exact hosted OpenAI parity claims
- replacing `codex-eval-loop` or `codex-eval-loop-bench-lite`
- committing raw `.tmp` run directories, terminal captures, or secrets
- treating pseudo-tool text such as `<read_file>` or `<tool_call>` as a real
  executable tool contract
- promoting flaky interactive cases into `codex-core` before they are
  deterministic

## Current Track Map

Separate V3 Codex tracks are now split as follows:

- eval breadth and auto-regression loop:
  [v3-codex-eval-harness.md](v3-codex-eval-harness.md)
- interactive PTY/session bridge:
  [v3-codex-interactive-command-session-bridge.md](v3-codex-interactive-command-session-bridge.md)
- shim-native request-shape and profile coverage:
  [v3-codex-shim-native-coverage.md](v3-codex-shim-native-coverage.md)
- manual TUI/app-server feature exploration: this document

## Preflight

Before starting a manual interactive pass:

1. Run the relevant automated baseline first:

```bash
make codex-eval-loop
```

For longer stability checks, use the benchmark-lite loop:

```bash
make codex-eval-loop-bench-lite
```

2. Use an isolated Codex home so caches, config, logs, and thread state do not
   bleed into normal development:

```bash
export CODEX_HOME="$PWD/.tmp/codex-manual/<model>-<timestamp>"
mkdir -p "$CODEX_HOME"
```

3. Configure Codex with a shim custom provider as documented in
   [Codex CLI](guides/codex-cli.md). Keep the shim log window for the test:

```bash
tail -n 0 -f .data/shim.log
```

4. Record at least these fields in the manual note:

- repo commit
- Codex version
- model and provider
- shim mode and upstream transport
- exact Codex config overrides
- user actions typed in the TUI
- expected behavior
- observed behavior
- relevant `$CODEX_HOME` log path
- relevant `.data/shim.log` time window
- decision: no issue, shim issue, Codex CLI issue, upstream model issue,
  ambiguous, or automate next

## Manual Feature Matrix

| Feature | Manual check | Evidence to keep | Automation target |
| --- | --- | --- | --- |
| TUI startup and normal prompt | Start `codex` against the shim, ask for a small repo read, then exit cleanly. | Terminal transcript, shim request summary, Codex log. | Existing smoke/core tasks if it fails deterministically. |
| Slash commands: basics | Check `/status`, `/diff`, `/copy`, `/clear`, `/theme`, `/exit`. These are mostly local TUI behavior and may not hit the shim. | Terminal transcript and Codex log. | Usually no runner task unless API traffic changes. |
| Slash commands: review/plan/compact | Check `/review`, `/plan`, and `/compact` on a small repo state. | Codex JSON/log, shim stream summary, final text, diff if edited. | `codex-compat` or future profile task after deterministic reduction. |
| Slash commands: goal | Check `/goal` only when the feature flag exposes it. Local Codex source gates it behind a goal-command flag, and app-server protocol has experimental `thread/goal/*` methods. | App-server/TUI log and shim request window. | Separate app-server compatibility task if the shim needs to proxy or emulate it. |
| Queued follow-up text | While a long turn runs, queue a follow-up with Tab and verify the next turn preserves state. | Terminal transcript, Codex log, shim sequence. | Future state-carrying profile if request shape differs from `codex exec`. |
| `!` shell command | Run a small local command from the TUI shell-command path and verify output display and no model confusion. | Terminal transcript and Codex log. | Shell profile only if it uses the model-visible tool loop. |
| Approval prompts | Force an approval-sensitive command under `workspace-write` and verify allow/deny behavior. | Approval UI transcript, Codex log, shim log if a model turn is involved. | Non-default `codex-compat` profile; do not put in core. |
| Unified exec PTY continuation | Start a live PTY command, capture the `session_id`, then send input. | Codex event log with `session_id`, shim log, terminal output. | Already tracked in `codex-core-interactive` and the interactive-session bridge doc. |
| Multi-agent tools | Enable `features.multi_agent`, ask for a bounded subagent task, then observe spawn/wait/close behavior. | Codex log, model-visible tool registry, shim request shape. | Future shim-native profile; not a real-upstream model score until stable. |
| MCP tools and resources | Configure a tiny local MCP server, list resources, read one resource, and call one tool. | Codex config, MCP status output, Codex log, shim request shape. | Future shim-native MCP profile. |
| Web search | Toggle web search configuration and verify whether Codex exposes/calls the tool. | Codex config and request-shape artifact. | Request-shape profile only; not a shim parity claim by itself. |
| Apps/connectors/plugins | Only test when explicitly enabled and available. Verify registration/listing before model work. | Codex log and local config. | Future app/plugin compatibility profile if shim-visible traffic exists. |
| External agent config import | Exercise `externalAgentConfig/detect` and `externalAgentConfig/import` only from app-server/TUI flows. | App-server protocol log and resulting config diff. | Separate migration/profile task if needed. |

## Suggested Manual Order

1. TUI startup and basic local slash commands.
2. Model-backed slash commands: `/review`, `/plan`, `/compact`.
3. Command and approval interactions.
4. Unified exec PTY continuation.
5. `/goal` if enabled by the current Codex build.
6. Multi-agent tools.
7. MCP resources and tools.
8. Web search, apps, plugins, and external-agent import.
9. Import any durable failure into an automated task or profile.

## Promotion Rules

Promote a manual case only after it has a small, repeatable form:

- If the failure is a normal Codex task failure, import it with
  `codex-eval-runner import-failure`, minimize it, then add it to a suite.
- If the failure is request-shape or transport-specific, add it to
  [v3-codex-shim-native-coverage.md](v3-codex-shim-native-coverage.md).
- If the failure needs a live process `session_id`, keep it under
  [v3-codex-interactive-command-session-bridge.md](v3-codex-interactive-command-session-bridge.md).
- If the failure is model discipline only and the shim request/response shape
  is correct, record it in the model matrix interpretation after a runner
  result exists.

Do not add a manual feature to `codex-core` just because it is interesting.
`codex-core` should stay deterministic and cheap enough to run often.
