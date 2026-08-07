package api

import (
	"errors"
	"html/template"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"content-pipeline-insider/internal/pipeline"
	"content-pipeline-insider/internal/resolver"
)

// listPipelines returns the newest pipelines across all tenants. It is an
// operator view, not a customer-facing one, which is why devOnly guards it.
func (h *handlers) listPipelines(w http.ResponseWriter, r *http.Request) {
	list, err := h.repo.ListRecentPipelines(r.Context(), 50)
	if err != nil {
		h.log.Error("list pipelines failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("could not list pipelines"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipelines": list, "count": len(list)})
}

func (h *handlers) listVersions(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pipelineID(w, r)
	if !ok {
		return
	}

	versions, err := h.repo.ListVersions(r.Context(), id)
	if err != nil {
		h.log.Error("list versions failed", "error", err)
		writeJSON(w, http.StatusInternalServerError, errorBody("could not list versions"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"pipelineId": id, "versions": versions})
}

func (h *handlers) getVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pipelineID(w, r)
	if !ok {
		return
	}
	number, ok := h.versionNumber(w, r.PathValue("version"))
	if !ok {
		return
	}

	v, err := h.repo.GetVersion(r.Context(), id, number)
	if err != nil {
		h.writeVersionError(w, err)
		return
	}
	// Upstream holds secret references, never values, so it is safe to return.
	writeJSON(w, http.StatusOK, v)
}

// renderPipeline runs a stored configuration against its partner API and
// returns the normalized object. Query parameters other than "version" are
// passed through as the pipeline's dynamic parameters.
func (h *handlers) renderPipeline(w http.ResponseWriter, r *http.Request) {
	id, ok := h.pipelineID(w, r)
	if !ok {
		return
	}

	number := 1
	if raw := r.URL.Query().Get("version"); raw != "" {
		if number, ok = h.versionNumber(w, raw); !ok {
			return
		}
	}

	v, err := h.repo.GetVersion(r.Context(), id, number)
	if err != nil {
		h.writeVersionError(w, err)
		return
	}

	// Fail with the names of the missing credentials rather than a bare
	// resolution error. renderd has no secret backend yet, so any config
	// built from a credentialed cURL lands here.
	//
	// Credentials are deliberately not accepted as query parameters: they
	// would end up in access logs and browser history.
	if missing := resolver.MissingSecrets(r.Context(), v.Upstream, h.secrets); len(missing) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":          "this pipeline needs credentials this server cannot resolve",
			"missingSecrets": missing,
			"hint":           "run it through `pipelinetry -load <id> -secret <ref>=<value>` until a secrets backend is wired up",
		})
		return
	}

	params := map[string]string{}
	for name, values := range r.URL.Query() {
		if name == "version" || len(values) == 0 {
			continue
		}
		params[name] = values[0]
	}

	res, err := h.resolver.Resolve(r.Context(), v.Upstream, v.Mappings, params)
	if err != nil {
		h.log.Warn("render failed", "pipeline", id, "version", number, "error", err)
		body := map[string]any{"error": err.Error()}
		if res != nil {
			body["upstreamStatus"] = res.Status
		}
		// The configuration is fine; the partner call or the response is not.
		writeJSON(w, http.StatusBadGateway, body)
		return
	}

	h.log.Info("rendered", "pipeline", id, "version", number, "result", resolver.Describe(res))
	writeJSON(w, http.StatusOK, map[string]any{
		"pipelineId":     id,
		"version":        number,
		"output":         res.Output,
		"upstreamStatus": res.Status,
		"elapsedMs":      res.Elapsed.Milliseconds(),
	})
}

func (h *handlers) pipelineID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorBody("pipeline id is not a UUID"))
		return uuid.Nil, false
	}
	return id, true
}

func (h *handlers) versionNumber(w http.ResponseWriter, raw string) (int, bool) {
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 {
		writeJSON(w, http.StatusBadRequest, errorBody("version must be a positive integer"))
		return 0, false
	}
	return n, true
}

func (h *handlers) writeVersionError(w http.ResponseWriter, err error) {
	if errors.Is(err, pipeline.ErrVersionNotFound) {
		writeJSON(w, http.StatusNotFound, errorBody(err.Error()))
		return
	}
	h.log.Error("get version failed", "error", err)
	writeJSON(w, http.StatusInternalServerError, errorBody("could not read the version"))
}

func errorBody(msg string) map[string]string { return map[string]string{"error": msg} }

// devOnly refuses a handler outside development. The listing views are not
// tenant-scoped, so they must not exist in production.
func (h *handlers) devOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if h.prod {
			http.NotFound(w, r)
			return
		}
		next(w, r)
	}
}

func (h *handlers) index(w http.ResponseWriter, r *http.Request) {
	// ServeMux's "/" pattern matches everything unmatched, so an unknown path
	// would otherwise render the index with a 200.
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	list, err := h.repo.ListRecentPipelines(r.Context(), 50)
	if err != nil {
		h.log.Error("index listing failed", "error", err)
		http.Error(w, "could not list pipelines", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := indexTemplate.Execute(w, list); err != nil {
		// Headers are already committed, so this can only be logged.
		h.log.Error("index render failed", "error", err)
	}
}

// html/template, not text/template: pipeline names are user-supplied.
var indexTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Content Pipelines</title>
<style>
 body { font: 15px/1.5 system-ui, sans-serif; margin: 2rem auto; max-width: 60rem; padding: 0 1rem; }
 table { border-collapse: collapse; width: 100%; }
 th, td { text-align: left; padding: .5rem .6rem; border-bottom: 1px solid #ddd; }
 th { font-size: .8rem; text-transform: uppercase; letter-spacing: .04em; color: #666; }
 code { font-size: .85em; background: #f4f4f5; padding: .1rem .3rem; border-radius: 3px; }
 .empty { color: #666; padding: 2rem 0; }
 a { color: #0a58ca; }
</style>
</head>
<body>
<h1>Content Pipelines</h1>
{{if .}}
<table>
  <tr><th>Name</th><th>Versions</th><th>Status</th><th>Created</th><th>Actions</th></tr>
  {{range .}}
  <tr>
    <td>{{.Name}}<br><code>{{.ID}}</code></td>
    <td>{{.VersionCount}}</td>
    <td>{{.Status}}</td>
    <td>{{.CreatedAt.Format "2006-01-02 15:04"}}</td>
    <td>
      <a href="/pipelines/{{.ID}}/versions">versions</a>
      {{if gt .LatestVersion 0}}
      &middot; <a href="/pipelines/{{.ID}}/versions/{{.LatestVersion}}">config</a>
      &middot; <a href="/pipelines/{{.ID}}/render?version={{.LatestVersion}}">render</a>
      {{end}}
    </td>
  </tr>
  {{end}}
</table>
{{else}}
<p class="empty">Nothing stored yet. Create one with:<br>
<code>go run ./cmd/pipelinetry -curl "curl 'https://dummyjson.com/products/1'" -map 'title:title:string' -save</code></p>
{{end}}
</body>
</html>
`))
