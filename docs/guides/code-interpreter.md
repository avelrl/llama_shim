# Code Interpreter

## What It Is

The shim supports a dev-oriented local `code_interpreter` subset inside
`/v1/responses`.

It gives the model a Python execution environment with shim-managed container
state and file handling.

## When To Use It

Use it when you want the model to:

- analyze tabular or text files
- run calculations or scripts
- generate plots or output files
- iterate on code during one response workflow

## Minimal Request

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Use the python tool to calculate the mean of 1, 2, 5, and 8.",
    "tools": [
      {
        "type": "code_interpreter",
        "container": {"type": "auto"}
      }
    ]
  }'
```

## Working With Files

- Input files can come from shim-owned `/v1/files`.
- Generated files are mirrored into shim-managed container file surfaces.
- Final assistant messages can include `container_file_citation` annotations for
  generated artifacts.

## Shim-Specific Notes

- Enable the local runtime with `responses.code_interpreter.backend=docker`.
- The shim reuses `container_id` across local `previous_response_id` lineage
  when possible.
- Shim-local `/v1/responses` accepts only `container: {"type":"auto"}`.
  Explicit `code_interpreter.container="cntr_*"` ids are rejected in local
  mode even though the upstream API supports explicit container mode.
- `include=["code_interpreter_call.outputs"]` is a logs-only practical subset.
- Remote `input_file.file_url` is disabled by default and must be explicitly
  allowlisted or unsafe-enabled.
- Expired local containers are cleaned up in the background via
  `responses.code_interpreter.cleanup_interval`.

## Gotchas

- This is a useful local subset, not hosted-equivalent Code Interpreter.
- Local `/v1/containers` resources exist for shim-managed container state and
  files, but the shim-local `code_interpreter` execution path itself stays on
  auto-managed containers only.
- Exact hosted artifact placement and richer hosted failure/status fidelity are
  out of scope for V2.

## Related Docs

- [Tools Overview](tools.md)
- [Operations](operations.md)
- [Official code-interpreter guide](https://developers.openai.com/api/docs/guides/tools-code-interpreter)
