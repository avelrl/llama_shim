package llama

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"llama_shim/internal/domain"
)

func TestBuildChatCompletionRequestPreservesAdjacentRoles(t *testing.T) {
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
	require.Len(t, payload.Messages, 3)
	require.Equal(t, "system", payload.Messages[0].Role)
	require.Equal(t, "You are a test assistant.", payload.Messages[0].Content)
	require.Equal(t, "user", payload.Messages[1].Role)
	require.Equal(t, "Remember: code=777. Reply OK.", payload.Messages[1].Content)
	require.Equal(t, "user", payload.Messages[2].Role)
	require.Equal(t, "What is the code? Reply with just the number.", payload.Messages[2].Content)
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

func TestCreateChatCompletionTextExtractsAssistantContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": `{"input":"hello 42"}`,
					},
				},
			},
		}))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	text, err := client.CreateChatCompletionText(context.Background(), []byte(`{"model":"test-model","messages":[{"role":"user","content":"ping"}]}`))
	require.NoError(t, err)
	require.Equal(t, `{"input":"hello 42"}`, text)
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

func TestCheckReady(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, http.MethodGet, r.Method)
			require.Equal(t, "/v1/models", r.URL.Path)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		}))
		defer server.Close()

		client := NewClient(server.URL, time.Second)
		require.NoError(t, client.CheckReady(context.Background()))
	})

	t.Run("upstream error", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "backend failed", http.StatusBadGateway)
		}))
		defer server.Close()

		client := NewClient(server.URL, time.Second)
		err := client.CheckReady(context.Background())
		var upstreamErr *UpstreamError
		require.ErrorAs(t, err, &upstreamErr)
		require.Equal(t, http.StatusBadGateway, upstreamErr.StatusCode)
	})

	t.Run("invalid payload", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
			}))
		}))
		defer server.Close()

		client := NewClient(server.URL, time.Second)
		var invalidErr *InvalidResponseError
		err := client.CheckReady(context.Background())
		require.ErrorAs(t, err, &invalidErr)
		require.Contains(t, invalidErr.Message, "did not contain data")
	})
}

func TestNewClientWithOptionsConfiguresTransport(t *testing.T) {
	client := NewClientWithOptions("http://example.com", time.Second, ClientOptions{
		Transport: TransportOptions{
			MaxIdleConns:          17,
			MaxIdleConnsPerHost:   9,
			MaxConnsPerHost:       5,
			IdleConnTimeout:       45 * time.Second,
			DialTimeout:           3 * time.Second,
			KeepAlive:             11 * time.Second,
			TLSHandshakeTimeout:   7 * time.Second,
			ExpectContinueTimeout: 2 * time.Second,
		},
	})

	requestTransport, ok := client.requestClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, 17, requestTransport.MaxIdleConns)
	require.Equal(t, 9, requestTransport.MaxIdleConnsPerHost)
	require.Equal(t, 5, requestTransport.MaxConnsPerHost)
	require.Equal(t, 45*time.Second, requestTransport.IdleConnTimeout)
	require.Equal(t, 7*time.Second, requestTransport.TLSHandshakeTimeout)
	require.Equal(t, 2*time.Second, requestTransport.ExpectContinueTimeout)

	streamTransport, ok := client.streamClient.Transport.(*http.Transport)
	require.True(t, ok)
	require.Equal(t, requestTransport.MaxIdleConns, streamTransport.MaxIdleConns)
	require.Equal(t, requestTransport.MaxIdleConnsPerHost, streamTransport.MaxIdleConnsPerHost)
	require.Equal(t, requestTransport.MaxConnsPerHost, streamTransport.MaxConnsPerHost)
}

func TestGenerateAdmissionControllerSerializesRequests(t *testing.T) {
	var (
		active  int32
		maxSeen int32
		hits    int32
	)
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		current := atomic.AddInt32(&active, 1)
		defer atomic.AddInt32(&active, -1)
		for {
			prior := atomic.LoadInt32(&maxSeen)
			if current <= prior || atomic.CompareAndSwapInt32(&maxSeen, prior, current) {
				break
			}
		}
		if atomic.AddInt32(&hits, 1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
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

	client := NewClientWithOptions(server.URL, time.Second, ClientOptions{MaxConcurrentRequests: 1})
	firstDone := make(chan error, 1)
	go func() {
		_, err := client.Generate(context.Background(), "test-model", []domain.MessageItem{
			domain.NewInputTextMessage("user", "first"),
		}, nil)
		firstDone <- err
	}()

	<-firstStarted
	secondDone := make(chan error, 1)
	go func() {
		_, err := client.Generate(context.Background(), "test-model", []domain.MessageItem{
			domain.NewInputTextMessage("user", "second"),
		}, nil)
		secondDone <- err
	}()

	time.Sleep(50 * time.Millisecond)
	require.Equal(t, int32(1), atomic.LoadInt32(&hits))

	close(releaseFirst)
	require.NoError(t, <-firstDone)
	require.NoError(t, <-secondDone)
	require.Equal(t, int32(2), atomic.LoadInt32(&hits))
	require.Equal(t, int32(1), atomic.LoadInt32(&maxSeen))
}

func TestGenerateAdmissionQueueTimeout(t *testing.T) {
	firstStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var hits int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			close(firstStarted)
			<-releaseFirst
		}
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

	client := NewClientWithOptions(server.URL, time.Second, ClientOptions{
		MaxConcurrentRequests: 1,
		MaxQueueWait:          20 * time.Millisecond,
	})
	firstDone := make(chan error, 1)
	go func() {
		_, err := client.Generate(context.Background(), "test-model", []domain.MessageItem{
			domain.NewInputTextMessage("user", "first"),
		}, nil)
		firstDone <- err
	}()

	<-firstStarted
	_, err := client.Generate(context.Background(), "test-model", []domain.MessageItem{
		domain.NewInputTextMessage("user", "second"),
	}, nil)
	var timeoutErr *TimeoutError
	require.ErrorAs(t, err, &timeoutErr)
	require.Contains(t, timeoutErr.Error(), "queue wait")
	require.Equal(t, int32(1), atomic.LoadInt32(&hits))

	close(releaseFirst)
	require.NoError(t, <-firstDone)
}

func TestProxyAdmissionSlotHeldUntilBodyClose(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		_, writeErr := io.WriteString(w, `{"ok":true}`)
		require.NoError(t, writeErr)
	}))
	defer server.Close()

	client := NewClientWithOptions(server.URL, time.Second, ClientOptions{MaxConcurrentRequests: 1})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"test"}`)))
	resp, err := client.Proxy(context.Background(), req)
	require.NoError(t, err)

	blockedCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err = client.Proxy(blockedCtx, httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"test"}`))))
	var timeoutErr *TimeoutError
	require.ErrorAs(t, err, &timeoutErr)

	require.NoError(t, resp.Body.Close())

	resp, err = client.Proxy(context.Background(), httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader([]byte(`{"model":"test"}`))))
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}
