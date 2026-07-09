package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"mongojson/backend/internal/app"
	"mongojson/backend/internal/buildinfo"
	"mongojson/backend/internal/platform/servicecontrol"
)

const defaultAPIBase = "http://127.0.0.1:18080/api"

var stdout io.Writer = os.Stdout

type cli struct {
	apiBase string
	client  *http.Client
}

func main() {
	fs := flag.NewFlagSet("steward", flag.ExitOnError)
	fs.Usage = printUsage
	apiBase := fs.String("api", envOrDefault("STEWARD_API_BASE", defaultAPIBase), "Steward API base URL")
	_ = fs.Parse(os.Args[1:])

	args := fs.Args()
	if len(args) == 0 {
		printUsage()
		os.Exit(2)
	}

	command := args[0]
	if command == "run" {
		if err := runServer(args[1:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if command == "keygen" {
		if err := keygen(args[1:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if command == "sync-keygen" {
		if err := syncKeygen(args[1:]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if command == "version" {
		if err := printVersion(); err != nil {
			log.Fatal(err)
		}
		return
	}

	c := cli{
		apiBase: strings.TrimRight(*apiBase, "/"),
		client:  &http.Client{Timeout: 5 * time.Second},
	}
	if err := c.run(command, args[1:]); err != nil {
		log.Fatal(err)
	}
}

func runServer(args []string) error {
	fs := flag.NewFlagSet("steward run", flag.ExitOnError)
	workDir := fs.String("workdir", "", "Working directory used before loading .env and local data")
	serviceName := fs.String("service-name", servicecontrol.DefaultName(), "System service name when running under a service manager")
	logDir := fs.String("log-dir", envOrDefault("STEWARD_LOG_DIR", ""), "Append process logs to this directory")
	uiDir := fs.String("ui-dir", envOrDefault("STEWARD_UI_DIR", ""), "Serve the built steward workspace from this directory")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*workDir) != "" {
		if err := os.Chdir(*workDir); err != nil {
			return fmt.Errorf("change workdir: %w", err)
		}
	}
	if strings.TrimSpace(*uiDir) != "" {
		if err := os.Setenv("STEWARD_UI_DIR", *uiDir); err != nil {
			return fmt.Errorf("set STEWARD_UI_DIR: %w", err)
		}
	}
	cleanupLogs, err := configureServiceLogging(*logDir, *serviceName)
	if err != nil {
		return err
	}
	defer cleanupLogs()
	return servicecontrol.Run(*serviceName, app.Run)
}

func configureServiceLogging(logDir string, serviceName string) (func(), error) {
	logDir = strings.TrimSpace(logDir)
	if logDir == "" {
		return func() {}, nil
	}
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	logPath := filepath.Join(logDir, serviceLogFileName(serviceName))
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log file: %w", err)
	}
	previous := log.Writer()
	log.SetOutput(io.MultiWriter(previous, file))
	log.Printf("steward log file enabled at %s", logPath)
	return func() {
		log.SetOutput(previous)
		_ = file.Close()
	}, nil
}

func serviceLogFileName(serviceName string) string {
	serviceName = defaultString(serviceName, servicecontrol.DefaultName())
	var builder strings.Builder
	for _, r := range serviceName {
		switch {
		case r >= 'a' && r <= 'z':
			builder.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			builder.WriteRune(r)
		case r >= '0' && r <= '9':
			builder.WriteRune(r)
		case r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}
	name := strings.Trim(builder.String(), "._-")
	if name == "" {
		name = "steward"
	}
	return name + ".log"
}

func keygen(args []string) error {
	fs := flag.NewFlagSet("steward keygen", flag.ExitOnError)
	prefix := fs.String("prefix", "", "Optional device id or label echoed in the output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate device keypair: %w", err)
	}
	publicKeyText := base64.StdEncoding.EncodeToString(publicKey)
	privateKeyText := base64.StdEncoding.EncodeToString(privateKey)
	payload := map[string]any{
		"algorithm":   "ed25519",
		"public_key":  publicKeyText,
		"private_key": privateKeyText,
		"env": map[string]string{
			"STEWARD_DEVICE_PUBLIC_KEY":  publicKeyText,
			"STEWARD_DEVICE_PRIVATE_KEY": privateKeyText,
		},
	}
	if strings.TrimSpace(*prefix) != "" {
		payload["label"] = strings.TrimSpace(*prefix)
	}
	return printJSON(payload)
}

func syncKeygen(args []string) error {
	fs := flag.NewFlagSet("steward sync-keygen", flag.ExitOnError)
	keyID := fs.String("key-id", "default", "Sync encryption key id shared by trusted devices")
	if err := fs.Parse(args); err != nil {
		return err
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("generate sync encryption key: %w", err)
	}
	keyText := base64.StdEncoding.EncodeToString(key)
	return printJSON(map[string]any{
		"algorithm": "aes-256-gcm",
		"key_id":    strings.TrimSpace(*keyID),
		"key":       keyText,
		"env": map[string]string{
			"STEWARD_SYNC_ENCRYPTION_KEY":    keyText,
			"STEWARD_SYNC_ENCRYPTION_KEY_ID": strings.TrimSpace(*keyID),
		},
	})
}

func printVersion() error {
	return printJSON(buildinfo.Info())
}

func (c cli) run(command string, args []string) error {
	switch command {
	case "help", "-h", "--help":
		printUsageTopic(args)
		return nil
	case "doctor":
		return c.doctor()
	case "status":
		return c.status()
	case "start":
		return c.printRequest(http.MethodPost, "/steward/agent/start", nil)
	case "stop":
		return c.printRequest(http.MethodPost, "/steward/agent/stop", nil)
	case "sync-status":
		return c.printRequest(http.MethodGet, "/steward/sync/status", nil)
	case "sync-device":
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return fmt.Errorf("sync-device requires a device id")
		}
		return c.printRequest(http.MethodPost, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[0]))+"/sync", nil)
	case "devices":
		return c.devices(args)
	case "pairing":
		return c.pairing(args)
	case "service":
		return c.service(args)
	case "autonomy":
		return c.autonomy(args)
	case "verify":
		return c.verify(args)
	default:
		printUsage()
		return fmt.Errorf("unknown command %q", command)
	}
}

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

func (c cli) devices(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printDevicesUsage()
		return nil
	}
	if len(args) == 0 {
		return c.printRequest(http.MethodGet, "/steward/devices", nil)
	}
	switch args[0] {
	case "list", "status":
		return c.printRequest(http.MethodGet, "/steward/devices", nil)
	case "register":
		return c.registerDevice(args[1:])
	case "revoke":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("devices revoke requires a device id")
		}
		return c.printRequest(http.MethodPost, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[1]))+"/revoke", nil)
	case "permissions":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("devices permissions requires a device id")
		}
		return c.printRequest(http.MethodGet, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[1]))+"/permissions", nil)
	case "permission-set":
		return c.setDevicePermission(args[1:])
	case "verify":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("devices verify requires a device id")
		}
		return c.printRequest(http.MethodPost, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[1]))+"/verify", nil)
	case "sync":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("devices sync requires a device id")
		}
		return c.printRequest(http.MethodPost, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[1]))+"/sync", nil)
	default:
		return fmt.Errorf("unknown devices command %q", args[0])
	}
}

func (c cli) registerDevice(args []string) error {
	fs := flag.NewFlagSet("steward devices register", flag.ExitOnError)
	id := fs.String("id", "", "Peer device id")
	name := fs.String("name", "", "Peer device name")
	platform := fs.String("platform", "unknown", "Peer platform: windows, darwin, linux, or unknown")
	apiBaseURL := fs.String("api-base-url", "", "Peer Steward API base URL, for example http://192.168.1.12:18080/api")
	permissionLevel := fs.String("permission-level", "A3", "Default peer permission ceiling")
	publicKey := fs.String("public-key", "", "Peer Ed25519 public key from steward keygen")
	syncEnabled := fs.Bool("sync-enabled", true, "Whether the peer can participate in sync")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("devices register requires --id")
	}
	payload := map[string]any{
		"id":               strings.TrimSpace(*id),
		"device_name":      defaultString(*name, strings.TrimSpace(*id)),
		"platform":         strings.TrimSpace(*platform),
		"role":             "peer",
		"sync_enabled":     *syncEnabled,
		"permission_level": strings.TrimSpace(*permissionLevel),
		"public_key":       strings.TrimSpace(*publicKey),
		"api_base_url":     strings.TrimRight(strings.TrimSpace(*apiBaseURL), "/"),
	}
	return c.printRequest(http.MethodPost, "/steward/devices", payload)
}

type cliDevicePermission struct {
	DeviceID           string `json:"device_id"`
	Capability         string `json:"capability"`
	Policy             string `json:"policy"`
	MaxPermissionLevel string `json:"max_permission_level"`
	ScopeSummary       string `json:"scope_summary"`
}

func (c cli) setDevicePermission(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("devices permission-set requires a device id, capability, and allow, confirm, or deny")
	}
	deviceID := strings.TrimSpace(args[0])
	capability := strings.TrimSpace(args[1])
	policy := strings.TrimSpace(args[2])
	if deviceID == "" || capability == "" {
		return fmt.Errorf("devices permission-set requires a device id and capability")
	}
	if !validCLIDevicePermissionPolicy(policy) {
		return fmt.Errorf("unsupported device permission policy %q", policy)
	}
	current, found, err := c.findDevicePermission(deviceID, capability)
	if err != nil {
		return err
	}
	maxPermissionLevel := ""
	if len(args) >= 4 {
		maxPermissionLevel = strings.TrimSpace(args[3])
	}
	if maxPermissionLevel == "" {
		if found {
			maxPermissionLevel = current.MaxPermissionLevel
		} else {
			maxPermissionLevel = "A3"
		}
	}
	if !validCLIPermissionLevel(maxPermissionLevel) {
		return fmt.Errorf("invalid max permission level %q", maxPermissionLevel)
	}
	payload := map[string]any{
		"policy":               policy,
		"max_permission_level": maxPermissionLevel,
	}
	if found && strings.TrimSpace(current.ScopeSummary) != "" {
		payload["scope_summary"] = current.ScopeSummary
	}
	return c.printRequest(
		http.MethodPut,
		"/steward/devices/"+url.PathEscape(deviceID)+"/permissions/"+url.PathEscape(capability),
		payload,
	)
}

func (c cli) findDevicePermission(deviceID string, capability string) (cliDevicePermission, bool, error) {
	body, err := c.request(http.MethodGet, "/steward/devices/"+url.PathEscape(deviceID)+"/permissions", nil)
	if err != nil {
		return cliDevicePermission{}, false, err
	}
	var response struct {
		Permissions []cliDevicePermission `json:"permissions"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return cliDevicePermission{}, false, fmt.Errorf("decode device permissions: %w", err)
	}
	for _, permission := range response.Permissions {
		if permission.Capability == capability {
			return permission, true, nil
		}
	}
	return cliDevicePermission{}, false, nil
}

func validCLIDevicePermissionPolicy(policy string) bool {
	switch strings.TrimSpace(policy) {
	case "allow", "confirm", "deny":
		return true
	default:
		return false
	}
}

func validCLIPermissionLevel(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	return len(value) == 2 && value[0] == 'A' && value[1] >= '0' && value[1] <= '9'
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
	if opts.DryRun && *startAfterInstall {
		return fmt.Errorf("service install --start cannot be used with --dry-run")
	}
	if opts.DryRun && postVerify.Verify {
		return fmt.Errorf("service install --verify cannot be used with --dry-run")
	}
	if err := validateServicePostVerifyOptions("service install", postVerify); err != nil {
		return err
	}
	opts.ExtraEnv = serviceInstallAdvisorEnv(fs, serviceInstallAdvisorFlagValues{
		Provider:         *llmProvider,
		BaseURL:          *llmBaseURL,
		Model:            *llmModel,
		APIKey:           *llmAPIKey,
		AllowNoAPIKey:    *llmAllowNoAPIKey,
		Timeout:          *llmTimeout,
		MaxDataLevel:     *llmMaxDataLevel,
		FailureThreshold: *llmFailureThreshold,
		FailureCooldown:  *llmFailureCooldown,
	})
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
			if !*strictSecurity {
				return nil
			}
			options := serviceInstallOptionsFromEnv(*name, target)
			options.Scope = *scope
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

func (c cli) doctor() error {
	root := strings.TrimSuffix(c.apiBase, "/api")
	checks := []string{root + "/healthz", root + "/readyz", c.apiBase + "/steward/agent"}
	result := map[string]any{}
	for _, endpoint := range checks {
		body, err := c.requestURL(http.MethodGet, endpoint, nil)
		if err != nil {
			result[endpoint] = map[string]string{"status": "error", "error": err.Error()}
			continue
		}
		var decoded any
		if err := json.Unmarshal(body, &decoded); err != nil {
			result[endpoint] = string(body)
			continue
		}
		result[endpoint] = decoded
	}
	return printJSON(result)
}

func (c cli) status() error {
	payload := map[string]any{}
	for key, path := range map[string]string{
		"agent":    "/steward/agent",
		"sync":     "/steward/sync/status",
		"autonomy": "/steward/autonomy",
	} {
		body, err := c.request(http.MethodGet, path, nil)
		if err != nil {
			payload[key] = map[string]string{"error": err.Error()}
			continue
		}
		var decoded map[string]any
		if err := json.Unmarshal(body, &decoded); err != nil {
			payload[key] = string(body)
			continue
		}
		payload[key] = decoded
	}
	return printJSON(payload)
}

func (c cli) autonomy(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printAutonomyUsage()
		return nil
	}
	if len(args) == 0 {
		return c.printRequest(http.MethodGet, "/steward/autonomy", nil)
	}
	switch args[0] {
	case "status":
		return c.printRequest(http.MethodGet, "/steward/autonomy", nil)
	case "pause":
		return c.printRequest(http.MethodPatch, "/steward/autonomy/settings", map[string]any{"paused": true})
	case "resume":
		return c.printRequest(http.MethodPatch, "/steward/autonomy/settings", map[string]any{"paused": false})
	case "run":
		return c.printRequest(http.MethodPost, "/steward/autonomy/run", nil)
	case "mode":
		if len(args) < 2 {
			return fmt.Errorf("autonomy mode requires suggest_only or controlled")
		}
		mode := strings.TrimSpace(args[1])
		if mode != "suggest_only" && mode != "controlled" {
			return fmt.Errorf("unsupported autonomy mode %q", mode)
		}
		return c.printRequest(http.MethodPatch, "/steward/autonomy/settings", map[string]any{"mode": mode})
	case "rules":
		return c.printRequest(http.MethodGet, "/steward/autonomy/rules", nil)
	case "rule-policy":
		if len(args) < 3 {
			return fmt.Errorf("autonomy rule-policy requires a rule id or name and suggest, confirm, auto, or never")
		}
		policy := strings.TrimSpace(args[2])
		if policy != "suggest" && policy != "confirm" && policy != "auto" && policy != "never" {
			return fmt.Errorf("unsupported autonomy rule policy %q", policy)
		}
		return c.updateAutonomyRule(strings.TrimSpace(args[1]), map[string]any{"policy": policy})
	case "rule-enable", "rule-disable":
		if len(args) < 2 {
			return fmt.Errorf("autonomy %s requires a rule id or name", args[0])
		}
		return c.updateAutonomyRule(strings.TrimSpace(args[1]), map[string]any{"enabled": args[0] == "rule-enable"})
	case "dismiss-candidates", "bulk-dismiss":
		return c.dismissAutonomyProposals(args[1:])
	default:
		return fmt.Errorf("unknown autonomy command %q", args[0])
	}
}

func (c cli) updateAutonomyRule(idOrName string, payload map[string]any) error {
	ruleID, err := c.resolveAutonomyRuleID(idOrName)
	if err != nil {
		return err
	}
	return c.printRequest(http.MethodPatch, "/steward/autonomy/rules/"+url.PathEscape(ruleID), payload)
}

func (c cli) resolveAutonomyRuleID(idOrName string) (string, error) {
	value := strings.TrimSpace(idOrName)
	if value == "" {
		return "", fmt.Errorf("autonomy rule id or name is required")
	}
	body, err := c.request(http.MethodGet, "/steward/autonomy/rules", nil)
	if err != nil {
		return "", err
	}
	var response struct {
		Rules []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode autonomy rules: %w", err)
	}
	for _, rule := range response.Rules {
		if rule.ID == value || rule.Name == value {
			return rule.ID, nil
		}
	}
	return "", fmt.Errorf("autonomy rule %q not found", value)
}

func (c cli) dismissAutonomyProposals(args []string) error {
	fs := flag.NewFlagSet("steward autonomy bulk-dismiss", flag.ExitOnError)
	status := fs.String("status", "candidate", "Proposal status to dismiss: candidate, approved, or blocked")
	limit := fs.Int("limit", 50, "Maximum proposals to dismiss")
	reason := fs.String("reason", "manual CLI cleanup", "Audit reason for the bulk cleanup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return c.printRequest(http.MethodPost, "/steward/autonomy/proposals/bulk-dismiss", map[string]any{
		"status": strings.TrimSpace(*status),
		"limit":  *limit,
		"reason": strings.TrimSpace(*reason),
	})
}

func (c cli) printRequest(method string, path string, payload any) error {
	body, err := c.request(method, path, payload)
	if err != nil {
		return err
	}
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		fmt.Fprintln(stdout, string(body))
		return nil
	}
	return printJSON(decoded)
}

func (c cli) request(method string, path string, payload any) ([]byte, error) {
	return c.requestURL(method, c.apiBase+path, payload)
}

func (c cli) requestURL(method string, endpoint string, payload any) ([]byte, error) {
	if _, err := url.ParseRequestURI(endpoint); err != nil {
		return nil, fmt.Errorf("invalid endpoint %s: %w", endpoint, err)
	}
	var body io.Reader
	if payload != nil {
		encoded, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(encoded)
	}
	req, err := http.NewRequest(method, endpoint, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		if len(data) == 0 {
			return nil, fmt.Errorf("request failed with %s", resp.Status)
		}
		return nil, errors.New(strings.TrimSpace(string(data)))
	}
	return data, nil
}

func printJSON(payload any) error {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(payload)
}

func envOrDefault(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envIsSet(key string) bool {
	_, ok := os.LookupEnv(key)
	return ok
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func envDurationOrDefault(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func isHelpArg(value string) bool {
	switch strings.TrimSpace(value) {
	case "help", "-h", "--help":
		return true
	default:
		return false
	}
}

func printUsageTopic(args []string) {
	if len(args) == 0 {
		printUsage()
		return
	}
	switch strings.TrimSpace(args[0]) {
	case "service":
		if len(args) > 1 && strings.TrimSpace(args[1]) == "env" {
			printServiceEnvUsage()
			return
		}
		printServiceUsage()
	case "verify":
		printVerifyUsage()
	case "autonomy":
		printAutonomyUsage()
	case "devices":
		printDevicesUsage()
	case "pairing":
		printPairingUsage()
	default:
		printUsage()
	}
}

func printUsage() {
	fmt.Fprintln(stdout, `usage: steward [--api http://127.0.0.1:18080/api] <command>
       steward help <service|verify|autonomy|devices|pairing>

commands:
  run                 run the local management API and optional restricted peer API
  version             print local steward binary build information
  keygen              generate an Ed25519 device keypair for peer sync signing
  sync-keygen         generate an AES-256-GCM key for encrypted peer sync payloads
  doctor              check healthz, readyz, and agent API
  status              print agent, sync, and autonomy status
  start               mark the local agent running and allow background work
  stop                mark the local agent stopped and pause background work
  sync-status         print private sync queue and conflict status
  sync-device <id>    pull from and push to a registered peer device
  devices list        list registered local and peer devices
  devices register    register a trusted peer device
  devices revoke <id> revoke a peer device and queue revocation sync
  devices permissions <id>  list per-capability policies for a device
  devices permission-set <id> <capability> <allow|confirm|deny> [A0-A9]
                      update a device capability policy without changing service env
  devices verify <id> verify peer private-key possession with a pairing challenge
  devices sync <id>   pull from and push to a registered peer device
  pairing keygen      generate an X25519 recipient keypair for encrypted pairing bundles
  pairing export      print a human-reviewed peer pairing bundle
                      use --encrypt-shared-sync-for to seal included shared sync material
                      bundles are signed automatically when --private-key is available
  pairing import      register a peer from a pairing bundle
                      use --decrypt-shared-sync-key to open encrypted shared sync material
                      use --require-signature to reject unsigned pairing bundles
  pairing bootstrap   plan peer import plus redacted service env update without mutating state
                      use --service-scope system when planning macOS/Linux system services
  pairing verify <id> verify an imported peer pairing challenge
  service install     install as Windows Service, macOS LaunchAgent, or Linux systemd user unit
                      use --scope system for macOS LaunchDaemon or Linux systemd system unit
                      use --strict-security to validate S3/S4 keys before install
                      use --llm-provider/--llm-model/--llm-api-key to persist optional S4 advisor env
                      use --start --verify to start and run service verification after install
                      use --verify-startup-timeout/--verify-watch-duration to control post-install checks
                      use --verify-evidence-dir to persist post-install verification JSON
                      use --verify-advisor-probe to include S4 model live checks
                      use --verify-advisor-probe-each-sample with --verify-watch-duration for long-run model checks
                      use --verify-advisor-privacy-probe to prove D2 data is rejected before model submission
  service plan        render offline Windows/macOS/Linux service install plans from --current-env-file
                      use --target windows,darwin,linux to choose platforms
                      use --strict-security to validate S3/S4 keys before rendering
  service env plan    preview service environment updates, including pairing suggested_env
  service env apply   update service environment with --confirm, optionally --restart
                      use --scope to target the same user/system service manager entry as install
                      use --strict-security to validate the full target service env
                      use --current-env-file with plan for offline target-env validation
                      use --rotate-sync-key-id/--rotate-local-key-id to generate key rotations
                      use --restart --verify to load and verify the target env immediately
                      use --verify-startup-timeout/--verify-watch-duration to control post-apply checks
                      use --verify-evidence-dir to persist post-apply verification JSON
                      use --verify-advisor-probe to include S4 model live checks
                      use --verify-advisor-probe-each-sample with --verify-watch-duration for long-run model checks
                      use --verify-advisor-privacy-probe to prove D2 data is rejected before model submission
  service uninstall   uninstall the system service entry
  service start       start the installed service
  service stop        stop the installed service
  service restart     restart the installed service
  service status      print installed service status
  verify runtime      run S3/S4 runtime verification checks through the local management API
                      use --evidence-dir to persist timestamped verification JSON
                      use --advisor-probe to call the configured S4 model advisor once
                      use --advisor-probe-each-sample with --watch-duration for long-run model checks
                      use --advisor-privacy-probe to verify D2 data is rejected before model submission
                      use --expect-advisor-provider/model/max-data-level to verify loaded S4 config
                      use --watch-duration to require repeated runtime samples and heartbeat advance
  verify service      verify system service status and S3/S4 runtime checks together
                      use --evidence-dir to persist timestamped verification JSON
  verify peers        verify registered peer trust, optionally running one sync
                      use --evidence-dir to persist timestamped verification JSON
  verify mesh         verify multiple local or securely tunneled management APIs
                      use --evidence-dir to persist timestamped verification JSON
                      repeat --expect-agent-id/platform/sync-key-id/local-key-id once per node to prove identity
                      use --expect-advisor-provider/model/max-data-level to check every node
                      use --watch-duration to require repeated node samples and heartbeat advance
  verify evidence     summarize persisted verification evidence files and enforce coverage gates
                      use --require-agent-id and --require-platform-agent to prove exact devices
                      use --require-kind-platform-service-scope KIND:PLATFORM:SCOPE to prove service scope
                      use --require-kind-platform-service-name KIND:PLATFORM:NAME to prove service name
                      use --require-kind-platform-advisor-model KIND:PLATFORM:MODEL to prove S4 model
                      use --require-check-platform CHECK:PLATFORM for per-platform critical checks
                      use --require-kind-check-platform KIND:CHECK:PLATFORM to bind checks to evidence kind
                      use --latest-per-kind to ignore stale older evidence of the same kind
                      use --preset s3s4-final-system for the high-permission physical three-platform gate
  autonomy status     print autonomy rules, proposals, approvals, and runs
  autonomy pause      pause autonomous proposal/execution creation
  autonomy resume     resume autonomy
  autonomy run        scan for candidate autonomous proposals
  autonomy mode <suggest_only|controlled>  set global autonomy execution mode
  autonomy rules      list configurable autonomy rules
  autonomy rule-policy <id-or-name> <suggest|confirm|auto|never>
  autonomy rule-enable <id-or-name>
  autonomy rule-disable <id-or-name>
  autonomy dismiss-candidates  dismiss candidate proposals in bulk
  autonomy bulk-dismiss        dismiss candidate, approved, or blocked proposals in bulk`)
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

func printVerifyUsage() {
	fmt.Fprintln(stdout, `usage: steward verify <runtime|service|peers|mesh|evidence> [flags]

verify commands:
  runtime      check health, readiness, agent, S3 sync safety, S4 autonomy, and advisor status
  service      check native service status plus runtime checks
  peers        verify trusted peer challenge and optional sync/probe coverage
  mesh         verify multiple local or securely tunneled node APIs
  evidence     summarize persisted evidence and enforce completion gates

important flags:
  --strict-security                    require complete S3/S4 runtime safety status
  --write-probes                       create low-risk sync/autonomy probes
  --advisor-probe                      call configured S4 advisor with D0 data
  --advisor-privacy-probe              prove D2 data is blocked before model submission
  --watch-duration 24h                 require repeated samples and heartbeat advance
  --evidence-dir <dir>                 persist timestamped evidence JSON
  --scope user|system                  with verify service, check this service manager scope
  --require-kind-platform-service-scope service:windows:system
                                      with verify evidence, require a specific service scope
  --require-kind-platform-service-name service:windows:MongojsonSteward
                                      with verify evidence, require a specific service name
  --require-kind-platform-advisor-model service:windows:<model>
                                      with verify evidence, require a specific S4 advisor model
  --preset s3s4-final-system           require physical Windows/macOS/Linux S3/S4 final gate, final-host wrapper, and system service scope

examples:
  steward verify runtime --strict-security --write-probes
  steward verify service --strict-security --watch-duration 24h --evidence-dir .\evidence\s3s4
  steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --strict-security --sync --write-probes
  steward verify evidence --dir .\evidence\s3s4 --preset s3s4-final-system`)
}

func printAutonomyUsage() {
	fmt.Fprintln(stdout, `usage: steward autonomy <status|pause|resume|run|mode|rules|rule-policy|rule-enable|rule-disable|dismiss-candidates|bulk-dismiss> [args]

autonomy commands:
  status                         print rules, proposals, approvals, runs, and advisor status
  pause                          stop background proposal/execution creation
  resume                         allow background autonomy cycles again
  run                            run one candidate scan now
  mode <suggest_only|controlled> set global autonomy mode
  rules                          list configurable autonomy rules
  rule-policy <id-or-name> <suggest|confirm|auto|never>
  rule-enable <id-or-name>
  rule-disable <id-or-name>
  bulk-dismiss --status candidate --limit 50

S4 guardrail:
  high-risk, A4+, external-send, delete, payment, credential, system-config, and publish/commit actions stay blocked by policy.`)
}

func printDevicesUsage() {
	fmt.Fprintln(stdout, `usage: steward devices <list|register|revoke|permissions|permission-set|verify|sync> [args]

device commands:
  list
  register --id <id> --name <name> --platform <windows|darwin|linux> --api-base-url <url> --public-key <key>
  revoke <id>
  permissions <id>
  permission-set <id> <capability> <allow|confirm|deny> [A0-A9]
  verify <id>
  sync <id>

notes:
  management APIs should stay on loopback; peer APIs should use the restricted sync surface only.`)
}

func printPairingUsage() {
	fmt.Fprintln(stdout, `usage: steward pairing <keygen|export|import|bootstrap|verify> [flags]

pairing commands:
  keygen       generate an X25519 recipient keypair for encrypted pairing bundles
  export       print a signed pairing bundle for this device
  import       register a peer from a pairing bundle
  bootstrap    produce a non-mutating import + service env plan
  verify <id>  verify imported peer private-key possession

examples:
  steward pairing keygen --label macbook-main
  steward pairing export --api-base-url http://192.168.1.10:18081/api --include-sync-encryption-key --encrypt-shared-sync-for <recipient-public-key>
  steward pairing bootstrap --file .\peer-pairing.encrypted.json --decrypt-shared-sync-key <recipient-private-key> --require-signature --current-env-file .\current-service-env.json --strict-security`)
}
