package privilegebroker

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type GeneratedKeys struct {
	ClientKey         string `json:"client_key"`
	ControlKey        string `json:"control_key"`
	SigningPublicKey  string `json:"signing_public_key"`
	SigningPrivateKey string `json:"signing_private_key"`
	KeyID             string `json:"key_id"`
}

func GenerateKeys() (GeneratedKeys, error) {
	clientKey := make([]byte, 32)
	if _, err := rand.Read(clientKey); err != nil {
		return GeneratedKeys{}, err
	}
	controlKey := make([]byte, 32)
	if _, err := rand.Read(controlKey); err != nil {
		return GeneratedKeys{}, err
	}
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return GeneratedKeys{}, err
	}
	return GeneratedKeys{
		ClientKey:         base64.StdEncoding.EncodeToString(clientKey),
		ControlKey:        base64.StdEncoding.EncodeToString(controlKey),
		SigningPublicKey:  base64.StdEncoding.EncodeToString(publicKey),
		SigningPrivateKey: base64.StdEncoding.EncodeToString(privateKey),
		KeyID:             publicKeyID(publicKey),
	}, nil
}

func ServerConfigFromEnv() (ServerConfig, error) {
	clientKey, err := decodeSharedKey(osEnv("STEWARD_BROKER_CLIENT_KEY"))
	if err != nil {
		return ServerConfig{}, err
	}
	controlKey, err := decodeSharedKey(osEnv("STEWARD_BROKER_CONTROL_KEY"))
	if err != nil {
		return ServerConfig{}, fmt.Errorf("decode broker control key: %w", err)
	}
	privateKey, err := decodePrivateKey(osEnv("STEWARD_BROKER_SIGNING_PRIVATE_KEY"))
	if err != nil {
		return ServerConfig{}, err
	}
	grantTTL, err := durationEnvValue("STEWARD_BROKER_GRANT_TTL", 30*time.Second)
	if err != nil {
		return ServerConfig{}, err
	}
	requestSkew, err := durationEnvValue("STEWARD_BROKER_REQUEST_SKEW", 30*time.Second)
	if err != nil {
		return ServerConfig{}, err
	}
	dataDir := defaultEnv("STEWARD_BROKER_DATA_DIR", filepath.Join(".", "data", "privilege-broker"))
	return ServerConfig{
		DeviceID:       defaultEnv("STEWARD_BROKER_DEVICE_ID", ""),
		ListenAddress:  defaultEnv("STEWARD_BROKER_LISTEN", "127.0.0.1:18100"),
		PolicyPath:     defaultEnv("STEWARD_BROKER_POLICY", filepath.Join(dataDir, "policy.json")),
		StatePath:      defaultEnv("STEWARD_BROKER_STATE", filepath.Join(dataDir, "state.json")),
		AuditPath:      defaultEnv("STEWARD_BROKER_AUDIT", filepath.Join(dataDir, "audit.jsonl")),
		CheckpointPath: defaultEnv("STEWARD_BROKER_CHECKPOINT", filepath.Join(dataDir, "checkpoint.json")),
		ClientKey:      clientKey, ControlKey: controlKey, SigningKey: privateKey, GrantTTL: grantTTL, RequestSkew: requestSkew,
	}, nil
}

func ClientEnvironmentConfigured() bool {
	value := strings.TrimSpace(osEnv("STEWARD_BROKER_CLIENT_KEY"))
	public := strings.TrimSpace(osEnv("STEWARD_BROKER_PUBLIC_KEY"))
	return value != "" || public != "" || strings.TrimSpace(osEnv("STEWARD_BROKER_URL")) != ""
}

func durationEnvValue(key string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(osEnv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}

var osEnv = os.Getenv

func defaultEnv(key, fallback string) string {
	value := strings.TrimSpace(osEnv(key))
	if value == "" {
		return fallback
	}
	return value
}
