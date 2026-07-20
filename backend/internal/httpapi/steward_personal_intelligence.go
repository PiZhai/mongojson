package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/steward"
)

const maxStewardPersonalIntelligenceBodyBytes = 1 << 20

func (h *Handler) requireStewardPersonalIntelligence(w http.ResponseWriter) (StewardPersonalIntelligenceStore, bool) {
	service, ok := h.deps.StewardService.(StewardPersonalIntelligenceStore)
	if !ok {
		httpError(w, http.StatusServiceUnavailable, "personal intelligence is not configured")
		return nil, false
	}
	return service, true
}

func (h *Handler) getStewardBackgroundStatus(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	status, err := service.GetBackgroundStatus(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"status": status})
}

func (h *Handler) getStewardProfile(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	projection := strings.TrimSpace(r.URL.Query().Get("view"))
	if _, valid := steward.ProfileSnapshotForView(domain.StewardProfileView{}, projection); !valid {
		httpError(w, http.StatusBadRequest, "view must be recent, stable, explicit, or merged")
		return
	}
	profile, err := service.GetProfileView(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	snapshot, _ := steward.ProfileSnapshotForView(profile, projection)
	respondJSON(w, http.StatusOK, map[string]any{"view": projection, "profile": snapshot})
}

func (h *Handler) listStewardProfileFacts(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	items, err := service.ListProfileFacts(r.Context(), stewardProfileFactsListInput(r))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"facts": items})
}

func (h *Handler) listStewardProfileHistory(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	items, err := service.ListProfileFacts(r.Context(), stewardProfileFactsListInput(r))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"facts": items})
}

func stewardProfileFactsListInput(r *http.Request) steward.ListProfileFactsInput {
	horizon := strings.TrimSpace(r.URL.Query().Get("horizon"))
	if horizon == "" {
		horizon = strings.TrimSpace(r.URL.Query().Get("view"))
	}
	return steward.ListProfileFactsInput{
		Horizon: horizon,
		Status:  strings.TrimSpace(r.URL.Query().Get("status")),
		Key:     strings.TrimSpace(r.URL.Query().Get("key")),
		Limit:   queryLimit(r, 100),
	}
}

func (h *Handler) correctStewardProfileFact(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	var input steward.UpsertProfileFactInput
	if err := decodeStewardPersonalIntelligenceJSON(w, r, &input); err != nil {
		httpError(w, http.StatusBadRequest, "invalid profile correction JSON")
		return
	}
	fact, err := service.CorrectProfileFact(r.Context(), input)
	if err != nil {
		if errors.Is(err, steward.ErrProfileCorrectionPropagation) {
			httpError(w, http.StatusInternalServerError, err.Error())
			return
		}
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]any{"fact": fact})
}

func (h *Handler) listStewardReports(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	items, err := service.ListReports(
		r.Context(),
		strings.TrimSpace(r.URL.Query().Get("cadence")),
		queryLimit(r, 50),
		queryBoolean(r, "include_history"),
	)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"reports": items})
}

func (h *Handler) getStewardReport(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	item, err := service.GetReport(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStewardPersonalIntelligenceLookupError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"report": item})
}

func (h *Handler) regenerateStewardReport(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	var input steward.RegenerateReportInput
	if err := decodeStewardPersonalIntelligenceJSON(w, r, &input); err != nil && !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, "invalid report regeneration JSON")
		return
	}
	result, err := service.RegenerateReport(r.Context(), chi.URLParam(r, "id"), input)
	if err != nil {
		writeStewardPersonalIntelligenceLookupError(w, err)
		return
	}
	w.Header().Set("Location", "/api/steward/background/jobs/"+result.Job.ID)
	respondJSON(w, http.StatusAccepted, map[string]any{"regeneration": result})
}

func (h *Handler) getStewardReminderPolicy(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	policy, err := service.GetReminderPolicy(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (h *Handler) updateStewardReminderPolicy(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	var input steward.UpdateReminderPolicyInput
	if err := decodeStewardPersonalIntelligenceJSON(w, r, &input); err != nil {
		httpError(w, http.StatusBadRequest, "invalid reminder policy JSON")
		return
	}
	policy, err := service.UpdateReminderPolicy(r.Context(), input)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"policy": policy})
}

func (h *Handler) listStewardReminderFeedback(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	items, err := service.ListReminderFeedback(r.Context(), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"feedback": items})
}

func (h *Handler) listStewardReceptivityWindows(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	items, err := service.ListReceptivityWindows(r.Context(), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"windows": items})
}

type stewardNotificationFeedbackCallbackInput struct {
	CallbackToken string         `json:"callback_token"`
	Action        string         `json:"action"`
	OccurredAt    *time.Time     `json:"occurred_at,omitempty"`
	DeviceID      string         `json:"device_id,omitempty"`
	Channel       string         `json:"channel,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

func (h *Handler) recordStewardNotificationFeedbackCallback(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	var input stewardNotificationFeedbackCallbackInput
	if err := decodeStewardPersonalIntelligenceJSON(w, r, &input); err != nil {
		httpError(w, http.StatusBadRequest, "invalid notification feedback JSON")
		return
	}
	input.CallbackToken = strings.TrimSpace(input.CallbackToken)
	input.Action = strings.ToLower(strings.TrimSpace(input.Action))
	if input.CallbackToken == "" || input.Action == "" {
		httpError(w, http.StatusBadRequest, "callback_token and action are required")
		return
	}
	metadata := cloneStewardCallbackMetadata(input.Metadata)
	metadata["reported_action"] = input.Action
	if input.OccurredAt != nil && !input.OccurredAt.IsZero() {
		metadata["reported_occurred_at"] = input.OccurredAt.UTC().Format(time.RFC3339Nano)
	}
	deviceID := firstNonEmptyStewardCallbackValue(input.DeviceID, r.Header.Get("X-Steward-Device-ID"), metadata["device_id"])
	channel := firstNonEmptyStewardCallbackValue(input.Channel, r.Header.Get("X-Steward-Notification-Channel"), metadata["channel"])
	if channel == "" {
		channel = "system"
	}
	feedback, err := service.RecordNotificationCallback(r.Context(), input.CallbackToken, deviceID, channel, metadata)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusAccepted, map[string]any{"feedback": feedback})
}

func (h *Handler) listStewardIntelligenceJobs(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	items, err := service.ListIntelligenceJobs(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"jobs": items})
}

func (h *Handler) getStewardIntelligenceJob(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	item, err := service.GetIntelligenceJob(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStewardPersonalIntelligenceLookupError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"job": item})
}

func (h *Handler) cancelStewardIntelligenceJob(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	id := chi.URLParam(r, "id")
	if err := service.CancelIntelligenceJob(r.Context(), id); err != nil {
		writeStewardPersonalIntelligenceLookupError(w, err)
		return
	}
	item, err := service.GetIntelligenceJob(r.Context(), id)
	if err != nil {
		writeStewardPersonalIntelligenceLookupError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"job": item})
}

func (h *Handler) runStewardActivityBatches(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	var input struct {
		At *time.Time `json:"at,omitempty"`
	}
	if r.ContentLength != 0 {
		if err := decodeStewardPersonalIntelligenceJSON(w, r, &input); err != nil {
			httpError(w, http.StatusBadRequest, "invalid activity batch run JSON")
			return
		}
	}
	now := time.Now().UTC()
	if input.At != nil && !input.At.IsZero() {
		now = input.At.UTC()
	}
	items, err := service.BuildDueActivityBatches(r.Context(), now)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"batches": items})
}

func (h *Handler) listStewardActivityBatches(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	items, err := service.ListActivityBatches(r.Context(), strings.TrimSpace(r.URL.Query().Get("status")), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"batches": items})
}

func (h *Handler) getStewardActivityBatch(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	item, err := service.GetActivityBatch(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStewardPersonalIntelligenceLookupError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"batch": item})
}

func (h *Handler) getStewardActivityBatchContext(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPersonalIntelligence(w)
	if !ok {
		return
	}
	item, err := service.GetActivityBatchContext(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeStewardPersonalIntelligenceLookupError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"context": item})
}

func decodeStewardPersonalIntelligenceJSON(w http.ResponseWriter, r *http.Request, target any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxStewardPersonalIntelligenceBodyBytes))
	return decoder.Decode(target)
}

func writeStewardPersonalIntelligenceLookupError(w http.ResponseWriter, err error) {
	if errors.Is(err, pgx.ErrNoRows) {
		httpError(w, http.StatusNotFound, "personal intelligence record not found")
		return
	}
	httpError(w, http.StatusInternalServerError, err.Error())
}

func queryBoolean(r *http.Request, key string) bool {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get(key))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func cloneStewardCallbackMetadata(input map[string]any) map[string]any {
	result := make(map[string]any, len(input)+2)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func firstNonEmptyStewardCallbackValue(values ...any) string {
	for _, value := range values {
		text := strings.TrimSpace(toString(value))
		if text != "" {
			return text
		}
	}
	return ""
}

func toString(value any) string {
	if value == nil {
		return ""
	}
	text, _ := value.(string)
	return text
}
