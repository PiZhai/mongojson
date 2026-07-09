package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"mongojson/backend/internal/platform/netpolicy"
	"mongojson/backend/internal/platform/servicecontrol"
)

const minStrictSyncSecretLength = 24

var strictAdvisorEnvKeys = []string{
	"STEWARD_LLM_PROVIDER",
	"STEWARD_LLM_BASE_URL",
	"STEWARD_LLM_MODEL",
	"STEWARD_LLM_API_KEY",
	"STEWARD_LLM_ALLOW_NO_API_KEY",
	"STEWARD_LLM_TIMEOUT",
	"STEWARD_LLM_MAX_DATA_LEVEL",
	"STEWARD_LLM_FAILURE_THRESHOLD",
	"STEWARD_LLM_FAILURE_COOLDOWN",
}

func validateStrictServiceSecurity(options servicecontrol.InstallOptions) error {
	var problems []string
	if err := netpolicy.ValidateListenerTopology(options.HTTPAddr, options.PeerHTTPAddr, false); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(options.PeerHTTPAddr) == "" {
		problems = append(problems, "STEWARD_PEER_HTTP_ADDR is required for strict S3 service installation")
	}
	if strings.TrimSpace(options.PublicAPIBase) == "" {
		problems = append(problems, "STEWARD_PUBLIC_API_BASE is required for strict S3 service installation and must point at the peer listener")
	} else if err := netpolicy.ValidatePeerAPIBase(options.PublicAPIBase, options.HTTPAddr); err != nil {
		problems = append(problems, err.Error())
	}

	agentID := strings.TrimSpace(options.AgentID)
	if agentID == "" || agentID == "local-s1" || agentID == servicecontrol.DefaultName() {
		problems = append(problems, "STEWARD_AGENT_ID must be a unique device id, for example windows-main")
	}
	if len(strings.TrimSpace(options.SyncSecret)) < minStrictSyncSecretLength {
		problems = append(problems, fmt.Sprintf("STEWARD_SYNC_SECRET must be at least %d characters", minStrictSyncSecretLength))
	}
	if err := validateServiceDeviceKeys(options.DevicePrivateKey, options.DevicePublicKey); err != nil {
		problems = append(problems, err.Error())
	}
	if err := validateServiceAESKey(options.SyncEncryptionKey, options.SyncEncryptionKeyID, "STEWARD_SYNC_ENCRYPTION_KEY", "STEWARD_SYNC_ENCRYPTION_KEY_ID"); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(options.SyncEncryptionPreviousKeys) != "" {
		if err := validatePreviousEncryptionKeys(options.SyncEncryptionPreviousKeys, "STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if err := validateServiceAESKey(options.LocalEncryptionKey, options.LocalEncryptionKeyID, "STEWARD_LOCAL_ENCRYPTION_KEY", "STEWARD_LOCAL_ENCRYPTION_KEY_ID"); err != nil {
		problems = append(problems, err.Error())
	}
	if strings.TrimSpace(options.LocalEncryptionPreviousKeys) != "" {
		if err := validatePreviousEncryptionKeys(options.LocalEncryptionPreviousKeys, "STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"); err != nil {
			problems = append(problems, err.Error())
		}
	}
	if err := validateStrictAdvisorEnvironment(options.ExtraEnv); err != nil {
		problems = append(problems, err.Error())
	}

	if len(problems) > 0 {
		return fmt.Errorf("strict service security validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func serviceInstallOptionsFromEnv(name string, env map[string]string) servicecontrol.InstallOptions {
	return servicecontrol.InstallOptions{
		Name:                        strings.TrimSpace(name),
		HTTPAddr:                    strings.TrimSpace(env["HTTP_ADDR"]),
		PeerHTTPAddr:                strings.TrimSpace(env["STEWARD_PEER_HTTP_ADDR"]),
		DatabaseURL:                 strings.TrimSpace(env["DATABASE_URL"]),
		StorageDir:                  strings.TrimSpace(env["STORAGE_DIR"]),
		UIDir:                       strings.TrimSpace(env["STEWARD_UI_DIR"]),
		AgentID:                     strings.TrimSpace(env["STEWARD_AGENT_ID"]),
		PublicAPIBase:               strings.TrimSpace(env["STEWARD_PUBLIC_API_BASE"]),
		SyncSecret:                  strings.TrimSpace(env["STEWARD_SYNC_SECRET"]),
		DevicePrivateKey:            strings.TrimSpace(env["STEWARD_DEVICE_PRIVATE_KEY"]),
		DevicePublicKey:             strings.TrimSpace(env["STEWARD_DEVICE_PUBLIC_KEY"]),
		SyncEncryptionKey:           strings.TrimSpace(env["STEWARD_SYNC_ENCRYPTION_KEY"]),
		SyncEncryptionKeyID:         strings.TrimSpace(env["STEWARD_SYNC_ENCRYPTION_KEY_ID"]),
		SyncEncryptionPreviousKeys:  strings.TrimSpace(env["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"]),
		LocalEncryptionKey:          strings.TrimSpace(env["STEWARD_LOCAL_ENCRYPTION_KEY"]),
		LocalEncryptionKeyID:        strings.TrimSpace(env["STEWARD_LOCAL_ENCRYPTION_KEY_ID"]),
		LocalEncryptionPreviousKeys: strings.TrimSpace(env["STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS"]),
		HeartbeatInterval:           serviceEnvDuration(env, "STEWARD_HEARTBEAT_INTERVAL"),
		SyncInterval:                serviceEnvDuration(env, "STEWARD_SYNC_INTERVAL"),
		AutonomyInterval:            serviceEnvDuration(env, "STEWARD_AUTONOMY_INTERVAL"),
		LogDir:                      strings.TrimSpace(env["STEWARD_LOG_DIR"]),
		ExtraEnv:                    advisorEnvFromServiceEnv(env),
	}
}

func serviceEnvDuration(env map[string]string, key string) time.Duration {
	value := strings.TrimSpace(env[key])
	if value == "" {
		return 0
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0
	}
	return parsed
}

func advisorEnvFromServiceEnv(env map[string]string) map[string]string {
	extra := map[string]string{}
	for _, key := range strictAdvisorEnvKeys {
		if value, ok := env[key]; ok {
			extra[key] = strings.TrimSpace(value)
		}
	}
	return extra
}

func validateServiceDeviceKeys(privateKeyValue string, publicKeyValue string) error {
	derivedPublicKey, err := publicKeyFromPrivateKey(privateKeyValue)
	if err != nil {
		return fmt.Errorf("STEWARD_DEVICE_PRIVATE_KEY: %w", err)
	}
	normalizedPublicKey, err := normalizeEd25519PublicKey(publicKeyValue)
	if err != nil {
		return fmt.Errorf("STEWARD_DEVICE_PUBLIC_KEY: %w", err)
	}
	if derivedPublicKey != normalizedPublicKey {
		return fmt.Errorf("STEWARD_DEVICE_PUBLIC_KEY does not match STEWARD_DEVICE_PRIVATE_KEY")
	}
	return nil
}

func validateServiceAESKey(keyValue string, keyID string, keyName string, keyIDName string) error {
	if strings.TrimSpace(keyID) == "" {
		return fmt.Errorf("%s is required", keyIDName)
	}
	if _, err := decodeBase64Material(keyValue, 32, keyName); err != nil {
		return err
	}
	return nil
}

func validatePreviousEncryptionKeys(value string, label string) error {
	valid := 0
	for _, entry := range strings.Split(value, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		keyID, material, ok := strings.Cut(entry, ":")
		if !ok || strings.TrimSpace(keyID) == "" || strings.TrimSpace(material) == "" {
			return fmt.Errorf("%s must use comma-separated key_id:base64 entries", label)
		}
		if _, err := decodeBase64Material(material, 32, label+" "+strings.TrimSpace(keyID)); err != nil {
			return err
		}
		valid++
	}
	if valid == 0 {
		return fmt.Errorf("%s must include at least one key_id:base64 entry", label)
	}
	return nil
}

func validateStrictAdvisorEnvironment(env map[string]string) error {
	provider := strings.ToLower(strings.TrimSpace(env["STEWARD_LLM_PROVIDER"]))
	if provider == "" || provider == "off" || provider == "disabled" || provider == "none" {
		return nil
	}
	var problems []string
	if provider != "openai-compatible" && provider != "openai" {
		problems = append(problems, "STEWARD_LLM_PROVIDER must be openai-compatible, openai, off, disabled, none, or empty")
	}
	if strings.TrimSpace(env["STEWARD_LLM_MODEL"]) == "" {
		problems = append(problems, "STEWARD_LLM_MODEL is required when S4 advisor is enabled")
	}

	baseURL := strings.TrimSpace(env["STEWARD_LLM_BASE_URL"])
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	parsedBaseURL, err := url.Parse(baseURL)
	if err != nil || parsedBaseURL.Scheme == "" || parsedBaseURL.Host == "" || (parsedBaseURL.Scheme != "http" && parsedBaseURL.Scheme != "https") {
		problems = append(problems, "STEWARD_LLM_BASE_URL must be an http or https URL with a host")
	}

	allowNoAPIKey, err := parseOptionalBool(env["STEWARD_LLM_ALLOW_NO_API_KEY"])
	if err != nil {
		problems = append(problems, "STEWARD_LLM_ALLOW_NO_API_KEY must be true or false")
	}
	if strings.TrimSpace(env["STEWARD_LLM_API_KEY"]) == "" && !allowNoAPIKey {
		problems = append(problems, "STEWARD_LLM_API_KEY is required unless STEWARD_LLM_ALLOW_NO_API_KEY=true")
	}
	if allowNoAPIKey && parsedBaseURL != nil && !isLoopbackHost(parsedBaseURL.Hostname()) {
		problems = append(problems, "STEWARD_LLM_ALLOW_NO_API_KEY=true is only allowed for loopback OpenAI-compatible endpoints")
	}

	maxDataLevel := strings.ToUpper(strings.TrimSpace(env["STEWARD_LLM_MAX_DATA_LEVEL"]))
	if maxDataLevel == "" {
		maxDataLevel = "D1"
	}
	if maxDataLevel != "D0" && maxDataLevel != "D1" {
		problems = append(problems, "STEWARD_LLM_MAX_DATA_LEVEL must be D0 or D1 under strict security")
	}

	if value := strings.TrimSpace(env["STEWARD_LLM_TIMEOUT"]); value != "" {
		if parsed, err := time.ParseDuration(value); err != nil || parsed <= 0 || parsed > 2*time.Minute {
			problems = append(problems, "STEWARD_LLM_TIMEOUT must be a duration greater than 0 and no more than 2m")
		}
	}
	if value := strings.TrimSpace(env["STEWARD_LLM_FAILURE_THRESHOLD"]); value != "" {
		if parsed, err := strconv.Atoi(value); err != nil || parsed <= 0 || parsed > 100 {
			problems = append(problems, "STEWARD_LLM_FAILURE_THRESHOLD must be an integer from 1 to 100")
		}
	}
	if value := strings.TrimSpace(env["STEWARD_LLM_FAILURE_COOLDOWN"]); value != "" {
		if parsed, err := time.ParseDuration(value); err != nil || parsed <= 0 || parsed > time.Hour {
			problems = append(problems, "STEWARD_LLM_FAILURE_COOLDOWN must be a duration greater than 0 and no more than 1h")
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("S4 advisor strict validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}

func parseOptionalBool(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	return strconv.ParseBool(value)
}

func isLoopbackHost(host string) bool {
	host = strings.TrimSpace(host)
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
