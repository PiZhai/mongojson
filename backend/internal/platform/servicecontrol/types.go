package servicecontrol

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"mongojson/backend/internal/platform/netpolicy"
)

const (
	DefaultDisplayName = "MongoJSON Steward"
	DefaultDescription = "Private AI steward background API service"
	ScopeUser          = "user"
	ScopeSystem        = "system"
)

type InstallOptions struct {
	Name                        string            `json:"name"`
	Scope                       string            `json:"scope"`
	DisplayName                 string            `json:"display_name"`
	Description                 string            `json:"description"`
	BinaryPath                  string            `json:"binary_path"`
	WorkDir                     string            `json:"work_dir"`
	HTTPAddr                    string            `json:"http_addr"`
	PeerHTTPAddr                string            `json:"peer_http_addr,omitempty"`
	DatabaseURL                 string            `json:"database_url"`
	StorageDir                  string            `json:"storage_dir"`
	UIDir                       string            `json:"ui_dir,omitempty"`
	AgentID                     string            `json:"agent_id"`
	PublicAPIBase               string            `json:"public_api_base"`
	SyncSecret                  string            `json:"-"`
	DevicePrivateKey            string            `json:"-"`
	DevicePublicKey             string            `json:"device_public_key,omitempty"`
	SyncEncryptionKey           string            `json:"-"`
	SyncEncryptionKeyID         string            `json:"sync_encryption_key_id,omitempty"`
	SyncEncryptionPreviousKeys  string            `json:"-"`
	LocalEncryptionKey          string            `json:"-"`
	LocalEncryptionKeyID        string            `json:"local_encryption_key_id,omitempty"`
	LocalEncryptionPreviousKeys string            `json:"-"`
	HeartbeatInterval           time.Duration     `json:"heartbeat_interval"`
	CollectionInterval          time.Duration     `json:"collection_interval"`
	SyncInterval                time.Duration     `json:"sync_interval"`
	AutonomyInterval            time.Duration     `json:"autonomy_interval"`
	LogDir                      string            `json:"log_dir"`
	ExtraEnv                    map[string]string `json:"extra_env,omitempty"`
	DryRun                      bool              `json:"dry_run"`
}

type EnvPatchOptions struct {
	Name            string                                        `json:"name"`
	Scope           string                                        `json:"scope"`
	Set             map[string]string                             `json:"set,omitempty"`
	Remove          []string                                      `json:"remove,omitempty"`
	DryRun          bool                                          `json:"dry_run"`
	TransformTarget func(current, target map[string]string) error `json:"-"`
	ValidateTarget  func(map[string]string) error                 `json:"-"`
}

type Result struct {
	Platform    string            `json:"platform"`
	Name        string            `json:"name"`
	Scope       string            `json:"scope,omitempty"`
	Status      string            `json:"status,omitempty"`
	Message     string            `json:"message,omitempty"`
	Files       []string          `json:"files,omitempty"`
	Commands    []string          `json:"commands,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
}

type StatusResult struct {
	Platform string `json:"platform"`
	Name     string `json:"name"`
	Scope    string `json:"scope,omitempty"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
}

type InstallPlan struct {
	Platform    string            `json:"platform"`
	Name        string            `json:"name"`
	Scope       string            `json:"scope"`
	Files       []string          `json:"files,omitempty"`
	Commands    []string          `json:"commands,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	RunArgs     []string          `json:"run_args,omitempty"`
	Artifacts   map[string]string `json:"artifacts,omitempty"`
}

func DefaultName() string {
	return DefaultNameForPlatform(runtime.GOOS)
}

func DefaultScope() string {
	return DefaultScopeForPlatform(runtime.GOOS)
}

func DefaultScopeForPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "windows":
		return ScopeSystem
	case "darwin", "linux":
		return ScopeUser
	default:
		return ScopeUser
	}
}

func DefaultNameForPlatform(platform string) string {
	switch strings.ToLower(strings.TrimSpace(platform)) {
	case "windows":
		return "MongojsonSteward"
	case "darwin":
		return "com.mongojson.steward"
	case "linux":
		return "mongojson-steward"
	default:
		return "mongojson-steward"
	}
}

func NormalizeScopeForPlatform(platform string, scope string) (string, error) {
	platform, err := NormalizeTargetPlatform(platform)
	if err != nil {
		return "", err
	}
	scope = strings.ToLower(strings.TrimSpace(scope))
	if scope == "" {
		scope = DefaultScopeForPlatform(platform)
	}
	switch platform {
	case "windows":
		if scope != ScopeSystem {
			return "", fmt.Errorf("windows service scope must be %q", ScopeSystem)
		}
		return ScopeSystem, nil
	case "darwin", "linux":
		switch scope {
		case ScopeUser, ScopeSystem:
			return scope, nil
		default:
			return "", fmt.Errorf("%s service scope must be %q or %q", platform, ScopeUser, ScopeSystem)
		}
	default:
		return "", fmt.Errorf("unsupported service scope platform %q", platform)
	}
}

func NormalizeTargetPlatform(platform string) (string, error) {
	platform = strings.ToLower(strings.TrimSpace(platform))
	if platform == "" {
		platform = runtime.GOOS
	}
	switch platform {
	case "windows", "darwin", "linux":
		return platform, nil
	default:
		return "", fmt.Errorf("unsupported service plan target platform %q", platform)
	}
}

func NormalizeInstallOptions(input InstallOptions) (InstallOptions, error) {
	return NormalizeInstallOptionsForPlatform(runtime.GOOS, input)
}

func NormalizeInstallOptionsForPlatform(platform string, input InstallOptions) (InstallOptions, error) {
	platform, err := NormalizeTargetPlatform(platform)
	if err != nil {
		return InstallOptions{}, err
	}
	out := input
	out.Name = defaultString(out.Name, DefaultNameForPlatform(platform))
	scope, err := NormalizeScopeForPlatform(platform, out.Scope)
	if err != nil {
		return InstallOptions{}, err
	}
	out.Scope = scope
	out.DisplayName = defaultString(out.DisplayName, DefaultDisplayName)
	out.Description = defaultString(out.Description, DefaultDescription)
	if strings.TrimSpace(out.BinaryPath) == "" {
		exe, err := os.Executable()
		if err != nil {
			return InstallOptions{}, fmt.Errorf("resolve executable path: %w", err)
		}
		out.BinaryPath = exe
	}
	binaryPath, err := filepath.Abs(out.BinaryPath)
	if err != nil {
		return InstallOptions{}, fmt.Errorf("resolve binary path: %w", err)
	}
	out.BinaryPath = binaryPath
	if strings.TrimSpace(out.WorkDir) == "" {
		wd, err := os.Getwd()
		if err != nil {
			return InstallOptions{}, fmt.Errorf("resolve work dir: %w", err)
		}
		out.WorkDir = wd
	}
	workDir, err := filepath.Abs(out.WorkDir)
	if err != nil {
		return InstallOptions{}, fmt.Errorf("resolve work dir: %w", err)
	}
	out.WorkDir = workDir
	out.HTTPAddr = defaultString(out.HTTPAddr, "127.0.0.1:18080")
	out.PeerHTTPAddr = defaultString(out.PeerHTTPAddr, ":18081")
	if err := netpolicy.ValidateListenerTopology(out.HTTPAddr, out.PeerHTTPAddr, false); err != nil {
		return InstallOptions{}, err
	}
	out.DatabaseURL = defaultString(out.DatabaseURL, "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable")
	out.StorageDir = defaultString(out.StorageDir, filepath.Join(out.WorkDir, "data"))
	if strings.TrimSpace(out.UIDir) != "" {
		uiDir, err := filepath.Abs(out.UIDir)
		if err != nil {
			return InstallOptions{}, fmt.Errorf("resolve ui dir: %w", err)
		}
		out.UIDir = uiDir
	}
	out.AgentID = strings.TrimSpace(out.AgentID)
	out.PublicAPIBase = strings.TrimSpace(out.PublicAPIBase)
	out.SyncSecret = strings.TrimSpace(out.SyncSecret)
	out.DevicePrivateKey = strings.TrimSpace(out.DevicePrivateKey)
	out.DevicePublicKey = strings.TrimSpace(out.DevicePublicKey)
	out.SyncEncryptionKey = strings.TrimSpace(out.SyncEncryptionKey)
	out.SyncEncryptionKeyID = strings.TrimSpace(out.SyncEncryptionKeyID)
	out.SyncEncryptionPreviousKeys = strings.TrimSpace(out.SyncEncryptionPreviousKeys)
	out.LocalEncryptionKey = strings.TrimSpace(out.LocalEncryptionKey)
	out.LocalEncryptionKeyID = strings.TrimSpace(out.LocalEncryptionKeyID)
	out.LocalEncryptionPreviousKeys = strings.TrimSpace(out.LocalEncryptionPreviousKeys)
	if out.HeartbeatInterval < 0 {
		out.HeartbeatInterval = 0
	}
	if out.CollectionInterval < 0 {
		out.CollectionInterval = 0
	}
	if out.SyncInterval < 0 {
		out.SyncInterval = 0
	}
	if out.AutonomyInterval < 0 {
		out.AutonomyInterval = 0
	}
	if strings.TrimSpace(out.LogDir) != "" {
		logDir, err := filepath.Abs(out.LogDir)
		if err != nil {
			return InstallOptions{}, fmt.Errorf("resolve log dir: %w", err)
		}
		out.LogDir = logDir
	}
	if out.ExtraEnv == nil {
		out.ExtraEnv = map[string]string{}
	}
	normalizedExtraEnv := map[string]string{}
	for key, value := range out.ExtraEnv {
		key = strings.TrimSpace(key)
		if err := validateEnvKey(key); err != nil {
			return InstallOptions{}, err
		}
		normalizedExtraEnv[key] = value
	}
	out.ExtraEnv = normalizedExtraEnv
	return out, nil
}

func Environment(options InstallOptions) map[string]string {
	env := map[string]string{
		"HTTP_ADDR":              options.HTTPAddr,
		"STEWARD_PEER_HTTP_ADDR": options.PeerHTTPAddr,
		"DATABASE_URL":           options.DatabaseURL,
		"STORAGE_DIR":            options.StorageDir,
		"STEWARD_AGENT_ID":       defaultString(options.AgentID, options.Name),
	}
	if strings.TrimSpace(options.PublicAPIBase) != "" {
		env["STEWARD_PUBLIC_API_BASE"] = options.PublicAPIBase
	}
	if strings.TrimSpace(options.UIDir) != "" {
		env["STEWARD_UI_DIR"] = options.UIDir
	}
	if strings.TrimSpace(options.SyncSecret) != "" {
		env["STEWARD_SYNC_SECRET"] = options.SyncSecret
	}
	if strings.TrimSpace(options.DevicePrivateKey) != "" {
		env["STEWARD_DEVICE_PRIVATE_KEY"] = options.DevicePrivateKey
	}
	if strings.TrimSpace(options.DevicePublicKey) != "" {
		env["STEWARD_DEVICE_PUBLIC_KEY"] = options.DevicePublicKey
	}
	if strings.TrimSpace(options.SyncEncryptionKey) != "" {
		env["STEWARD_SYNC_ENCRYPTION_KEY"] = options.SyncEncryptionKey
	}
	if strings.TrimSpace(options.SyncEncryptionKeyID) != "" {
		env["STEWARD_SYNC_ENCRYPTION_KEY_ID"] = options.SyncEncryptionKeyID
	}
	if strings.TrimSpace(options.SyncEncryptionPreviousKeys) != "" {
		env["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] = options.SyncEncryptionPreviousKeys
	}
	if strings.TrimSpace(options.LocalEncryptionKey) != "" {
		env["STEWARD_LOCAL_ENCRYPTION_KEY"] = options.LocalEncryptionKey
	}
	if strings.TrimSpace(options.LocalEncryptionKeyID) != "" {
		env["STEWARD_LOCAL_ENCRYPTION_KEY_ID"] = options.LocalEncryptionKeyID
	}
	if strings.TrimSpace(options.LocalEncryptionPreviousKeys) != "" {
		env["STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"] = options.LocalEncryptionPreviousKeys
	}
	if options.HeartbeatInterval > 0 {
		env["STEWARD_HEARTBEAT_INTERVAL"] = options.HeartbeatInterval.String()
	}
	if options.CollectionInterval > 0 {
		env["STEWARD_COLLECTION_INTERVAL"] = options.CollectionInterval.String()
	}
	if options.SyncInterval > 0 {
		env["STEWARD_SYNC_INTERVAL"] = options.SyncInterval.String()
	}
	if options.AutonomyInterval > 0 {
		env["STEWARD_AUTONOMY_INTERVAL"] = options.AutonomyInterval.String()
	}
	if strings.TrimSpace(options.LogDir) != "" {
		env["STEWARD_LOG_DIR"] = options.LogDir
	}
	for key, value := range options.ExtraEnv {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		env[key] = value
	}
	return env
}

func redactedEnvironment(env map[string]string) map[string]string {
	redacted := map[string]string{}
	for key, value := range env {
		if isSensitiveEnvKey(key) {
			redacted[key] = "<redacted>"
			continue
		}
		redacted[key] = value
	}
	return redacted
}

func redactedEnvList(env map[string]string) []string {
	return envList(redactedEnvironment(env))
}

func isSensitiveEnvKey(key string) bool {
	key = strings.ToUpper(strings.TrimSpace(key))
	if key == "STEWARD_LLM_ALLOW_NO_API_KEY" {
		return false
	}
	return key == "DATABASE_URL" ||
		strings.Contains(key, "SECRET") ||
		strings.Contains(key, "TOKEN") ||
		strings.Contains(key, "PASSWORD") ||
		strings.Contains(key, "API_KEY") ||
		(strings.Contains(key, "ENCRYPTION_KEY") && !strings.Contains(key, "ENCRYPTION_KEY_ID")) ||
		strings.Contains(key, "PREVIOUS_KEYS") ||
		strings.Contains(key, "PRIVATE_KEY")
}

func envList(env map[string]string) []string {
	items := make([]string, 0, len(env))
	for key, value := range env {
		items = append(items, key+"="+value)
	}
	sort.Strings(items)
	return items
}

func parseEnvList(items []string) map[string]string {
	env := map[string]string{}
	for _, item := range items {
		key, value, ok := strings.Cut(item, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	return env
}

func NormalizeEnvPatchOptions(input EnvPatchOptions) (EnvPatchOptions, error) {
	out := input
	out.Name = defaultString(out.Name, DefaultName())
	scope, err := NormalizeScopeForPlatform(runtime.GOOS, out.Scope)
	if err != nil {
		return EnvPatchOptions{}, err
	}
	out.Scope = scope
	if out.Set == nil {
		out.Set = map[string]string{}
	}
	normalizedSet := map[string]string{}
	for key, value := range out.Set {
		key = strings.TrimSpace(key)
		if err := validateEnvKey(key); err != nil {
			return EnvPatchOptions{}, err
		}
		normalizedSet[key] = value
	}
	out.Set = normalizedSet
	normalizedRemove := make([]string, 0, len(out.Remove))
	seenRemove := map[string]struct{}{}
	for _, key := range out.Remove {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if err := validateEnvKey(key); err != nil {
			return EnvPatchOptions{}, err
		}
		if _, ok := seenRemove[key]; ok {
			continue
		}
		seenRemove[key] = struct{}{}
		normalizedRemove = append(normalizedRemove, key)
	}
	out.Remove = normalizedRemove
	if len(out.Set) == 0 && len(out.Remove) == 0 && out.TransformTarget == nil {
		return EnvPatchOptions{}, fmt.Errorf("environment patch requires at least one --set or --remove value")
	}
	return out, nil
}

func buildEnvPatchTarget(current map[string]string, options EnvPatchOptions) (map[string]string, error) {
	next := patchEnvironment(current, options.Set, options.Remove)
	if options.TransformTarget != nil {
		if err := options.TransformTarget(copyEnvMap(current), next); err != nil {
			return nil, err
		}
	}
	if err := validateTargetEnvironment(next, options.ValidateTarget); err != nil {
		return nil, err
	}
	return next, nil
}

func validateTargetEnvironment(env map[string]string, validate func(map[string]string) error) error {
	if validate == nil {
		return nil
	}
	return validate(copyEnvMap(env))
}

func validateEnvKey(key string) error {
	if key == "" {
		return fmt.Errorf("environment variable name is required")
	}
	for i, r := range key {
		if i == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return fmt.Errorf("invalid environment variable name %q", key)
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return fmt.Errorf("invalid environment variable name %q", key)
	}
	return nil
}

func patchEnvironment(current map[string]string, set map[string]string, remove []string) map[string]string {
	next := copyEnvMap(current)
	for _, key := range remove {
		delete(next, key)
	}
	for key, value := range set {
		next[key] = value
	}
	return next
}

func copyEnvMap(env map[string]string) map[string]string {
	copy := map[string]string{}
	for key, value := range env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		copy[key] = value
	}
	return copy
}

func serviceRunArgs(options InstallOptions) []string {
	return []string{"run", "--service-name", options.Name, "--workdir", options.WorkDir}
}

func defaultString(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func commandString(name string, args ...string) string {
	parts := append([]string{name}, args...)
	for i, part := range parts {
		if strings.ContainsAny(part, " \t\"'") {
			parts[i] = fmt.Sprintf("%q", part)
		}
	}
	return strings.Join(parts, " ")
}

func Install(ctx context.Context, options InstallOptions) (Result, error) {
	return installPlatform(ctx, options)
}

func Uninstall(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	return uninstallPlatform(ctx, defaultString(name, DefaultName()), defaultString(scope, DefaultScope()), dryRun)
}

func Start(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	return startPlatform(ctx, defaultString(name, DefaultName()), defaultString(scope, DefaultScope()), dryRun)
}

func Stop(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	return stopPlatform(ctx, defaultString(name, DefaultName()), defaultString(scope, DefaultScope()), dryRun)
}

func Restart(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	return restartPlatform(ctx, defaultString(name, DefaultName()), defaultString(scope, DefaultScope()), dryRun)
}

func Status(ctx context.Context, name string, scope string) (StatusResult, error) {
	return statusPlatform(ctx, defaultString(name, DefaultName()), defaultString(scope, DefaultScope()))
}

func PatchEnvironment(ctx context.Context, options EnvPatchOptions) (Result, error) {
	return envPatchPlatform(ctx, options)
}

func PlanInstall(targetPlatform string, input InstallOptions) (InstallPlan, error) {
	platform, err := NormalizeTargetPlatform(targetPlatform)
	if err != nil {
		return InstallPlan{}, err
	}
	if strings.TrimSpace(input.Name) == "" {
		input.Name = DefaultNameForPlatform(platform)
	}
	options, err := NormalizeInstallOptionsForPlatform(platform, input)
	if err != nil {
		return InstallPlan{}, err
	}
	env := Environment(options)
	redacted := redactedEnvironment(env)
	runArgs := append([]string{options.BinaryPath}, serviceRunArgs(options)...)
	plan := InstallPlan{
		Platform:    platform,
		Name:        targetServiceName(platform, options.Name),
		Scope:       options.Scope,
		Environment: redacted,
		RunArgs:     runArgs,
		Artifacts:   map[string]string{},
	}
	switch platform {
	case "windows":
		plan.Commands = []string{
			commandString("sc.exe", "create", options.Name, "binPath=", commandString(options.BinaryPath, serviceRunArgs(options)...), "start=", "auto", "DisplayName=", options.DisplayName),
			"registry Environment=" + fmt.Sprintf("%q", redactedEnvList(env)),
		}
		plan.Artifacts["service_type"] = "Windows Service"
		plan.Artifacts["bin_path"] = commandString(options.BinaryPath, serviceRunArgs(options)...)
		plan.Artifacts["registry_environment"] = strings.Join(redactedEnvList(env), "\n")
	case "darwin":
		plistPath := "~/Library/LaunchAgents/" + options.Name + ".plist"
		parentDomain := "gui/<uid>"
		domain := parentDomain + "/" + options.Name
		serviceType := "macOS LaunchAgent"
		if options.Scope == ScopeSystem {
			plistPath = "/Library/LaunchDaemons/" + options.Name + ".plist"
			parentDomain = "system"
			domain = "system/" + options.Name
			serviceType = "macOS LaunchDaemon"
		}
		plan.Files = []string{plistPath}
		plan.Commands = []string{
			commandString("launchctl", "bootstrap", parentDomain, plistPath),
			commandString("launchctl", "enable", domain),
		}
		plan.Artifacts["service_type"] = serviceType
		plan.Artifacts["plist"] = renderLaunchAgentPlan(options, redacted)
		plan.Artifacts["plist_mode"] = "0600"
	case "linux":
		unitName := targetLinuxUnitName(options.Name)
		unitPath := "~/.config/systemd/user/" + unitName
		envPath := targetLinuxEnvironmentFile(options.Name, options.Scope)
		systemctl := "systemctl --user"
		serviceType := "Linux systemd user unit"
		if options.Scope == ScopeSystem {
			unitPath = "/etc/systemd/system/" + unitName
			systemctl = "systemctl"
			serviceType = "Linux systemd system unit"
		}
		plan.Name = unitName
		plan.Files = []string{unitPath, envPath}
		plan.Commands = []string{
			systemctl + " daemon-reload",
			systemctl + " enable " + unitName,
		}
		plan.Artifacts["service_type"] = serviceType
		plan.Artifacts["systemd_unit"] = renderSystemdUnitPlan(options, envPath)
		plan.Artifacts["environment_file"] = renderSystemdEnvironmentFile(redacted)
		plan.Artifacts["environment_file_mode"] = "0600"
	}
	return plan, nil
}

func PlanEnvironmentPatch(current map[string]string, input EnvPatchOptions) (Result, error) {
	options, err := NormalizeEnvPatchOptions(input)
	if err != nil {
		return Result{}, err
	}
	next, err := buildEnvPatchTarget(current, options)
	if err != nil {
		return Result{}, err
	}
	return Result{
		Platform:    runtime.GOOS,
		Name:        options.Name,
		Scope:       options.Scope,
		Message:     "dry run: environment patch was planned from an explicit current environment; no service manager state was read",
		Environment: redactedEnvironment(next),
		Commands: []string{
			"plan environment patch from explicit current environment",
			"restart the platform service after applying these values",
		},
	}, nil
}

func targetServiceName(platform string, name string) string {
	if platform == "linux" {
		return targetLinuxUnitName(name)
	}
	return name
}

func normalizeServiceActionTarget(platform string, name string, scope string) (string, string, error) {
	platform, err := NormalizeTargetPlatform(platform)
	if err != nil {
		return "", "", err
	}
	name = defaultString(name, DefaultNameForPlatform(platform))
	scope, err = NormalizeScopeForPlatform(platform, scope)
	if err != nil {
		return "", "", err
	}
	return name, scope, nil
}

func targetLinuxUnitName(name string) string {
	name = defaultString(name, DefaultNameForPlatform("linux"))
	if !strings.HasSuffix(name, ".service") {
		name += ".service"
	}
	return name
}

func targetLinuxEnvironmentFile(name string, scope string) string {
	unitName := targetLinuxUnitName(name)
	if scope == ScopeSystem {
		return "/etc/mongojson-steward/" + unitName + ".env"
	}
	return "~/.config/mongojson-steward/" + unitName + ".env"
}

func renderSystemdUnitPlan(options InstallOptions, envFilePath string) string {
	wantedBy := "default.target"
	if options.Scope == ScopeSystem {
		wantedBy = "multi-user.target"
	}
	return strings.Join([]string{
		"[Unit]",
		"Description=" + options.Description,
		"After=network-online.target",
		"Wants=network-online.target",
		"",
		"[Service]",
		"Type=simple",
		"WorkingDirectory=" + strconv.Quote(options.WorkDir),
		"EnvironmentFile=" + strconv.Quote(envFilePath),
		"ExecStart=" + shellQuotedCommand(append([]string{options.BinaryPath}, serviceRunArgs(options)...)),
		"Restart=always",
		"RestartSec=5",
		"",
		"[Install]",
		"WantedBy=" + wantedBy,
		"",
	}, "\n")
}

func shellQuotedCommand(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, strconv.Quote(arg))
	}
	return strings.Join(quoted, " ")
}

func renderLaunchAgentPlan(options InstallOptions, env map[string]string) string {
	var builder strings.Builder
	builder.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	builder.WriteString(`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n")
	builder.WriteString(`<plist version="1.0">` + "\n<dict>\n")
	plistPlanKeyValue(&builder, "Label", options.Name)
	builder.WriteString("  <key>ProgramArguments</key>\n  <array>\n")
	for _, arg := range append([]string{options.BinaryPath}, serviceRunArgs(options)...) {
		builder.WriteString("    <string>" + escapePlanXML(arg) + "</string>\n")
	}
	builder.WriteString("  </array>\n")
	plistPlanKeyValue(&builder, "WorkingDirectory", options.WorkDir)
	builder.WriteString("  <key>EnvironmentVariables</key>\n  <dict>\n")
	for _, item := range envList(env) {
		key, value, _ := strings.Cut(item, "=")
		plistPlanKeyValue(&builder, key, value)
	}
	builder.WriteString("  </dict>\n")
	builder.WriteString("  <key>KeepAlive</key>\n  <true/>\n")
	builder.WriteString("  <key>RunAtLoad</key>\n  <true/>\n")
	if strings.TrimSpace(options.LogDir) != "" {
		plistPlanKeyValue(&builder, "StandardOutPath", filepath.Join(options.LogDir, options.Name+".out.log"))
		plistPlanKeyValue(&builder, "StandardErrorPath", filepath.Join(options.LogDir, options.Name+".err.log"))
	}
	builder.WriteString("</dict>\n</plist>\n")
	return builder.String()
}

func plistPlanKeyValue(builder *strings.Builder, key string, value string) {
	builder.WriteString("  <key>" + escapePlanXML(key) + "</key>\n")
	builder.WriteString("  <string>" + escapePlanXML(value) + "</string>\n")
}

func escapePlanXML(value string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(value)
}
