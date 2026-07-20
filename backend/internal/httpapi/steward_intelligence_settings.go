package httpapi

import (
	"encoding/json"
	"net/http"

	"mongojson/backend/internal/service/steward"
)

func (h *Handler) getStewardIntelligenceSettings(w http.ResponseWriter, r *http.Request) {
	service, ok := h.deps.StewardService.(StewardIntelligenceSettingsStore)
	if !ok {
		httpError(w, http.StatusServiceUnavailable, "continuous intelligence is not configured")
		return
	}
	settings, err := service.GetIntelligenceSettings(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"settings": settings})
}

func (h *Handler) updateStewardIntelligenceSettings(w http.ResponseWriter, r *http.Request) {
	service, ok := h.deps.StewardService.(StewardIntelligenceSettingsStore)
	if !ok {
		httpError(w, http.StatusServiceUnavailable, "continuous intelligence is not configured")
		return
	}
	var input steward.UpdateIntelligenceSettingsInput
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input); err != nil {
		httpError(w, http.StatusBadRequest, "invalid intelligence settings JSON")
		return
	}
	settings, err := service.UpdateIntelligenceSettings(r.Context(), input)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"settings": settings})
}
