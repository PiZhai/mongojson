package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"mongojson/backend/internal/config"
	"mongojson/backend/internal/domain"
	"mongojson/backend/internal/privilegebroker"
	"mongojson/backend/internal/service/filemeta"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
	"mongojson/backend/internal/service/presets"
	"mongojson/backend/internal/service/steward"
	"mongojson/backend/internal/service/watchsync"
)

type MemoStore interface {
	GetOrCreate(context.Context, string) (domain.MemoRecord, error)
	SaveMemo(context.Context, memo.SaveInput) (domain.MemoRecord, error)
}

type StewardStore interface {
	GetOverview(context.Context) (domain.StewardOverview, error)
	GetAgentStatus(context.Context) (domain.StewardAgentStatus, error)
	StartAgent(context.Context) (domain.StewardAgentStatus, error)
	StopAgent(context.Context) (domain.StewardAgentStatus, error)
	ListCollectors(context.Context) ([]domain.StewardCollectorConfig, error)
	UpdateCollector(context.Context, string, steward.UpdateCollectorInput) (domain.StewardCollectorConfig, error)
	ListEvents(context.Context, int) ([]domain.StewardEvent, error)
	CreateEvent(context.Context, steward.CreateEventInput) (domain.StewardEvent, error)
	DeleteEvent(context.Context, string) error
	HideEvent(context.Context, string) (domain.StewardEvent, error)
	ConvertEvent(context.Context, string, steward.ConvertEventInput) (map[string]any, error)
	ListTimelineSegments(context.Context, int) ([]domain.StewardTimelineSegment, error)
	DeleteTimelineSegment(context.Context, string) error
	ListTasks(context.Context, int) ([]domain.StewardTask, error)
	CreateTask(context.Context, steward.CreateTaskInput) (domain.StewardTask, error)
	UpdateTask(context.Context, string, steward.UpdateTaskInput) (domain.StewardTask, error)
	CompleteTask(context.Context, string) (domain.StewardTask, error)
	CancelTask(context.Context, string) (domain.StewardTask, error)
	DeleteTask(context.Context, string) error
	ListIntents(context.Context, int) ([]domain.StewardIntent, error)
	CreateIntent(context.Context, steward.CreateIntentInput) (domain.StewardIntent, error)
	AcceptIntent(context.Context, string) (domain.StewardTask, error)
	DismissIntent(context.Context, string) (domain.StewardIntent, error)
	MuteIntent(context.Context, string) (domain.StewardIntent, error)
	DeleteIntent(context.Context, string) error
	ListMemories(context.Context, int) ([]domain.StewardMemory, error)
	CreateMemory(context.Context, steward.CreateMemoryInput) (domain.StewardMemory, error)
	CorrectMemory(context.Context, string, steward.CorrectMemoryInput) (domain.StewardMemory, error)
	ArchiveMemory(context.Context, string) (domain.StewardMemory, error)
	DeleteMemory(context.Context, string) error
	ListMemoryVersions(context.Context, string) ([]domain.StewardMemoryVersion, error)
	ListKnowledgeItems(context.Context, int) ([]domain.StewardKnowledgeItem, error)
	CreateKnowledgeItem(context.Context, steward.CreateKnowledgeInput) (domain.StewardKnowledgeItem, error)
	DeleteKnowledgeItem(context.Context, string) error
	ListSourceRefs(context.Context, string, string, int) ([]domain.StewardSourceRef, error)
	CreateSourceRef(context.Context, steward.CreateSourceRefInput) (domain.StewardSourceRef, error)
	ListDataTags(context.Context) ([]domain.StewardDataTag, error)
	CreateDataTag(context.Context, steward.CreateDataTagInput) (domain.StewardDataTag, error)
	AssignDataTag(context.Context, steward.AssignTagInput) error
	Search(context.Context, steward.SearchInput) ([]domain.StewardSearchResult, error)
	ExportData(context.Context, bool) (domain.StewardOverview, error)
	ListAuditLogs(context.Context, int) ([]domain.StewardAuditLog, error)
	GetSyncStatus(context.Context) (domain.StewardSyncStatus, error)
	ListDevices(context.Context) ([]domain.StewardDevice, error)
	RegisterDevice(context.Context, steward.RegisterDeviceInput) (domain.StewardDevice, error)
	RevokeDevice(context.Context, string) (domain.StewardDevice, error)
	VerifyDeviceTrust(context.Context, string) (steward.VerifyDeviceTrustResult, error)
	SyncDevice(context.Context, string) (steward.SyncDeviceResult, error)
	ProbeDeviceSyncEntity(context.Context, string, steward.SyncEntityProbeInput) (steward.SyncEntityProbeResult, error)
	ListDevicePermissions(context.Context, string) ([]domain.StewardDevicePermission, error)
	UpdateDevicePermission(context.Context, string, string, steward.UpdateDevicePermissionInput) (domain.StewardDevicePermission, error)
	CreateSyncChange(context.Context, steward.CreateSyncChangeInput) (domain.StewardSyncChange, error)
	ListSyncConflicts(context.Context, string, int) ([]domain.StewardSyncConflict, error)
	ResolveSyncConflict(context.Context, string, steward.ResolveSyncConflictInput) (domain.StewardSyncConflict, error)
	GetAutonomyOverview(context.Context) (domain.StewardAutonomyOverview, error)
	GetModelSettings(context.Context) (steward.StewardModelSettings, error)
	UpdateModelSettings(context.Context, steward.UpdateStewardModelSettingsInput) (steward.StewardModelSettings, error)
	ProbeAutonomyAdvisor(context.Context, steward.ProbeAutonomyAdvisorInput) (steward.ProbeAutonomyAdvisorResult, error)
	GetAutonomySettings(context.Context) (domain.StewardAutonomySettings, error)
	UpdateAutonomySettings(context.Context, steward.UpdateAutonomySettingsInput) (domain.StewardAutonomySettings, error)
	ListAutonomyRules(context.Context) ([]domain.StewardAutonomyRule, error)
	UpdateAutonomyRule(context.Context, string, steward.UpdateAutonomyRuleInput) (domain.StewardAutonomyRule, error)
	RunAutonomyCycle(context.Context, int) (domain.StewardAutonomyOverview, error)
	CreateAutonomyProposal(context.Context, steward.CreateAutonomyProposalInput) (domain.StewardAutonomyProposal, error)
	ListAutonomyProposals(context.Context, string, int) ([]domain.StewardAutonomyProposal, error)
	ApproveAutonomyProposal(context.Context, string) (domain.StewardAutonomyProposal, error)
	DismissAutonomyProposal(context.Context, string) (domain.StewardAutonomyProposal, error)
	DismissAutonomyProposals(context.Context, steward.DismissAutonomyProposalsInput) (steward.DismissAutonomyProposalsResult, error)
	SimulateAutonomyProposal(context.Context, string) (domain.StewardAutonomousRun, error)
	ExecuteAutonomyProposal(context.Context, string) (domain.StewardAutonomousRun, error)
	RetryAutonomyProposal(context.Context, string) (domain.StewardAutonomousRun, error)
	ListApprovalRequests(context.Context, string, int) ([]domain.StewardApprovalRequest, error)
	ApproveRequest(context.Context, string, steward.DecideApprovalInput) (domain.StewardApprovalRequest, error)
	RejectRequest(context.Context, string, steward.DecideApprovalInput) (domain.StewardApprovalRequest, error)
	ListAutonomousRuns(context.Context, int) ([]domain.StewardAutonomousRun, error)
}

type StewardPeerStore interface {
	SignPairingChallenge(context.Context, steward.PairingChallengeInput) (steward.PairingChallengeResult, error)
	ProbeLocalSyncEntity(context.Context, steward.SyncEntityProbeInput) (steward.SyncEntityProbeResult, error)
	ListSyncChanges(context.Context, int64, int) ([]domain.StewardSyncChange, error)
	PrepareOutboundSyncChanges(*http.Request, []domain.StewardSyncChange) ([]domain.StewardSyncChange, error)
	VerifySyncRequest(*http.Request, []byte) error
	ImportSyncChanges(context.Context, steward.ImportSyncChangesInput) (steward.ImportSyncChangesResult, error)
	AcceptRemoteExecution(context.Context, steward.RemoteExecutionDispatchEnvelope, string) (steward.RemoteExecutionStatusEnvelope, error)
	GetRemoteExecutionStatus(context.Context, string, string) (steward.RemoteExecutionStatusEnvelope, error)
	CancelRemoteExecution(context.Context, string, string) (steward.RemoteExecutionStatusEnvelope, error)
	RemoteBrokerStatus(context.Context) (privilegebroker.Status, error)
}

type StewardConversationStore interface {
	CreateConversation(context.Context, steward.CreateConversationInput) (domain.StewardConversation, error)
	ListConversations(context.Context, int) ([]domain.StewardConversation, error)
	ListConversationMessages(context.Context, string, int) ([]domain.StewardConversationMessage, error)
	SendConversationMessage(context.Context, string, steward.SendConversationMessageInput) (steward.SendConversationMessageResult, error)
	DecideConversationSuggestion(context.Context, string, steward.DecideConversationSuggestionInput) (domain.StewardConversationSuggestion, error)
	DecideConversationExecution(context.Context, string, steward.DecideConversationExecutionInput) (domain.StewardConversationExecution, error)
}

type StewardActivityStore interface {
	CreateObservation(context.Context, steward.CreateObservationInput) (domain.StewardObservation, error)
	ListObservations(context.Context, int) ([]domain.StewardObservation, error)
	ListActivitySessions(context.Context, int) ([]domain.StewardActivitySession, error)
	ListEntities(context.Context, int) ([]domain.StewardEntity, error)
	ListEntityRelations(context.Context, string, int) ([]domain.StewardRelation, error)
	ListHabits(context.Context, int) ([]domain.StewardHabit, error)
	UpdateHabit(context.Context, string, steward.UpdateInferenceInput) (domain.StewardHabit, error)
	ListInsights(context.Context, int) ([]domain.StewardInsight, error)
	UpdateInsight(context.Context, string, steward.UpdateInferenceInput) (domain.StewardInsight, error)
	GetLifecycleStatus(context.Context) (domain.StewardLifecycleStatus, error)
	EvaluateLifecycle(context.Context, steward.EvaluateLifecycleInput) (domain.StewardLifecycleEvaluation, error)
	PurgeLifecycle(context.Context, steward.PurgeLifecycleInput) (domain.StewardPurgeResult, error)
	ListRetentionPolicies(context.Context) ([]domain.StewardRetentionPolicy, error)
	UpdateRetentionPolicy(context.Context, string, steward.UpdateRetentionPolicyInput) (domain.StewardRetentionPolicy, error)
}

type StewardAutomationPolicyStore interface {
	ListDataPolicies(context.Context) ([]domain.StewardDataPolicy, error)
	UpsertDataPolicy(context.Context, steward.UpsertDataPolicyInput) (domain.StewardDataPolicy, error)
	ListPermissionPolicies(context.Context) ([]domain.StewardPermissionPolicy, error)
	UpsertPermissionPolicy(context.Context, steward.UpsertPermissionPolicyInput) (domain.StewardPermissionPolicy, error)
	ListModelDispatches(context.Context, int) ([]domain.StewardModelDispatch, error)
	RunModelDispatches(context.Context, int) ([]domain.StewardModelDispatch, error)
	ListToolDefinitions(context.Context) ([]domain.StewardToolDefinition, error)
	UpsertToolDefinition(context.Context, steward.UpsertToolDefinitionInput) (domain.StewardToolDefinition, error)
}

type StewardRuntimeStore interface {
	ListRuntimeToolSpecs(context.Context) ([]domain.StewardToolSpec, error)
	GetRuntimePlannerStatus() domain.StewardRuntimePlannerStatus
	ListAgentRuns(context.Context, string, int) ([]domain.StewardAgentRunSummary, error)
	PlanAgentRun(context.Context, steward.PlanAgentRunInput) (domain.StewardAgentRun, error)
	CreateAgentRun(context.Context, steward.CreateAgentRunInput) (domain.StewardAgentRun, error)
	GetAgentRun(context.Context, string) (domain.StewardAgentRun, error)
	GetEvidenceArtifact(context.Context, string, string) (domain.StewardEvidenceArtifact, error)
	ListAgentRunEvents(context.Context, string, int64, int) ([]domain.StewardRunEvent, error)
	StartAgentRun(context.Context, string) (domain.StewardAgentRun, error)
	CancelAgentRun(context.Context, string) (domain.StewardAgentRun, error)
	ResumeAgentRun(context.Context, string) (domain.StewardAgentRun, error)
	ApproveAgentRun(context.Context, string, steward.ApproveAgentRunInput) (domain.StewardAgentRun, error)
	GetRuntimeExecutionControl(context.Context) (domain.StewardRuntimeExecutionControl, error)
	PauseRuntimeExecution(context.Context, steward.SetRuntimeExecutionControlInput) (domain.StewardRuntimeExecutionControl, error)
	ResumeRuntimeExecution(context.Context, steward.SetRuntimeExecutionControlInput) (domain.StewardRuntimeExecutionControl, error)
}

type StewardOrchestrationStore interface {
	ListOrchestrationAgents(context.Context) ([]domain.StewardOrchestrationAgent, error)
	UpsertOrchestrationAgent(context.Context, steward.UpsertOrchestrationAgentInput) (domain.StewardOrchestrationAgent, error)
	ListOrchestrations(context.Context, string, int) ([]domain.StewardOrchestration, error)
	CreateOrchestration(context.Context, steward.CreateOrchestrationInput) (domain.StewardOrchestration, error)
	GetOrchestration(context.Context, string) (domain.StewardOrchestration, error)
	StartOrchestration(context.Context, string) (domain.StewardOrchestration, error)
	CancelOrchestration(context.Context, string) (domain.StewardOrchestration, error)
	PreviewRemotePrivilegeNode(context.Context, string, string) (steward.RemotePrivilegePreview, error)
	ApproveRemotePrivilegeNode(context.Context, string, string, steward.ApproveRemotePrivilegeInput) (domain.StewardOrchestration, error)
}

type Dependencies struct {
	Config         config.Config
	FileService    *filemeta.Service
	JobService     *jobs.Service
	MemoService    MemoStore
	PresetService  *presets.Service
	StewardService StewardStore
	WatchSync      *watchsync.Hub
	Readiness      func(context.Context) (map[string]string, error)
}

type PeerDependencies struct {
	StewardService StewardPeerStore
	Readiness      func(context.Context) (map[string]string, error)
}

const maxPeerRequestBodyBytes int64 = 16 << 20

// RegisterRoutes is retained as the management-router entry point for callers
// that do not need to name the listener role explicitly.
func RegisterRoutes(router chi.Router, deps Dependencies) {
	RegisterManagementRoutes(router, deps)
}

func RegisterManagementRoutes(router chi.Router, deps Dependencies) {
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
		r.Get("/watch/rooms/{roomID}/ws", handler.watchRoomWebSocket)
		r.Get("/steward/overview", handler.getStewardOverview)
		r.Get("/steward/agent", handler.getStewardAgent)
		r.Post("/steward/agent/start", handler.startStewardAgent)
		r.Post("/steward/agent/stop", handler.stopStewardAgent)
		r.Get("/steward/collectors", handler.listStewardCollectors)
		r.Patch("/steward/collectors/{name}", handler.updateStewardCollector)
		r.Get("/steward/automation/data-policies", handler.listStewardDataPolicies)
		r.Put("/steward/automation/data-policies", handler.upsertStewardDataPolicy)
		r.Get("/steward/automation/permission-policies", handler.listStewardPermissionPolicies)
		r.Put("/steward/automation/permission-policies", handler.upsertStewardPermissionPolicy)
		r.Get("/steward/automation/model-dispatches", handler.listStewardModelDispatches)
		r.Post("/steward/automation/model-dispatches/run", handler.runStewardModelDispatches)
		r.Get("/steward/automation/tools", handler.listStewardToolDefinitions)
		r.Put("/steward/automation/tools", handler.upsertStewardToolDefinition)
		r.Get("/steward/runtime/tools", handler.listStewardRuntimeTools)
		r.Get("/steward/runtime/planner", handler.getStewardRuntimePlanner)
		r.Get("/steward/runtime/control", handler.getStewardRuntimeControl)
		r.Post("/steward/runtime/control/pause", handler.pauseStewardRuntime)
		r.Post("/steward/runtime/control/resume", handler.resumeStewardRuntime)
		r.Get("/steward/execution/control", handler.getStewardRuntimeControl)
		r.Post("/steward/execution/control/stop", handler.pauseStewardRuntime)
		r.Post("/steward/execution/control/resume", handler.resumeStewardRuntime)
		r.Post("/steward/runs/plan", handler.planStewardAgentRun)
		r.Get("/steward/runs", handler.listStewardAgentRuns)
		r.Post("/steward/runs", handler.createStewardAgentRun)
		r.Get("/steward/runs/{id}", handler.getStewardAgentRun)
		r.Get("/steward/runs/{id}/evidence/{evidenceID}", handler.getStewardAgentRunEvidence)
		r.Get("/steward/runs/{id}/events", handler.streamStewardAgentRunEvents)
		r.Post("/steward/runs/{id}/start", handler.startStewardAgentRun)
		r.Post("/steward/runs/{id}/cancel", handler.cancelStewardAgentRun)
		r.Post("/steward/runs/{id}/resume", handler.resumeStewardAgentRun)
		r.Post("/steward/runs/{id}/approve", handler.approveStewardAgentRun)
		r.Get("/steward/orchestration/agents", handler.listStewardOrchestrationAgents)
		r.Put("/steward/orchestration/agents", handler.upsertStewardOrchestrationAgent)
		r.Get("/steward/orchestrations", handler.listStewardOrchestrations)
		r.Post("/steward/orchestrations", handler.createStewardOrchestration)
		r.Get("/steward/orchestrations/{id}", handler.getStewardOrchestration)
		r.Post("/steward/orchestrations/{id}/start", handler.startStewardOrchestration)
		r.Post("/steward/orchestrations/{id}/cancel", handler.cancelStewardOrchestration)
		r.Post("/steward/orchestrations/{id}/nodes/{nodeID}/remote-privilege/preview", handler.previewStewardRemotePrivilege)
		r.Post("/steward/orchestrations/{id}/nodes/{nodeID}/remote-privilege/approve", handler.approveStewardRemotePrivilege)
		r.Get("/steward/conversations", handler.listStewardConversations)
		r.Post("/steward/conversations", handler.createStewardConversation)
		r.Get("/steward/conversations/{id}/messages", handler.listStewardConversationMessages)
		r.Post("/steward/conversations/{id}/messages", handler.sendStewardConversationMessage)
		r.Post("/steward/conversation-suggestions/{id}/decision", handler.decideStewardConversationSuggestion)
		r.Post("/steward/conversation-executions/{id}/decision", handler.decideStewardConversationExecution)
		r.Get("/steward/model-settings", handler.getStewardModelSettings)
		r.Patch("/steward/model-settings", handler.updateStewardModelSettings)
		r.Get("/steward/activity/observations", handler.listStewardObservations)
		r.Post("/steward/activity/observations", handler.createStewardObservation)
		r.Get("/steward/activity/sessions", handler.listStewardActivitySessions)
		r.Get("/steward/activity/timeline", handler.listStewardTimelineSegments)
		r.Get("/steward/entities", handler.listStewardEntities)
		r.Get("/steward/entities/{id}/relations", handler.listStewardEntityRelations)
		r.Get("/steward/habits", handler.listStewardHabits)
		r.Patch("/steward/habits/{id}", handler.updateStewardHabit)
		r.Get("/steward/insights", handler.listStewardInsights)
		r.Patch("/steward/insights/{id}", handler.updateStewardInsight)
		r.Get("/steward/lifecycle/status", handler.getStewardLifecycleStatus)
		r.Post("/steward/lifecycle/evaluate", handler.evaluateStewardLifecycle)
		r.Post("/steward/lifecycle/purge", handler.purgeStewardLifecycle)
		r.Get("/steward/retention-policies", handler.listStewardRetentionPolicies)
		r.Patch("/steward/retention-policies/{id}", handler.updateStewardRetentionPolicy)
		r.Get("/steward/events", handler.listStewardEvents)
		r.Post("/steward/events", handler.createStewardEvent)
		r.Delete("/steward/events/{id}", handler.deleteStewardEvent)
		r.Patch("/steward/events/{id}/hide", handler.hideStewardEvent)
		r.Post("/steward/events/{id}/convert", handler.convertStewardEvent)
		r.Get("/steward/timeline-segments", handler.listStewardTimelineSegments)
		r.Delete("/steward/timeline-segments/{id}", handler.deleteStewardTimelineSegment)
		r.Get("/steward/tasks", handler.listStewardTasks)
		r.Post("/steward/tasks", handler.createStewardTask)
		r.Patch("/steward/tasks/{id}", handler.updateStewardTask)
		r.Post("/steward/tasks/{id}/complete", handler.completeStewardTask)
		r.Post("/steward/tasks/{id}/cancel", handler.cancelStewardTask)
		r.Delete("/steward/tasks/{id}", handler.deleteStewardTask)
		r.Get("/steward/intents", handler.listStewardIntents)
		r.Post("/steward/intents", handler.createStewardIntent)
		r.Post("/steward/intents/{id}/accept", handler.acceptStewardIntent)
		r.Post("/steward/intents/{id}/dismiss", handler.dismissStewardIntent)
		r.Post("/steward/intents/{id}/mute", handler.muteStewardIntent)
		r.Delete("/steward/intents/{id}", handler.deleteStewardIntent)
		r.Get("/steward/memories", handler.listStewardMemories)
		r.Post("/steward/memories", handler.createStewardMemory)
		r.Post("/steward/memories/{id}/correct", handler.correctStewardMemory)
		r.Post("/steward/memories/{id}/archive", handler.archiveStewardMemory)
		r.Delete("/steward/memories/{id}", handler.deleteStewardMemory)
		r.Get("/steward/memories/{id}/versions", handler.listStewardMemoryVersions)
		r.Get("/steward/knowledge-items", handler.listStewardKnowledgeItems)
		r.Post("/steward/knowledge-items", handler.createStewardKnowledgeItem)
		r.Delete("/steward/knowledge-items/{id}", handler.deleteStewardKnowledgeItem)
		r.Get("/steward/source-refs", handler.listStewardSourceRefs)
		r.Post("/steward/source-refs", handler.createStewardSourceRef)
		r.Get("/steward/tags", handler.listStewardTags)
		r.Post("/steward/tags", handler.createStewardTag)
		r.Post("/steward/tags/assign", handler.assignStewardTag)
		r.Get("/steward/search", handler.searchStewardData)
		r.Get("/steward/audit-logs", handler.listStewardAuditLogs)
		r.Get("/steward/export", handler.exportStewardSummary)
		r.Get("/steward/sync/status", handler.getStewardSyncStatus)
		r.Post("/steward/sync/changes", handler.createStewardSyncChange)
		r.Get("/steward/sync/conflicts", handler.listStewardSyncConflicts)
		r.Post("/steward/sync/conflicts/{id}/resolve", handler.resolveStewardSyncConflict)
		r.Get("/steward/devices", handler.listStewardDevices)
		r.Post("/steward/devices", handler.registerStewardDevice)
		r.Post("/steward/devices/{id}/verify", handler.verifyStewardDeviceTrust)
		r.Post("/steward/devices/{id}/sync", handler.syncStewardDevice)
		r.Post("/steward/devices/{id}/probe", handler.probeStewardDeviceSyncEntity)
		r.Post("/steward/devices/{id}/revoke", handler.revokeStewardDevice)
		r.Get("/steward/devices/{id}/permissions", handler.listStewardDevicePermissions)
		r.Put("/steward/devices/{id}/permissions/{capability}", handler.updateStewardDevicePermission)
		r.Get("/steward/autonomy", handler.getStewardAutonomy)
		r.Post("/steward/autonomy/advisor/probe", handler.probeStewardAutonomyAdvisor)
		r.Get("/steward/autonomy/settings", handler.getStewardAutonomySettings)
		r.Patch("/steward/autonomy/settings", handler.updateStewardAutonomySettings)
		r.Post("/steward/autonomy/run", handler.runStewardAutonomyCycle)
		r.Get("/steward/autonomy/rules", handler.listStewardAutonomyRules)
		r.Patch("/steward/autonomy/rules/{id}", handler.updateStewardAutonomyRule)
		r.Get("/steward/autonomy/proposals", handler.listStewardAutonomyProposals)
		r.Post("/steward/autonomy/proposals", handler.createStewardAutonomyProposal)
		r.Post("/steward/autonomy/proposals/bulk-dismiss", handler.dismissStewardAutonomyProposals)
		r.Post("/steward/autonomy/proposals/{id}/approve", handler.approveStewardAutonomyProposal)
		r.Post("/steward/autonomy/proposals/{id}/dismiss", handler.dismissStewardAutonomyProposal)
		r.Post("/steward/autonomy/proposals/{id}/simulate", handler.simulateStewardAutonomyProposal)
		r.Post("/steward/autonomy/proposals/{id}/execute", handler.executeStewardAutonomyProposal)
		r.Post("/steward/autonomy/proposals/{id}/retry", handler.retryStewardAutonomyProposal)
		r.Get("/steward/autonomy/approvals", handler.listStewardApprovalRequests)
		r.Post("/steward/autonomy/approvals/{id}/approve", handler.approveStewardApprovalRequest)
		r.Post("/steward/autonomy/approvals/{id}/reject", handler.rejectStewardApprovalRequest)
		r.Get("/steward/autonomy/runs", handler.listStewardAutonomousRuns)
	})
}

// RegisterPeerRoutes exposes only the protocol surface required by trusted
// devices. It deliberately excludes tasks, memories, autonomy, permissions,
// device revocation, and every other management operation.
func RegisterPeerRoutes(router chi.Router, deps PeerDependencies) {
	handler := &Handler{deps: Dependencies{
		Readiness: deps.Readiness,
	}, peerService: deps.StewardService}
	router.Use(limitPeerRequestBody)

	router.Get("/healthz", handler.healthz)
	router.Get("/readyz", handler.readyz)
	router.Route("/api/steward", func(r chi.Router) {
		r.Get("/sync/changes", handler.listStewardSyncChanges)
		r.Post("/sync/changes/import", handler.importStewardSyncChanges)
		r.Get("/sync/probe", handler.probeLocalStewardSyncEntity)
		r.Post("/pairing/challenge", handler.signStewardPairingChallenge)
		r.Post("/remote-execution/dispatches", handler.acceptStewardRemoteExecution)
		r.Get("/remote-execution/dispatches/{id}", handler.getStewardRemoteExecutionStatus)
		r.Post("/remote-execution/dispatches/{id}/cancel", handler.cancelStewardRemoteExecution)
		r.Get("/broker-federation/status", handler.getStewardRemoteBrokerStatus)
	})
}

func limitPeerRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body == nil || r.Method == http.MethodGet || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		if r.ContentLength > maxPeerRequestBodyBytes {
			httpError(w, http.StatusRequestEntityTooLarge, "peer request body exceeds 16 MiB")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, maxPeerRequestBodyBytes+1))
		if err != nil {
			httpError(w, http.StatusBadRequest, "invalid peer request body")
			return
		}
		if int64(len(body)) > maxPeerRequestBodyBytes {
			httpError(w, http.StatusRequestEntityTooLarge, "peer request body exceeds 16 MiB")
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		next.ServeHTTP(w, r)
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
