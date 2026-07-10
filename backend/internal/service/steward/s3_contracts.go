package steward

import "mongojson/backend/internal/domain"

const (
	DeviceRoleLocal    = "local"
	DeviceRolePeer     = "peer"
	DeviceTrusted      = "trusted"
	DeviceRevoked      = "revoked"
	SyncPending        = "pending"
	SyncApplied        = "applied"
	SyncStored         = "stored"
	SyncConflictStatus = "conflict"
	SyncCreate         = "create"
	SyncUpdate         = "update"
	SyncDelete         = "delete"

	SyncHeaderDeviceID  = "X-Steward-Device-ID"
	SyncHeaderTimestamp = "X-Steward-Timestamp"
	SyncHeaderBodyHash  = "X-Steward-Body-SHA256"
	SyncHeaderSignature = "X-Steward-Signature"

	SyncHeaderKeyAlgorithm  = "X-Steward-Key-Algorithm"
	SyncHeaderKeySignature  = "X-Steward-Key-Signature"
	SyncKeyAlgorithmEd25519 = "ed25519"

	syncChangeWindowLimit = 200
	syncMaxWindowsPerRun  = 25
)

type RegisterDeviceInput struct {
	ID              string `json:"id"`
	DeviceName      string `json:"device_name"`
	Platform        string `json:"platform"`
	Role            string `json:"role"`
	SyncEnabled     *bool  `json:"sync_enabled"`
	PermissionLevel string `json:"permission_level"`
	PublicKey       string `json:"public_key"`
	APIBaseURL      string `json:"api_base_url"`
}

type UpdateDevicePermissionInput struct {
	Policy             string `json:"policy"`
	MaxPermissionLevel string `json:"max_permission_level"`
	ScopeSummary       string `json:"scope_summary"`
}

type CreateSyncChangeInput struct {
	ID             string         `json:"id"`
	EntityType     string         `json:"entity_type"`
	EntityID       string         `json:"entity_id"`
	Operation      string         `json:"operation"`
	OriginDeviceID string         `json:"origin_device_id"`
	Version        int            `json:"version"`
	DataLevel      string         `json:"data_level"`
	Payload        map[string]any `json:"payload"`
}

type ImportSyncChangesInput struct {
	Device  RegisterDeviceInput     `json:"device"`
	Changes []CreateSyncChangeInput `json:"changes"`
}

type ImportSyncChangesResult struct {
	Imported  int                          `json:"imported"`
	Applied   int                          `json:"applied"`
	Skipped   int                          `json:"skipped"`
	Denied    int                          `json:"denied"`
	Conflicts []domain.StewardSyncConflict `json:"conflicts"`
	Changes   []domain.StewardSyncChange   `json:"changes"`
}

type PeerSyncChangesResult struct {
	Changes      []domain.StewardSyncChange `json:"changes"`
	NextSequence int64                      `json:"next_sequence"`
	HasMore      bool                       `json:"has_more"`
}

type ResolveSyncConflictInput struct {
	Resolution string `json:"resolution"`
}

type SyncDeviceResult struct {
	Device             domain.StewardDevice         `json:"device"`
	Pulled             int                          `json:"pulled"`
	Imported           int                          `json:"imported"`
	Applied            int                          `json:"applied"`
	Skipped            int                          `json:"skipped"`
	Pushed             int                          `json:"pushed"`
	Denied             int                          `json:"denied"`
	RemoteLastSequence int64                        `json:"remote_last_sequence"`
	LocalSentSequence  int64                        `json:"local_sent_sequence"`
	Conflicts          []domain.StewardSyncConflict `json:"conflicts"`
	Errors             []string                     `json:"errors"`
}

type SyncEntityProbeInput struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
}

type SyncEntityProbeResult struct {
	DeviceID   string `json:"device_id"`
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Exists     bool   `json:"exists"`
	Detail     any    `json:"detail,omitempty"`
}

type syncChangeListMode string

const (
	syncChangeListReplay syncChangeListMode = "replay"
	syncChangeListRecent syncChangeListMode = "recent"
)

type syncPullWindow struct {
	Input              ImportSyncChangesInput
	RemoteLastSequence int64
	Skipped            int
}

type syncPushWindow struct {
	Input             ImportSyncChangesInput
	LocalSentSequence int64
}
