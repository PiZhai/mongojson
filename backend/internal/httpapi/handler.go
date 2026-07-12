package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/canvas"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
	"mongojson/backend/internal/service/music"
)

type Handler struct {
	deps Dependencies
}

const maxUploadBytes = 64 << 20
const maxMusicUploadBytes = 512 << 20
const maxCanvasSceneBytes = 16 << 20
const multipartMemoryBytes = 32 << 20

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) readyz(w http.ResponseWriter, r *http.Request) {
	if h.deps.Readiness == nil {
		httpError(w, http.StatusServiceUnavailable, "readiness checker is not configured")
		return
	}

	ctx, cancel := withTimeout(r.Context())
	defer cancel()

	checks, err := h.deps.Readiness(ctx)
	if err != nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status": "not_ready",
			"checks": checks,
			"error":  err.Error(),
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"status": "ready",
		"checks": checks,
	})
}

func (h *Handler) uploadFile(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxUploadBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpError(w, http.StatusRequestEntityTooLarge, "upload body exceeds 64 MiB")
			return
		}
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		httpError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	record, err := h.deps.FileService.SaveUpload(r.Context(), file, header)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.FileRecord{"file": record})
}

func (h *Handler) getMemo(w http.ResponseWriter, r *http.Request) {
	record, err := h.deps.MemoService.GetOrCreate(r.Context(), r.URL.Query().Get("slug"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.MemoRecord{"memo": record})
}

func (h *Handler) saveMemo(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slug          string           `json:"slug"`
		Title         string           `json:"title"`
		ContentHTML   string           `json:"content_html"`
		ContentText   string           `json:"content_text"`
		FloatingCards *json.RawMessage `json:"floating_cards"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	record, err := h.deps.MemoService.SaveMemo(r.Context(), memo.SaveInput{
		Slug:          body.Slug,
		Title:         body.Title,
		ContentHTML:   body.ContentHTML,
		ContentText:   body.ContentText,
		FloatingCards: body.FloatingCards,
	})
	if err != nil {
		if errors.Is(err, memo.ErrInvalidFloatingCards) {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.MemoRecord{"memo": record})
}

func (h *Handler) uploadMusicTrack(w http.ResponseWriter, r *http.Request) {
	if h.deps.MusicService == nil {
		httpError(w, http.StatusServiceUnavailable, "music storage is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxMusicUploadBytes)
	if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpError(w, http.StatusRequestEntityTooLarge, "music upload body exceeds 512 MiB")
			return
		}
		httpError(w, http.StatusBadRequest, "invalid music upload")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	var lyric multipart.File
	var lyricHeader *multipart.FileHeader
	lyric, lyricHeader, err = r.FormFile("lyric")
	if err != nil && !errors.Is(err, http.ErrMissingFile) {
		httpError(w, http.StatusBadRequest, "invalid lyric file")
		return
	}
	if lyric != nil {
		defer lyric.Close()
	}
	var duration *float64
	if value := strings.TrimSpace(r.FormValue("duration")); value != "" {
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || parsed < 0 {
			httpError(w, http.StatusBadRequest, "duration must be a positive number")
			return
		}
		duration = &parsed
	}
	result, err := h.deps.MusicService.SaveUpload(r.Context(), music.UploadInput{
		File: file, Header: header, Lyric: lyric, LyricHeader: lyricHeader, Title: r.FormValue("title"), Artist: r.FormValue("artist"),
		Note: r.FormValue("note"), Duration: duration, AudioQuality: json.RawMessage(r.FormValue("audio_quality")),
	})
	if err != nil {
		if errors.Is(err, music.ErrUnsupportedAudio) || errors.Is(err, music.ErrUnsupportedLyric) {
			httpError(w, http.StatusUnsupportedMediaType, err.Error())
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	status := http.StatusCreated
	if result.Duplicate {
		status = http.StatusOK
	}
	respondJSON(w, status, result)
}

func (h *Handler) listMusicTracks(w http.ResponseWriter, r *http.Request) {
	if h.deps.MusicService == nil {
		httpError(w, http.StatusServiceUnavailable, "music storage is not configured")
		return
	}
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			httpError(w, http.StatusBadRequest, "limit must be between 1 and 100")
			return
		}
		limit = parsed
	}
	page, err := h.deps.MusicService.List(r.Context(), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		if errors.Is(err, music.ErrInvalidCursor) {
			httpError(w, http.StatusBadRequest, err.Error())
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, page)
}

func (h *Handler) streamMusicTrack(w http.ResponseWriter, r *http.Request) {
	if h.deps.MusicService == nil {
		httpError(w, http.StatusServiceUnavailable, "music storage is not configured")
		return
	}
	record, err := h.deps.MusicService.GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	file, err := os.Open(record.StoragePath)
	if err != nil {
		httpError(w, http.StatusNotFound, "music file not found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "cannot inspect music file")
		return
	}
	w.Header().Set("Content-Type", record.MIMEType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": record.OriginalName}))
	http.ServeContent(w, r, record.OriginalName, info.ModTime(), file)
}

func (h *Handler) streamMusicLyrics(w http.ResponseWriter, r *http.Request) {
	if h.deps.MusicService == nil {
		httpError(w, http.StatusServiceUnavailable, "music storage is not configured")
		return
	}
	record, err := h.deps.MusicService.GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil || record.LyricStoragePath == "" {
		httpError(w, http.StatusNotFound, music.ErrLyricsNotFound.Error())
		return
	}
	file, err := os.Open(record.LyricStoragePath)
	if err != nil {
		httpError(w, http.StatusNotFound, music.ErrLyricsNotFound.Error())
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "cannot inspect lyric file")
		return
	}
	w.Header().Set("Content-Type", record.LyricMIMEType)
	http.ServeContent(w, r, record.LyricFileName, info.ModTime(), file)
}

func (h *Handler) deleteMusicTrack(w http.ResponseWriter, r *http.Request) {
	if h.deps.MusicService == nil {
		httpError(w, http.StatusServiceUnavailable, "music storage is not configured")
		return
	}
	if err := h.deps.MusicService.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, music.ErrTrackNotFound) {
			httpError(w, http.StatusNotFound, err.Error())
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listCanvasBoards(w http.ResponseWriter, r *http.Request) {
	if h.deps.CanvasService == nil {
		httpError(w, http.StatusServiceUnavailable, "canvas storage is not configured")
		return
	}
	items, err := h.deps.CanvasService.List(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.CanvasBoardRecord{"boards": items})
}

func (h *Handler) createCanvasBoard(w http.ResponseWriter, r *http.Request) {
	if h.deps.CanvasService == nil {
		httpError(w, http.StatusServiceUnavailable, "canvas storage is not configured")
		return
	}
	var body struct {
		Title string `json:"title"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	record, err := h.deps.CanvasService.Create(r.Context(), body.Title)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.CanvasBoardRecord{"board": record})
}

func (h *Handler) getCanvasBoard(w http.ResponseWriter, r *http.Request) {
	if h.deps.CanvasService == nil {
		httpError(w, http.StatusServiceUnavailable, "canvas storage is not configured")
		return
	}
	record, err := h.deps.CanvasService.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, canvas.ErrBoardNotFound) {
			httpError(w, http.StatusNotFound, err.Error())
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.CanvasBoardRecord{"board": record})
}

func (h *Handler) saveCanvasBoard(w http.ResponseWriter, r *http.Request) {
	if h.deps.CanvasService == nil {
		httpError(w, http.StatusServiceUnavailable, "canvas storage is not configured")
		return
	}
	var body struct {
		Title    string          `json:"title"`
		Scene    json.RawMessage `json:"scene"`
		Revision int64           `json:"revision"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxCanvasSceneBytes)).Decode(&body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpError(w, http.StatusRequestEntityTooLarge, "canvas scene exceeds 16 MiB")
			return
		}
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	record, err := h.deps.CanvasService.Save(r.Context(), chi.URLParam(r, "id"), canvas.SaveInput{
		Title: body.Title, Scene: body.Scene, Revision: body.Revision,
	})
	if err != nil {
		switch {
		case errors.Is(err, canvas.ErrBoardNotFound):
			httpError(w, http.StatusNotFound, err.Error())
		case errors.Is(err, canvas.ErrRevisionConflict):
			httpError(w, http.StatusConflict, err.Error())
		case errors.Is(err, canvas.ErrInvalidScene):
			httpError(w, http.StatusBadRequest, err.Error())
		default:
			httpError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.CanvasBoardRecord{"board": record})
}

func (h *Handler) deleteCanvasBoard(w http.ResponseWriter, r *http.Request) {
	if h.deps.CanvasService == nil {
		httpError(w, http.StatusServiceUnavailable, "canvas storage is not configured")
		return
	}
	if err := h.deps.CanvasService.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		if errors.Is(err, canvas.ErrBoardNotFound) {
			httpError(w, http.StatusNotFound, err.Error())
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) uploadCanvasAsset(w http.ResponseWriter, r *http.Request) {
	if h.deps.CanvasService == nil {
		httpError(w, http.StatusServiceUnavailable, "canvas storage is not configured")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(multipartMemoryBytes); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpError(w, http.StatusRequestEntityTooLarge, "canvas asset exceeds 64 MiB")
			return
		}
		httpError(w, http.StatusBadRequest, "invalid canvas asset upload")
		return
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		httpError(w, http.StatusBadRequest, "missing file")
		return
	}
	defer file.Close()
	record, err := h.deps.CanvasService.UploadAsset(r.Context(), chi.URLParam(r, "id"), r.FormValue("canvas_file_id"), file, header)
	if err != nil {
		if errors.Is(err, canvas.ErrBoardNotFound) {
			httpError(w, http.StatusNotFound, err.Error())
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.CanvasAssetRecord{"asset": record})
}

func (h *Handler) streamCanvasAsset(w http.ResponseWriter, r *http.Request) {
	if h.deps.CanvasService == nil {
		httpError(w, http.StatusServiceUnavailable, "canvas storage is not configured")
		return
	}
	record, err := h.deps.CanvasService.GetAsset(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	file, err := os.Open(record.StoragePath)
	if err != nil {
		httpError(w, http.StatusNotFound, "canvas asset file not found")
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "cannot inspect canvas asset")
		return
	}
	w.Header().Set("Content-Type", record.MIMEType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": record.OriginalName}))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, record.OriginalName, info.ModTime(), file)
}

func (h *Handler) downloadFile(w http.ResponseWriter, r *http.Request) {
	record, err := h.deps.FileService.GetByID(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	file, err := os.Open(record.StoragePath)
	if err != nil {
		httpError(w, http.StatusNotFound, "file not found")
		return
	}
	defer file.Close()

	w.Header().Set("Content-Type", record.MIMEType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, record.OriginalName))
	_, _ = io.Copy(w, file)
}

func (h *Handler) createJob(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ToolType    string                 `json:"tool_type"`
		InputFileID string                 `json:"input_file_id"`
		Params      map[string]interface{} `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.ToolType == "" {
		httpError(w, http.StatusBadRequest, "tool_type is required")
		return
	}
	if !h.deps.JobService.SupportsToolType(body.ToolType) {
		httpError(w, http.StatusServiceUnavailable, "asynchronous job processing is disabled for this tool in this build")
		return
	}

	var inputFileID *string
	if body.InputFileID != "" {
		if _, err := uuid.Parse(body.InputFileID); err != nil {
			httpError(w, http.StatusBadRequest, "input_file_id must be a valid UUID")
			return
		}
		inputFileID = &body.InputFileID
	}

	job, err := h.deps.JobService.Create(r.Context(), body.ToolType, inputFileID, body.Params)
	if err != nil {
		if errors.Is(err, jobs.ErrQueueFull) {
			httpError(w, http.StatusServiceUnavailable, "job queue is full; try again later")
			return
		}
		if errors.Is(err, jobs.ErrProcessingDisabled) {
			httpError(w, http.StatusServiceUnavailable, "asynchronous job processing is disabled in this build")
			return
		}
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.JobRecord{"job": job})
}

func (h *Handler) getJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.deps.JobService.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.JobRecord{"job": job})
}

func (h *Handler) listPresets(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.PresetService.List(r.Context(), r.URL.Query().Get("tool_type"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.PresetRecord{"presets": items})
}

func (h *Handler) createPreset(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ToolType string                 `json:"tool_type"`
		Name     string                 `json:"name"`
		Payload  map[string]interface{} `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.ToolType == "" || body.Name == "" {
		httpError(w, http.StatusBadRequest, "tool_type and name are required")
		return
	}
	record, err := h.deps.PresetService.Save(r.Context(), body.ToolType, body.Name, body.Payload)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.PresetRecord{"preset": record})
}

func (h *Handler) watchRoomWebSocket(w http.ResponseWriter, r *http.Request) {
	if h.deps.WatchSync == nil {
		httpError(w, http.StatusServiceUnavailable, "watch sync is not configured")
		return
	}
	h.deps.WatchSync.ServeRoom(w, r, chi.URLParam(r, "roomID"))
}

func withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 3*time.Second)
}

func notFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
