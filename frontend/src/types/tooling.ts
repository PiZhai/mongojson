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
  last_run_at?: string | null;
  last_error?: string | null;
  created_at: string;
  updated_at: string;
  audit_id?: string | null;
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
  max_data_level?: string;
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
