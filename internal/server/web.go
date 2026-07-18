package server

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed web/*
var embeddedWeb embed.FS

func (api *API) registerWebRoutes() {
	root, err := fs.Sub(embeddedWeb, "web")
	if err != nil {
		panic(err)
	}
	assets, err := fs.Sub(root, "assets")
	if err != nil {
		panic(err)
	}
	api.mux.Handle("GET /assets/", http.StripPrefix("/assets/", http.FileServer(http.FS(assets))))
	api.mux.HandleFunc("GET /", func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/" {
			http.NotFound(response, request)
			return
		}
		content, err := fs.ReadFile(root, "index.html")
		if err != nil {
			http.Error(response, "web application is unavailable", http.StatusInternalServerError)
			return
		}
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		response.Header().Set("Cache-Control", "no-cache")
		_, _ = response.Write(content)
	})
}
