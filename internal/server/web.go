package server

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var embeddedWeb embed.FS

func (api *API) requireWebAccess(next http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		username, key, ok := request.BasicAuth()
		digest := sha256.Sum256([]byte(key))
		if !ok || username != "viewer" || key == "" || subtle.ConstantTimeCompare(digest[:], api.webAccessDigest[:]) != 1 {
			response.Header().Set("WWW-Authenticate", `Basic realm="Server Status", charset="UTF-8"`)
			writeError(response, http.StatusUnauthorized, "a valid dashboard access key is required")
			return
		}
		next(response, request)
	}
}

func (api *API) registerWebRoutes() {
	root, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		panic(err)
	}
	assets, err := fs.Sub(root, "assets")
	if err != nil {
		panic(err)
	}
	assetServer := http.StripPrefix("/assets/", http.FileServer(http.FS(assets)))
	api.mux.HandleFunc("GET /assets/", api.requireWebAccess(assetServer.ServeHTTP))
	servePage := func(name string) http.HandlerFunc {
		return func(response http.ResponseWriter, _ *http.Request) {
			content, err := fs.ReadFile(root, name)
			if err != nil {
				http.Error(response, "web application is unavailable", http.StatusInternalServerError)
				return
			}
			response.Header().Set("Content-Type", "text/html; charset=utf-8")
			response.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
			_, _ = response.Write(content)
		}
	}
	api.mux.HandleFunc("GET /{$}", api.requireWebAccess(servePage("index.html")))
	api.mux.HandleFunc("GET /alerts", api.requireWebAccess(servePage("alerts.html")))
	// Use /alerts so browser Basic credentials apply to the site's top-level paths.
	api.mux.Handle("GET /alerts/{$}", http.RedirectHandler("/alerts", http.StatusTemporaryRedirect))
	api.mux.HandleFunc("GET /install-agent.sh", func(response http.ResponseWriter, _ *http.Request) {
		content, err := fs.ReadFile(root, "install-agent.sh")
		if err != nil {
			http.Error(response, "agent installer is unavailable", http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		_, _ = response.Write(content)
	})
	api.mux.HandleFunc("GET /install-agent-macos.sh", func(response http.ResponseWriter, _ *http.Request) {
		content, err := fs.ReadFile(root, "install-agent-macos.sh")
		if err != nil {
			http.Error(response, "macOS Agent installer is unavailable", http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		response.Header().Set("Cache-Control", "no-store")
		_, _ = response.Write(content)
	})
}
