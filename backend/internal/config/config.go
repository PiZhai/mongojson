package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"mongojson/backend/internal/platform/netpolicy"
)

type Config struct {
	HTTPAddr                  string
	PeerHTTPAddr              string
	AllowRemoteManagement     bool
	ManagementAuthRequired    bool
	ManagementAuthToken       string
	ManagementAllowedOrigins  []string
	DatabaseURL               string
	StorageDir                string
	StewardUIDir              string
	FileRetention             time.Duration
	DisabledModules           map[string]bool
	MongoReviewAnalyzerURL    string
	MongoReviewRepositoryRoot string
	MongoReviewEncryptionKey  string
}

const DefaultHTTPAddr = "127.0.0.1:18080"

func Load() (Config, error) {
	_ = godotenv.Load()

	retentionHours := getenvInt("FILE_RETENTION_HOURS", 24)
	allowRemoteManagement, err := getenvBool("STEWARD_ALLOW_REMOTE_MANAGEMENT", false)
	if err != nil {
		return Config{}, err
	}
	managementAuthRequired, err := getenvBool("STEWARD_MANAGEMENT_AUTH_REQUIRED", false)
	if err != nil {
		return Config{}, err
	}
	restrictedService, err := getenvBool("STEWARD_RESTRICTED_SERVICE", false)
	if err != nil {
		return Config{}, err
	}
	managementAuthToken := strings.TrimSpace(os.Getenv("STEWARD_MANAGEMENT_AUTH_TOKEN"))
	if managementAuthToken != "" && len(managementAuthToken) < 32 {
		return Config{}, fmt.Errorf("STEWARD_MANAGEMENT_AUTH_TOKEN must contain at least 32 characters")
	}
	if (managementAuthRequired || allowRemoteManagement || restrictedService) && managementAuthToken == "" {
		return Config{}, fmt.Errorf("STEWARD_MANAGEMENT_AUTH_TOKEN is required when management authentication, remote management, or restricted-service mode is enabled")
	}
	managementAllowedOrigins, err := parseManagementAllowedOrigins(os.Getenv("STEWARD_MANAGEMENT_ALLOWED_ORIGINS"))
	if err != nil {
		return Config{}, err
	}
	disabledModules, err := parseDisabledModules(os.Getenv("APP_DISABLED_MODULES"))
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		HTTPAddr:                  strings.TrimSpace(getenv("HTTP_ADDR", DefaultHTTPAddr)),
		PeerHTTPAddr:              strings.TrimSpace(os.Getenv("STEWARD_PEER_HTTP_ADDR")),
		AllowRemoteManagement:     allowRemoteManagement,
		ManagementAuthRequired:    managementAuthRequired || managementAuthToken != "" || allowRemoteManagement || restrictedService,
		ManagementAuthToken:       managementAuthToken,
		ManagementAllowedOrigins:  managementAllowedOrigins,
		DatabaseURL:               getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable"),
		StorageDir:                getenv("STORAGE_DIR", "./data"),
		StewardUIDir:              strings.TrimSpace(os.Getenv("STEWARD_UI_DIR")),
		FileRetention:             time.Duration(retentionHours) * time.Hour,
		DisabledModules:           disabledModules,
		MongoReviewAnalyzerURL:    strings.TrimRight(getenv("MONGODB_REVIEW_ANALYZER_URL", "http://127.0.0.1:8090"), "/"),
		MongoReviewRepositoryRoot: strings.TrimSpace(getenv("MONGODB_REVIEW_REPOSITORY_ROOT", "/Users/administrator/GolandProjects/script")),
		MongoReviewEncryptionKey:  strings.TrimSpace(os.Getenv("MONGODB_REVIEW_ENCRYPTION_KEY")),
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if err := netpolicy.ValidateListenerTopology(cfg.HTTPAddr, cfg.PeerHTTPAddr, cfg.AllowRemoteManagement); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) ModuleDisabled(module string) bool {
	return c.DisabledModules[strings.ToLower(strings.TrimSpace(module))]
}

func parseDisabledModules(value string) (map[string]bool, error) {
	const stewardModule = "steward"
	disabled := map[string]bool{}
	for _, raw := range strings.Split(value, ",") {
		module := strings.ToLower(strings.TrimSpace(raw))
		if module == "" {
			continue
		}
		if module != stewardModule {
			return nil, fmt.Errorf("APP_DISABLED_MODULES contains unsupported backend module %q", module)
		}
		disabled[module] = true
	}
	return disabled, nil
}

func parseManagementAllowedOrigins(value string) ([]string, error) {
	seen := map[string]struct{}{}
	result := make([]string, 0)
	for _, raw := range strings.Split(value, ",") {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		if raw == "*" {
			return nil, fmt.Errorf("STEWARD_MANAGEMENT_ALLOWED_ORIGINS must not contain wildcard origins")
		}
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("STEWARD_MANAGEMENT_ALLOWED_ORIGINS contains invalid origin %q", raw)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("STEWARD_MANAGEMENT_ALLOWED_ORIGINS origin %q must use http or https", raw)
		}
		origin := strings.ToLower(parsed.Scheme + "://" + parsed.Host)
		if _, exists := seen[origin]; exists {
			continue
		}
		seen[origin] = struct{}{}
		result = append(result, origin)
	}
	return result, nil
}

func getenv(key string, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func getenvBool(key string, fallback bool) (bool, error) {
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
