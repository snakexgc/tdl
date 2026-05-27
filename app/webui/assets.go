package webui

import (
	"net/http"
	"strings"
)

func (s *Server) handleViewAsset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/views/")
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "\\") || !strings.HasSuffix(name, ".html") {
		http.NotFound(w, r)
		return
	}
	s.serveAsset(w, r, "views/"+name, "text/html; charset=utf-8")
}

func (s *Server) handleAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.serveAsset(w, r, name, contentType)
	}
}

func (s *Server) serveAsset(w http.ResponseWriter, r *http.Request, name, contentType string) {
	data, err := assets.ReadFile(name)
	if err != nil {
		http.Error(w, "asset not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write(data)
}
