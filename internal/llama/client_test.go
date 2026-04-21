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

func TestCheckReadyDoesNotUseStartupCalibrationToken(t *testing.T) {
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		require.Equal(t, "/v1/models", r.URL.Path)
		seenAuth = r.Header.Get("Authorization")
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "test-model", "object": "model"},
			},
		}))
	}))
	defer server.Close()

	client := NewClientWithOptions(server.URL, time.Second, ClientOptions{
		StartupCalibrationBearerToken: "startup-probe-secret",
	})
	require.NoError(t, client.CheckReady(context.Background()))
	require.Empty(t, seenAuth)
}

func TestGenerateDoesNotUseStartupCalibrationTokenWithoutContextAuthorization(t *testing.T) {
	var seenAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	client := NewClientWithOptions(server.URL, time.Second, ClientOptions{
		StartupCalibrationBearerToken: "startup-probe-secret",
	})

	_, err := client.Generate(context.Background(), "test-model", []domain.MessageItem{
		domain.NewInputTextMessage("user", "ping"),
	}, nil)
	var upstreamErr *UpstreamError
	require.ErrorAs(t, err, &upstreamErr)
	require.Equal(t, http.StatusUnauthorized, upstreamErr.StatusCode)
	require.Empty(t, seenAuth)
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

func TestCreateChatCompletionTextExtractsTypedAssistantContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"content": []map[string]any{
							{"type": "text", "text": "STEP 1"},
							{"type": "text", "text": map[string]any{"value": "\nSTEP 2"}},
						},
					},
				},
			},
		}))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	text, err := client.CreateChatCompletionText(context.Background(), []byte(`{"model":"test-model","messages":[{"role":"user","content":"ping"}]}`))
	require.NoError(t, err)
	require.Equal(t, "STEP 1\nSTEP 2", text)
}

func TestCreateChatCompletionTextFallsBackToChoiceText(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"choices": []map[string]any{
				{
					"text": "legacy text payload",
				},
			},
		}))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	text, err := client.CreateChatCompletionText(context.Background(), []byte(`{"model":"test-model","messages":[{"role":"user","content":"ping"}]}`))
	require.NoError(t, err)
	require.Equal(t, "legacy text payload", text)
}

func TestCreateChatCompletionTextExtractsNestedAssistantParts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
			"id":                 "chatcmpl-test",
			"object":             "chat.completion",
			"created":            1776799448,
			"model":              "unsloth/Kimi-K2.5",
			"system_fingerprint": "b8683-d0a",
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role": "assistant",
						"content": map[string]any{
							"parts": []map[string]any{
								{"type": "output_text", "value": "OK"},
							},
						},
					},
					"finish_reason": "stop",
				},
			},
		}))
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	text, err := client.CreateChatCompletionText(context.Background(), []byte(`{"model":"test-model","messages":[{"role":"user","content":"ping"}]}`))
	require.NoError(t, err)
	require.Equal(t, "OK", text)
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

func TestRunStartupCalibrationCompletesWithRecommendations(t *testing.T) {
	var chatHits int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			atomic.AddInt32(&chatHits, 1)
			time.Sleep(20 * time.Millisecond)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": "OK",
						},
					},
				},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	snapshot := client.RunStartupCalibration(context.Background(), StartupCalibrationOptions{
		Enabled:              true,
		ProbeCount:           3,
		RequestTimeout:       200 * time.Millisecond,
		UpstreamTimeout:      time.Second,
		ShimWriteTimeout:     2 * time.Second,
		CurrentMaxConcurrent: 12,
	})

	require.Equal(t, startupCalibrationStatusCompleted, snapshot.Status)
	require.True(t, snapshot.Enabled)
	require.True(t, snapshot.ModelsReady)
	require.Equal(t, "test-model", snapshot.Model)
	require.Equal(t, 3, snapshot.ProbeCount)
	require.Equal(t, 3, snapshot.SuccessfulProbes)
	require.NotNil(t, snapshot.ObservedLatency)
	require.NotNil(t, snapshot.Recommendation)
	require.NotZero(t, snapshot.Recommendation.SuggestedMaxConcurrentRequests)
	require.NotEmpty(t, snapshot.Recommendation.Warnings)
	require.Equal(t, int32(3), atomic.LoadInt32(&chatHits))

	latest := client.StartupCalibrationSnapshot()
	require.Equal(t, startupCalibrationStatusCompleted, latest.Status)
	require.Equal(t, snapshot.Model, latest.Model)
}

func TestRunStartupCalibrationFailsWhenNoChatProbeSucceeds(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			http.Error(w, "backend failed", http.StatusBadGateway)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	snapshot := client.RunStartupCalibration(context.Background(), StartupCalibrationOptions{
		Enabled:        true,
		ProbeCount:     2,
		RequestTimeout: 100 * time.Millisecond,
	})

	require.Equal(t, startupCalibrationStatusFailed, snapshot.Status)
	require.Contains(t, snapshot.Error, "all startup chat probes failed")
	require.Nil(t, snapshot.ObservedLatency)
	require.Nil(t, snapshot.Recommendation)
}

func TestRunStartupCalibrationUsesConfiguredBearerToken(t *testing.T) {
	var (
		seenModelsAuth string
		seenChatAuth   string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			seenModelsAuth = r.Header.Get("Authorization")
			if seenModelsAuth != "Bearer startup-probe-secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			seenChatAuth = r.Header.Get("Authorization")
			if seenChatAuth != "Bearer startup-probe-secret" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
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
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClientWithOptions(server.URL, time.Second, ClientOptions{
		StartupCalibrationBearerToken: "startup-probe-secret",
	})
	snapshot := client.RunStartupCalibration(context.Background(), StartupCalibrationOptions{
		Enabled:        true,
		ProbeCount:     1,
		RequestTimeout: 200 * time.Millisecond,
	})

	require.Equal(t, startupCalibrationStatusCompleted, snapshot.Status)
	require.Equal(t, "Bearer startup-probe-secret", seenModelsAuth)
	require.Equal(t, "Bearer startup-probe-secret", seenChatAuth)
}

func TestRunStartupCalibrationUsesMixedProbeShapes(t *testing.T) {
	var seenPayloads []map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			var payload map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
			seenPayloads = append(seenPayloads, payload)
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": "OK",
						},
					},
				},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	snapshot := client.RunStartupCalibration(context.Background(), StartupCalibrationOptions{
		Enabled:        true,
		ProbeCount:     3,
		RequestTimeout: 200 * time.Millisecond,
	})

	require.Equal(t, startupCalibrationStatusCompleted, snapshot.Status)
	require.Len(t, seenPayloads, 3)
	require.Equal(t, "test-model", seenPayloads[0]["model"])
	require.Equal(t, float64(1), seenPayloads[0]["max_tokens"])
	require.Equal(t, "Reply with OK.", seenPayloads[0]["messages"].([]any)[0].(map[string]any)["content"])
	require.Equal(t, "test-model", seenPayloads[1]["model"])
	require.Equal(t, float64(224), seenPayloads[1]["max_tokens"])
	require.Contains(t, seenPayloads[1]["messages"].([]any)[0].(map[string]any)["content"], "A courier starts at 09:00.")
	require.Contains(t, seenPayloads[1]["messages"].([]any)[0].(map[string]any)["content"], "Reply with exactly four lines.")
	require.Equal(t, "test-model", seenPayloads[2]["model"])
	require.Equal(t, float64(320), seenPayloads[2]["max_tokens"])
	require.Contains(t, seenPayloads[2]["messages"].([]any)[0].(map[string]any)["content"], "A technician starts at 08:30 from the depot")
	require.Contains(t, seenPayloads[2]["messages"].([]any)[0].(map[string]any)["content"], "Reply with exactly five lines.")
}

func TestRunStartupCalibrationEmitsProgressEvents(t *testing.T) {
	var events []StartupCalibrationProgressEvent
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/models":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "test-model", "object": "model"},
				},
			}))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/chat/completions":
			require.NoError(t, json.NewEncoder(w).Encode(map[string]any{
				"choices": []map[string]any{
					{
						"message": map[string]any{
							"content": "OK",
						},
					},
				},
			}))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, time.Second)
	snapshot := client.RunStartupCalibration(context.Background(), StartupCalibrationOptions{
		Enabled:        true,
		ProbeCount:     2,
		RequestTimeout: 200 * time.Millisecond,
		Progress: func(event StartupCalibrationProgressEvent) {
			events = append(events, event)
		},
	})

	require.Equal(t, startupCalibrationStatusCompleted, snapshot.Status)
	require.Len(t, events, 3)
	require.Equal(t, "models", events[0].Step)
	require.True(t, events[0].Success)
	require.Equal(t, 200, events[0].StatusCode)
	require.Equal(t, 1, events[0].ModelsCount)
	require.Equal(t, "test-model", events[0].ResponsePreview)
	require.Equal(t, "probe", events[1].Step)
	require.True(t, events[1].Success)
	require.Equal(t, 1, events[1].ProbeIndex)
	require.Equal(t, 2, events[1].ProbeCount)
	require.Equal(t, "quick_ok", events[1].ProbeProfile)
	require.Equal(t, 1, events[1].MaxTokens)
	require.Equal(t, "test-model", events[1].Model)
	require.Equal(t, 200, events[1].StatusCode)
	require.Equal(t, "OK", events[1].ResponsePreview)
	require.Equal(t, "probe", events[2].Step)
	require.True(t, events[2].Success)
	require.Equal(t, 2, events[2].ProbeIndex)
	require.Equal(t, "reasoning_schedule", events[2].ProbeProfile)
	require.Equal(t, 224, events[2].MaxTokens)
}
