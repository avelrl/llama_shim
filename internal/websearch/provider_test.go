package websearch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSearXNGProviderOpenPageRejectsRedirectToLocalhost(t *testing.T) {
	local := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("secret"))
	}))
	defer local.Close()

	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, local.URL, http.StatusFound)
	}))
	defer remote.Close()

	provider := newSearXNGProvider(Config{
		BaseURL:    remote.URL,
		Timeout:    defaultTimeout,
		MaxResults: defaultMaxResults,
	})

	_, err := provider.OpenPage(context.Background(), remote.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private IP")
}

func TestSearXNGProviderOpenPageRejectsRedirectToPrivateIP(t *testing.T) {
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Redirect(w, &http.Request{}, "http://127.0.0.1/private", http.StatusFound)
	}))
	defer remote.Close()

	provider := newSearXNGProvider(Config{
		BaseURL:    remote.URL,
		Timeout:    defaultTimeout,
		MaxResults: defaultMaxResults,
	})

	_, err := provider.OpenPage(context.Background(), remote.URL)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private IP")
}
