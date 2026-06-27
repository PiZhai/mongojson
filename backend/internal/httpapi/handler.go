package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
)

type Handler struct {
	deps Dependencies
}

const maxUploadBytes = 64 << 20

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

func withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 3*time.Second)
}

func notFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
