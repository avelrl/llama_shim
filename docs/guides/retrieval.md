# Retrieval And File Search

## What It Is

`llama_shim` ships a local retrieval substrate behind OpenAI-shaped routes:

- `/v1/files`
- `/v1/vector_stores`
- `/v1/vector_stores/{id}/files`
- `/v1/vector_stores/{id}/search`
- local `file_search` inside `/v1/responses`

This is the main way to build RAG-style flows over your own documents.

## When To Use It

Use retrieval when you want the model to answer from your files instead of only
from model memory.

Typical cases:

- internal docs assistants
- product manuals and runbooks
- ticket or policy search
- local knowledge bases for tool-using agents

## Typical Flow

### 1. Upload a file

```bash
curl http://127.0.0.1:8080/v1/files \
  -H "Content-Type: multipart/form-data" \
  -F purpose=assistants \
  -F file=@./docs/manual.txt
```

### 2. Create a vector store

```bash
curl http://127.0.0.1:8080/v1/vector_stores \
  -H "Content-Type: application/json" \
  -d '{"name": "manuals"}'
```

### 3. Attach the file to the vector store

```bash
curl http://127.0.0.1:8080/v1/vector_stores/vs_.../files \
  -H "Content-Type: application/json" \
  -d '{"file_id": "file_..."}'
```

### 4. Query the store directly

```bash
curl http://127.0.0.1:8080/v1/vector_stores/vs_.../search \
  -H "Content-Type: application/json" \
  -d '{
    "query": "What is the retention policy?",
    "max_num_results": 5
  }'
```

### 5. Let `Responses` use `file_search`

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Answer from the uploaded docs: what is the retention policy?",
    "tools": [
      {
        "type": "file_search",
        "vector_store_ids": ["vs_..."]
      }
    ]
  }'
```

## Shim-Specific Notes

- Lexical search works in the default local setup.
- Semantic, hybrid, and local rerank subsets become available when
  `retrieval.index.backend=sqlite_vec` and a retrieval embedder are configured.
- Canonical ranking values are `auto` and `default-2024-08-21`; shim-local
  `none` disables the local rerank stage.
- `include=["file_search_call.results"]` returns the practical local result
  subset that was retrieved.
- Local `file_search` injects a bounded 20-chunk grounding context before the
  final answer stage.

## Gotchas

- Binary and unsupported attachments are not silently accepted. They can fail
  with explicit file/vector-store status errors.
- Exact hosted retrieval ranking quality is not claimed.
- `file_search` is usable and practical, but it is still documented as a broad
  subset rather than exact hosted planner parity.

## Related Docs

- [Tools Overview](tools.md)
- [Local semantic retrieval setup](../semantic-retrieval-embedanything.md)
- [Official file-search guide](https://developers.openai.com/api/docs/guides/tools-file-search)
