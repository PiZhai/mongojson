package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	HTTPAddr      string
	DatabaseURL   string
	StorageDir    string
	FileRetention time.Duration
}

func Load() (Config, error) {
	_ = godotenv.Load()

	retentionHours := getenvInt("FILE_RETENTION_HOURS", 24)
	cfg := Config{
		HTTPAddr:      getenv("HTTP_ADDR", ":8080"),
		DatabaseURL:   getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/mongojson?sslmode=disable"),
		StorageDir:    getenv("STORAGE_DIR", "./data"),
		FileRetention: time.Duration(retentionHours) * time.Hour,
	}

	if cfg.DatabaseURL == "" {
		return Config{}, fmt.Errorf("DATABASE_URL is required")
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
