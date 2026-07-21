export type ToolStatusKind = "idle" | "success" | "warning" | "error";

export type ToolStatus = {
  kind: ToolStatusKind;
  message: string;
};

export type JsonNode =
  | { type: "object"; entries: Array<{ key: string; value: JsonNode }> }
  | { type: "array"; items: JsonNode[] }
  | { type: "string"; value: string }
  | { type: "number"; value: string }
  | { type: "literal"; value: string }
  | { type: "mongo"; func: string; args: string | null };

export type JsonFormatResult = {
  formatted: string;
  ast: JsonNode;
  lineCount: number;
  charCount: number;
  maxDepth: number;
};

export type JsonFormatError = {
  error: string;
  position?: number;
};

export type JsonFormatResponse = JsonFormatResult | JsonFormatError;

export type FormatMeta = {
  text: string;
  error: string | null;
  lineCount: number;
  charCount: number;
  maxDepth?: number;
  ast: JsonNode | null;
  keyLineMap: Record<string, number>;
};

export type TableSchemaColumn = {
  path: string;
  dominantType: string;
  isMixed: boolean;
  typeCounts: Record<string, number>;
  nullCount: number;
  totalCount: number;
  nullRatio: number;
};

export type TableValidation = {
  level: "ok" | "warn" | "err";
  msg: string;
};

export type TableData = {
  schema: TableSchemaColumn[];
  rows: Array<Array<JsonNode | null>>;
  validation: TableValidation[];
  docCount: number;
};

export type ShellArg = {
  text: string;
  start: number;
  end: number;
};

export type ShellMethod = {
  name: string;
  nameStart: number;
  nameEnd: number;
  openParen: number;
  closeParen: number;
  argsRaw: ShellArg[];
};

export type ShellStatement = {
  collection: string;
  collectionStart: number;
  collectionEnd: number;
  methods: ShellMethod[];
  operators: Array<{ name: string; pos: number }>;
};

export type ShellValidation = {
  level: "ok" | "warn" | "err";
  msg: string;
};

export type DiffSummary = {
  leftOnly: string[];
  rightOnly: string[];
  changed: string[];
};

export type InspectInputKind =
  | "standard-json"
  | "mongo-json"
  | "escaped-json-string"
  | "mongo-shell"
  | "curl"
  | "log-json-fragment"
  | "ndjson"
  | "unknown";

export type InspectSuggestedActionId =
  "format" | "repair" | "unescape" | "diff" | "table" | "shell" | "extract";

export type InspectSuggestedAction = {
  id: InspectSuggestedActionId;
  label: string;
  description: string;
  targetPath?: string;
};

export type InspectIssue = {
  level: "info" | "warn" | "error";
  message: string;
};

export type InspectResult = {
  kind: InspectInputKind;
  confidence: number;
  extractedText: string;
  issues: InspectIssue[];
  suggestedActions: InspectSuggestedAction[];
};

export type SemanticDiffOptions = {
  ignorePaths?: string[];
  arrayMatchKey?: string;
};

export type SemanticDiffChange = {
  path: string;
  leftType?: string;
  rightType?: string;
  leftValue?: string;
  rightValue?: string;
  message: string;
};

export type JsonPatchOperation =
  | { op: "add"; path: string; value: unknown }
  | { op: "remove"; path: string }
  | { op: "replace"; path: string; value: unknown };

export type SemanticDiffResult = {
  added: SemanticDiffChange[];
  removed: SemanticDiffChange[];
  typeChanged: SemanticDiffChange[];
  valueChanged: SemanticDiffChange[];
  patch: JsonPatchOperation[];
};

export type SchemaProfileField = {
  path: string;
  dominantType: string;
  optional: boolean;
  nullRatio: number;
  presenceRatio: number;
  isMixed: boolean;
  typeCounts: Record<string, number>;
  examples: string[];
  risks: string[];
};

export type SchemaProfile = {
  docCount: number;
  fieldCount: number;
  nullableFieldCount: number;
  mixedFieldCount: number;
  riskFieldCount: number;
  fields: SchemaProfileField[];
};

export type GeneratedSchemaTarget = "typescript" | "zod" | "go";

export type GeneratedSchema = {
  target: GeneratedSchemaTarget;
  code: string;
};

export type MongoQueryRisk = {
  level: "info" | "warn" | "danger";
  code: string;
  message: string;
  method?: string;
};

export type PipelineStageSummary = {
  index: number;
  operator: string;
  title: string;
  description: string;
  fieldHints: string[];
  risks: string[];
  raw: string;
};

export type PipelineInspectionResult = {
  collection: string;
  methodChain: string[];
  risks: MongoQueryRisk[];
  stages: PipelineStageSummary[];
};

export type ChartSeriesRow = Record<string, string | number | null>;

export type MusicTrackSource = "remote" | "local";

export type PlaybackMode = "order" | "repeat-all" | "repeat-one" | "shuffle";

export type MusicAudioQuality = {
  container?: string;
  codec?: string;
  bitrate?: number;
  sampleRate?: number;
  bitsPerSample?: number;
  numberOfChannels?: number;
  lossless?: boolean;
  duration?: number;
  fileSize?: number;
  analyzedAt: string;
  analysisSource: "metadata" | "inferred";
  error?: string;
};

export type MusicTrack = {
  id: string;
  source: MusicTrackSource;
  title: string;
  artist?: string;
  note?: string;
  remoteUrl?: string;
  localHandleId?: string;
  folderHandleId?: string;
  relativePath?: string;
  lyricHandleId?: string;
  lyricFileName?: string;
  lyricRelativePath?: string;
  fileName?: string;
  mimeType?: string;
  duration?: number;
  audioQuality?: MusicAudioQuality;
  addedAt: string;
};

export type MusicLibraryFolder = {
  id: string;
  name: string;
  addedAt: string;
  lastScannedAt?: string;
  trackCount?: number;
};

export type MusicLibraryState = {
  tracks: MusicTrack[];
  folders: MusicLibraryFolder[];
  queue: string[];
  currentTrackId?: string;
  volume: number;
  mode: PlaybackMode;
};

export type JobStatus =
  "pending" | "running" | "success" | "failed" | "expired";

export type JobSummary = {
  id: string;
  tool_type: string;
  status: JobStatus;
  input_file_id?: string | null;
  output_file_id?: string | null;
  params?: Record<string, unknown>;
  error_message?: string | null;
  created_at: string;
  finished_at?: string | null;
  expires_at?: string | null;
};

export type FileSummary = {
  id: string;
  original_name: string;
  stored_name: string;
  mime_type: string;
  size_bytes: number;
  category: string;
  expires_at?: string | null;
  created_at: string;
};

export type PresetRecord = {
  id: string;
  tool_type: string;
  name: string;
  payload: Record<string, unknown>;
  created_at: string;
  updated_at: string;
};

export type MemoRecord = {
  id: string;
  slug: string;
  title: string;
  content_html: string;
  content_text: string;
  floating_cards: MemoFloatingCardRecord[];
  created_at: string;
  updated_at: string;
};

export type MemoFloatingCardRecord = {
  id: string;
  content: string;
  color: string;
  created_at: string;
  updated_at: string;
};

export type StewardRuntimePlannerStatus = {
  enabled: boolean;
  provider: string;
  model?: string;
  reason?: string;
  version: string;
};

export type StewardRuntimeControlEvent = {
  sequence: number;
  action: "paused" | "stopped" | "resumed";
  reason?: string;
  changed_by: string;
  created_at: string;
};

export type StewardRuntimeExecutionControl = {
  paused: boolean;
  stopped: boolean;
  generation: number;
  scopes: string[];
  draining: boolean;
  reason?: string;
  changed_by: string;
  changed_at: string;
  watchdog: {
    enabled: boolean;
    lease_ttl_seconds: number;
    active_invocations: number;
    stale_invocations: number;
  };
  broker: {
    configured: boolean;
    reachable: boolean;
    stopped: boolean;
    generation: number;
    instance_id?: string;
    policy_digest?: string;
    key_id?: string;
    capability_count: number;
    active_executions: number;
    approval_proof_required: boolean;
    approval_authorities: Array<{
      name: string;
      algorithm: string;
      key_id: string;
	  credential_id?: string;
	  rp_id?: string;
	  allowed_origins?: string[];
    }>;
    error?: string;
    capabilities: Array<{
      name: string;
      description: string;
      permission_level: string;
      risk_level: string;
      executable_name: string;
      argument_count: number;
      timeout_seconds: number;
      capability_digest: string;
    }>;
  };
  events: StewardRuntimeControlEvent[];
};

export type StewardRuntimeRunStatus =
  | "draft"
  | "planning"
  | "awaiting_approval"
  | "queued"
  | "running"
  | "verifying"
  | "succeeded"
  | "failed"
  | "cancelled"
  | "compensating"
  | "blocked";

export type StewardAgentRunSummary = {
  id: string;
  goal: string;
  status: StewardRuntimeRunStatus;
  mode: string;
  plan_hash: string;
  planner: string;
  permission_ceiling: string;
  data_level: string;
  step_count: number;
  completed_steps: number;
  requires_approval: boolean;
  failure_summary?: string;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
};

export type StewardToolInvocation = {
  id: string;
  run_id: string;
  step_id: string;
  tool_name: string;
  tool_version: string;
  attempt: number;
  idempotency_key: string;
  status: string;
  input: Record<string, unknown>;
  output?: Record<string, unknown>;
  error_summary?: string;
  lease_owner?: string;
  control_generation: number;
  started_at: string;
  finished_at?: string | null;
  heartbeat_at?: string | null;
  lease_expires_at?: string | null;
};

export type StewardEvidenceArtifact = {
  id: string;
  run_id: string;
  step_id: string;
  kind: string;
  summary: string;
  data_level: string;
  content_type: string;
  payload_state: 'inline' | 'encrypted' | 'summary_only';
  payload_available: boolean;
  size_bytes: number;
  sha256: string;
  redacted: boolean;
  payload?: Record<string, unknown>;
  created_at: string;
};

export type StewardApprovalGrant = {
  id: string;
  run_id: string;
  plan_hash: string;
  scope: string;
  granted_by: string;
  status: string;
  reason?: string;
  created_at: string;
  expires_at?: string | null;
  revoked_at?: string | null;
  approval_proof_id?: string;
  approval_key_id?: string;
  approval_proof_expires_at?: string | null;
};

export type StewardSignedApprovalProof = {
  claims: {
    version: string;
    proof_id: string;
    subject: string;
    plan_hash: string;
    capability: string;
    control_generation: number;
    granted_by: string;
    reason: string;
    issued_at: string;
    expires_at: string;
  };
  key_id: string;
	algorithm?: "ed25519" | "webauthn-es256";
	signature?: string;
	webauthn?: {
	  credential_id: string;
	  client_data_json: string;
	  authenticator_data: string;
	  signature: string;
	};
};

export type StewardRunStep = {
  id: string;
  run_id: string;
  key: string;
  position: number;
  title: string;
  tool_name: string;
  tool_version: string;
  arguments: Record<string, unknown>;
  expected_output?: Record<string, unknown>;
  depends_on: string[];
  status: string;
  attempt: number;
  max_attempts: number;
  timeout_seconds: number;
  idempotency_key: string;
  tool_idempotency: string;
  policy_decision: string;
  policy_reason: string;
  requires_approval: boolean;
  last_error?: string;
  invocations: StewardToolInvocation[];
  evidence: StewardEvidenceArtifact[];
  created_at: string;
  updated_at: string;
  started_at?: string | null;
  completed_at?: string | null;
};

export type StewardAgentRun = {
  id: string;
  goal: string;
  status: StewardRuntimeRunStatus;
  mode: string;
  plan_version: number;
  plan_hash: string;
  idempotency_key?: string;
  requested_by: string;
  target_device: string;
  data_level: string;
  permission_ceiling: string;
  planner: string;
  planner_version: string;
  source_instruction?: string;
  plan_summary?: string;
  policy_summary: Record<string, unknown>;
  cancel_requested: boolean;
  failure_summary?: string;
  steps: StewardRunStep[];
  approvals: StewardApprovalGrant[];
  created_at: string;
  updated_at: string;
  started_at?: string | null;
  completed_at?: string | null;
};

export type StewardRunEvent = {
  sequence: number;
  id: string;
  run_id: string;
  step_id?: string;
  type: string;
  status: string;
  message: string;
  payload: Record<string, unknown>;
  created_at: string;
};

export type StewardAgentStatus = {
  agent_id: string;
  device_name: string;
  platform: string;
  status:
    "stopped" | "starting" | "running" | "degraded" | "stopping" | "error";
  version: string;
  enabled_collectors: string[];
  started_at?: string | null;
  last_heartbeat_at?: string | null;
  last_error?: string | null;
  background_loops: StewardBackgroundLoopStatus[];
  updated_at: string;
};

export type StewardBackgroundLoopStatus = {
  name: string;
  enabled: boolean;
  running: boolean;
  interval: string;
  last_started_at?: string | null;
  last_completed_at?: string | null;
  last_success_at?: string | null;
  last_error?: string | null;
  consecutive_failures: number;
  updated_at: string;
};

export type StewardCollectorConfig = {
  id: string;
  name: string;
  enabled: boolean;
  scope_summary: string;
  settings: Record<string, unknown>;
  execution_target: 'main' | 'companion' | 'system' | 'auto' | string;
  user_overridden: boolean;
  last_run_at?: string | null;
  last_error?: string | null;
  created_at: string;
  updated_at: string;
  audit_id?: string | null;
};

export type StewardDataPolicy = {
  id: string;
  data_level: string;
  source_pattern: string;
  collect_mode: "deny" | "manual" | "auto";
  model_mode: "deny" | "manual" | "auto";
  model_content_mode: "metadata" | "summary" | "redacted" | "raw";
  allow_local_persistence: boolean;
  allow_sync: boolean;
  require_encryption: boolean;
  consent_expires_at?: string | null;
  description: string;
  created_at: string;
  updated_at: string;
};

export type StewardPermissionPolicy = {
  id: string;
  permission_level: string;
  action_pattern: string;
  execution_mode: "deny" | "manual" | "auto";
  require_simulation: boolean;
  require_rollback: boolean;
  max_batch_size: number;
  cooldown_seconds: number;
  description: string;
  created_at: string;
  updated_at: string;
};

export type StewardModelDispatch = {
  id: string;
  observation_id: string;
  observation_time: string;
  source: string;
  data_level: string;
  content_mode: string;
  status: string;
  attempts: number;
  request_summary: string;
  response_summary: string;
  last_error?: string;
  next_attempt_at?: string | null;
  provider: string;
  model: string;
  audit_id?: string | null;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
};

export type StewardProactiveRun = {
  id: string;
  cadence: "daily" | "weekly" | string;
  period_key: string;
  period_start: string;
  period_end: string;
  status: "processing" | "silent" | "message" | "execution" | "blocked" | "failed" | string;
  summary: string;
  analysis: Record<string, unknown>;
  decision: string;
  conversation_id?: string;
  message_id?: string;
  execution_id?: string;
  provider: string;
  model: string;
  error_summary?: string;
  audit_id?: string;
  created_at: string;
  updated_at: string;
  completed_at?: string;
};

export type StewardToolDefinition = {
  id: string;
  action: string;
  name: string;
  description: string;
  executable: string;
  arguments: string[];
  working_directory: string;
  permission_level: string;
  risk_level: string;
  enabled: boolean;
  timeout_seconds: number;
  rollback_executable: string;
  rollback_arguments: string[];
  created_at: string;
  updated_at: string;
};

export type StewardConversation = {
  id: string;
  title: string;
  status: string;
  message_count: number;
  last_message_at?: string;
  archived_at?: string;
  created_at: string;
  updated_at: string;
};

export type StewardConversationSuggestion = {
  id: string;
  message_id: string;
  kind: "intent" | "memory" | "task";
  title: string;
  summary: string;
  content: string;
  suggested_action: string;
  risk_level: string;
  status: string;
  target_id?: string;
  created_at: string;
  updated_at: string;
};

export type StewardConversationMessage = {
  id: string;
  conversation_id: string;
  role: "user" | "assistant";
  content: string;
  model?: string;
  context_summary?: string;
  suggestions: StewardConversationSuggestion[];
  executions: StewardConversationExecution[];
  episodes: StewardAgentEpisode[];
  created_at: string;
};

export type StewardToolVersion = {
  tool_name: string;
  version: string;
  runtime: 'builtin' | 'powershell' | 'python' | 'node' | 'composite' | string;
  status: string;
  manifest: Record<string, unknown>;
  package_path?: string;
  content_sha256: string;
  sbom: Record<string, unknown>;
  provenance: Record<string, unknown>;
  validation_summary?: string;
  created_at: string;
  validated_at?: string;
};

export type StewardToolTestRun = {
  id: string;
  tool_name: string;
  tool_version: string;
  test_name: string;
  status: string;
  input: Record<string, unknown>;
  output: Record<string, unknown>;
  error_summary?: string;
  evidence: Record<string, unknown>[];
  started_at: string;
  completed_at?: string;
};

export type StewardCatalogTool = {
  name: string;
  title: string;
  description: string;
  origin: string;
  enabled: boolean;
  active_version: string;
  execution_target: 'system' | 'session' | 'auto' | string;
  health_status: string;
  health_summary?: string;
  catalog_generation: number;
  invocation_count: number;
  created_by_episode_id?: string;
  created_by_model?: string;
  active?: StewardToolVersion;
  versions?: StewardToolVersion[];
  recent_tests?: StewardToolTestRun[];
  dependency_changes?: Array<Record<string, unknown>>;
  created_at: string;
  updated_at: string;
};

export type StewardToolHostStatus = {
  name: string;
  target: string;
  transport: string;
  online: boolean;
  summary?: string;
  checked_at: string;
};

export type StewardAgentToolCall = {
  id: string;
  tool_name: string;
  arguments: Record<string, unknown>;
  target_device_id?: string;
};

export type StewardAgentToolResult = {
  tool_call_id: string;
  tool_name: string;
  output?: Record<string, unknown>;
  error?: string;
  evidence?: Record<string, unknown>;
};

export type StewardAgentTurn = {
  id: string;
  episode_id: string;
  round_index: number;
  status: string;
  assistant_content?: string;
  tool_calls: StewardAgentToolCall[];
  tool_results: StewardAgentToolResult[];
  execution_id?: string;
  failure_summary?: string;
};

export type StewardAgentWorkingState = {
  summary?: string;
  anchors?: string[];
  pending_items?: string[];
  evidence_references?: string[];
  completed_rounds?: number;
};

export type StewardAgentTurnPage = {
  turns: StewardAgentTurn[];
  next_before_round?: number;
  has_more: boolean;
  total: number;
};

export type StewardAgentEpisode = {
  id: string;
  conversation_id: string;
  trigger_message_id: string;
  progress_message_id?: string;
  final_message_id?: string;
  trigger_kind: string;
  goal: string;
  status: 'thinking' | 'executing' | 'awaiting_input' | 'paused' | 'completed' | 'failed' | 'cancelled' | 'blocked';
  current_round: number;
  tool_call_count: number;
  max_rounds: number;
  max_tool_calls: number;
  max_duration_seconds: number;
  no_progress_limit: number;
  no_progress_count: number;
  model_failure_count?: number;
  target_device_id?: string;
  active_execution_id?: string;
  failure_summary?: string;
  last_result_summary?: string;
  working_state?: StewardAgentWorkingState;
  summary_through_round?: number;
  turn_count?: number;
  turns_has_more?: boolean;
  turns?: StewardAgentTurn[];
  created_at: string;
  updated_at: string;
  deadline_at?: string;
  completed_at?: string;
};

export type StewardConversationExecution = {
  id: string;
  conversation_id: string;
  message_id: string;
  request_message_id: string;
  instruction: string;
  summary: string;
  kind: "run" | "orchestration" | "question";
  status: "needs_input" | "awaiting_confirmation" | "queued" | "running" | "paused" | "succeeded" | "failed" | "cancelled" | "blocked";
  run_id?: string;
  orchestration_id?: string;
  target_device_id: string;
  target_device_name: string;
  risk_level: string;
  plan_hash: string;
  requires_confirmation: boolean;
  confirmation_reason?: string;
  question?: string;
  capability?: string;
  approval_subject?: string;
  control_generation?: number;
  episode_id?: string;
  turn_id?: string;
  round_index?: number;
  evidence: {
    child_run_count?: number;
    artifact_count?: number;
    redacted_count?: number;
    manifest_sha256?: string;
  };
  failure_summary?: string;
  created_at: string;
  updated_at: string;
  confirmed_at?: string;
  completed_at?: string;
};

export type StewardEvent = {
  id: string;
  type: string;
  title: string;
  summary: string;
  source: string;
  data_level: string;
  permission_level: string;
  status: string;
  device_id: string;
  user_confirmed: boolean;
  version: number;
  audit_id?: string | null;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
};

export type StewardTask = {
  id: string;
  type: string;
  title: string;
  description: string;
  status: "open" | "in_progress" | "waiting" | "done" | "canceled" | "archived";
  priority: "low" | "normal" | "high";
  due_at?: string | null;
  source: string;
  data_level: string;
  permission_level: string;
  device_id: string;
  risk_level: string;
  user_confirmed: boolean;
  version: number;
  audit_id?: string | null;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
  completed_at?: string | null;
  canceled_at?: string | null;
};

export type StewardAuditLog = {
  id: string;
  occurred_at: string;
  actor: string;
  action: string;
  target_type: string;
  target_id?: string | null;
  source: string;
  permission_level: string;
  data_level: string;
  input_summary: string;
  output_summary: string;
  before_summary: string;
  after_summary: string;
  reason: string;
  user_confirmed: boolean;
  syncable: boolean;
  version: number;
  device_id: string;
  result_status: string;
  error_summary?: string | null;
};

export type StewardTimelineSegment = {
  id: string;
  type: string;
  title: string;
  summary: string;
  status: string;
  source: string;
  data_level: string;
  permission_level: string;
  device_id: string;
  start_at?: string | null;
  end_at?: string | null;
  confidence: number;
  user_confirmed: boolean;
  version: number;
  audit_id?: string | null;
  event_count: number;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
};

export type StewardIntent = {
  id: string;
  type: string;
  title: string;
  summary: string;
  reason: string;
  suggested_action: string;
  risk_level: string;
  status: "candidate" | "accepted" | "dismissed" | "muted" | "expired" | string;
  source: string;
  data_level: string;
  permission_level: string;
  device_id: string;
  confidence: number;
  user_confirmed: boolean;
  version: number;
  audit_id?: string | null;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
};

export type StewardMemory = {
  id: string;
  type: string;
  title: string;
  summary: string;
  content: string;
  scope: string;
  status: "draft" | "active" | "disputed" | "stale" | "archived" | string;
  source: string;
  data_level: string;
  permission_level: string;
  device_id: string;
  confidence: number;
  user_confirmed: boolean;
  version: number;
  last_verified_at?: string | null;
  audit_id?: string | null;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
};

export type StewardMemoryVersion = {
  id: string;
  memory_id: string;
  version: number;
  title: string;
  summary: string;
  content: string;
  reason: string;
  audit_id?: string | null;
  created_at: string;
};

export type StewardKnowledgeItem = {
  id: string;
  type: string;
  title: string;
  summary: string;
  source: string;
  original_uri: string;
  import_method: string;
  content_hash: string;
  status: string;
  data_level: string;
  permission_level: string;
  device_id: string;
  allow_index: boolean;
  user_confirmed: boolean;
  version: number;
  audit_id?: string | null;
  created_at: string;
  updated_at: string;
  deleted_at?: string | null;
};

export type StewardSourceRef = {
  id: string;
  target_type: string;
  target_id: string;
  source_type: string;
  source_id: string;
  location: string;
  summary: string;
  confidence: number;
  sensitive: boolean;
  displayable: boolean;
  audit_id?: string | null;
  created_at: string;
};

export type StewardDataTag = {
  id: string;
  name: string;
  type: string;
  color: string;
  description: string;
  created_at: string;
  updated_at: string;
};

export type StewardSearchResult = {
  entity_type: string;
  id: string;
  type: string;
  title: string;
  summary: string;
  status: string;
  data_level: string;
  source: string;
  updated_at: string;
};

export type StewardDevice = {
  id: string;
  device_name: string;
  platform: string;
  role: string;
  trust_status: string;
  sync_enabled: boolean;
  permission_level: string;
  public_key: string;
  api_base_url: string;
  last_sync_sequence: number;
  last_sent_sequence: number;
  last_seen_at?: string | null;
  last_sync_at?: string | null;
  last_sync_error?: string | null;
  revoked_at?: string | null;
  created_at: string;
  updated_at: string;
};

export type StewardDevicePermission = {
  id: string;
  device_id: string;
  capability: string;
  policy: string;
  max_permission_level: string;
  scope_summary: string;
  created_at: string;
  updated_at: string;
};

export type StewardSyncChange = {
  id: string;
  sequence: number;
  entity_type: string;
  entity_id: string;
  operation: string;
  origin_device_id: string;
  version: number;
  data_level: string;
  payload: Record<string, unknown>;
  payload_hash: string;
  sync_status: string;
  error_summary?: string | null;
  created_at: string;
  applied_at?: string | null;
};

export type StewardSyncConflict = {
  id: string;
  entity_type: string;
  entity_id: string;
  local_change_id?: string | null;
  remote_change_id?: string | null;
  reason: string;
  status: string;
  resolution: string;
  created_at: string;
  updated_at: string;
  resolved_at?: string | null;
};

export type StewardSyncSecurityStatus = {
  management_api_addr: string;
  management_remote_access: boolean;
  peer_api_addr?: string;
  peer_api_enabled: boolean;
  public_api_base?: string;
  peer_api_advertised: boolean;
  auth_required: boolean;
  insecure_mode_active: boolean;
  hmac_secret_configured: boolean;
  device_private_key_configured: boolean;
  device_private_key_valid: boolean;
  device_public_key_configured: boolean;
  device_public_key_valid: boolean;
  device_signing_ready: boolean;
  device_identity_advertisable: boolean;
  sync_encryption_configured: boolean;
  sync_encryption_key_id?: string;
  sync_previous_key_count: number;
  local_encryption_configured: boolean;
  local_encryption_key_id?: string;
  local_previous_key_count: number;
  config_errors: string[];
};

export type StewardDiscoveredPeer = {
  device_id: string;
  device_name: string;
  platform: string;
  peer_api_base: string;
  public_key: string;
  public_key_fingerprint: string;
  issued_at: string;
  last_seen_at: string;
  expires_at: string;
  signature_verified: boolean;
};

export type StewardPeerDiscoveryStatus = {
  enabled: boolean;
  running: boolean;
  listen_addr?: string;
  targets: string[];
  candidate_count: number;
  rejected_announcements: number;
  last_announcement_at?: string | null;
  last_discovery_at?: string | null;
  last_error?: string;
};

export type StewardSyncStatus = {
  local_device: StewardDevice;
  devices: StewardDevice[];
  permissions: StewardDevicePermission[];
  capabilities: StewardDeviceCapability[];
  security: StewardSyncSecurityStatus;
  discovery: StewardPeerDiscoveryStatus;
  discovered_peers: StewardDiscoveredPeer[];
  pending_changes: number;
  pending_relations: number;
  conflict_count: number;
  last_change_at?: string | null;
  recent_changes: StewardSyncChange[];
  conflicts: StewardSyncConflict[];
  change_contract: {
    healthy: boolean;
    checked_changes: number;
    invalid_changes: number;
    issues: string[];
  };
};

export type StewardDeviceCapability = {
  device_id: string;
  capability: string;
  description: string;
  target_type: string;
  risk_level: string;
  max_permission_level: string;
  version: number;
  updated_at: string;
};

export type StewardDeviceSyncResult = {
  device: StewardDevice;
  pulled: number;
  imported: number;
  applied: number;
  skipped: number;
  pushed: number;
  denied: number;
  remote_last_sequence: number;
  local_sent_sequence: number;
  conflicts: StewardSyncConflict[];
  errors: string[];
};

export type StewardPairingChallengeResponse = {
  device_id: string;
  public_key: string;
  algorithm: string;
  challenge: string;
  signature: string;
  signed_at: string;
};

export type StewardDeviceTrustVerification = {
  device: StewardDevice;
  verified: boolean;
  challenge: string;
  algorithm: string;
  signed_at?: string | null;
  public_key_match: boolean;
  response?: StewardPairingChallengeResponse | null;
};

export type StewardAutonomySettings = {
  id: string;
  paused: boolean;
  mode: string;
  max_auto_permission: string;
  updated_at: string;
};

export type StewardAutonomyRule = {
  id: string;
  name: string;
  trigger_type: string;
  target_type: string;
  action: string;
  policy: string;
  risk_level: string;
  max_permission_level: string;
  enabled: boolean;
  scope_summary: string;
  created_at: string;
  updated_at: string;
};

export type StewardAutonomyActionCapability = {
  action: string;
  description: string;
  target_type: string;
  risk_level: string;
  max_permission_level: string;
};

export type StewardAutonomyProposal = {
  id: string;
  rule_id?: string | null;
  source_entity_type: string;
  source_entity_id?: string | null;
  action: string;
  title: string;
  summary: string;
  trigger_reason: string;
  suggested_action: string;
  risk_level: string;
  permission_level: string;
  data_level: string;
  status: string;
  policy: string;
  impact_summary: string;
  score: number;
  score_reason: string;
  created_task_id?: string | null;
  execution_target_type?: string;
  execution_target_id?: string;
  audit_id?: string | null;
  failed_attempts: number;
  retry_eligible: boolean;
  retry_exhausted: boolean;
  auto_retry_at?: string | null;
  created_at: string;
  updated_at: string;
};

export type StewardAutonomyBulkDismissResult = {
  dismissed: number;
  status: string;
  ids: string[];
};

export type StewardApprovalRequest = {
  id: string;
  proposal_id?: string | null;
  requested_action: string;
  risk_summary: string;
  plan_summary: string;
  status: string;
  decided_by: string;
  decision_reason: string;
  created_at: string;
  decided_at?: string | null;
  approval_proof_id?: string;
  approval_key_id?: string;
  approval_proof_expires_at?: string | null;
  approval_proof_required: boolean;
  approval_proof_expectation?: {
    subject: string;
    plan_hash: string;
    capability: string;
    control_generation: number;
  };
};

export type StewardAutonomousRun = {
  id: string;
  proposal_id?: string | null;
  rule_id?: string | null;
  mode: string;
  status: string;
  trigger_reason: string;
  impact_summary: string;
  recovery_hint: string;
  audit_id?: string | null;
  created_at: string;
};

export type StewardAutonomyAdvisorStatus = {
  enabled: boolean;
  provider: string;
  model?: string;
  base_url?: string;
  reason?: string;
  circuit_open?: boolean;
  consecutive_failures?: number;
  retry_at?: string | null;
  last_error?: string;
};

export type StewardAutonomyRetryPolicy = {
  max_attempts: number;
  backoff: string;
  max_backoff: string;
};

export type StewardAutonomyPolicyGateStatus = {
  enabled: boolean;
  backend: string;
  cycle_read_barrier: boolean;
  execution_read_barrier: boolean;
  settings_write_barrier: boolean;
  rule_write_barrier: boolean;
  current_rule_revalidation: boolean;
};

export type StewardAutonomyOverview = {
  settings: StewardAutonomySettings;
  advisor: StewardAutonomyAdvisorStatus;
  retry_policy: StewardAutonomyRetryPolicy;
  policy_gate: StewardAutonomyPolicyGateStatus;
  actions: StewardAutonomyActionCapability[];
  rules: StewardAutonomyRule[];
  proposals: StewardAutonomyProposal[];
  approvals: StewardApprovalRequest[];
  runs: StewardAutonomousRun[];
};

export type StewardModelSettings = {
  provider: 'disabled' | 'openai-compatible' | string;
  base_url: string;
  model: string;
  api_key_configured: boolean;
  api_key_mask?: string;
  allow_no_api_key: boolean;
  timeout_seconds: number;
  agent_max_rounds: number;
  agent_max_tool_calls: number;
  agent_max_duration_seconds: number;
  agent_no_progress_limit: number;
  agent_progress_detail: 'compact' | 'full' | 'final_only' | string;
  source: 'database' | 'environment' | 'default' | string;
  advisor: StewardAutonomyAdvisorStatus;
  planner: StewardRuntimePlannerStatus;
  updated_at?: string | null;
};

export type StewardObservation = {
  id: string;
  source: string;
  type: string;
  summary: string;
  data_level: string;
  permission_level: string;
  device_id: string;
  context_key: string;
  fingerprint: string;
  payload_encrypted: boolean;
  has_media: boolean;
  media_type?: string;
  media_size_bytes?: number;
  status: string;
  system_generated: boolean;
  retention_locked: boolean;
  duplicate_count: number;
  session_id?: string | null;
  occurred_at: string;
  ended_at?: string | null;
  expires_at?: string | null;
  created_at: string;
  metadata?: Record<string, unknown>;
};

export type StewardActivitySession = {
  id: string;
  type: string;
  title: string;
  summary: string;
  source: string;
  context_key: string;
  device_id: string;
  data_level: string;
  status: string;
  observation_count: number;
  confidence: number;
  value_score: number;
  started_at: string;
  ended_at: string;
  timeline_id?: string | null;
};

export type StewardEntity = {
  id: string;
  type: string;
  canonical_key: string;
  display_name: string;
  summary: string;
  data_level: string;
  status: string;
  confidence: number;
  evidence_count: number;
  first_seen_at: string;
  last_seen_at: string;
};

export type StewardRelationEvidence = {
  id: string;
  relation_id: string;
  source_ref_id?: string | null;
  observation_id?: string | null;
  evidence_type: string;
  summary: string;
  confidence: number;
  created_at: string;
};

export type StewardRelation = {
  id: string;
  source_entity_id: string;
  target_entity_id: string;
  source_entity?: StewardEntity;
  target_entity?: StewardEntity;
  relation_type: string;
  confidence: number;
  evidence_count: number;
  data_level: string;
  status: string;
  inference_state: string;
  first_seen_at: string;
  last_seen_at: string;
  evidence: StewardRelationEvidence[];
};

export type StewardHabit = {
  id: string;
  type: string;
  title: string;
  summary: string;
  pattern: string;
  status: string;
  data_level: string;
  confidence: number;
  evidence_count: number;
  value_score: number;
  user_confirmed: boolean;
  retention_locked: boolean;
  last_evidence_at?: string | null;
  quarantined_at?: string | null;
};

export type StewardInsight = {
  id: string;
  type: string;
  title: string;
  summary: string;
  suggested_action: string;
  status: string;
  data_level: string;
  confidence: number;
  evidence_count: number;
  value_score: number;
  user_confirmed: boolean;
  retention_locked: boolean;
  quarantined_at?: string | null;
};

export type StewardRetentionPolicy = {
  id: string;
  source_pattern: string;
  data_kind: string;
  data_level: string;
  ttl_days: number;
  quarantine_days: number;
  auto_purge: boolean;
  require_preview: boolean;
  protect_user_confirmed: boolean;
  protect_referenced: boolean;
  deletion_tombstone_days: number;
  description: string;
  updated_at: string;
};

export type StewardLifecycleLayerStatus = {
  kind: string;
  count: number;
  bytes: number;
  expired_count: number;
  quarantined_count: number;
};

export type StewardLifecycleStatus = {
  profile: 'deep' | 'light';
  vector_search_enabled: boolean;
  local_encryption_ready: boolean;
  layers: StewardLifecycleLayerStatus[];
  retention_policies: StewardRetentionPolicy[];
  last_runs: Record<string, string | null>;
  next_expiring_at?: string | null;
  updated_at: string;
};

export type StewardLifecycleAction = {
  target_type: string;
  target_id: string;
  action: string;
  reason: string;
  value_score: number;
  requires_preview: boolean;
  recoverable_to?: string | null;
};

export type StewardLifecycleEvaluation = {
  id: string;
  dry_run: boolean;
  evaluated_at: string;
  actions: StewardLifecycleAction[];
  counts: Record<string, number>;
};

export type StewardPurgeResult = {
  audit_id: string;
  dry_run: boolean;
  deleted: number;
  quarantined: number;
  skipped: number;
  actions: StewardLifecycleAction[];
  completed_at: string;
};

export type StewardOverview = {
  agent: StewardAgentStatus;
  collectors: StewardCollectorConfig[];
  events: StewardEvent[];
  timeline_segments: StewardTimelineSegment[];
  tasks: StewardTask[];
  intents: StewardIntent[];
  memories: StewardMemory[];
  knowledge_items: StewardKnowledgeItem[];
  source_refs: StewardSourceRef[];
  tags: StewardDataTag[];
  audit_logs: StewardAuditLog[];
  sync?: StewardSyncStatus | null;
  autonomy?: StewardAutonomyOverview | null;
  counts: Record<string, number>;
};

export type StewardNotificationAction = {
  id: string;
  label: string;
  kind: string;
  value?: string;
};

export type StewardNotificationDelivery = {
  id: string;
  notification_id: string;
  schedule_revision?: number;
  endpoint_id?: string | null;
  channel: string;
  status: string;
  attempt_count: number;
  max_attempts: number;
  next_attempt_at: string;
  provider_message_id?: string;
  last_error?: string;
  accepted_at?: string | null;
};

export type StewardNotification = {
  id: string;
  schedule_revision?: number;
  source_type: string;
  source_id?: string;
  title: string;
  body: string;
  category: string;
  priority: 'low' | 'normal' | 'high' | 'urgent';
  status: string;
  scheduled_at: string;
  allowed_window_start?: string | null;
  allowed_window_end?: string | null;
  expires_at?: string | null;
  actions: StewardNotificationAction[];
  deliveries: StewardNotificationDelivery[];
  acknowledged_at?: string | null;
  created_at: string;
  updated_at: string;
};

export type StewardNotificationEndpoint = {
  id: string;
  channel: 'system' | 'linux_desktop' | 'ntfy' | 'email';
  name: string;
  enabled: boolean;
  config: Record<string, unknown>;
  secret_set: boolean;
  last_success_at?: string | null;
  last_error?: string;
  updated_at: string;
};

export type StewardActivitySourceStatus = {
  device_id: string;
  collector_name: string;
  source_key: string;
  execution_target: string;
  status: string;
  cursor: Record<string, unknown>;
  capabilities: Record<string, unknown>;
  last_poll_at?: string | null;
  last_source_event_at?: string | null;
  last_ingested_at?: string | null;
  backlog_count: number;
  max_expected_lag_seconds: number;
  last_error?: string;
  fresh: boolean;
};

export type StewardActivityPipelineStatus = {
  enabled: boolean;
  mode: string;
  sources: StewardActivitySourceStatus[];
  pending_batches: number;
  processing_batches: number;
  waiting_model: number;
  failed_batches: number;
  last_batch_at?: string | null;
  updated_at: string;
};

export type StewardIntelligenceSettings = {
  enabled: boolean;
  mode: 'batch' | 'legacy';
  capture_profile: 'metadata' | 'hybrid' | 'deep';
  timezone: string;
  activity_sample_seconds: number;
  sessionize_interval_seconds: number;
  batch_interval_seconds: number;
  boundary_grace_seconds: number;
  daily_report_fallback_local: string;
  weekly_report_day: number;
  weekly_report_local: string;
  monthly_report_local: string;
  recent_profile_days: number;
  stable_min_evidence_days: number;
  profile_bootstrap_days: number;
  report_catchup_days: number;
  background_max_rounds: number;
  background_max_tool_calls: number;
  background_max_duration_seconds: number;
  background_no_progress_limit: number;
  quiet_start_local: string;
  quiet_end_local: string;
  reminder_daily_soft_budget: number;
  reminder_category_soft_budget: number;
  reminder_cooldown_seconds: number;
  raw_metadata_retention_days: number;
  unreferenced_media_retention_days: number;
  revision: number;
  created_at: string;
  updated_at: string;
};

export type StewardBackgroundQueueStatus = {
  pending: number;
  processing: number;
  waiting_model: number;
  failed: number;
  last_success_at?: string | null;
};

export type StewardNotificationQueueStatus = {
  queued: number;
  sending: number;
  retrying: number;
  failed: number;
  accepted: number;
  last_sent_at?: string | null;
};

export type StewardHealthIssue = {
  code: string;
  message: string;
  action: string;
};

export type StewardRatioMetric = {
  available: boolean;
  value?: number;
  numerator: number;
  denominator: number;
  reason?: string;
};

export type StewardReportCoverageMetrics = {
  available: boolean;
  report_count: number;
  average?: number;
  reason?: string;
};

export type StewardModelUsageMetrics = {
  available: boolean;
  input_tokens?: number | null;
  output_tokens?: number | null;
  total_tokens?: number | null;
  cost?: number | null;
  currency?: string;
  reason: string;
};

export type StewardBackgroundMetrics = {
  window_start: string;
  window_end: string;
  observations_1h: number;
  sessions_1h: number;
  session_compression_ratio: StewardRatioMetric;
  batch_status_counts: Record<string, number>;
  model_episodes_1h: {
    completed: number;
    failed: number;
  };
  report_coverage: StewardReportCoverageMetrics;
  reminder_feedback_1h: {
    total: number;
    by_action: Record<string, number>;
  };
  model_usage: StewardModelUsageMetrics;
};

export type StewardBackgroundStatus = {
  state: 'healthy' | 'degraded' | 'unhealthy' | 'disabled';
  enabled: boolean;
  mode: string;
  checked_at: string;
  agent: StewardAgentStatus;
  loops: StewardBackgroundLoopStatus[];
  pipeline: StewardActivityPipelineStatus;
  intelligence_queue: StewardBackgroundQueueStatus;
  notifications: StewardNotificationQueueStatus;
  model: StewardAutonomyAdvisorStatus;
  latest_outcome?: {
    kind: string;
    status: string;
    summary?: string;
    at: string;
  } | null;
  latest_report_at?: string | null;
  profile_updated_at?: string | null;
  issues: string[];
  issue_details?: StewardHealthIssue[];
  metrics?: StewardBackgroundMetrics;
  next_consolidation_at?: string | null;
  next_daily_report_at?: string | null;
};

export type StewardActivityBatch = {
  id: string;
  device_id: string;
  window_start: string;
  window_end: string;
  trigger_kind: string;
  revision: number;
  status: string;
  due_at: string;
  episode_id?: string | null;
  created_at: string;
  updated_at: string;
};

export type StewardIntelligenceJob = {
  id: string;
  kind: string;
  period_key: string;
  period_start: string;
  period_end: string;
  status: string;
  input: Record<string, unknown>;
  checkpoint: Record<string, unknown>;
  attempts: number;
  max_attempts: number;
  due_at: string;
  next_attempt_at: string;
  lease_owner?: string;
  lease_expires_at?: string | null;
  control_generation: number;
  episode_id?: string | null;
  report_id?: string | null;
  failure_summary?: string;
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
};

export type StewardReportRegenerationResult = {
  source_report_id: string;
  source_revision: number;
  job: StewardIntelligenceJob;
  created: boolean;
};

export type StewardProfileEvidence = {
  id: string;
  fact_id: string;
  source_type: string;
  source_id: string;
  summary?: string;
  evidence_day: string;
  content_hash?: string;
  created_at: string;
};

export type StewardProfileFact = {
  id: string;
  key: string;
  value: Record<string, unknown>;
  summary: string;
  horizon: 'recent' | 'stable' | 'explicit';
  status: string;
  version: number;
  confidence: number;
  evidence_count: number;
  evidence_days: number;
  user_confirmed: boolean;
  conflict_group: string;
  supersedes_fact_id?: string | null;
  created_by: string;
  provider?: string;
  model?: string;
  valid_from: string;
  valid_to?: string | null;
  evidence?: StewardProfileEvidence[];
  created_at: string;
  updated_at: string;
};

export type StewardProfileSnapshot = {
  id: string;
  horizon: 'recent' | 'stable' | 'explicit' | 'merged';
  revision: number;
  window_start?: string | null;
  window_end: string;
  facts: StewardProfileFact[];
  profile: Record<string, unknown>;
  created_by: string;
  created_at: string;
};

export type StewardProfileView = {
  recent?: StewardProfileSnapshot | null;
  stable?: StewardProfileSnapshot | null;
  explicit?: StewardProfileSnapshot | null;
  merged?: StewardProfileSnapshot | null;
};

export type StewardReportEvidence = {
  id: string;
  report_id: string;
  source_type: string;
  source_id: string;
  summary?: string;
  content_hash?: string;
  created_at: string;
};

export type StewardReport = {
  id: string;
  cadence: 'daily' | 'weekly' | 'monthly';
  period_key: string;
  period_start: string;
  period_end: string;
  revision: number;
  status: string;
  title: string;
  summary: string;
  body: string;
  metrics: Record<string, unknown>;
  silent: boolean;
  evidence_count: number;
  supersedes_id?: string | null;
  provider?: string;
  model?: string;
  error_summary?: string;
  evidence?: StewardReportEvidence[];
  created_at: string;
  updated_at: string;
  completed_at?: string | null;
};

export type StewardReminderFeedback = {
  id: string;
  notification_id: string;
  schedule_revision: number;
  policy_id?: string | null;
  action: string;
  device_id: string;
  channel: string;
  category: string;
  timezone: string;
  activity_context: string;
  response_seconds?: number | null;
  snooze_seconds?: number | null;
  new_scheduled_at?: string | null;
  idempotency_key: string;
  metadata: Record<string, unknown>;
  created_at: string;
};

export type StewardReceptivityWindow = {
  id: string;
  profile_scope: string;
  category: string;
  weekday: number;
  time_slot: number;
  activity_context: string;
  device_id: string;
  channel: string;
  sample_count: number;
  opened_count: number;
  acted_count: number;
  acknowledged_count: number;
  snoozed_count: number;
  dismissed_count: number;
  ignored_count: number;
  cancelled_count: number;
  auto_resolved_count: number;
  mean_response_seconds: number;
  confidence: number;
  score: number;
  created_at: string;
  updated_at: string;
};

export type StewardReminderPolicy = {
  id: string;
  profile_scope: string;
  category: string;
  version: number;
  status: string;
  policy: Record<string, unknown>;
  rationale: string;
  evidence_manifest: string[];
  source_episode_id?: string | null;
  supersedes_id?: string | null;
  created_at: string;
  updated_at: string;
};
