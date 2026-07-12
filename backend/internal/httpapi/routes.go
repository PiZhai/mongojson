package httpapi

import (
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/config"
	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/canvas"
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
	CreateDocument(context.Context, string, string) (domain.MemoRecord, error)
	GetDocument(context.Context, string) (domain.MemoRecord, error)
	SaveDocument(context.Context, string, memo.DocumentSaveInput) (domain.MemoRecord, error)
	DeleteDocument(context.Context, string) error
	ListSideNotes(context.Context, string) ([]domain.MemoSideNoteRecord, error)
	CreateSideNote(context.Context, string, memo.SideNoteInput) (domain.MemoSideNoteRecord, error)
	SaveSideNote(context.Context, string, memo.SideNoteInput) (domain.MemoSideNoteRecord, error)
	DeleteSideNote(context.Context, string) error
}

type MusicStore interface {
	SaveUpload(context.Context, music.UploadInput) (music.UploadResult, error)
	List(context.Context, string, int) (music.Page, error)
	GetByID(context.Context, string) (domain.MusicTrackRecord, error)
	Delete(context.Context, string) error
}

type CanvasStore interface {
	Create(context.Context, string) (domain.CanvasBoardRecord, error)
	List(context.Context) ([]domain.CanvasBoardRecord, error)
	Get(context.Context, string) (domain.CanvasBoardRecord, error)
	Save(context.Context, string, canvas.SaveInput) (domain.CanvasBoardRecord, error)
	Delete(context.Context, string) error
	UploadAsset(context.Context, string, string, multipart.File, *multipart.FileHeader) (domain.CanvasAssetRecord, error)
	GetAsset(context.Context, string) (domain.CanvasAssetRecord, error)
}

type Dependencies struct {
	Config        config.Config
	FileService   *filemeta.Service
	JobService    *jobs.Service
	MemoService   MemoStore
	MusicService  MusicStore
	CanvasService CanvasStore
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
		r.Post("/memo/documents", handler.createMemoDocument)
		r.Get("/memo/documents/{slug}", handler.getMemoDocument)
		r.Put("/memo/documents/{id}", handler.saveMemoDocument)
		r.Delete("/memo/documents/{id}", handler.deleteMemoDocument)
		r.Get("/memo/documents/{id}/notes", handler.listMemoSideNotes)
		r.Post("/memo/documents/{id}/notes", handler.createMemoSideNote)
		r.Put("/memo/notes/{id}", handler.saveMemoSideNote)
		r.Delete("/memo/notes/{id}", handler.deleteMemoSideNote)
		r.Post("/music/tracks", handler.uploadMusicTrack)
		r.Get("/music/tracks", handler.listMusicTracks)
		r.Get("/music/tracks/{id}/content", handler.streamMusicTrack)
		r.Get("/music/tracks/{id}/lyrics", handler.streamMusicLyrics)
		r.Delete("/music/tracks/{id}", handler.deleteMusicTrack)
		r.Get("/canvas/boards", handler.listCanvasBoards)
		r.Post("/canvas/boards", handler.createCanvasBoard)
		r.Get("/canvas/boards/{id}", handler.getCanvasBoard)
		r.Put("/canvas/boards/{id}", handler.saveCanvasBoard)
		r.Delete("/canvas/boards/{id}", handler.deleteCanvasBoard)
		r.Post("/canvas/boards/{id}/assets", handler.uploadCanvasAsset)
		r.Get("/canvas/assets/{id}/content", handler.streamCanvasAsset)
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
