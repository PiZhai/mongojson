package main

import (
	"fmt"
	"strings"
	"time"

	"mongojson/backend/internal/platform/servicecontrol"
)

type runtimeVerifyOptions struct {
	WriteProbes                 bool          `json:"write_probes"`
	StrictSecurity              bool          `json:"strict_security"`
	AdvisorProbe                bool          `json:"advisor_probe"`
	AdvisorProbeEachSample      bool          `json:"advisor_probe_each_sample"`
	AdvisorPrivacyProbe         bool          `json:"advisor_privacy_probe"`
	AutonomyLimit               int           `json:"autonomy_limit"`
	ExpectAgentID               string        `json:"expect_agent_id,omitempty"`
	ExpectAgentVersion          string        `json:"expect_agent_version,omitempty"`
	ExpectAgentPlatform         string        `json:"expect_agent_platform,omitempty"`
	ExpectAdvisorProvider       string        `json:"expect_advisor_provider,omitempty"`
	ExpectAdvisorModel          string        `json:"expect_advisor_model,omitempty"`
	ExpectAdvisorMaxDataLevel   string        `json:"expect_advisor_max_data_level,omitempty"`
	ExpectSyncKeyID             string        `json:"expect_sync_key_id,omitempty"`
	ExpectLocalKeyID            string        `json:"expect_local_key_id,omitempty"`
	ExpectSyncPreviousKeyCount  *int          `json:"expect_sync_previous_key_count,omitempty"`
	ExpectLocalPreviousKeyCount *int          `json:"expect_local_previous_key_count,omitempty"`
	WatchDuration               time.Duration `json:"watch_duration"`
	WatchInterval               time.Duration `json:"watch_interval"`
}

type runtimeVerificationCheck struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
	Detail  any    `json:"detail,omitempty"`
}

type runtimeVerificationResult struct {
	OK        bool                        `json:"ok"`
	APIBase   string                      `json:"api_base"`
	Options   runtimeVerifyOptions        `json:"options"`
	Checks    []runtimeVerificationCheck  `json:"checks"`
	Artifacts map[string]string           `json:"artifacts,omitempty"`
	Samples   []runtimeVerificationSample `json:"samples,omitempty"`
}

type runtimeVerificationSample struct {
	Index       int                        `json:"index"`
	StartedAt   time.Time                  `json:"started_at"`
	CompletedAt time.Time                  `json:"completed_at"`
	OK          bool                       `json:"ok"`
	Checks      []runtimeVerificationCheck `json:"checks"`
	Artifacts   map[string]string          `json:"artifacts,omitempty"`
}

type peerVerifyOptions struct {
	Sync         bool `json:"sync"`
	Strict       bool `json:"strict"`
	RequirePeers bool `json:"require_peers"`
	WriteProbes  bool `json:"write_probes"`
}

type peerWriteProbe struct {
	TaskID   string            `json:"task_id"`
	Title    string            `json:"title"`
	Entities []peerEntityProbe `json:"entities"`
}

type peerEntityProbe struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Label      string `json:"label,omitempty"`
}

type peerVerificationResult struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	Platform     string `json:"platform,omitempty"`
	Status       string `json:"status"`
	Message      string `json:"message"`
	Verified     bool   `json:"verified"`
	Synced       bool   `json:"synced"`
	ProbeVisible bool   `json:"probe_visible,omitempty"`
	SyncSummary  any    `json:"sync_summary,omitempty"`
	ProbeSummary any    `json:"probe_summary,omitempty"`
	Error        string `json:"error,omitempty"`
}

type peersVerificationResult struct {
	OK          bool                       `json:"ok"`
	APIBase     string                     `json:"api_base"`
	Options     peerVerifyOptions          `json:"options"`
	LocalDevice *peerVerificationDevice    `json:"local_device,omitempty"`
	Probe       *peerWriteProbe            `json:"probe,omitempty"`
	Peers       []peerVerificationResult   `json:"peers"`
	Checks      []runtimeVerificationCheck `json:"checks,omitempty"`
}

type peerVerificationDevice struct {
	AgentID  string `json:"agent_id"`
	Name     string `json:"device_name,omitempty"`
	Platform string `json:"platform,omitempty"`
}

type meshVerifyOptions struct {
	NodeAPIs                  []string      `json:"node_apis"`
	NodeManagementTokens      []string      `json:"-"`
	StrictSecurity            bool          `json:"strict_security"`
	StrictPeers               bool          `json:"strict_peers"`
	RequirePeers              bool          `json:"require_peers"`
	Sync                      bool          `json:"sync"`
	WriteProbes               bool          `json:"write_probes"`
	AdvisorProbe              bool          `json:"advisor_probe"`
	AdvisorProbeEachSample    bool          `json:"advisor_probe_each_sample"`
	AdvisorPrivacyProbe       bool          `json:"advisor_privacy_probe"`
	ExpectAgentIDs            []string      `json:"expect_agent_ids,omitempty"`
	ExpectAgentVersions       []string      `json:"expect_agent_versions,omitempty"`
	ExpectAgentPlatforms      []string      `json:"expect_agent_platforms,omitempty"`
	ExpectSyncKeyIDs          []string      `json:"expect_sync_key_ids,omitempty"`
	ExpectLocalKeyIDs         []string      `json:"expect_local_key_ids,omitempty"`
	ExpectAdvisorProvider     string        `json:"expect_advisor_provider,omitempty"`
	ExpectAdvisorModel        string        `json:"expect_advisor_model,omitempty"`
	ExpectAdvisorMaxDataLevel string        `json:"expect_advisor_max_data_level,omitempty"`
	AutonomyLimit             int           `json:"autonomy_limit"`
	WatchDuration             time.Duration `json:"watch_duration"`
	WatchInterval             time.Duration `json:"watch_interval"`
}

type meshNodeVerificationResult struct {
	OK      bool                      `json:"ok"`
	APIBase string                    `json:"api_base"`
	Runtime runtimeVerificationResult `json:"runtime"`
	Peers   peersVerificationResult   `json:"peers"`
}

type meshVerificationResult struct {
	OK      bool                         `json:"ok"`
	Options meshVerifyOptions            `json:"options"`
	Nodes   []meshNodeVerificationResult `json:"nodes"`
	Checks  []runtimeVerificationCheck   `json:"checks,omitempty"`
	Samples []meshVerificationSample     `json:"samples,omitempty"`
}

type meshVerificationSample struct {
	Index       int                          `json:"index"`
	StartedAt   time.Time                    `json:"started_at"`
	CompletedAt time.Time                    `json:"completed_at"`
	OK          bool                         `json:"ok"`
	Nodes       []meshNodeVerificationResult `json:"nodes"`
}

type serviceVerifyOptions struct {
	Name           string               `json:"name"`
	Scope          string               `json:"scope"`
	StrictSecurity bool                 `json:"strict_security"`
	WriteProbes    bool                 `json:"write_probes"`
	AutonomyLimit  int                  `json:"autonomy_limit"`
	WatchDuration  time.Duration        `json:"watch_duration"`
	WatchInterval  time.Duration        `json:"watch_interval"`
	Runtime        runtimeVerifyOptions `json:"runtime"`
}

type serviceVerificationResult struct {
	OK      bool                        `json:"ok"`
	APIBase string                      `json:"api_base"`
	Options serviceVerifyOptions        `json:"options"`
	Service servicecontrol.StatusResult `json:"service"`
	Runtime runtimeVerificationResult   `json:"runtime"`
	Checks  []runtimeVerificationCheck  `json:"checks"`
	Samples []serviceVerificationSample `json:"samples,omitempty"`
}

type serviceVerificationSample struct {
	Index       int                         `json:"index"`
	StartedAt   time.Time                   `json:"started_at"`
	CompletedAt time.Time                   `json:"completed_at"`
	OK          bool                        `json:"ok"`
	Service     servicecontrol.StatusResult `json:"service"`
	Runtime     runtimeVerificationResult   `json:"runtime"`
	Checks      []runtimeVerificationCheck  `json:"checks"`
}

type optionalIntFlag struct {
	value int
	set   bool
}

func (f *optionalIntFlag) String() string {
	if f == nil || !f.set {
		return ""
	}
	return fmt.Sprintf("%d", f.value)
}

func (f *optionalIntFlag) Set(value string) error {
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed); err != nil {
		return fmt.Errorf("invalid integer %q", value)
	}
	if parsed < 0 {
		return fmt.Errorf("expected count must be >= 0")
	}
	f.value = parsed
	f.set = true
	return nil
}

func (f *optionalIntFlag) ptr() *int {
	if f == nil || !f.set {
		return nil
	}
	value := f.value
	return &value
}

type peerDevice struct {
	ID          string
	Name        string
	Platform    string
	Role        string
	TrustStatus string
	SyncEnabled bool
	PublicKey   string
	APIBaseURL  string
}
