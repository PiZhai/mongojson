package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/config"
	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/filemeta"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
	"mongojson/backend/internal/service/music"
	"mongojson/backend/internal/service/presets"
	"mongojson/backend/internal/service/watchsync"
)

type MemoStore interface {
	GetOrCreate(context.Context, string) (domain.MemoRecord, error)
	SaveMemo(context.Context, memo.SaveInput) (domain.MemoRecord, error)
}

type MusicStore interface {
	SaveUpload(context.Context, music.UploadInput) (domain.MusicTrackRecord, error)
	List(context.Context, string, int) (music.Page, error)
	GetByID(context.Context, string) (domain.MusicTrackRecord, error)
}

type Dependencies struct {
	Config        config.Config
	FileService   *filemeta.Service
	JobService    *jobs.Service
	MemoService   MemoStore
	MusicService  MusicStore
	PresetService *presets.Service
	WatchSync     *watchsync.Hub
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
		r.Post("/music/tracks", handler.uploadMusicTrack)
		r.Get("/music/tracks", handler.listMusicTracks)
		r.Get("/music/tracks/{id}/content", handler.streamMusicTrack)
		r.Post("/jobs", handler.createJob)
		r.Get("/jobs/{id}", handler.getJob)
		r.Get("/presets", handler.listPresets)
		r.Post("/presets", handler.createPreset)
		r.Get("/watch/rooms/{roomID}/ws", handler.watchRoomWebSocket)
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
