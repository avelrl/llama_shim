# V4 Extensions And Plugin Model

Last updated: April 23, 2026.

This document is the parking lot for post-compatibility work that is useful in
practice, but should not be confused with the shim's core OpenAI-compatibility
promise.

V2 is the broad compatibility facade.
V3 is backend and runtime expansion around that facade.
V4 is where the shim can grow opinionated memory, retrieval, and plugin
capabilities without pretending they are first-party OpenAI API contracts.

## Why V4 Exists

As of April 15, 2026, the public OpenAI surfaces relevant here are:

- hosted `file_search` over `vector_stores` for knowledge retrieval
- managed conversation state via `previous_response_id` and the Conversations
  API
- Agents SDK `session` patterns for durable memory that your application
  controls

What OpenAI does not currently expose as a stable public API contract is a
generic long-term user-memory endpoint that the shim should mirror one-to-one.

That matters because "memory" work splits into at least two different jobs:

- short-term conversation continuity
- durable application-owned user or task state

Those should not be modeled as the same thing.

## Practical Read On OpenAI Memory

OpenAI's current public guidance points in a useful direction:

- `previous_response_id` and Conversations API are the light server-managed
  state layer for continuing a conversation
- Agents SDK sessions are the recommended higher-level memory/state layer when
  the application wants durable storage under its own control
- hosted `file_search` and Retrieval are for knowledge-base lookup, not for
  being the only memory primitive for mutable user state

The OpenAI cookbook guidance is explicit that retrieval-based memory is weaker
than state-based memory for evolving user preferences and constraints. In that
guidance, retrieval treats old interactions like loosely related documents,
which makes it brittle to phrasing, easy to miss on overrides, and poor at
resolving conflicts or recency.

For the shim, that means:

- retrieval is the right substrate for documents, manuals, policies, and large
  corpora
- compaction and sessions are the right substrate for short-term continuity
- state-based memory is the right substrate for durable preferences,
  constraints, open loops, and profile-like facts

## Classification

V4 work should be classified before implementation.

### 1. Core compatibility

Use this label when the shim is trying to match a documented OpenAI HTTP or SDK
surface closely enough to make a compatibility claim.

Examples:

- `/v1/responses` request and response semantics
- `file_search` request shape and output subset
- `previous_response_id` continuation behavior

Core compatibility work belongs in V2 or V3, not here, unless it is only being
referenced as a dependency.

### 2. Extension

Use this label when the shim adds useful behavior on top of OpenAI-shaped
surfaces without claiming that the behavior itself is an official OpenAI API
feature.

Examples:

- automatic memory injection into local `/v1/responses`
- durable profile memory carried across local conversations
- hybrid compaction plus memory plus retrieval policies

Extensions should prefer one of these shapes:

- shim-local config under existing config files
- shim-owned metadata attached to local state
- behavior behind existing OpenAI-shaped routes, without inventing fake parity

Avoid new public HTTP routes unless there is a strong operational reason.

### 3. Plugin

Use this label when the behavior is primarily a pluggable backend or provider
implementation behind an extension interface.

Examples:

- `MemoryStore` backed by SQLite, Postgres, Redis, or a managed memory service
- retrieval adapters for local vector stores, pgvector, Pinecone, Weaviate, or
  graph-backed retrieval
- embedders, rerankers, and memory consolidators that can be swapped without
  changing the public shim contract

Plugins are about substrate choice. Extensions are about feature behavior.

## Candidate V4 Tracks

### 1. State-based memory extension

Classification: extension with pluggable backends.

Goal:
Let the shim maintain durable user and task state without pretending that this
is an OpenAI-native public API surface.

Useful directions:

- global memory notes for durable preferences and constraints
- session memory notes for short-lived context
- explicit promotion rules from session memory into global memory
- recency-aware conflict resolution and deduplication
- memory injection policies for local `/v1/responses` and `/v1/conversations`
- guardrails for PII, consent, and redaction

### 2. Retrieval-backed knowledge extension

Classification: extension with plugin backends.

Goal:
Keep retrieval useful for what it is actually good at: external knowledge and
large document corpora.

Useful directions:

- external vector-store adapters
- richer chunking and ingestion pipelines
- reranker plugins
- graph or hybrid retrieval for multi-hop knowledge lookup
- stronger source attribution and grounding metadata

This is not a substitute for durable state-based memory.

### 3. Hybrid memory orchestration

Classification: extension.

Goal:
Coordinate compaction, session state, retrieval, and long-term memory without
forcing one mechanism to do every job badly.

Useful directions:

- policy engine for "keep raw vs compact vs store as memory vs send to
  retrieval"
- per-turn extraction of candidate durable facts
- explicit separation between conversational state and knowledge retrieval
- replay-safe memory injection for local create, stream, and retrieve flows

### 4. Personalization and profile memory

Classification: extension with pluggable storage backends.

Goal:
Store user preferences and stable profile facts in a more deterministic form
than retrieval can provide.

Useful directions:

- structured profile fields with precedence rules
- scoped overrides such as global vs tenant vs project vs session
- TTL and archival rules
- audit trail for memory mutations
- admin controls for export, purge, and redaction

### 5. Entity and graph memory

Classification: extension with plugin backends.

Goal:
Support workflows where state is better represented as entities, relations, and
time-aware facts rather than flat notes.

Useful directions:

- entity extraction pipelines
- relation and timeline storage
- temporal validity and supersession rules
- graph traversal as a retrieval substrate

### 6. Plugin SDK and backend contract cleanup

Classification: plugin platform work.

Goal:
Make backend diversity practical without leaking provider-specific behavior into
the public OpenAI-compatible facade.

Useful directions:

- stable interfaces such as `Compactor`, `MemoryStore`, `RetrievalStore`,
  `Embedder`, `Reranker`, `MemoryConsolidator`
- readiness and capability reporting per plugin
- namespaced config for optional backends
- provider-specific knobs kept behind backend config, not exposed as fake
  OpenAI request fields

### 7. Local execution hardening

Classification: extension and platform hardening.

Goal:
Strengthen shim-local execution isolation beyond the current Docker baseline
without pretending that this is part of the public OpenAI API contract.

Useful directions:

- minimal purpose-built runtime images instead of a general Python base image
- tighter seccomp, AppArmor, or similar sandbox profiles
- alternative isolated runtimes such as gVisor, Kata, or comparable container
  sandboxes
- smaller visible filesystem and fewer bundled userland tools inside the local
  execution image
- clearer capability reporting for which hardening layer is active in a given
  deployment

### 8. Shim-owned opaque state encryption

Classification: extension and platform hardening.

Goal:
Make shim-owned continuation artifacts less readable outside the shim without
claiming exact hosted OpenAI encrypted-state parity.

The V3 compaction track currently uses an OpenAI-shaped `compaction` item with
shim-owned opaque content. The public surface is intentionally still
`encrypted_content`, but the local implementation uses a readable
`llama_shim.compaction.v1:<base64-json>` payload so developers can inspect and
debug the compacted state while the feature is maturing.

This is acceptable for a local development shim, but it should not be treated
as a long-term security boundary. Once compaction state is shared across users,
stored in less trusted systems, or logged in production, the shim should support
real local encryption for its own opaque state.

Useful directions:

- add an AES-GCM encrypted compaction payload version such as
  `llama_shim.compaction.v2:<base64url nonce+ciphertext>`
- load encryption material from shim-owned config or environment, not from
  OpenAI-compatible request fields
- keep the key-management story explicit: required key length, rotation plan,
  startup validation, and failure behavior when a key is missing or wrong
- preserve backward-compatible reads for existing `v1` payloads during a
  migration window
- keep client-visible semantics unchanged: clients still pass the
  `compaction` item through as opaque `encrypted_content`
- avoid claiming hosted OpenAI parity; this is local confidentiality hardening,
  not evidence that the shim matches OpenAI's internal encrypted state runtime
- add tests for decrypt failure, unknown version, tampered ciphertext, key
  rotation, `previous_response_id`, conversation replay, standalone compact,
  and automatic `context_management` compaction

Non-goals for the first slice:

- no public route for reading decrypted compaction state
- no client-provided encryption keys on OpenAI-compatible endpoints
- no hard dependency on encryption for local development configs
- no change to the OpenAI-compatible response shape

## Working Rule

If a task improves a public OpenAI-compatible contract we already expose, it is
not V4.

If a task adds application-owned behavior on top of that contract, it is an
extension.

If a task mainly swaps the storage, ranking, or execution substrate for an
extension, it is a plugin.
