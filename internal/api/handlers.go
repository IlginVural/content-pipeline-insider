package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"content-pipeline-insider/internal/pipeline"
	"content-pipeline-insider/internal/resolver"
	"content-pipeline-insider/internal/secrets"
	"content-pipeline-insider/internal/store"
)

// handlers holds the dependencies every HTTP handler needs. Kept as a
// struct rather than free functions so dependencies arrive explicitly
// at construction — the same reason main.go passes the logger down
// instead of reaching for a package-level global.
type handlers struct {
	log      *slog.Logger
	store    *store.Store
	repo     *pipeline.Repository
	resolver *resolver.Resolver
	secrets  secrets.Resolver

	// prod gates the listing views, which are not tenant-scoped.
	prod bool
}

// health reports that the process is up and serving. It deliberately
// does not check Postgres or Redis: this endpoint answers "should the
// load balancer send traffic here", and a dependency being down is a
// different question with a different answer.
func (h *handlers) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ready answers the question health deliberately does not: can this process
// actually serve a request. Every pipeline configuration lives in Postgres,
// so an unreachable database means yes-I'm-alive but no-don't-route-to-me.
func (h *handlers) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := h.store.Ping(ctx); err != nil {
		h.log.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"status": "unavailable",
			"reason": "database is unreachable",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// render will resolve a campaign's external-data bindings and return
// the normalized content object.
//
// It is not implemented yet: the transformer, output validator, and
// resolver packages the README describes do not exist. Returning 501
// is the honest answer — an empty 200 would let a caller believe the
// pipeline ran and produced nothing.
func (h *handlers) render(w http.ResponseWriter, r *http.Request) {
	h.log.Warn("render called but the resolution pipeline is not implemented")
	writeJSON(w, http.StatusNotImplemented, map[string]string{
		"error": "render is not implemented yet",
	})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// The status and headers are already committed by this point, so a
	// failed encode cannot become an error response. Nothing useful is
	// left to do but drop it.
	_ = json.NewEncoder(w).Encode(body)
}
