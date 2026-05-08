package httpapi

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDebugTraceStoreBoundsAndCopiesEntries(t *testing.T) {
	store := NewDebugTraceStore(1)
	firstReq, err := http.NewRequest(http.MethodGet, "/v1/responses", nil)
	require.NoError(t, err)
	firstCtx := RequestContextWithID(context.Background(), "req_first")
	firstCtx = store.Begin(firstCtx, firstReq, time.Unix(1, 0))
	store.Finish(firstCtx, http.StatusOK, "/v1/responses", "application/json", time.Millisecond, time.Unix(1, int64(time.Millisecond)))

	secondReq, err := http.NewRequest(http.MethodGet, "/v1/chat/completions", nil)
	require.NoError(t, err)
	secondCtx := RequestContextWithID(context.Background(), "req_second")
	secondCtx = store.Begin(secondCtx, secondReq, time.Unix(2, 0))
	store.Finish(secondCtx, http.StatusAccepted, "/v1/chat/completions", "application/json", 2*time.Millisecond, time.Unix(2, int64(2*time.Millisecond)))

	_, ok := store.Get("req_first")
	require.False(t, ok)
	trace, ok := store.Get("req_second")
	require.True(t, ok)
	require.Equal(t, "shim.debug_trace", trace.Object)
	require.Equal(t, "chat_completions", trace.Surface)
	require.Equal(t, http.StatusAccepted, trace.FinalStatus)

	trace.Method = "MUTATED"
	again, ok := store.Get("req_second")
	require.True(t, ok)
	require.Equal(t, http.MethodGet, again.Method)
}
