package websearch

import (
	"context"
	"net"
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

func TestSearXNGProviderOpenPageRejectsResolvedPrivateIP(t *testing.T) {
	provider := newSearXNGProvider(Config{
		BaseURL:    "https://example.test",
		Timeout:    defaultTimeout,
		MaxResults: defaultMaxResults,
	})
	provider.resolveIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}

	_, err := provider.validateOpenPageURL(context.Background(), "https://rebind.example/path")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private IP")
}

func TestSearXNGProviderOpenPageAllowsResolvedPublicIP(t *testing.T) {
	provider := newSearXNGProvider(Config{
		BaseURL:    "https://example.test",
		Timeout:    defaultTimeout,
		MaxResults: defaultMaxResults,
	})
	provider.resolveIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}

	parsed, err := provider.validateOpenPageURL(context.Background(), "https://example.com/path")
	require.NoError(t, err)
	require.Equal(t, "https://example.com/path", parsed.String())
}

func TestSearXNGProviderOpenPageAllowsConfiguredBackendOrigin(t *testing.T) {
	provider := newSearXNGProvider(Config{
		BaseURL:    "http://fixture:8081",
		Timeout:    defaultTimeout,
		MaxResults: defaultMaxResults,
	})
	provider.resolveIP = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("172.18.0.2")}}, nil
	}

	parsed, err := provider.validateOpenPageURL(context.Background(), "http://fixture:8081/pages/web-search-guide")
	require.NoError(t, err)
	require.Equal(t, "http://fixture:8081/pages/web-search-guide", parsed.String())
}

func TestSearXNGProviderOpenPageStillRejectsOtherPrivateOrigins(t *testing.T) {
	provider := newSearXNGProvider(Config{
		BaseURL:    "http://fixture:8081",
		Timeout:    defaultTimeout,
		MaxResults: defaultMaxResults,
	})
	provider.resolveIP = func(_ context.Context, host string) ([]net.IPAddr, error) {
		switch host {
		case "fixture":
			return []net.IPAddr{{IP: net.ParseIP("172.18.0.2")}}, nil
		case "other":
			return []net.IPAddr{{IP: net.ParseIP("172.18.0.3")}}, nil
		default:
			return nil, nil
		}
	}

	_, err := provider.validateOpenPageURL(context.Background(), "http://other:8081/private")
	require.Error(t, err)
	require.Contains(t, err.Error(), "private IP")
}
