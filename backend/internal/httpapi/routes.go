package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/config"
	"mongojson/backend/internal/service/filemeta"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
	"mongojson/backend/internal/service/presets"
)

type Dependencies struct {
	Config        config.Config
	FileService   *filemeta.Service
	JobService    *jobs.Service
	MemoService   *memo.Service
	PresetService *presets.Service
	Readiness     func(context.Context) (map[string]string, error)
}

func RegisterRoutes(router chi.Router, deps Dependencies) {
	handler := &Handler{deps: deps}

	router.Get("/healthz", handler.healthz)
	router.Get("/readyz", handler.readyz)

	router.Route("/api", func(r chi.Router) {
		r.Post("/files", handler.uploadFile)
		r.Get("/files/{id}/download", handler.downloadFile)
		r.Get("/memo", handler.getMemo)
		r.Put("/memo", handler.saveMemo)
		r.Post("/jobs", handler.createJob)
		r.Get("/jobs/{id}", handler.getJob)
		r.Get("/presets", handler.listPresets)
		r.Post("/presets", handler.createPreset)
	})
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func httpError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message})
}

func sanitizeName(name string) string {
	if name == "" {
		return "output"
	}
	return strings.ReplaceAll(name, " ", "-")
}
