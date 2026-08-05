package api

import (
	"context"
	"log/slog"
	"net/http"

	"content-pipeline-insider/internal/config"
)

// Server wraps *http.Server so main.go doesn't touch stdlib HTTP directly.
type Server struct {
	http *http.Server
	log  *slog.Logger
}

// NewServer builds the HTTP server with routes and middleware wired up.
// Dependencies are passed in explicitly; nothing is fetched from globals.
func NewServer(cfg *config.Config, log *slog.Logger) *Server {
	mux := http.NewServeMux()
	h := &handlers{log: log}

	// Go 1.22+ ServeMux supports "METHOD /path" patterns natively.
	// No router library needed until the routing table gets complex.
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("POST /render", h.render)

	// Chain middleware. Order matters — outermost wraps innermost.
	handler := requestLogger(log)(mux)

	return &Server{
		http: &http.Server{
			Addr:         cfg.HTTPAddr,
			Handler:      handler,
			ReadTimeout:  cfg.HTTPReadTimeout,
			WriteTimeout: cfg.HTTPWriteTimeout,
		},
		log: log,
	}
}

func (s *Server) ListenAndServe() error {
	return s.http.ListenAndServe()
}

func (s *Server) Shutdown(ctx context.Context) error {
	return s.http.Shutdown(ctx)
}
