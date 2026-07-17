package main

import (
	"bytes"
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

const defaultAPIRequestTimeout = 20 * time.Second

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
		// The steward is a single-user, device-owner assistant. Production runs
		// default to full local visibility and execution access; legacy D/A policy
		// fields remain only for storage and protocol compatibility.
		if _, configured := os.LookupEnv("STEWARD_OWNER_MODE"); !configured {
			_ = os.Setenv("STEWARD_OWNER_MODE", "true")
		}
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
		client:  &http.Client{Timeout: envDurationOrDefault("STEWARD_API_TIMEOUT", defaultAPIRequestTimeout)},
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
	resolvedUIDir := currentBinaryUIDir(*uiDir)
	if resolvedUIDir != "" {
		if err := os.Setenv("STEWARD_UI_DIR", resolvedUIDir); err != nil {
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
                      use --verify-advisor-privacy-probe to prove unsupported D7 data is rejected before model submission
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
                      use --verify-advisor-privacy-probe to prove unsupported D7 data is rejected before model submission
  service uninstall   uninstall the system service entry
  service start       start the installed service
  service stop        stop the installed service
  service restart     restart the installed service
  service status      print installed service status
  verify runtime      run S3/S4 runtime verification checks through the local management API
                      use --evidence-dir to persist timestamped verification JSON
                      use --advisor-probe to call the configured S4 model advisor once
                      use --advisor-probe-each-sample with --watch-duration for long-run model checks
                      use --advisor-privacy-probe to verify unsupported D7 data is rejected before model submission
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
  --advisor-privacy-probe              prove unsupported D7 data is blocked before model submission
  --watch-duration 24h                 require repeated samples and heartbeat advance
  --evidence-dir <dir>                 persist timestamped evidence JSON
  --scope user|system                  with verify service, check this service manager scope
  --require-kind-platform-service-scope service:windows:system
                                      with verify evidence, require a specific service scope
  --require-kind-platform-service-name service:windows:MongojsonSteward
                                      with verify evidence, require a specific service name
  --require-kind-platform-advisor-model service:windows:<model>
                                      with verify evidence, require a specific S4 advisor model
  --preset s3s4-final-system           require physical Windows/macOS/Linux install, service, mesh, final-host, and system-scope evidence

examples:
  steward verify runtime --strict-security --write-probes
  steward verify service --strict-security --watch-duration 24h --evidence-dir .\evidence\s3s4
  steward verify mesh --node http://127.0.0.1:18080/api --node http://127.0.0.1:28080/api --strict-security --sync --write-probes
  steward verify evidence --dir .\evidence\s3s4 --preset s3s4-final-system`)
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
