# llama_shim v0.1.0

Initial public OSS release of `llama_shim`, a pragmatic OpenAI-compatible shim for running Codex-style and agent-style clients against local or third-party OpenAI-compatible backends.

## Highlights

- Responses API compatibility layer over stateless Chat Completions backends
- Conversations and previous response continuation with local SQLite-backed state
- SSE streaming support
- stored Chat Completions shadow storage
- fallback proxying for non-shim routes
- provider/model routing support
- OpenAPI contract and compatibility documentation
- runtime hardening notes and configurable limits
- companion compatibility validation path via `openai-compatible-tester`

## Intended use

This release is aimed at OSS maintainers and developers experimenting with local AI, llama.cpp, OpenAI-compatible gateways, Codex-style clients, and coding-agent workflows.

It is not a claim of full hosted OpenAI parity. The project focuses on practical compatibility for real developer workflows.

## Security

This release includes a public security policy and documents security-sensitive surfaces such as authentication, proxying, local persistence, file/retrieval flows, code interpreter backends, and external tool integrations.
