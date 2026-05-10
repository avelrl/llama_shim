package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

//go:embed operator_ui_dist
var operatorUIAssets embed.FS

type UIConfig struct {
	Enabled            bool
	BasePath           string
	PublicStaticAssets bool
}

func normalizeUIConfig(cfg UIConfig) UIConfig {
	cfg.BasePath = normalizeUIBasePath(cfg.BasePath)
	return cfg
}

func normalizeUIBasePath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "/ui/"
	}
	cleaned := path.Clean(value)
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = "/" + cleaned
	}
	if cleaned == "/" {
		return "/ui/"
	}
	return strings.TrimRight(cleaned, "/") + "/"
}

func (cfg UIConfig) publicStaticPath(requestPath string) bool {
	if !cfg.Enabled || !cfg.PublicStaticAssets {
		return false
	}
	requestPath = strings.TrimSpace(requestPath)
	base := cfg.BasePath
	if requestPath == strings.TrimRight(base, "/") {
		return true
	}
	return strings.HasPrefix(requestPath, base)
}

func mountOperatorUI(mux *http.ServeMux, cfg UIConfig) {
	if mux == nil {
		return
	}
	basePath := cfg.BasePath
	baseNoSlash := strings.TrimRight(basePath, "/")
	if baseNoSlash != "" && baseNoSlash != basePath {
		mux.HandleFunc(baseNoSlash, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
				return
			}
			http.Redirect(w, r, basePath, http.StatusMovedPermanently)
		})
	}
	if !cfg.Enabled {
		mux.Handle(basePath, http.NotFoundHandler())
		return
	}
	dist, err := fs.Sub(operatorUIAssets, "operator_ui_dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.StripPrefix(basePath, http.FileServer(http.FS(dist)))
	mux.Handle(basePath, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			WriteError(w, http.StatusMethodNotAllowed, "invalid_request_error", "method not allowed", "")
			return
		}
		fileServer.ServeHTTP(w, r)
	}))
}
