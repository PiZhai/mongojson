package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"mongojson/backend/internal/platform/peerdiscovery"
	"mongojson/backend/internal/platform/servicecontrol"
)

func (c cli) service(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printServiceUsage()
		return nil
	}
	switch args[0] {
	case "install":
		return serviceInstall(args[1:])
	case "plan":
		return servicePlan(args[1:])
	case "env":
		return serviceEnv(args[1:])
	case "uninstall":
		return serviceSimpleAction(args[1:], "uninstall")
	case "start":
		return serviceSimpleAction(args[1:], "start")
	case "stop":
		return serviceSimpleAction(args[1:], "stop")
	case "restart":
		return serviceSimpleAction(args[1:], "restart")
	case "status":
		return serviceStatus(args[1:])
	default:
		return fmt.Errorf("unknown service command %q", args[0])
	}
}

type stringListFlag []string

func (f *stringListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *stringListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

type serviceEnvApplyOutput struct {
	ServiceEnv               servicecontrol.Result         `json:"service_env"`
	Verification             *serviceEnvVerificationAdvice `json:"verification,omitempty"`
	Restart                  *servicecontrol.Result        `json:"restart,omitempty"`
	Status                   *servicecontrol.StatusResult  `json:"status,omitempty"`
	VerificationResult       *serviceVerificationResult    `json:"verification_result,omitempty"`
	VerificationEvidencePath string                        `json:"verification_evidence_path,omitempty"`
	RestartError             string                        `json:"restart_error,omitempty"`
	StatusError              string                        `json:"status_error,omitempty"`
	VerificationError        string                        `json:"verification_error,omitempty"`
}

type serviceInstallOutput struct {
	Service                  servicecontrol.Result         `json:"service"`
	Verification             *serviceEnvVerificationAdvice `json:"verification,omitempty"`
	Start                    *servicecontrol.Result        `json:"start,omitempty"`
	Status                   *servicecontrol.StatusResult  `json:"status,omitempty"`
	VerificationResult       *serviceVerificationResult    `json:"verification_result,omitempty"`
	VerificationEvidencePath string                        `json:"verification_evidence_path,omitempty"`
	StartError               string                        `json:"start_error,omitempty"`
	StatusError              string                        `json:"status_error,omitempty"`
	VerificationError        string                        `json:"verification_error,omitempty"`
}

type servicePlanOutput struct {
	Plans                  []servicecontrol.InstallPlan             `json:"plans"`
	Verification           *serviceEnvVerificationAdvice            `json:"verification,omitempty"`
	VerificationByPlatform map[string]*serviceEnvVerificationAdvice `json:"verification_by_platform,omitempty"`
}

type serviceInstallAdvisorFlagValues struct {
	Provider         string
	BaseURL          string
	Model            string
	APIKey           string
	AllowNoAPIKey    bool
	Timeout          time.Duration
	MaxDataLevel     string
	FailureThreshold int
	FailureCooldown  time.Duration
}

type serviceInstallDiscoveryFlagValues struct {
	Enabled    bool
	DeviceName string
	ListenAddr string
	Targets    string
	Interval   time.Duration
	TTL        time.Duration
}

func serviceInstallAdvisorEnv(fs *flag.FlagSet, values serviceInstallAdvisorFlagValues) map[string]string {
	env := map[string]string{}
	setStringEnvIfPresent(env, "STEWARD_LLM_PROVIDER", values.Provider, fs, "llm-provider")
	setStringEnvIfPresent(env, "STEWARD_LLM_BASE_URL", values.BaseURL, fs, "llm-base-url")
	setStringEnvIfPresent(env, "STEWARD_LLM_MODEL", values.Model, fs, "llm-model")
	setStringEnvIfPresent(env, "STEWARD_LLM_API_KEY", values.APIKey, fs, "llm-api-key")
	setStringEnvIfPresent(env, "STEWARD_LLM_MAX_DATA_LEVEL", values.MaxDataLevel, fs, "llm-max-data-level")
	if values.AllowNoAPIKey || envIsSet("STEWARD_LLM_ALLOW_NO_API_KEY") || flagWasSet(fs, "llm-allow-no-api-key") {
		env["STEWARD_LLM_ALLOW_NO_API_KEY"] = strconv.FormatBool(values.AllowNoAPIKey)
	}
	if values.Timeout > 0 {
		env["STEWARD_LLM_TIMEOUT"] = values.Timeout.String()
	}
	if values.FailureThreshold > 0 {
		env["STEWARD_LLM_FAILURE_THRESHOLD"] = strconv.Itoa(values.FailureThreshold)
	}
	if values.FailureCooldown > 0 {
		env["STEWARD_LLM_FAILURE_COOLDOWN"] = values.FailureCooldown.String()
	}
	return env
}

func serviceInstallDiscoveryEnv(fs *flag.FlagSet, values serviceInstallDiscoveryFlagValues) map[string]string {
	env := map[string]string{}
	selected := values.Enabled || envIsSet("STEWARD_DISCOVERY_ENABLED") || flagWasSet(fs, "discovery-enabled")
	selected = selected || envIsSet("STEWARD_DEVICE_NAME") || flagWasSet(fs, "device-name")
	selected = selected || envIsSet("STEWARD_DISCOVERY_LISTEN_ADDR") || flagWasSet(fs, "discovery-listen-addr")
	selected = selected || envIsSet("STEWARD_DISCOVERY_TARGETS") || flagWasSet(fs, "discovery-targets")
	selected = selected || envIsSet("STEWARD_DISCOVERY_INTERVAL") || flagWasSet(fs, "discovery-interval")
	selected = selected || envIsSet("STEWARD_DISCOVERY_TTL") || flagWasSet(fs, "discovery-ttl")
	if !selected {
		return env
	}
	env["STEWARD_DISCOVERY_ENABLED"] = strconv.FormatBool(values.Enabled)
	env["STEWARD_DEVICE_NAME"] = strings.TrimSpace(values.DeviceName)
	env["STEWARD_DISCOVERY_LISTEN_ADDR"] = strings.TrimSpace(values.ListenAddr)
	env["STEWARD_DISCOVERY_TARGETS"] = strings.TrimSpace(values.Targets)
	env["STEWARD_DISCOVERY_INTERVAL"] = values.Interval.String()
	env["STEWARD_DISCOVERY_TTL"] = values.TTL.String()
	return env
}

func serviceInstallDiscoveryDefaultsFromEnv(agentID string) (serviceInstallDiscoveryFlagValues, error) {
	enabled, err := strictBoolEnv("STEWARD_DISCOVERY_ENABLED", false)
	if err != nil {
		return serviceInstallDiscoveryFlagValues{}, err
	}
	interval, err := strictPositiveDurationEnv("STEWARD_DISCOVERY_INTERVAL", peerdiscovery.DefaultInterval)
	if err != nil {
		return serviceInstallDiscoveryFlagValues{}, err
	}
	ttl, err := strictPositiveDurationEnv("STEWARD_DISCOVERY_TTL", peerdiscovery.DefaultTTL)
	if err != nil {
		return serviceInstallDiscoveryFlagValues{}, err
	}
	return serviceInstallDiscoveryFlagValues{
		Enabled:    enabled,
		DeviceName: envOrDefault("STEWARD_DEVICE_NAME", agentID),
		ListenAddr: envOrDefault("STEWARD_DISCOVERY_LISTEN_ADDR", peerdiscovery.DefaultListenAddr),
		Targets:    envOrDefault("STEWARD_DISCOVERY_TARGETS", ""),
		Interval:   interval,
		TTL:        ttl,
	}, nil
}

func strictBoolEnv(key string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return parsed, nil
}

func strictPositiveDurationEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return parsed, nil
}

func mergeServiceEnv(groups ...map[string]string) map[string]string {
	merged := map[string]string{}
	for _, group := range groups {
		for key, value := range group {
			merged[key] = value
		}
	}
	return merged
}

func setStringEnvIfPresent(env map[string]string, key string, value string, fs *flag.FlagSet, flagName string) {
	value = strings.TrimSpace(value)
	if value == "" && !envIsSet(key) && !flagWasSet(fs, flagName) {
		return
	}
	env[key] = value
}

func flagWasSet(fs *flag.FlagSet, name string) bool {
	seen := false
	fs.Visit(func(flag *flag.Flag) {
		if flag.Name == name {
			seen = true
		}
	})
	return seen
}

func serviceInstall(args []string) error {
	opts := servicecontrol.InstallOptions{
		Name:                        servicecontrol.DefaultName(),
		Scope:                       servicecontrol.DefaultScope(),
		DisplayName:                 servicecontrol.DefaultDisplayName,
		Description:                 servicecontrol.DefaultDescription,
		HTTPAddr:                    envOrDefault("HTTP_ADDR", "127.0.0.1:18080"),
		PeerHTTPAddr:                envOrDefault("STEWARD_PEER_HTTP_ADDR", ":18081"),
		DatabaseURL:                 envOrDefault("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable"),
		StorageDir:                  envOrDefault("STORAGE_DIR", "./data"),
		UIDir:                       envOrDefault("STEWARD_UI_DIR", ""),
		AgentID:                     envOrDefault("STEWARD_AGENT_ID", servicecontrol.DefaultName()),
		PublicAPIBase:               envOrDefault("STEWARD_PUBLIC_API_BASE", ""),
		SyncSecret:                  envOrDefault("STEWARD_SYNC_SECRET", ""),
		DevicePrivateKey:            envOrDefault("STEWARD_DEVICE_PRIVATE_KEY", ""),
		DevicePublicKey:             envOrDefault("STEWARD_DEVICE_PUBLIC_KEY", ""),
		SyncEncryptionKey:           envOrDefault("STEWARD_SYNC_ENCRYPTION_KEY", ""),
		SyncEncryptionKeyID:         envOrDefault("STEWARD_SYNC_ENCRYPTION_KEY_ID", ""),
		SyncEncryptionPreviousKeys:  envOrDefault("STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS", ""),
		LocalEncryptionKey:          envOrDefault("STEWARD_LOCAL_ENCRYPTION_KEY", ""),
		LocalEncryptionKeyID:        envOrDefault("STEWARD_LOCAL_ENCRYPTION_KEY_ID", ""),
		LocalEncryptionPreviousKeys: envOrDefault("STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS", ""),
		HeartbeatInterval:           envDurationOrDefault("STEWARD_HEARTBEAT_INTERVAL", time.Minute),
		SyncInterval:                envDurationOrDefault("STEWARD_SYNC_INTERVAL", 0),
		AutonomyInterval:            envDurationOrDefault("STEWARD_AUTONOMY_INTERVAL", 0),
		LogDir:                      envOrDefault("STEWARD_LOG_DIR", ""),
		ExtraEnv:                    map[string]string{},
	}
	if exe, err := os.Executable(); err == nil {
		opts.BinaryPath = exe
	}
	if wd, err := os.Getwd(); err == nil {
		opts.WorkDir = wd
	}
	discoveryDefaults, err := serviceInstallDiscoveryDefaultsFromEnv(opts.AgentID)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("steward service install", flag.ExitOnError)
	fs.StringVar(&opts.Name, "name", opts.Name, "Service name, launchd label, or systemd unit name")
	fs.StringVar(&opts.Scope, "scope", opts.Scope, "Service manager scope: user or system; Windows supports system only")
	fs.StringVar(&opts.DisplayName, "display-name", opts.DisplayName, "Human-readable service name")
	fs.StringVar(&opts.Description, "description", opts.Description, "Service description")
	fs.StringVar(&opts.BinaryPath, "binary", opts.BinaryPath, "Path to the steward executable")
	fs.StringVar(&opts.WorkDir, "workdir", opts.WorkDir, "Working directory for .env and relative storage paths")
	fs.StringVar(&opts.HTTPAddr, "http-addr", opts.HTTPAddr, "HTTP_ADDR for the local management API; keep this on loopback")
	fs.StringVar(&opts.PeerHTTPAddr, "peer-http-addr", opts.PeerHTTPAddr, "STEWARD_PEER_HTTP_ADDR for the restricted cross-device protocol API")
	fs.StringVar(&opts.DatabaseURL, "database-url", opts.DatabaseURL, "DATABASE_URL for the background steward API")
	fs.StringVar(&opts.StorageDir, "storage-dir", opts.StorageDir, "STORAGE_DIR for local files")
	fs.StringVar(&opts.UIDir, "ui-dir", opts.UIDir, "STEWARD_UI_DIR for optional built frontend workspace served by the management listener")
	fs.StringVar(&opts.AgentID, "agent-id", opts.AgentID, "Unique STEWARD_AGENT_ID for this device")
	fs.StringVar(&opts.PublicAPIBase, "public-api-base", opts.PublicAPIBase, "STEWARD_PUBLIC_API_BASE advertised to peer devices; point it at the peer listener")
	fs.StringVar(&opts.SyncSecret, "sync-secret", opts.SyncSecret, "STEWARD_SYNC_SECRET used to sign and verify peer sync traffic")
	fs.StringVar(&opts.DevicePrivateKey, "device-private-key", opts.DevicePrivateKey, "STEWARD_DEVICE_PRIVATE_KEY Ed25519 private key used to sign peer sync traffic")
	fs.StringVar(&opts.DevicePublicKey, "device-public-key", opts.DevicePublicKey, "STEWARD_DEVICE_PUBLIC_KEY Ed25519 public key advertised for this device")
	fs.StringVar(&opts.SyncEncryptionKey, "sync-encryption-key", opts.SyncEncryptionKey, "STEWARD_SYNC_ENCRYPTION_KEY AES-256-GCM key for peer sync payload encryption")
	fs.StringVar(&opts.SyncEncryptionKeyID, "sync-encryption-key-id", opts.SyncEncryptionKeyID, "STEWARD_SYNC_ENCRYPTION_KEY_ID advertised in encrypted sync payload envelopes")
	fs.StringVar(&opts.SyncEncryptionPreviousKeys, "sync-encryption-previous-keys", opts.SyncEncryptionPreviousKeys, "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS comma-separated key_id:base64 entries accepted for decrypt-only rotation")
	fs.StringVar(&opts.LocalEncryptionKey, "local-encryption-key", opts.LocalEncryptionKey, "STEWARD_LOCAL_ENCRYPTION_KEY AES-256-GCM key for local sync payload storage encryption")
	fs.StringVar(&opts.LocalEncryptionKeyID, "local-encryption-key-id", opts.LocalEncryptionKeyID, "STEWARD_LOCAL_ENCRYPTION_KEY_ID stored in local encrypted sync payload envelopes")
	fs.StringVar(&opts.LocalEncryptionPreviousKeys, "local-encryption-previous-keys", opts.LocalEncryptionPreviousKeys, "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS comma-separated key_id:base64 entries accepted for decrypt-only local key rotation")
	fs.DurationVar(&opts.HeartbeatInterval, "heartbeat-interval", opts.HeartbeatInterval, "STEWARD_HEARTBEAT_INTERVAL for agent heartbeat updates")
	fs.DurationVar(&opts.SyncInterval, "sync-interval", opts.SyncInterval, "STEWARD_SYNC_INTERVAL for background trusted-peer sync; 0 disables it")
	fs.DurationVar(&opts.AutonomyInterval, "autonomy-interval", opts.AutonomyInterval, "STEWARD_AUTONOMY_INTERVAL for background autonomy scans; 0 disables it")
	fs.StringVar(&opts.LogDir, "log-dir", opts.LogDir, "STEWARD_LOG_DIR for append-only service process logs")
	llmProvider := fs.String("llm-provider", envOrDefault("STEWARD_LLM_PROVIDER", ""), "STEWARD_LLM_PROVIDER for the S4 autonomy advisor; empty keeps it disabled")
	llmBaseURL := fs.String("llm-base-url", envOrDefault("STEWARD_LLM_BASE_URL", ""), "STEWARD_LLM_BASE_URL for an OpenAI-compatible advisor endpoint")
	llmModel := fs.String("llm-model", envOrDefault("STEWARD_LLM_MODEL", ""), "STEWARD_LLM_MODEL for the S4 autonomy advisor")
	llmAPIKey := fs.String("llm-api-key", envOrDefault("STEWARD_LLM_API_KEY", ""), "STEWARD_LLM_API_KEY for the S4 autonomy advisor; dry-run output is redacted")
	llmAllowNoAPIKey := fs.Bool("llm-allow-no-api-key", envBoolOrDefault("STEWARD_LLM_ALLOW_NO_API_KEY", false), "Set STEWARD_LLM_ALLOW_NO_API_KEY=true for local OpenAI-compatible endpoints")
	llmTimeout := fs.Duration("llm-timeout", envDurationOrDefault("STEWARD_LLM_TIMEOUT", 0), "STEWARD_LLM_TIMEOUT for advisor HTTP requests; 0 omits the value")
	llmMaxDataLevel := fs.String("llm-max-data-level", envOrDefault("STEWARD_LLM_MAX_DATA_LEVEL", ""), "STEWARD_LLM_MAX_DATA_LEVEL sent to the S4 autonomy advisor, normally D0 or D1")
	llmFailureThreshold := fs.Int("llm-failure-threshold", envIntOrDefault("STEWARD_LLM_FAILURE_THRESHOLD", 0), "STEWARD_LLM_FAILURE_THRESHOLD for advisor circuit breaking; 0 omits the value")
	llmFailureCooldown := fs.Duration("llm-failure-cooldown", envDurationOrDefault("STEWARD_LLM_FAILURE_COOLDOWN", 0), "STEWARD_LLM_FAILURE_COOLDOWN for advisor circuit breaking; 0 omits the value")
	autonomyRetryMaxAttempts := fs.Int("autonomy-retry-max-attempts", envIntOrDefault("STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS", 0), "Maximum automatic execution attempts before manual recovery is required; 0 omits the value")
	autonomyRetryBackoff := fs.Duration("autonomy-retry-backoff", envDurationOrDefault("STEWARD_AUTONOMY_RETRY_BACKOFF", 0), "Initial backoff between automatic execution retries; 0 omits the value")
	autonomyRetryMaxBackoff := fs.Duration("autonomy-retry-max-backoff", envDurationOrDefault("STEWARD_AUTONOMY_RETRY_MAX_BACKOFF", 0), "Maximum exponential backoff between automatic execution retries; 0 omits the value")
	discoveryEnabled := fs.Bool("discovery-enabled", discoveryDefaults.Enabled, "Enable signed LAN candidate discovery without automatically trusting devices")
	deviceName := fs.String("device-name", discoveryDefaults.DeviceName, "STEWARD_DEVICE_NAME advertised in signed discovery announcements")
	discoveryListenAddr := fs.String("discovery-listen-addr", discoveryDefaults.ListenAddr, "UDP listen address or multicast group for signed peer discovery")
	discoveryTargets := fs.String("discovery-targets", discoveryDefaults.Targets, "Comma-separated UDP discovery targets; empty uses the listen multicast group")
	discoveryInterval := fs.Duration("discovery-interval", discoveryDefaults.Interval, "Interval between signed peer discovery announcements")
	discoveryTTL := fs.Duration("discovery-ttl", discoveryDefaults.TTL, "Lifetime of a signed peer discovery candidate")
	fs.BoolVar(&opts.DryRun, "dry-run", false, "Print service actions without changing the system")
	strictSecurity := fs.Bool("strict-security", false, "Validate required S3/S4 security material before installing")
	startAfterInstall := fs.Bool("start", false, "Start the service after installing it")
	postVerify := servicePostVerifyOptions{}
	fs.BoolVar(&postVerify.Verify, "verify", false, "Run service verification after install/start")
	fs.DurationVar(&postVerify.StartupTimeout, "verify-startup-timeout", 30*time.Second, "Wait up to this long for post-install service verification to pass")
	fs.DurationVar(&postVerify.WatchDuration, "verify-watch-duration", 0, "Run post-install service verification in watch mode for this duration")
	fs.DurationVar(&postVerify.WatchInterval, "verify-watch-interval", time.Minute, "Interval between post-install verification samples when --verify-watch-duration is set")
	fs.BoolVar(&postVerify.AdvisorProbe, "verify-advisor-probe", false, "Call the configured S4 autonomy advisor during post-install verification")
	fs.BoolVar(&postVerify.AdvisorProbeEachSample, "verify-advisor-probe-each-sample", false, "When used with --verify-advisor-probe and --verify-watch-duration, call the advisor in every watch sample")
	fs.BoolVar(&postVerify.AdvisorPrivacyProbe, "verify-advisor-privacy-probe", false, "Verify the S4 autonomy advisor rejects D2 data during post-install verification")
	fs.StringVar(&postVerify.EvidenceDir, "verify-evidence-dir", "", "Write post-install verification evidence JSON to this directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	opts.UIDir = resolveStewardUIDir(opts.UIDir, opts.BinaryPath)
	if opts.DryRun && *startAfterInstall {
		return fmt.Errorf("service install --start cannot be used with --dry-run")
	}
	if opts.DryRun && postVerify.Verify {
		return fmt.Errorf("service install --verify cannot be used with --dry-run")
	}
	if err := validateServicePostVerifyOptions("service install", postVerify); err != nil {
		return err
	}
	retryEnv := map[string]string{}
	if *autonomyRetryMaxAttempts > 0 {
		retryEnv["STEWARD_AUTONOMY_RETRY_MAX_ATTEMPTS"] = strconv.Itoa(*autonomyRetryMaxAttempts)
	}
	if *autonomyRetryBackoff > 0 {
		retryEnv["STEWARD_AUTONOMY_RETRY_BACKOFF"] = autonomyRetryBackoff.String()
	}
	if *autonomyRetryMaxBackoff > 0 {
		retryEnv["STEWARD_AUTONOMY_RETRY_MAX_BACKOFF"] = autonomyRetryMaxBackoff.String()
	}
	opts.ExtraEnv = mergeServiceEnv(serviceInstallAdvisorEnv(fs, serviceInstallAdvisorFlagValues{
		Provider:         *llmProvider,
		BaseURL:          *llmBaseURL,
		Model:            *llmModel,
		APIKey:           *llmAPIKey,
		AllowNoAPIKey:    *llmAllowNoAPIKey,
		Timeout:          *llmTimeout,
		MaxDataLevel:     *llmMaxDataLevel,
		FailureThreshold: *llmFailureThreshold,
		FailureCooldown:  *llmFailureCooldown,
	}), retryEnv, serviceInstallDiscoveryEnv(fs, serviceInstallDiscoveryFlagValues{
		Enabled:    *discoveryEnabled,
		DeviceName: *deviceName,
		ListenAddr: *discoveryListenAddr,
		Targets:    *discoveryTargets,
		Interval:   *discoveryInterval,
		TTL:        *discoveryTTL,
	}))
	if err := validateServiceDiscoveryEnvironment(opts); err != nil {
		return err
	}
	if *strictSecurity {
		if err := validateStrictServiceSecurity(opts); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := servicecontrol.Install(ctx, opts)
	if err != nil {
		return err
	}
	output := serviceInstallOutput{
		Service:      result,
		Verification: serviceEnvVerificationAdviceFromEnvironmentForPlatform(result.Name, result.Scope, result.Environment, result.Platform),
	}
	if *startAfterInstall {
		startResult, err := servicecontrol.Start(ctx, result.Name, result.Scope, false)
		if err != nil {
			output.StartError = err.Error()
			if printErr := printJSON(output); printErr != nil {
				return printErr
			}
			return fmt.Errorf("service installed but start failed: %w", err)
		}
		output.Start = &startResult
		status, err := servicecontrol.Status(ctx, result.Name, result.Scope)
		if err != nil {
			output.StatusError = err.Error()
			if printErr := printJSON(output); printErr != nil {
				return printErr
			}
			return fmt.Errorf("service start requested but status check failed: %w", err)
		}
		output.Status = &status
	}
	if postVerify.Verify {
		verification := runServiceVerificationForEnvironment(result.Name, result.Scope, result.Environment, postVerify)
		output.VerificationResult = &verification
		evidencePath, err := writeServicePostVerificationEvidence("service-install", postVerify, verification)
		if err != nil {
			output.VerificationError = err.Error()
			if printErr := printJSON(output); printErr != nil {
				return printErr
			}
			return err
		}
		output.VerificationEvidencePath = evidencePath
		if !verification.OK {
			output.VerificationError = "service verification failed"
			if printErr := printJSON(output); printErr != nil {
				return printErr
			}
			return fmt.Errorf("service installed but verification failed")
		}
	}
	return printJSON(output)
}

func servicePlan(args []string) error {
	fs := flag.NewFlagSet("steward service plan", flag.ExitOnError)
	name := fs.String("name", "", "Service name, launchd label, or systemd unit name; defaults per target platform")
	targets := fs.String("target", "windows,darwin,linux", "Comma-separated target platforms: windows,darwin,linux")
	currentEnvFile := fs.String("current-env-file", "", "JSON current service environment to render into service install plans")
	displayName := fs.String("display-name", servicecontrol.DefaultDisplayName, "Human-readable service name")
	description := fs.String("description", servicecontrol.DefaultDescription, "Service description")
	scope := fs.String("scope", "", "Service manager scope for all target platforms: user or system; empty uses each platform default")
	binaryPath := fs.String("binary", "", "Path to the steward executable")
	workDir := fs.String("workdir", "", "Working directory for .env and relative storage paths")
	uiDir := fs.String("ui-dir", "", "STEWARD_UI_DIR for optional built frontend workspace served by the management listener")
	logDir := fs.String("log-dir", "", "STEWARD_LOG_DIR for append-only service process logs")
	strictSecurity := fs.Bool("strict-security", false, "Validate required S3/S4 security material before rendering plans")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*currentEnvFile) == "" {
		return fmt.Errorf("service plan requires --current-env-file")
	}
	env, err := readCurrentServiceEnvFile(*currentEnvFile)
	if err != nil {
		return err
	}
	targetList, err := servicePlanTargets(*targets)
	if err != nil {
		return err
	}
	if strings.TrimSpace(*binaryPath) == "" {
		if exe, exeErr := os.Executable(); exeErr == nil {
			*binaryPath = exe
		}
	}
	if strings.TrimSpace(*workDir) == "" {
		if wd, wdErr := os.Getwd(); wdErr == nil {
			*workDir = wd
		}
	}

	plans := make([]servicecontrol.InstallPlan, 0, len(targetList))
	verificationByPlatform := map[string]*serviceEnvVerificationAdvice{}
	for _, target := range targetList {
		targetName := strings.TrimSpace(*name)
		if targetName == "" {
			targetName = servicecontrol.DefaultNameForPlatform(target)
		}
		options := serviceInstallOptionsFromEnv(targetName, env)
		options.Scope = strings.TrimSpace(*scope)
		options.DisplayName = *displayName
		options.Description = *description
		options.BinaryPath = *binaryPath
		options.WorkDir = *workDir
		if strings.TrimSpace(*uiDir) != "" {
			options.UIDir = *uiDir
		}
		options.LogDir = *logDir
		if err := validateServiceDiscoveryEnvironment(options); err != nil {
			return err
		}
		if *strictSecurity {
			if err := validateStrictServiceSecurity(options); err != nil {
				return err
			}
		}
		plan, err := servicecontrol.PlanInstall(target, options)
		if err != nil {
			return err
		}
		plans = append(plans, plan)
		verificationByPlatform[target] = serviceEnvVerificationAdviceFromEnvironmentForPlatform(plan.Name, plan.Scope, plan.Environment, target)
	}
	output := servicePlanOutput{
		Plans:                  plans,
		Verification:           verificationByPlatform[targetList[0]],
		VerificationByPlatform: verificationByPlatform,
	}
	return printJSON(output)
}

func servicePlanTargets(value string) ([]string, error) {
	items := strings.Split(value, ",")
	targets := []string{}
	seen := map[string]struct{}{}
	for _, item := range items {
		target, err := servicecontrol.NormalizeTargetPlatform(item)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[target]; ok {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("service plan requires at least one target platform")
	}
	return targets, nil
}

func serviceEnv(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printServiceEnvUsage()
		return nil
	}
	action := strings.TrimSpace(args[0])
	if action != "plan" && action != "apply" {
		return fmt.Errorf("unknown service env command %q", action)
	}
	fs := flag.NewFlagSet("steward service env "+action, flag.ExitOnError)
	name := fs.String("name", servicecontrol.DefaultName(), "Service name, launchd label, or systemd unit name")
	scope := fs.String("scope", servicecontrol.DefaultScope(), "Service manager scope: user or system; Windows supports system only")
	currentEnvFile := fs.String("current-env-file", "", "Plan against this JSON current environment without reading the service manager; only supported with plan")
	fromPairing := fs.String("from-pairing", "", "Optional pairing bundle path whose suggested_env should be applied")
	decryptSharedSyncKey := fs.String("decrypt-shared-sync-key", envOrDefault("STEWARD_PAIRING_PRIVATE_KEY", ""), "Recipient X25519 private key used to decrypt encrypted shared sync material")
	requireSignature := fs.Bool("require-signature", false, "Reject pairing bundles that do not carry a valid Ed25519 bundle signature")
	confirm := fs.Bool("confirm", false, "Required for service env apply")
	restart := fs.Bool("restart", false, "Restart the service after a confirmed apply and print the post-restart service status")
	strictSecurity := fs.Bool("strict-security", false, "Validate the full target service environment before printing or applying it")
	postVerify := servicePostVerifyOptions{}
	fs.BoolVar(&postVerify.Verify, "verify", false, "Run service verification after a confirmed apply and restart")
	fs.DurationVar(&postVerify.StartupTimeout, "verify-startup-timeout", 30*time.Second, "Wait up to this long for post-apply service verification to pass")
	fs.DurationVar(&postVerify.WatchDuration, "verify-watch-duration", 0, "Run post-apply service verification in watch mode for this duration")
	fs.DurationVar(&postVerify.WatchInterval, "verify-watch-interval", time.Minute, "Interval between post-apply verification samples when --verify-watch-duration is set")
	fs.BoolVar(&postVerify.AdvisorProbe, "verify-advisor-probe", false, "Call the configured S4 autonomy advisor during post-apply verification")
	fs.BoolVar(&postVerify.AdvisorProbeEachSample, "verify-advisor-probe-each-sample", false, "When used with --verify-advisor-probe and --verify-watch-duration, call the advisor in every watch sample")
	fs.BoolVar(&postVerify.AdvisorPrivacyProbe, "verify-advisor-privacy-probe", false, "Verify the S4 autonomy advisor rejects D2 data during post-apply verification")
	fs.StringVar(&postVerify.EvidenceDir, "verify-evidence-dir", "", "Write post-apply verification evidence JSON to this directory")
	rotateSyncKeyID := fs.String("rotate-sync-key-id", "", "Generate a new STEWARD_SYNC_ENCRYPTION_KEY with this id and keep the current key as a previous decrypt-only key")
	rotateLocalKeyID := fs.String("rotate-local-key-id", "", "Generate a new STEWARD_LOCAL_ENCRYPTION_KEY with this id and keep the current key as a previous decrypt-only key")
	var setFlags stringListFlag
	var removeFlags stringListFlag
	fs.Var(&setFlags, "set", "Set an environment value as KEY=VALUE; can be repeated")
	fs.Var(&removeFlags, "remove", "Remove an environment value by KEY; can be repeated")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	set := map[string]string{}
	for _, item := range setFlags {
		key, value, ok := strings.Cut(item, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return fmt.Errorf("--set must use KEY=VALUE")
		}
		set[key] = value
	}
	if strings.TrimSpace(*fromPairing) != "" {
		bundle, err := readPairingBundle(*fromPairing)
		if err != nil {
			return err
		}
		_, suggestedEnv, err := pairingImportPayload(bundle, true, "", *decryptSharedSyncKey, *requireSignature)
		if err != nil {
			return err
		}
		if len(suggestedEnv) == 0 {
			return fmt.Errorf("pairing bundle does not contain suggested service environment values")
		}
		for key, value := range suggestedEnv {
			set[key] = value
		}
	}
	rotations := serviceEnvRotationOptions{
		SyncKeyID:  *rotateSyncKeyID,
		LocalKeyID: *rotateLocalKeyID,
	}
	if err := validateServiceEnvRotationConflicts(set, []string(removeFlags), rotations); err != nil {
		return err
	}
	if action == "apply" && !*confirm {
		return fmt.Errorf("service env apply requires --confirm")
	}
	if action == "plan" && *restart {
		return fmt.Errorf("service env plan does not support --restart")
	}
	if action == "plan" && postVerify.Verify {
		return fmt.Errorf("service env plan does not support --verify")
	}
	if action == "apply" && strings.TrimSpace(*currentEnvFile) != "" {
		return fmt.Errorf("service env apply does not support --current-env-file")
	}
	if err := validateServicePostVerifyOptions("service env", postVerify); err != nil {
		return err
	}
	if action == "apply" && postVerify.Verify && !*restart {
		return fmt.Errorf("service env apply --verify requires --restart so the target environment is loaded")
	}

	timeout := 30 * time.Second
	if *restart {
		timeout = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	patchOptions := servicecontrol.EnvPatchOptions{
		Name:            *name,
		Scope:           *scope,
		Set:             set,
		Remove:          []string(removeFlags),
		DryRun:          action == "plan",
		TransformTarget: rotations.transform(),
		ValidateTarget: func(target map[string]string) error {
			options := serviceInstallOptionsFromEnv(*name, target)
			options.Scope = *scope
			if err := validateServiceDiscoveryEnvironment(options); err != nil {
				return err
			}
			if !*strictSecurity {
				return nil
			}
			return validateStrictServiceSecurity(options)
		},
	}
	var result servicecontrol.Result
	var err error
	if strings.TrimSpace(*currentEnvFile) != "" {
		currentEnv, readErr := readCurrentServiceEnvFile(*currentEnvFile)
		if readErr != nil {
			return readErr
		}
		result, err = servicecontrol.PlanEnvironmentPatch(currentEnv, patchOptions)
	} else {
		result, err = servicecontrol.PatchEnvironment(ctx, patchOptions)
	}
	if err != nil {
		return err
	}
	output := serviceEnvApplyOutput{
		ServiceEnv:   result,
		Verification: serviceEnvVerificationAdviceFromEnvironmentForPlatform(result.Name, result.Scope, result.Environment, result.Platform),
	}
	if action == "apply" && *restart {
		restartResult, err := servicecontrol.Restart(ctx, *name, *scope, false)
		if err != nil {
			output.RestartError = err.Error()
			if printErr := printJSON(output); printErr != nil {
				return printErr
			}
			return fmt.Errorf("service env updated but restart failed: %w", err)
		}
		output.Restart = &restartResult
		status, err := servicecontrol.Status(ctx, *name, *scope)
		if err != nil {
			output.StatusError = err.Error()
			if printErr := printJSON(output); printErr != nil {
				return printErr
			}
			return fmt.Errorf("service restarted but status check failed: %w", err)
		}
		output.Status = &status
	}
	if action == "apply" && postVerify.Verify {
		verification := runServiceVerificationForEnvironment(*name, *scope, result.Environment, postVerify)
		output.VerificationResult = &verification
		evidencePath, err := writeServicePostVerificationEvidence("service-env", postVerify, verification)
		if err != nil {
			output.VerificationError = err.Error()
			if printErr := printJSON(output); printErr != nil {
				return printErr
			}
			return err
		}
		output.VerificationEvidencePath = evidencePath
		if !verification.OK {
			output.VerificationError = "service verification failed"
			if printErr := printJSON(output); printErr != nil {
				return printErr
			}
			return fmt.Errorf("service env updated and restarted but verification failed")
		}
	}
	return printJSON(output)
}

func readCurrentServiceEnvFile(path string) (map[string]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, fmt.Errorf("--current-env-file is required")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open current env file: %w", err)
	}
	defer file.Close()

	raw := map[string]any{}
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode current env file: %w", err)
	}
	env := map[string]string{}
	for key, value := range raw {
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("current env file contains an empty key")
		}
		stringValue, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("current env value %s must be a string", key)
		}
		env[key] = stringValue
	}
	if len(env) == 0 {
		return nil, fmt.Errorf("current env file must contain at least one value")
	}
	return env, nil
}

func serviceSimpleAction(args []string, action string) error {
	name, scope, dryRun, err := parseServiceActionFlags(action, args)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result servicecontrol.Result
	switch action {
	case "uninstall":
		result, err = servicecontrol.Uninstall(ctx, name, scope, dryRun)
	case "start":
		result, err = servicecontrol.Start(ctx, name, scope, dryRun)
	case "stop":
		result, err = servicecontrol.Stop(ctx, name, scope, dryRun)
	case "restart":
		result, err = servicecontrol.Restart(ctx, name, scope, dryRun)
	default:
		err = fmt.Errorf("unknown service action %q", action)
	}
	if err != nil {
		return err
	}
	return printJSON(map[string]servicecontrol.Result{"service": result})
}

func serviceStatus(args []string) error {
	fs := flag.NewFlagSet("steward service status", flag.ExitOnError)
	name := fs.String("name", servicecontrol.DefaultName(), "Service name, launchd label, or systemd unit name")
	scope := fs.String("scope", servicecontrol.DefaultScope(), "Service manager scope: user or system; Windows supports system only")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := servicecontrol.Status(ctx, *name, *scope)
	if err != nil {
		return err
	}
	return printJSON(map[string]servicecontrol.StatusResult{"service": result})
}

func parseServiceActionFlags(action string, args []string) (string, string, bool, error) {
	fs := flag.NewFlagSet("steward service "+action, flag.ExitOnError)
	name := fs.String("name", servicecontrol.DefaultName(), "Service name, launchd label, or systemd unit name")
	scope := fs.String("scope", servicecontrol.DefaultScope(), "Service manager scope: user or system; Windows supports system only")
	dryRun := fs.Bool("dry-run", false, "Print service action without changing the system")
	if err := fs.Parse(args); err != nil {
		return "", "", false, err
	}
	return *name, *scope, *dryRun, nil
}

func printServiceUsage() {
	fmt.Fprintln(stdout, `usage: steward service <install|plan|env|uninstall|start|stop|restart|status> [flags]

service commands:
  install       install the current steward binary as the native platform service
                Windows Service, macOS LaunchAgent/LaunchDaemon, or Linux systemd unit
  plan          render offline Windows/macOS/Linux install artifacts without writing them
  env plan      preview service environment changes without writing them
  env apply     write service environment changes; requires --confirm
  uninstall     remove the installed service entry
  start         request native service start
  stop          request native service stop
  restart       request native service restart
  status        print native service status

common install flags:
  --scope user|system                  choose service manager scope; Windows supports system only
  --strict-security                    require S3/S4 service safety prerequisites
  --start --verify                     start service and run post-install verification
  --verify-watch-duration 24h          prove long-running heartbeat after install
  --verify-evidence-dir <dir>          persist post-install verification evidence
  --ui-dir <dir>                       serve built steward workspace on management listener
  --llm-provider openai-compatible     persist optional S4 advisor provider
  --llm-base-url <url>                 OpenAI-compatible base URL
  --llm-model <model>                  advisor model name
  --llm-api-key <key>                  advisor API key; redacted in output
  --discovery-enabled                  enable signed LAN candidate discovery
  --discovery-listen-addr <udp-addr>   multicast group or explicit UDP listener
  --discovery-targets <udp-addr,...>   explicit announcement targets when multicast is unavailable

examples:
  steward service install --dry-run --strict-security
  steward service plan --current-env-file .\current-service-env.json --target windows,darwin,linux --strict-security
  steward service install --strict-security --start --verify --verify-evidence-dir .\evidence\s3s4
  steward service env plan --from-pairing .\peer-pairing.encrypted.json --require-signature --strict-security

see also:
  steward help service env`)
}

func printServiceEnvUsage() {
	fmt.Fprintln(stdout, `usage: steward service env <plan|apply> [flags]

service env commands:
  plan      preview the target service environment without writing service manager state
  apply     write the target service environment; requires --confirm

common flags:
  --scope user|system                  target the user or system service manager entry
  --set KEY=VALUE                      set an environment variable; repeatable
  --remove KEY                         remove an environment variable; repeatable
  --from-pairing <file>                import suggested_env from a pairing bundle
  --decrypt-shared-sync-key <key>      decrypt sealed shared sync material
  --require-signature                  reject unsigned pairing bundles
  --strict-security                    validate the full target service environment
  --rotate-sync-key-id <id>            generate a new shared sync AES key
  --rotate-local-key-id <id>           generate a new local-at-rest AES key
  --restart --verify                   restart and verify after apply
  --verify-watch-duration 24h          prove long-running heartbeat after apply
  --verify-evidence-dir <dir>          persist post-apply verification evidence

examples:
  steward service env plan --set STEWARD_SYNC_INTERVAL=5m
  steward service env apply --from-pairing .\peer-pairing.encrypted.json --require-signature --strict-security --confirm --restart --verify`)
}
