package imagegen

import (
	"context"
	"encoding/json"
	"io"
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
