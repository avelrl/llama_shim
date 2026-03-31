package llama

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestBuildChatCompletionRequestCollapsesAdjacentRoles(t *testing.T) {
	client := NewClient("http://example.com", 0)

	body, err := client.buildChatCompletionRequest("test-model", []domain.MessageItem{
		domain.NewInputTextMessage("system", "You are a test assistant."),
		domain.NewInputTextMessage("user", "Remember: code=777. Reply OK."),
		domain.NewInputTextMessage("user", "What is the code? Reply with just the number."),
	}, false, nil)
	require.NoError(t, err)

	var payload ChatCompletionRequest
	require.NoError(t, json.Unmarshal(body, &payload))
	require.Equal(t, "test-model", payload.Model)
	require.Len(t, payload.Messages, 2)
	require.Equal(t, "system", payload.Messages[0].Role)
	require.Equal(t, "You are a test assistant.", payload.Messages[0].Content)
	require.Equal(t, "user", payload.Messages[1].Role)
	require.Equal(t, "Remember: code=777. Reply OK.\n\nWhat is the code? Reply with just the number.", payload.Messages[1].Content)
}

func TestGenerateForwardsAuthorizationFromContext(t *testing.T) {
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": "OK",
					},
				},
			},
		}))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	ctx := ContextWithForwardHeaders(context.Background(), http.Header{
		"Authorization": []string{"Bearer test-token"},
	})

	text, err := client.Generate(ctx, "test-model", []domain.MessageItem{
		domain.NewInputTextMessage("user", "ping"),
	}, nil)
	require.NoError(t, err)
	require.Equal(t, "OK", text)
	require.Equal(t, "Bearer test-token", seenAuth)
}

func TestContextHeadersDoNotOverrideExplicitAuthorization(t *testing.T) {
	headers := http.Header{
		"Authorization": []string{"Bearer request-token"},
	}
	ctx := ContextWithForwardHeaders(context.Background(), http.Header{
		"Authorization": []string{"Bearer context-token"},
		"X-Api-Key":     []string{"secret"},
	})

	applyContextHeaders(ctx, headers)

	require.Equal(t, "Bearer request-token", headers.Get("Authorization"))
	require.Equal(t, "secret", headers.Get("X-Api-Key"))
}
