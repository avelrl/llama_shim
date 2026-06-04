# Security Policy

## Supported Versions

`llama_shim` is currently an early-stage OSS project. Security fixes are handled on the `master` branch until tagged release lines are introduced.

| Version | Supported |
| ------- | --------- |
| master  | Yes       |
| < 0.1.0 | No        |

## Reporting a Vulnerability

Please report security issues privately by opening a GitHub Security Advisory if available, or by contacting the maintainer through GitHub.

Do not open a public issue for vulnerabilities that could expose credentials, local files, execution surfaces, or deployment internals.

When reporting, please include:

- a short description of the issue
- affected endpoint or component
- reproduction steps
- expected vs actual behavior
- impact assessment
- relevant logs with secrets removed

## Security Scope

The following areas are considered security-sensitive:

- authentication and bearer token handling
- request proxying to upstream OpenAI-compatible backends
- local state storage and SQLite persistence
- file upload and retrieval-style flows
- code interpreter / container execution integrations
- web search, image generation, and computer-use backends
- request logging and telemetry configuration
- rate limits and resource limits
- environment variable and config file handling

## Non-Goals

This project does not provide a sandboxed model runtime by itself. If an upstream model server, tool backend, container runtime, or external provider is enabled, operators are responsible for securing those services and their network boundaries.

`llama_shim` aims to make security-relevant behavior explicit and configurable, but it should not be exposed to untrusted networks without authentication, rate limits, logging review, and deployment hardening.

## Disclosure

The maintainer will acknowledge valid reports when possible and prioritize fixes based on impact, exploitability, and affected deployment surface.
