package api

import (
	"context"
	"log/slog"
	"net/http"

	"content-pipeline-insider/internal/config"
	"content-pipeline-insider/internal/fetcher"
	"content-pipeline-insider/internal/pipeline"
	"content-pipeline-insider/internal/resolver"
	"content-pipeline-insider/internal/secrets"
	"content-pipeline-insider/internal/store"
)

// Server wraps *http.Server so main.go doesn't touch stdlib HTTP directly.
type Server struct {
	http *http.Server
	log  *slog.Logger
}

// NewServer builds the HTTP server with routes and middleware wired up.
// Dependencies are passed in explicitly; nothing is fetched from globals.
func NewServer(cfg *config.Config, log *slog.Logger, st *store.Store) *Server {
	mux := http.NewServeMux()

	// Empty: renderd has no secrets backend yet, so any stored config holding
	// a secret reference fails the readable "missing credentials" path rather
	// than a bare resolution error. Swapping in AWS Secrets Manager is a
	// change here and nowhere else.
	secretResolver := secrets.NewMemoryResolver(nil)

	h := &handlers{
		log:      log,
		store:    st,
		repo:     pipeline.NewRepository(st.Pool()),
		resolver: resolver.New(fetcher.New(fetcher.Options{}), secretResolver),
		secrets:  secretResolver,
		prod:     cfg.Env == config.EnvProd,
	}

	// Go 1.22+ ServeMux supports "METHOD /path" patterns natively.
	// No router library needed until the routing table gets complex.
	mux.HandleFunc("GET /health", h.health)
	mux.HandleFunc("GET /ready", h.ready)
	mux.HandleFunc("POST /render", h.render)

	mux.HandleFunc("GET /{$}", h.devOnly(h.index))
	mux.HandleFunc("GET /pipelines", h.devOnly(h.listPipelines))
	mux.HandleFunc("GET /pipelines/{id}/versions", h.listVersions)
	mux.HandleFunc("GET /pipelines/{id}/versions/{version}", h.getVersion)
	mux.HandleFunc("GET /pipelines/{id}/render", h.renderPipeline)

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
