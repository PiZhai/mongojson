package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/service/mongoreview"
)

func (h *Handler) mongoReviewService(w http.ResponseWriter) (*mongoreview.Service, bool) {
	if h.deps.MongoReviewService == nil {
		httpError(w, http.StatusServiceUnavailable, "MongoDB script review is not configured")
		return nil, false
	}
	return h.deps.MongoReviewService, true
}

func (h *Handler) listMongoReviewEnvironments(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	items, err := service.ListEnvironments(r.Context())
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"environments": items})
}

func (h *Handler) saveMongoReviewEnvironment(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	var input mongoreview.EnvironmentInput
	if !decodeMongoReviewBody(w, r, &input, 64<<10) {
		return
	}
	item, err := service.SaveEnvironment(r.Context(), chi.URLParam(r, "environment"), input)
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"environment": item})
}

func (h *Handler) testMongoReviewEnvironment(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	var input mongoreview.EnvironmentInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	if err := decoder.Decode(&input); err != nil && !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := service.TestEnvironment(r.Context(), chi.URLParam(r, "environment"), input); err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) listMongoReviewRules(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	items, err := service.ListRules(r.Context())
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"query_rules": items})
}

func (h *Handler) saveMongoReviewRule(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	var input mongoreview.QueryRule
	if !decodeMongoReviewBody(w, r, &input, 256<<10) {
		return
	}
	item, err := service.SaveRule(r.Context(), input)
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"query_rule": item})
}

func (h *Handler) deleteMongoReviewRule(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	if err := service.DeleteRule(r.Context(), chi.URLParam(r, "id")); err != nil {
		respondMongoReviewError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listMongoReviewRepositoryFiles(w http.ResponseWriter, _ *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	items, err := service.ListRepositoryFiles()
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"files": items})
}

func (h *Handler) listMongoReviewRepositoryIndex(w http.ResponseWriter, _ *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	projects, tasks, err := service.ListRepositoryIndex()
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"projects": projects, "tasks": tasks})
}

func (h *Handler) getMongoReviewRepositoryTask(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	task, err := service.GetRepositoryTask(r.Context(), chi.URLParam(r, "taskKey"))
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (h *Handler) createMongoReviewRepositoryFile(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	var input mongoreview.CreateRepositoryFileInput
	if !decodeMongoReviewBody(w, r, &input, mongoreview.MaxScriptBytes+(32<<10)) {
		return
	}
	file, err := service.CreateRepositoryFile(input)
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"file": file})
}

func (h *Handler) readMongoReviewRepositoryFile(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	content, err := service.ReadRepositoryFile(r.URL.Query().Get("path"))
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{
		"path": r.URL.Query().Get("path"), "source": string(content),
	})
}

func (h *Handler) parseMongoReviewScript(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	var body struct {
		Source string `json:"source"`
	}
	if !decodeMongoReviewBody(w, r, &body, mongoreview.MaxScriptBytes) {
		return
	}
	result, err := service.Parse(r.Context(), body.Source)
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) listMongoReviewScripts(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	items, err := service.ListScripts(r.Context())
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"scripts": items})
}

func (h *Handler) saveMongoReviewScript(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	var input mongoreview.Script
	if !decodeMongoReviewBody(w, r, &input, mongoreview.MaxScriptBytes+(64<<10)) {
		return
	}
	item, err := service.SaveScript(r.Context(), input)
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"script": item})
}

func (h *Handler) getMongoReviewScript(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	item, err := service.GetScript(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"script": item})
}

func (h *Handler) deleteMongoReviewScript(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	if err := service.DeleteScript(r.Context(), chi.URLParam(r, "id")); err != nil {
		respondMongoReviewError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) startMongoReview(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	var input mongoreview.ReviewRequest
	if !decodeMongoReviewBody(w, r, &input, mongoreview.MaxScriptBytes+(256<<10)) {
		return
	}
	item, err := service.StartReview(r.Context(), input)
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	w.Header().Set("Location", "/api/mongodb-review/reviews/"+item.ID)
	respondJSON(w, http.StatusAccepted, map[string]any{"review": item})
}

func (h *Handler) getMongoReview(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	item, err := service.GetReview(chi.URLParam(r, "id"))
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"review": item})
}

func (h *Handler) streamMongoReviewEvents(w http.ResponseWriter, r *http.Request) {
	service, ok := h.mongoReviewService(w)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming is unavailable")
		return
	}
	after, _ := strconv.ParseInt(r.URL.Query().Get("after"), 10, 64)
	events, cancel, err := service.SubscribeReview(chi.URLParam(r, "id"), after)
	if err != nil {
		respondMongoReviewError(w, err)
		return
	}
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": heartbeat\n\n")
			flusher.Flush()
		case event, open := <-events:
			if !open {
				return
			}
			payload, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, payload)
			flusher.Flush()
			if event.Review.Status == "completed" || event.Review.Status == "failed" {
				return
			}
		}
	}
}

func decodeMongoReviewBody(w http.ResponseWriter, r *http.Request, target any, limit int64) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit))
	if err := decoder.Decode(target); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

func respondMongoReviewError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, mongoreview.ErrNotFound):
		httpError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, mongoreview.ErrInvalidInput):
		httpError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, mongoreview.ErrNotConfigured):
		httpError(w, http.StatusServiceUnavailable, err.Error())
	default:
		httpError(w, http.StatusInternalServerError, err.Error())
	}
}
