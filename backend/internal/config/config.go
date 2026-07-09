package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"

	"mongojson/backend/internal/platform/netpolicy"
)

type Config struct {
	HTTPAddr              string
	PeerHTTPAddr          string
	AllowRemoteManagement bool
	DatabaseURL           string
	StorageDir            string
	StewardUIDir          string
	FileRetention         time.Duration
}

func Load() (Config, error) {
	_ = godotenv.Load()

	retentionHours := getenvInt("FILE_RETENTION_HOURS", 24)
	cfg := Config{
		HTTPAddr:              strings.TrimSpace(getenv("HTTP_ADDR", "127.0.0.1:8080")),
		PeerHTTPAddr:          strings.TrimSpace(os.Getenv("STEWARD_PEER_HTTP_ADDR")),
		AllowRemoteManagement: getenvBool("STEWARD_ALLOW_REMOTE_MANAGEMENT", false),
		DatabaseURL:           getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable"),
		StorageDir:            getenv("STORAGE_DIR", "./data"),
		StewardUIDir:          strings.TrimSpace(os.Getenv("STEWARD_UI_DIR")),
		FileRetention:         time.Duration(retentionHours) * time.Hour,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	}
	if err := netpolicy.ValidateListenerTopology(cfg.HTTPAddr, cfg.PeerHTTPAddr, cfg.AllowRemoteManagement); err != nil {
		return Config{}, err
	}
	return cfg, nil
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

func getenvBool(key string, fallback bool) bool {
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
