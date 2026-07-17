package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
	"mongojson/backend/internal/service/steward"
)

type Handler struct {
	deps        Dependencies
	peerService StewardPeerStore
}

const (
	maxUploadBytes       = 64 << 20
	maxAgentRunBodyBytes = 1 << 20
)

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

func (h *Handler) watchRoomWebSocket(w http.ResponseWriter, r *http.Request) {
	if h.deps.WatchSync == nil {
		httpError(w, http.StatusServiceUnavailable, "watch sync is not configured")
		return
	}
	h.deps.WatchSync.ServeRoom(w, r, chi.URLParam(r, "roomID"))
}

func (h *Handler) getStewardOverview(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	overview, err := service.GetOverview(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardOverview{"overview": overview})
}

func (h *Handler) getStewardAgent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	status, err := service.GetAgentStatus(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAgentStatus{"agent": status})
}

func (h *Handler) startStewardAgent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	status, err := service.StartAgent(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAgentStatus{"agent": status})
}

func (h *Handler) stopStewardAgent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	status, err := service.StopAgent(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAgentStatus{"agent": status})
}

func (h *Handler) listStewardCollectors(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	collectors, err := service.ListCollectors(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardCollectorConfig{"collectors": collectors})
}

func (h *Handler) updateStewardCollector(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.UpdateCollectorInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	collector, err := service.UpdateCollector(r.Context(), chi.URLParam(r, "name"), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardCollectorConfig{"collector": collector})
}

func (h *Handler) listStewardDataPolicies(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardAutomationPolicyService(w)
	if !ok {
		return
	}
	items, err := service.ListDataPolicies(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardDataPolicy{"data_policies": items})
}

func (h *Handler) upsertStewardDataPolicy(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardAutomationPolicyService(w)
	if !ok {
		return
	}
	var body steward.UpsertDataPolicyInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.UpsertDataPolicy(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardDataPolicy{"data_policy": item})
}

func (h *Handler) listStewardPermissionPolicies(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardAutomationPolicyService(w)
	if !ok {
		return
	}
	items, err := service.ListPermissionPolicies(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardPermissionPolicy{"permission_policies": items})
}

func (h *Handler) upsertStewardPermissionPolicy(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardAutomationPolicyService(w)
	if !ok {
		return
	}
	var body steward.UpsertPermissionPolicyInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.UpsertPermissionPolicy(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardPermissionPolicy{"permission_policy": item})
}

func (h *Handler) listStewardModelDispatches(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardAutomationPolicyService(w)
	if !ok {
		return
	}
	items, err := service.ListModelDispatches(r.Context(), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardModelDispatch{"model_dispatches": items})
}

func (h *Handler) runStewardModelDispatches(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardAutomationPolicyService(w)
	if !ok {
		return
	}
	items, err := service.RunModelDispatches(r.Context(), queryLimit(r, 20))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardModelDispatch{"model_dispatches": items})
}

func (h *Handler) listStewardToolDefinitions(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardAutomationPolicyService(w)
	if !ok {
		return
	}
	items, err := service.ListToolDefinitions(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardToolDefinition{"tools": items})
}

func (h *Handler) upsertStewardToolDefinition(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardAutomationPolicyService(w)
	if !ok {
		return
	}
	var body steward.UpsertToolDefinitionInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.UpsertToolDefinition(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardToolDefinition{"tool": item})
}

func (h *Handler) listStewardRuntimeTools(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	items, err := service.ListRuntimeToolSpecs(r.Context())
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardToolSpec{"tools": items})
}

func (h *Handler) getStewardRuntimePlanner(w http.ResponseWriter, _ *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardRuntimePlannerStatus{"planner": service.GetRuntimePlannerStatus()})
}

func (h *Handler) listStewardAgentRuns(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	items, err := service.ListAgentRuns(r.Context(), r.URL.Query().Get("status"), queryLimit(r, 40))
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardAgentRunSummary{"runs": items})
}

func (h *Handler) getStewardRuntimeControl(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	control, err := service.GetRuntimeExecutionControl(r.Context())
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardRuntimeExecutionControl{"control": control})
}

func (h *Handler) pauseStewardRuntime(w http.ResponseWriter, r *http.Request) {
	h.setStewardRuntimeControl(w, r, true)
}

func (h *Handler) resumeStewardRuntime(w http.ResponseWriter, r *http.Request) {
	h.setStewardRuntimeControl(w, r, false)
}

func (h *Handler) setStewardRuntimeControl(w http.ResponseWriter, r *http.Request, paused bool) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	var body steward.SetRuntimeExecutionControlInput
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentRunBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpError(w, http.StatusRequestEntityTooLarge, "runtime control JSON body exceeds 1 MiB")
			return
		}
		httpError(w, http.StatusBadRequest, "invalid runtime control JSON body: "+err.Error())
		return
	}
	var (
		control domain.StewardRuntimeExecutionControl
		err     error
	)
	if paused {
		control, err = service.PauseRuntimeExecution(r.Context(), body)
	} else {
		control, err = service.ResumeRuntimeExecution(r.Context(), body)
	}
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardRuntimeExecutionControl{"control": control})
}

func (h *Handler) planStewardAgentRun(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	var body steward.PlanAgentRunInput
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentRunBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpError(w, http.StatusRequestEntityTooLarge, "natural-language plan JSON body exceeds 1 MiB")
			return
		}
		httpError(w, http.StatusBadRequest, "invalid natural-language plan JSON body: "+err.Error())
		return
	}
	run, err := service.PlanAgentRun(r.Context(), body)
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardAgentRun{"run": run})
}

func (h *Handler) createStewardAgentRun(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	var body steward.CreateAgentRunInput
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentRunBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpError(w, http.StatusRequestEntityTooLarge, "agent run JSON body exceeds 1 MiB")
			return
		}
		httpError(w, http.StatusBadRequest, "invalid agent run JSON body: "+err.Error())
		return
	}
	run, err := service.CreateAgentRun(r.Context(), body)
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardAgentRun{"run": run})
}

func (h *Handler) getStewardAgentRun(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	run, err := service.GetAgentRun(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAgentRun{"run": run})
}

func (h *Handler) getStewardAgentRunEvidence(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	if !isLocalRequest(r) {
		httpError(w, http.StatusForbidden, "execution evidence payload can only be revealed through the local management endpoint")
		return
	}
	evidence, err := service.GetEvidenceArtifact(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "evidenceID"))
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardEvidenceArtifact{"evidence": evidence})
}

func (h *Handler) startStewardAgentRun(w http.ResponseWriter, r *http.Request) {
	h.transitionStewardAgentRun(w, r, "start")
}

func (h *Handler) cancelStewardAgentRun(w http.ResponseWriter, r *http.Request) {
	h.transitionStewardAgentRun(w, r, "cancel")
}

func (h *Handler) resumeStewardAgentRun(w http.ResponseWriter, r *http.Request) {
	h.transitionStewardAgentRun(w, r, "resume")
}

func (h *Handler) transitionStewardAgentRun(w http.ResponseWriter, r *http.Request, action string) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	var (
		run domain.StewardAgentRun
		err error
	)
	switch action {
	case "start":
		run, err = service.StartAgentRun(r.Context(), chi.URLParam(r, "id"))
	case "cancel":
		run, err = service.CancelAgentRun(r.Context(), chi.URLParam(r, "id"))
	case "resume":
		run, err = service.ResumeAgentRun(r.Context(), chi.URLParam(r, "id"))
	default:
		err = steward.ErrAgentRunInvalidTransition
	}
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAgentRun{"run": run})
}

func (h *Handler) approveStewardAgentRun(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	var body steward.ApproveAgentRunInput
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentRunBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpError(w, http.StatusRequestEntityTooLarge, "approval JSON body exceeds 1 MiB")
			return
		}
		httpError(w, http.StatusBadRequest, "invalid approval JSON body: "+err.Error())
		return
	}
	run, err := service.ApproveAgentRun(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAgentRun{"run": run})
}

func (h *Handler) streamStewardAgentRunEvents(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardRuntimeService(w)
	if !ok {
		return
	}
	runID := chi.URLParam(r, "id")
	if _, err := service.GetAgentRun(r.Context(), runID); err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming is not supported by this server")
		return
	}
	after := queryInt64(r, "after")
	if lastID := strings.TrimSpace(r.Header.Get("Last-Event-ID")); lastID != "" {
		if parsed, err := strconv.ParseInt(lastID, 10, 64); err == nil && parsed > after {
			after = parsed
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		events, err := service.ListAgentRunEvents(r.Context(), runID, after, 100)
		if err != nil {
			_, _ = fmt.Fprintf(w, "event: error\ndata: %s\n\n", mustJSON(map[string]string{"error": err.Error()}))
			flusher.Flush()
			return
		}
		for _, event := range events {
			payload, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, payload)
			after = event.Sequence
		}
		if len(events) > 0 {
			flusher.Flush()
		}
		if len(events) == 100 {
			continue
		}
		run, err := service.GetAgentRun(r.Context(), runID)
		if err != nil || runtimeRunTerminalStatus(run.Status) {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			_, _ = fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func runtimeRunTerminalStatus(status string) bool {
	switch status {
	case steward.RuntimeRunSucceeded, steward.RuntimeRunFailed, steward.RuntimeRunCancelled, steward.RuntimeRunBlocked:
		return true
	default:
		return false
	}
}

func (h *Handler) listStewardOrchestrationAgents(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardOrchestrationService(w)
	if !ok {
		return
	}
	items, err := service.ListOrchestrationAgents(r.Context())
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardOrchestrationAgent{"agents": items})
}

func (h *Handler) upsertStewardOrchestrationAgent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardOrchestrationService(w)
	if !ok {
		return
	}
	var body steward.UpsertOrchestrationAgentInput
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentRunBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid orchestration agent JSON body: "+err.Error())
		return
	}
	item, err := service.UpsertOrchestrationAgent(r.Context(), body)
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardOrchestrationAgent{"agent": item})
}

func (h *Handler) listStewardOrchestrations(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardOrchestrationService(w)
	if !ok {
		return
	}
	items, err := service.ListOrchestrations(r.Context(), r.URL.Query().Get("status"), queryLimit(r, 40))
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardOrchestration{"orchestrations": items})
}

func (h *Handler) createStewardOrchestration(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardOrchestrationService(w)
	if !ok {
		return
	}
	var body steward.CreateOrchestrationInput
	r.Body = http.MaxBytesReader(w, r.Body, maxAgentRunBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid orchestration JSON body: "+err.Error())
		return
	}
	item, err := service.CreateOrchestration(r.Context(), body)
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardOrchestration{"orchestration": item})
}

func (h *Handler) getStewardOrchestration(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardOrchestrationService(w)
	if !ok {
		return
	}
	item, err := service.GetOrchestration(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardOrchestration{"orchestration": item})
}

func (h *Handler) startStewardOrchestration(w http.ResponseWriter, r *http.Request) {
	h.transitionStewardOrchestration(w, r, "start")
}

func (h *Handler) cancelStewardOrchestration(w http.ResponseWriter, r *http.Request) {
	h.transitionStewardOrchestration(w, r, "cancel")
}

func (h *Handler) previewStewardRemotePrivilege(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardOrchestrationService(w)
	if !ok {
		return
	}
	preview, err := service.PreviewRemotePrivilegeNode(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "nodeID"))
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.RemotePrivilegePreview{"preview": preview})
}

func (h *Handler) approveStewardRemotePrivilege(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardOrchestrationService(w)
	if !ok {
		return
	}
	var body steward.ApproveRemotePrivilegeInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAgentRunBodyBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid remote privilege approval body: "+err.Error())
		return
	}
	item, err := service.ApproveRemotePrivilegeNode(r.Context(), chi.URLParam(r, "id"), chi.URLParam(r, "nodeID"), body)
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardOrchestration{"orchestration": item})
}

func (h *Handler) transitionStewardOrchestration(w http.ResponseWriter, r *http.Request, action string) {
	service, ok := h.requireStewardOrchestrationService(w)
	if !ok {
		return
	}
	var item domain.StewardOrchestration
	var err error
	if action == "start" {
		item, err = service.StartOrchestration(r.Context(), chi.URLParam(r, "id"))
	} else {
		item, err = service.CancelOrchestration(r.Context(), chi.URLParam(r, "id"))
	}
	if err != nil {
		respondStewardRuntimeError(w, err)
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardOrchestration{"orchestration": item})
}

func mustJSON(value any) string {
	payload, _ := json.Marshal(value)
	return string(payload)
}

func respondStewardRuntimeError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, steward.ErrRuntimeV2Disabled), errors.Is(err, steward.ErrRuntimeR2Disabled):
		status = http.StatusServiceUnavailable
	case errors.Is(err, steward.ErrOrchestrationDisabled):
		status = http.StatusServiceUnavailable
	case errors.Is(err, steward.ErrAgentRunNotFound), errors.Is(err, steward.ErrOrchestrationNotFound), errors.Is(err, steward.ErrOrchestrationAgentNotFound):
		status = http.StatusNotFound
	case errors.Is(err, steward.ErrRuntimePolicyDenied),
		errors.Is(err, steward.ErrRuntimePathDenied),
		errors.Is(err, steward.ErrRuntimeCommandDenied),
		errors.Is(err, steward.ErrAdvisorDataLevelDenied):
		status = http.StatusForbidden
	case errors.Is(err, steward.ErrRuntimePlannerUnsupported), errors.Is(err, steward.ErrRuntimePlannerToolUnavailable):
		status = http.StatusUnprocessableEntity
	case errors.Is(err, steward.ErrAgentRunInvalid), errors.Is(err, steward.ErrRuntimeToolInput), errors.Is(err, steward.ErrOrchestrationInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, steward.ErrAgentRunConflict),
		errors.Is(err, steward.ErrAgentRunInvalidTransition),
		errors.Is(err, steward.ErrAgentRunPlanHashMismatch),
		errors.Is(err, steward.ErrOrchestrationConflict),
		errors.Is(err, steward.ErrOrchestrationInvalidTransition):
		status = http.StatusConflict
	}
	httpError(w, status, err.Error())
}

func (h *Handler) listStewardConversations(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardConversationService(w)
	if !ok {
		return
	}
	items, err := service.ListConversations(r.Context(), queryLimit(r, 30))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardConversation{"conversations": items})
}

func (h *Handler) createStewardConversation(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardConversationService(w)
	if !ok {
		return
	}
	var body steward.CreateConversationInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.CreateConversation(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardConversation{"conversation": item})
}

func (h *Handler) listStewardConversationMessages(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardConversationService(w)
	if !ok {
		return
	}
	items, err := service.ListConversationMessages(r.Context(), chi.URLParam(r, "id"), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardConversationMessage{"messages": items})
}

func (h *Handler) sendStewardConversationMessage(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardConversationService(w)
	if !ok {
		return
	}
	var body steward.SendConversationMessageInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	result, err := service.SendConversationMessage(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, result)
}

func (h *Handler) decideStewardConversationSuggestion(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardConversationService(w)
	if !ok {
		return
	}
	var body steward.DecideConversationSuggestionInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.DecideConversationSuggestion(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardConversationSuggestion{"suggestion": item})
}

func (h *Handler) decideStewardConversationExecution(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardConversationService(w)
	if !ok {
		return
	}
	var body steward.DecideConversationExecutionInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.DecideConversationExecution(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardConversationExecution{"execution": item})
}

func (h *Handler) listStewardEvents(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	events, err := service.ListEvents(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardEvent{"events": events})
}

func (h *Handler) createStewardEvent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateEventInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	event, err := service.CreateEvent(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardEvent{"event": event})
}

func (h *Handler) deleteStewardEvent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	if err := service.DeleteEvent(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) hideStewardEvent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	event, err := service.HideEvent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardEvent{"event": event})
}

func (h *Handler) convertStewardEvent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.ConvertEventInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	result, err := service.ConvertEvent(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, result)
}

func (h *Handler) listStewardTimelineSegments(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.ListTimelineSegments(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardTimelineSegment{"timeline_segments": items})
}

func (h *Handler) deleteStewardTimelineSegment(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	if err := service.DeleteTimelineSegment(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) listStewardTasks(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	tasks, err := service.ListTasks(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardTask{"tasks": tasks})
}

func (h *Handler) createStewardTask(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateTaskInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	task, err := service.CreateTask(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardTask{"task": task})
}

func (h *Handler) updateStewardTask(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.UpdateTaskInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	task, err := service.UpdateTask(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardTask{"task": task})
}

func (h *Handler) completeStewardTask(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	task, err := service.CompleteTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardTask{"task": task})
}

func (h *Handler) cancelStewardTask(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	task, err := service.CancelTask(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardTask{"task": task})
}

func (h *Handler) deleteStewardTask(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	if err := service.DeleteTask(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) listStewardIntents(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.ListIntents(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardIntent{"intents": items})
}

func (h *Handler) createStewardIntent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateIntentInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	intent, err := service.CreateIntent(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardIntent{"intent": intent})
}

func (h *Handler) acceptStewardIntent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	task, err := service.AcceptIntent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardTask{"task": task})
}

func (h *Handler) dismissStewardIntent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	intent, err := service.DismissIntent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardIntent{"intent": intent})
}

func (h *Handler) muteStewardIntent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	intent, err := service.MuteIntent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardIntent{"intent": intent})
}

func (h *Handler) deleteStewardIntent(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	if err := service.DeleteIntent(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) listStewardMemories(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.ListMemories(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardMemory{"memories": items})
}

func (h *Handler) createStewardMemory(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateMemoryInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	memory, err := service.CreateMemory(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardMemory{"memory": memory})
}

func (h *Handler) correctStewardMemory(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CorrectMemoryInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	memory, err := service.CorrectMemory(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardMemory{"memory": memory})
}

func (h *Handler) archiveStewardMemory(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	memory, err := service.ArchiveMemory(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardMemory{"memory": memory})
}

func (h *Handler) deleteStewardMemory(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	if err := service.DeleteMemory(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) listStewardMemoryVersions(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.ListMemoryVersions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardMemoryVersion{"versions": items})
}

func (h *Handler) listStewardKnowledgeItems(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.ListKnowledgeItems(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardKnowledgeItem{"knowledge_items": items})
}

func (h *Handler) createStewardKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateKnowledgeInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.CreateKnowledgeItem(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardKnowledgeItem{"knowledge_item": item})
}

func (h *Handler) deleteStewardKnowledgeItem(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	if err := service.DeleteKnowledgeItem(r.Context(), chi.URLParam(r, "id")); err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *Handler) listStewardSourceRefs(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.ListSourceRefs(
		r.Context(),
		r.URL.Query().Get("target_type"),
		r.URL.Query().Get("target_id"),
		queryLimit(r, 50),
	)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardSourceRef{"source_refs": items})
}

func (h *Handler) createStewardSourceRef(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateSourceRefInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.CreateSourceRef(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardSourceRef{"source_ref": item})
}

func (h *Handler) listStewardTags(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.ListDataTags(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardDataTag{"tags": items})
}

func (h *Handler) createStewardTag(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateDataTagInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	item, err := service.CreateDataTag(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardDataTag{"tag": item})
}

func (h *Handler) assignStewardTag(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.AssignTagInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := service.AssignDataTag(r.Context(), body); err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"status": "assigned"})
}

func (h *Handler) searchStewardData(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.Search(r.Context(), steward.SearchInput{
		Query:      r.URL.Query().Get("q"),
		EntityType: r.URL.Query().Get("entity_type"),
		Status:     r.URL.Query().Get("status"),
		DataLevel:  r.URL.Query().Get("data_level"),
		Limit:      queryLimit(r, 50),
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardSearchResult{"results": items})
}

func (h *Handler) listStewardAuditLogs(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	logs, err := service.ListAuditLogs(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardAuditLog{"audit_logs": logs})
}

func (h *Handler) exportStewardSummary(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	includeSensitive := r.URL.Query().Get("include_sensitive") == "true"
	overview, err := service.ExportData(r.Context(), includeSensitive)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardOverview{"export": overview})
}

func (h *Handler) getStewardSyncStatus(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	status, err := service.GetSyncStatus(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardSyncStatus{"sync": status})
}

func (h *Handler) listStewardDevices(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	devices, err := service.ListDevices(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardDevice{"devices": devices})
}

func (h *Handler) registerStewardDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.RegisterDeviceInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	device, err := service.RegisterDevice(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardDevice{"device": device})
}

func (h *Handler) revokeStewardDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	device, err := service.RevokeDevice(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardDevice{"device": device})
}

func (h *Handler) signStewardPairingChallenge(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPeerService(w)
	if !ok {
		return
	}
	var body steward.PairingChallengeInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	result, err := service.SignPairingChallenge(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.PairingChallengeResult{"challenge": result})
}

func (h *Handler) verifyStewardDeviceTrust(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	result, err := service.VerifyDeviceTrust(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.VerifyDeviceTrustResult{"verification": result})
}

func (h *Handler) syncStewardDevice(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	result, err := service.SyncDevice(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.SyncDeviceResult{"sync": result})
}

func (h *Handler) probeStewardDeviceSyncEntity(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.SyncEntityProbeInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	result, err := service.ProbeDeviceSyncEntity(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.SyncEntityProbeResult{"probe": result})
}

func (h *Handler) listStewardDevicePermissions(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	permissions, err := service.ListDevicePermissions(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardDevicePermission{"permissions": permissions})
}

func (h *Handler) updateStewardDevicePermission(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.UpdateDevicePermissionInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	permission, err := service.UpdateDevicePermission(
		r.Context(),
		chi.URLParam(r, "id"),
		chi.URLParam(r, "capability"),
		body,
	)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardDevicePermission{"permission": permission})
}

func (h *Handler) listStewardSyncChanges(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPeerService(w)
	if !ok {
		return
	}
	if err := service.VerifySyncRequest(r, nil); err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	sinceSequence := queryInt64(r, "since_sequence")
	limit := queryLimit(r, 50)
	if limit > 200 {
		limit = 50
	}
	scanned, err := service.ListSyncChanges(r.Context(), sinceSequence, limit)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nextSequence := sinceSequence
	for _, change := range scanned {
		if change.Sequence > nextSequence {
			nextSequence = change.Sequence
		}
	}
	changes, err := service.PrepareOutboundSyncChanges(r, scanned)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, steward.PeerSyncChangesResult{
		Changes: changes, NextSequence: nextSequence, HasMore: len(scanned) == limit,
	})
}

func (h *Handler) probeLocalStewardSyncEntity(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPeerService(w)
	if !ok {
		return
	}
	if err := service.VerifySyncRequest(r, nil); err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	result, err := service.ProbeLocalSyncEntity(r.Context(), steward.SyncEntityProbeInput{
		EntityType: r.URL.Query().Get("entity_type"),
		EntityID:   r.URL.Query().Get("entity_id"),
	})
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.SyncEntityProbeResult{"probe": result})
}

func (h *Handler) createStewardSyncChange(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateSyncChangeInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	change, err := service.CreateSyncChange(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardSyncChange{"change": change})
}

func (h *Handler) importStewardSyncChanges(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPeerService(w)
	if !ok {
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := service.VerifySyncRequest(r, bodyBytes); err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var body steward.ImportSyncChangesInput
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	signedDeviceID := strings.TrimSpace(r.Header.Get(steward.SyncHeaderDeviceID))
	if signedDeviceID != "" && signedDeviceID != strings.TrimSpace(body.Device.ID) {
		httpError(w, http.StatusUnauthorized, "sync signature device does not match payload device")
		return
	}
	result, err := service.ImportSyncChanges(r.Context(), body)
	if err != nil {
		if errors.Is(err, steward.ErrSyncPermissionDenied) {
			httpError(w, http.StatusForbidden, err.Error())
			return
		}
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]steward.ImportSyncChangesResult{"result": result})
}

func (h *Handler) acceptStewardRemoteExecution(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPeerService(w)
	if !ok {
		return
	}
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := service.VerifySyncRequest(r, bodyBytes); err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	var body steward.RemoteExecutionDispatchEnvelope
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid remote execution envelope")
		return
	}
	result, err := service.AcceptRemoteExecution(r.Context(), body, strings.TrimSpace(r.Header.Get(steward.SyncHeaderDeviceID)))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusAccepted, result)
}

func (h *Handler) getStewardRemoteExecutionStatus(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPeerService(w)
	if !ok {
		return
	}
	if err := service.VerifySyncRequest(r, nil); err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	result, err := service.GetRemoteExecutionStatus(r.Context(), chi.URLParam(r, "id"), strings.TrimSpace(r.Header.Get(steward.SyncHeaderDeviceID)))
	if err != nil {
		httpError(w, http.StatusNotFound, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) cancelStewardRemoteExecution(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPeerService(w)
	if !ok {
		return
	}
	if err := service.VerifySyncRequest(r, nil); err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	result, err := service.CancelRemoteExecution(r.Context(), chi.URLParam(r, "id"), strings.TrimSpace(r.Header.Get(steward.SyncHeaderDeviceID)))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) getStewardRemoteBrokerStatus(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardPeerService(w)
	if !ok {
		return
	}
	if err := service.VerifySyncRequest(r, nil); err != nil {
		httpError(w, http.StatusUnauthorized, err.Error())
		return
	}
	status, err := service.RemoteBrokerStatus(r.Context())
	if err != nil {
		httpError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, status)
}

func (h *Handler) listStewardSyncConflicts(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	conflicts, err := service.ListSyncConflicts(r.Context(), r.URL.Query().Get("status"), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardSyncConflict{"conflicts": conflicts})
}

func (h *Handler) resolveStewardSyncConflict(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.ResolveSyncConflictInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	conflict, err := service.ResolveSyncConflict(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardSyncConflict{"conflict": conflict})
}

func (h *Handler) getStewardAutonomy(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	overview, err := service.GetAutonomyOverview(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomyOverview{"autonomy": overview})
}

func (h *Handler) getStewardModelSettings(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	settings, err := service.GetModelSettings(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.StewardModelSettings{"settings": settings})
}

func (h *Handler) updateStewardModelSettings(w http.ResponseWriter, r *http.Request) {
	if !trustedLocalSettingsOrigin(r.Header.Get("Origin")) {
		httpError(w, http.StatusForbidden, "model settings may only be changed from the local management UI")
		return
	}
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.UpdateStewardModelSettingsInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	settings, err := service.UpdateModelSettings(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.StewardModelSettings{"settings": settings})
}

func trustedLocalSettingsOrigin(origin string) bool {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return true
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Hostname() == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (h *Handler) probeStewardAutonomyAdvisor(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.ProbeAutonomyAdvisorInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	result, err := service.ProbeAutonomyAdvisor(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.ProbeAutonomyAdvisorResult{"probe": result})
}

func (h *Handler) getStewardAutonomySettings(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	settings, err := service.GetAutonomySettings(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomySettings{"settings": settings})
}

func (h *Handler) updateStewardAutonomySettings(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.UpdateAutonomySettingsInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	settings, err := service.UpdateAutonomySettings(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomySettings{"settings": settings})
}

func (h *Handler) runStewardAutonomyCycle(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	overview, err := service.RunAutonomyCycle(r.Context(), queryLimit(r, 12))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomyOverview{"autonomy": overview})
}

func (h *Handler) listStewardProactiveRuns(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	items, err := service.ListProactiveRuns(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardProactiveRun{"runs": items})
}

func (h *Handler) runStewardProactiveCycle(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.RunProactiveInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	items, err := service.RunProactiveCycle(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardProactiveRun{"runs": items})
}

func (h *Handler) listStewardAutonomyRules(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	rules, err := service.ListAutonomyRules(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardAutonomyRule{"rules": rules})
}

func (h *Handler) updateStewardAutonomyRule(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.UpdateAutonomyRuleInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	rule, err := service.UpdateAutonomyRule(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomyRule{"rule": rule})
}

func (h *Handler) listStewardAutonomyProposals(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	proposals, err := service.ListAutonomyProposals(r.Context(), r.URL.Query().Get("status"), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardAutonomyProposal{"proposals": proposals})
}

func (h *Handler) createStewardAutonomyProposal(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.CreateAutonomyProposalInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	proposal, err := service.CreateAutonomyProposal(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardAutonomyProposal{"proposal": proposal})
}

func (h *Handler) approveStewardAutonomyProposal(w http.ResponseWriter, r *http.Request) {
	h.updateStewardAutonomyProposalDecision(w, r, true)
}

func (h *Handler) dismissStewardAutonomyProposal(w http.ResponseWriter, r *http.Request) {
	h.updateStewardAutonomyProposalDecision(w, r, false)
}

func (h *Handler) dismissStewardAutonomyProposals(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.DismissAutonomyProposalsInput
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			httpError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
	}
	result, err := service.DismissAutonomyProposals(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]steward.DismissAutonomyProposalsResult{"result": result})
}

func (h *Handler) updateStewardAutonomyProposalDecision(w http.ResponseWriter, r *http.Request, approve bool) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var (
		proposal domain.StewardAutonomyProposal
		err      error
	)
	if approve {
		proposal, err = service.ApproveAutonomyProposal(r.Context(), chi.URLParam(r, "id"))
	} else {
		proposal, err = service.DismissAutonomyProposal(r.Context(), chi.URLParam(r, "id"))
	}
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomyProposal{"proposal": proposal})
}

func (h *Handler) simulateStewardAutonomyProposal(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	run, err := service.SimulateAutonomyProposal(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomousRun{"run": run})
}

func (h *Handler) executeStewardAutonomyProposal(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	run, err := service.ExecuteAutonomyProposal(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomousRun{"run": run})
}

func (h *Handler) retryStewardAutonomyProposal(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	run, err := service.RetryAutonomyProposal(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardAutonomousRun{"run": run})
}

func (h *Handler) listStewardApprovalRequests(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	approvals, err := service.ListApprovalRequests(r.Context(), r.URL.Query().Get("status"), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardApprovalRequest{"approvals": approvals})
}

func (h *Handler) approveStewardApprovalRequest(w http.ResponseWriter, r *http.Request) {
	h.decideStewardApprovalRequest(w, r, true)
}

func (h *Handler) rejectStewardApprovalRequest(w http.ResponseWriter, r *http.Request) {
	h.decideStewardApprovalRequest(w, r, false)
}

func (h *Handler) decideStewardApprovalRequest(w http.ResponseWriter, r *http.Request, approve bool) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	var body steward.DecideApprovalInput
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	var (
		approval domain.StewardApprovalRequest
		err      error
	)
	if approve {
		approval, err = service.ApproveRequest(r.Context(), chi.URLParam(r, "id"), body)
	} else {
		approval, err = service.RejectRequest(r.Context(), chi.URLParam(r, "id"), body)
	}
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardApprovalRequest{"approval": approval})
}

func (h *Handler) listStewardAutonomousRuns(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardService(w)
	if !ok {
		return
	}
	runs, err := service.ListAutonomousRuns(r.Context(), queryLimit(r, 50))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardAutonomousRun{"runs": runs})
}

func (h *Handler) createStewardObservation(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	if !isLocalRequest(r) {
		httpError(w, http.StatusForbidden, "activity observations can only be written through the local management endpoint")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 44<<20)
	var body steward.CreateObservationInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid observation JSON body")
		return
	}
	item, err := service.CreateObservation(r.Context(), body)
	if err != nil {
		if errors.Is(err, steward.ErrCredentialDataBlocked) {
			httpError(w, http.StatusForbidden, err.Error())
			return
		}
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusCreated, map[string]domain.StewardObservation{"observation": item})
}

func (h *Handler) listStewardObservations(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	items, err := service.ListObservations(r.Context(), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardObservation{"observations": items})
}

func (h *Handler) listStewardActivitySessions(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	items, err := service.ListActivitySessions(r.Context(), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardActivitySession{"sessions": items})
}

func (h *Handler) listStewardEntities(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	items, err := service.ListEntities(r.Context(), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardEntity{"entities": items})
}

func (h *Handler) listStewardEntityRelations(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	items, err := service.ListEntityRelations(r.Context(), chi.URLParam(r, "id"), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardRelation{"relations": items})
}

func (h *Handler) listStewardHabits(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	items, err := service.ListHabits(r.Context(), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardHabit{"habits": items})
}

func (h *Handler) updateStewardHabit(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	var body steward.UpdateInferenceInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid habit decision JSON body")
		return
	}
	item, err := service.UpdateHabit(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardHabit{"habit": item})
}

func (h *Handler) listStewardInsights(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	items, err := service.ListInsights(r.Context(), queryLimit(r, 100))
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardInsight{"insights": items})
}

func (h *Handler) updateStewardInsight(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	var body steward.UpdateInferenceInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid insight decision JSON body")
		return
	}
	item, err := service.UpdateInsight(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardInsight{"insight": item})
}

func (h *Handler) getStewardLifecycleStatus(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	status, err := service.GetLifecycleStatus(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardLifecycleStatus{"lifecycle": status})
}

func (h *Handler) evaluateStewardLifecycle(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	var body steward.EvaluateLifecycleInput
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	evaluation, err := service.EvaluateLifecycle(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardLifecycleEvaluation{"evaluation": evaluation})
}

func (h *Handler) purgeStewardLifecycle(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	var body steward.PurgeLifecycleInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid lifecycle purge JSON body")
		return
	}
	result, err := service.PurgeLifecycle(r.Context(), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardPurgeResult{"purge": result})
}

func (h *Handler) listStewardRetentionPolicies(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	items, err := service.ListRetentionPolicies(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string][]domain.StewardRetentionPolicy{"retention_policies": items})
}

func (h *Handler) updateStewardRetentionPolicy(w http.ResponseWriter, r *http.Request) {
	service, ok := h.requireStewardActivityService(w)
	if !ok {
		return
	}
	var body steward.UpdateRetentionPolicyInput
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "invalid retention policy JSON body")
		return
	}
	item, err := service.UpdateRetentionPolicy(r.Context(), chi.URLParam(r, "id"), body)
	if err != nil {
		httpError(w, http.StatusBadRequest, err.Error())
		return
	}
	respondJSON(w, http.StatusOK, map[string]domain.StewardRetentionPolicy{"retention_policy": item})
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (h *Handler) requireStewardService(w http.ResponseWriter) (StewardStore, bool) {
	if h.deps.StewardService == nil {
		httpError(w, http.StatusServiceUnavailable, "steward S1 prototype is not configured")
		return nil, false
	}
	return h.deps.StewardService, true
}

func (h *Handler) requireStewardActivityService(w http.ResponseWriter) (StewardActivityStore, bool) {
	service, ok := h.deps.StewardService.(StewardActivityStore)
	if !ok || service == nil {
		httpError(w, http.StatusServiceUnavailable, "steward activity and lifecycle service is not configured")
		return nil, false
	}
	return service, true
}

func (h *Handler) requireStewardConversationService(w http.ResponseWriter) (StewardConversationStore, bool) {
	service, ok := h.deps.StewardService.(StewardConversationStore)
	if !ok || service == nil {
		httpError(w, http.StatusServiceUnavailable, "steward conversation service is not configured")
		return nil, false
	}
	return service, true
}

func (h *Handler) requireStewardAutomationPolicyService(w http.ResponseWriter) (StewardAutomationPolicyStore, bool) {
	service, ok := h.deps.StewardService.(StewardAutomationPolicyStore)
	if !ok || service == nil {
		httpError(w, http.StatusServiceUnavailable, "steward automation policy service is not configured")
		return nil, false
	}
	return service, true
}

func (h *Handler) requireStewardRuntimeService(w http.ResponseWriter) (StewardRuntimeStore, bool) {
	service, ok := h.deps.StewardService.(StewardRuntimeStore)
	if !ok || service == nil {
		httpError(w, http.StatusServiceUnavailable, "steward runtime v2 service is not configured")
		return nil, false
	}
	return service, true
}

func (h *Handler) requireStewardOrchestrationService(w http.ResponseWriter) (StewardOrchestrationStore, bool) {
	service, ok := h.deps.StewardService.(StewardOrchestrationStore)
	if !ok || service == nil {
		httpError(w, http.StatusServiceUnavailable, "steward R4.0 orchestration service is not configured")
		return nil, false
	}
	return service, true
}

func (h *Handler) requireStewardPeerService(w http.ResponseWriter) (StewardPeerStore, bool) {
	if h.peerService == nil {
		httpError(w, http.StatusServiceUnavailable, "steward peer protocol is not configured")
		return nil, false
	}
	return h.peerService, true
}

func queryLimit(r *http.Request, fallback int) int {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func queryInt64(r *http.Request, key string) int64 {
	value := r.URL.Query().Get(key)
	if value == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0
	}
	return parsed
}

func withTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 3*time.Second)
}

func notFound(err error) bool {
	return errors.Is(err, os.ErrNotExist)
}
