package privilegebroker

import "time"

const (
	APIVersion        = "steward-privilege-broker/v1.3"
	DelegationVersion = "steward-broker-delegation/v1"

	HeaderTimestamp = "X-Steward-Broker-Timestamp"
	HeaderNonce     = "X-Steward-Broker-Nonce"
	HeaderSignature = "X-Steward-Broker-Signature"
)

type PublicCapability struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	PermissionLevel  string   `json:"permission_level"`
	RiskLevel        string   `json:"risk_level"`
	ExecutableName   string   `json:"executable_name"`
	ArgumentCount    int      `json:"argument_count"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	MaxOutputBytes   int      `json:"max_output_bytes"`
	CapabilityDigest string   `json:"capability_digest"`
	CredentialIDs    []string `json:"credential_ids,omitempty"`
}

type PublicBrokerPeer struct {
	DeviceID            string   `json:"device_id"`
	Name                string   `json:"name"`
	PublicKey           string   `json:"public_key"`
	KeyID               string   `json:"key_id"`
	AllowedCapabilities []string `json:"allowed_capabilities"`
	AllowedCredentials  []string `json:"allowed_credentials,omitempty"`
}

type Status struct {
	Version             string                    `json:"version"`
	InstanceID          string                    `json:"instance_id"`
	Stopped             bool                      `json:"stopped"`
	Generation          int64                     `json:"generation"`
	PolicyDigest        string                    `json:"policy_digest"`
	Capabilities        []PublicCapability        `json:"capabilities"`
	ApprovalAuthorities []PublicApprovalAuthority `json:"approval_authorities"`
	BrokerPeers         []PublicBrokerPeer        `json:"broker_peers,omitempty"`
	ActiveExecutions    int                       `json:"active_executions"`
	PublicKey           string                    `json:"public_key"`
	KeyID               string                    `json:"key_id"`
	IssuedAt            time.Time                 `json:"issued_at"`
	Signature           string                    `json:"signature"`
}

type GrantRequest struct {
	Capability        string              `json:"capability"`
	Subject           string              `json:"subject"`
	PlanHash          string              `json:"plan_hash"`
	ApprovalRef       string              `json:"approval_ref"`
	ApprovalProof     SignedApprovalProof `json:"approval_proof"`
	ControlGeneration int64               `json:"control_generation"`
}

type CapabilityTokenClaims struct {
	TokenID           string    `json:"token_id"`
	BrokerInstanceID  string    `json:"broker_instance_id"`
	Capability        string    `json:"capability"`
	CapabilityDigest  string    `json:"capability_digest"`
	Subject           string    `json:"subject"`
	PlanHash          string    `json:"plan_hash"`
	ApprovalRef       string    `json:"approval_ref"`
	ApprovalProofID   string    `json:"approval_proof_id"`
	ApprovalKeyID     string    `json:"approval_key_id"`
	ApprovalExpiresAt time.Time `json:"approval_expires_at"`
	ControlGeneration int64     `json:"control_generation"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
	DelegationID      string    `json:"delegation_id,omitempty"`
	OriginBrokerKeyID string    `json:"origin_broker_key_id,omitempty"`
	CredentialRefs    []string  `json:"credential_refs,omitempty"`
}

type GrantResponse struct {
	Token  string                `json:"token"`
	Claims CapabilityTokenClaims `json:"claims"`
	KeyID  string                `json:"key_id"`
}

type ExecuteRequest struct {
	Token             string `json:"token"`
	Capability        string `json:"capability"`
	Subject           string `json:"subject"`
	PlanHash          string `json:"plan_hash"`
	ApprovalRef       string `json:"approval_ref"`
	ControlGeneration int64  `json:"control_generation"`
}

type ExecutionReceipt struct {
	ExecutionID       string    `json:"execution_id"`
	BrokerInstanceID  string    `json:"broker_instance_id"`
	Capability        string    `json:"capability"`
	CapabilityDigest  string    `json:"capability_digest"`
	Subject           string    `json:"subject"`
	PlanHash          string    `json:"plan_hash"`
	ApprovalRef       string    `json:"approval_ref"`
	ApprovalProofID   string    `json:"approval_proof_id"`
	ApprovalKeyID     string    `json:"approval_key_id"`
	ApprovalExpiresAt time.Time `json:"approval_expires_at"`
	ControlGeneration int64     `json:"control_generation"`
	ExitCode          int       `json:"exit_code"`
	Succeeded         bool      `json:"succeeded"`
	StdoutSHA256      string    `json:"stdout_sha256"`
	StderrSHA256      string    `json:"stderr_sha256"`
	StdoutBytes       int64     `json:"stdout_bytes"`
	StderrBytes       int64     `json:"stderr_bytes"`
	StdoutTruncated   bool      `json:"stdout_truncated"`
	StderrTruncated   bool      `json:"stderr_truncated"`
	AuditPersisted    bool      `json:"audit_persisted"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
	ErrorCode         string    `json:"error_code,omitempty"`
	DelegationID      string    `json:"delegation_id,omitempty"`
	OriginBrokerKeyID string    `json:"origin_broker_key_id,omitempty"`
	CredentialRefs    []string  `json:"credential_refs,omitempty"`
}

type SignedExecutionReceipt struct {
	Payload   ExecutionReceipt `json:"payload"`
	KeyID     string           `json:"key_id"`
	Signature string           `json:"signature"`
}

type ExecuteResponse struct {
	Stdout  string                 `json:"stdout"`
	Stderr  string                 `json:"stderr"`
	Receipt SignedExecutionReceipt `json:"receipt"`
}

type ControlRequest struct {
	Generation int64  `json:"generation"`
	Reason     string `json:"reason"`
	ChangedBy  string `json:"changed_by"`
}

type ErrorResponse struct {
	Code  string `json:"code"`
	Error string `json:"error"`
}

type Authorization struct {
	Capability        string
	Subject           string
	PlanHash          string
	ApprovalRef       string
	ApprovalProof     SignedApprovalProof
	ControlGeneration int64
}

type BrokerDelegationRequest struct {
	TargetDeviceID    string              `json:"target_device_id"`
	TargetStatus      Status              `json:"target_status"`
	Capability        string              `json:"capability"`
	CredentialRefs    []string            `json:"credential_refs,omitempty"`
	Subject           string              `json:"subject"`
	PlanHash          string              `json:"plan_hash"`
	ApprovalRef       string              `json:"approval_ref"`
	ApprovalProof     SignedApprovalProof `json:"approval_proof"`
	ControlGeneration int64               `json:"control_generation"`
}

type BrokerDelegationClaims struct {
	Version                 string    `json:"version"`
	DelegationID            string    `json:"delegation_id"`
	OriginBrokerPublicKey   string    `json:"origin_broker_public_key"`
	OriginBrokerKeyID       string    `json:"origin_broker_key_id"`
	OriginBrokerInstanceID  string    `json:"origin_broker_instance_id"`
	OriginDeviceID          string    `json:"origin_device_id"`
	OriginControlGeneration int64     `json:"origin_control_generation"`
	TargetDeviceID          string    `json:"target_device_id"`
	TargetBrokerKeyID       string    `json:"target_broker_key_id"`
	TargetBrokerInstanceID  string    `json:"target_broker_instance_id"`
	TargetPolicyDigest      string    `json:"target_policy_digest"`
	TargetControlGeneration int64     `json:"target_control_generation"`
	Capability              string    `json:"capability"`
	CapabilityDigest        string    `json:"capability_digest"`
	CredentialRefs          []string  `json:"credential_refs,omitempty"`
	Subject                 string    `json:"subject"`
	PlanHash                string    `json:"plan_hash"`
	ApprovalRef             string    `json:"approval_ref"`
	ApprovalProofID         string    `json:"approval_proof_id"`
	ApprovalKeyID           string    `json:"approval_key_id"`
	ApprovalExpiresAt       time.Time `json:"approval_expires_at"`
	IssuedAt                time.Time `json:"issued_at"`
	ExpiresAt               time.Time `json:"expires_at"`
}

type SignedBrokerDelegation struct {
	Claims    BrokerDelegationClaims `json:"claims"`
	KeyID     string                 `json:"key_id"`
	Signature string                 `json:"signature"`
}

type DelegatedExecuteRequest struct {
	Delegation   SignedBrokerDelegation `json:"delegation"`
	OriginStatus Status                 `json:"origin_status"`
}
