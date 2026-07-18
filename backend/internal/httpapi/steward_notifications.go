package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/service/steward"
)

func (h *Handler) listStewardNotifications(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.StewardService.ListNotifications(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"notifications": items})
}

func (h *Handler) createStewardNotification(w http.ResponseWriter, r *http.Request) {
	var input steward.CreateNotificationInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		httpError(w, http.StatusBadRequest, "invalid notification JSON")
		return
	}
	item, err := h.deps.StewardService.CreateNotification(r.Context(), input)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"notification": item})
}

func (h *Handler) decideStewardNotification(w http.ResponseWriter, r *http.Request) {
	var input steward.NotificationDecisionInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 128<<10)).Decode(&input); err != nil {
		httpError(w, http.StatusBadRequest, "invalid notification decision JSON")
		return
	}
	item, err := h.deps.StewardService.DecideNotification(r.Context(), chi.URLParam(r, "id"), input)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"notification": item})
}

func (h *Handler) listStewardNotificationEndpoints(w http.ResponseWriter, r *http.Request) {
	items, err := h.deps.StewardService.ListNotificationEndpoints(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"endpoints": items})
}

func (h *Handler) upsertStewardNotificationEndpoint(w http.ResponseWriter, r *http.Request) {
	var input steward.UpdateNotificationEndpointInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		httpError(w, http.StatusBadRequest, "invalid notification endpoint JSON")
		return
	}
	item, err := h.deps.StewardService.UpsertNotificationEndpoint(r.Context(), input)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"endpoint": item})
}

func (h *Handler) testStewardNotificationEndpoint(w http.ResponseWriter, r *http.Request) {
	item, err := h.deps.StewardService.TestNotificationEndpoint(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusBadGateway, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"endpoint": item})
}
