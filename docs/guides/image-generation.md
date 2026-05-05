# Image Generation

## What It Is

The shim supports a practical local `image_generation` subset inside
`/v1/responses`.

It delegates image work to a configured shim-local image backend while keeping
the surrounding response state, storage, and replay in the shim.

## When To Use It

Use it when you want the model to:

- generate an image from a text prompt
- edit an image across turns when the configured backend supports edit inputs
- keep the image workflow inside `Responses`

## Minimal Request

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "input": "Generate a logo sketch for a local search product.",
    "tools": [{"type": "image_generation"}]
  }'
```

## Multi-Turn Edit Pattern

```bash
curl http://127.0.0.1:8080/v1/responses \
  -H "Content-Type: application/json" \
  -d '{
    "model": "<model>",
    "previous_response_id": "resp_...",
    "input": "Keep the same composition, but make it monochrome.",
    "tools": [{"type": "image_generation"}]
  }'
```

## Shim-Specific Notes

- Enable the local runtime with `responses.image_generation.backend=responses`
  for a separate OpenAI-compatible image service, `comfyui` for a configured
  ComfyUI workflow, or `fixture` for the deterministic devstack/test backend.
- The shim keeps the outer response object, storage, and retrieve-stream story
  local, even though actual image generation is delegated.
- Current-turn image inputs and local image-edit lineage are flattened into the
  shim-owned input sent to the image backend.
- If the backend emits `response.image_generation_call.partial_image`, the shim
  persists those partial artifacts for later replay.
- The generic `comfyui` backend queues `/prompt`, polls
  `/history/{prompt_id}`, downloads the selected `/view` image, and returns it
  as an `image_generation_call.result`. It supports text-to-image generation;
  image edit/input support is workflow-specific and rejected until a dedicated
  adapter is configured.
- The `fixture` backend returns a deterministic placeholder image and partial
  image stream. It is for regression coverage, not production image generation.

## Gotchas

- This is a practical local subset, not an exact hosted image-generation
  planner contract.
- Exact live partial-image timing and richer hosted failure choreography are not
  part of the V2 promise.

## Related Docs

- [Tools Overview](tools.md)
- [Official image generation guide](https://developers.openai.com/api/docs/guides/image-generation)
