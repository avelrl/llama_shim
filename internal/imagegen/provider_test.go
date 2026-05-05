package imagegen

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeConfigAcceptsFixtureWithoutBaseURL(t *testing.T) {
	cfg, err := NormalizeConfig(Config{
		Backend: BackendFixture,
		BaseURL: "http://ignored.example",
		Timeout: 30 * time.Second,
	})
	require.NoError(t, err)
	require.Equal(t, BackendFixture, cfg.Backend)
	require.Empty(t, cfg.BaseURL)
	require.Zero(t, cfg.Timeout)
}

func TestNormalizeConfigAcceptsComfyUIWithInlineWorkflow(t *testing.T) {
	cfg, err := NormalizeConfig(Config{
		Backend: BackendComfyUI,
		BaseURL: "http://127.0.0.1:8188/",
		ComfyUI: ComfyUIConfig{
			Workflow: map[string]any{
				"3": map[string]any{
					"class_type": "SaveImage",
				},
			},
		},
	})
	require.NoError(t, err)
	require.Equal(t, BackendComfyUI, cfg.Backend)
	require.Equal(t, "http://127.0.0.1:8188", cfg.BaseURL)
	require.Equal(t, defaultTimeout, cfg.Timeout)
	require.Equal(t, defaultComfyUIPoll, cfg.ComfyUI.PollInterval)
	require.Equal(t, defaultComfyUIMaxWait, cfg.ComfyUI.MaxWait)
	require.EqualValues(t, defaultComfyUIMaxImageBytes, cfg.ComfyUI.MaxImageBytes)
}

func TestNormalizeConfigRejectsComfyUIWithoutWorkflow(t *testing.T) {
	_, err := NormalizeConfig(Config{
		Backend: BackendComfyUI,
		BaseURL: "http://127.0.0.1:8188",
	})
	require.ErrorContains(t, err, "responses.image_generation.comfyui.workflow or workflow_path")
}

func TestFixtureProviderCreateReturnsDeterministicImageResponse(t *testing.T) {
	provider, err := NewProvider(Config{Backend: BackendFixture})
	require.NoError(t, err)
	require.NotNil(t, provider)

	body := []byte(`{
		"model": "devstack-model",
		"input": "Generate a tiny orange cat in a teacup.",
		"tools": [{"type": "image_generation", "output_format": "png", "quality": "low", "size": "1024x1024"}]
	}`)
	raw, err := provider.Create(context.Background(), body)
	require.NoError(t, err)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, "completed", decoded["status"])
	require.Equal(t, "devstack-model", decoded["model"])
	output := decoded["output"].([]any)
	require.Len(t, output, 1)
	item := output[0].(map[string]any)
	require.Equal(t, "image_generation_call", item["type"])
	require.Equal(t, "completed", item["status"])
	require.Equal(t, fixtureImageBase64, item["result"])
	require.Equal(t, "Generate a tiny orange cat in a teacup.", item["revised_prompt"])
}

func TestComfyUIProviderCreateQueuesPollsAndFetchesImage(t *testing.T) {
	var capturedPrompt map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/system_stats":
			_ = json.NewEncoder(w).Encode(map[string]any{"system": "ok"})
		case r.Method == http.MethodPost && r.URL.Path == "/prompt":
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			capturedPrompt = payload["prompt"].(map[string]any)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"prompt_id":   "prompt_fixture_1",
				"node_errors": map[string]any{},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/history/prompt_fixture_1":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"prompt_fixture_1": map[string]any{
					"status": map[string]any{
						"completed":  true,
						"status_str": "success",
					},
					"outputs": map[string]any{
						"9": map[string]any{
							"images": []map[string]any{{
								"filename":  "llama_shim.png",
								"subfolder": "",
								"type":      "output",
							}},
						},
					},
				},
			})
		case r.Method == http.MethodGet && r.URL.Path == "/view":
			require.Equal(t, "llama_shim.png", r.URL.Query().Get("filename"))
			_, _ = w.Write([]byte("fake-image"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	provider, err := NewProvider(Config{
		Backend: BackendComfyUI,
		BaseURL: server.URL,
		Timeout: time.Second,
		ComfyUI: ComfyUIConfig{
			PollInterval:  time.Millisecond,
			MaxWait:       time.Second,
			MaxImageBytes: 1024,
			OutputNodeID:  "9",
			Workflow: map[string]any{
				"6": map[string]any{
					"class_type": "CLIPTextEncode",
					"inputs": map[string]any{
						"text": "{{prompt}}",
					},
				},
				"5": map[string]any{
					"class_type": "EmptyLatentImage",
					"inputs": map[string]any{
						"width":  "{{width}}",
						"height": "{{height}}",
					},
				},
				"9": map[string]any{
					"class_type": "SaveImage",
					"inputs": map[string]any{
						"filename_prefix": "{{filename_prefix}}",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	require.NoError(t, provider.CheckReady(context.Background()))

	raw, err := provider.Create(context.Background(), []byte(`{
		"model": "devstack-model",
		"input": "Generate a tiny orange cat in a teacup.",
		"tools": [{"type": "image_generation", "size": "512x768"}]
	}`))
	require.NoError(t, err)

	textNode := capturedPrompt["6"].(map[string]any)["inputs"].(map[string]any)
	require.Equal(t, "Generate a tiny orange cat in a teacup.", textNode["text"])
	latentNode := capturedPrompt["5"].(map[string]any)["inputs"].(map[string]any)
	require.EqualValues(t, 512, latentNode["width"])
	require.EqualValues(t, 768, latentNode["height"])

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(raw, &decoded))
	output := decoded["output"].([]any)
	item := output[0].(map[string]any)
	require.Equal(t, "image_generation_call", item["type"])
	require.Equal(t, fixtureImageBase64, item["result"])
	require.Equal(t, "512x768", item["size"])
	require.Equal(t, "Generate a tiny orange cat in a teacup.", item["revised_prompt"])
}

func TestComfyUIProviderRejectsGenericEdit(t *testing.T) {
	provider, err := NewProvider(Config{
		Backend: BackendComfyUI,
		BaseURL: "http://127.0.0.1:8188",
		Timeout: time.Second,
		ComfyUI: ComfyUIConfig{
			Workflow: map[string]any{
				"9": map[string]any{
					"class_type": "SaveImage",
				},
			},
		},
	})
	require.NoError(t, err)

	_, err = provider.Create(context.Background(), []byte(`{
		"model": "devstack-model",
		"input": "Edit the previous image.",
		"tools": [{"type": "image_generation", "action": "edit"}]
	}`))
	require.ErrorContains(t, err, "supports generate/auto only")
}

func TestFixtureProviderStreamEmitsPartialImagesAndCompletedResponse(t *testing.T) {
	provider, err := NewProvider(Config{Backend: BackendFixture})
	require.NoError(t, err)

	stream, err := provider.CreateStream(context.Background(), []byte(`{
		"model": "devstack-model",
		"input": "Generate a fixture image.",
		"tools": [{"type": "image_generation"}]
	}`))
	require.NoError(t, err)
	defer stream.Body.Close()

	require.Equal(t, 200, stream.StatusCode)
	require.Contains(t, stream.Header.Get("Content-Type"), "text/event-stream")
	raw, err := io.ReadAll(stream.Body)
	require.NoError(t, err)
	sse := string(raw)
	require.Contains(t, sse, "event: response.image_generation_call.partial_image")
	require.Contains(t, sse, `"partial_image_b64":"cGFydGlhbC0w"`)
	require.Contains(t, sse, `"partial_image_b64":"cGFydGlhbC0x"`)
	require.Contains(t, sse, "event: response.completed")
	require.True(t, strings.Contains(sse, `"type":"response.completed"`))
}
