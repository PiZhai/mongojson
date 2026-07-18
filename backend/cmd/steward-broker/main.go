package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"mongojson/backend/internal/platform/servicecontrol"
	"mongojson/backend/internal/privilegebroker"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = runBroker(os.Args[2:])
	case "keygen":
		err = keygen()
	case "bootstrap":
		err = bootstrap(argsAfterCommand())
	case "refresh-system-policy":
		err = refreshSystemPolicy(argsAfterCommand())
	case "validate-policy":
		err = validatePolicy(os.Args[2:])
	case "initialize-checkpoint":
		err = initializeCheckpoint(os.Args[2:])
	case "status":
		err = brokerStatus()
	case "tool-execute":
		err = brokerToolExecute(argsAfterCommand())
	case "control":
		err = brokerControl(os.Args[2:])
	case "service":
		err = brokerService(os.Args[2:])
	case "session0-self-test-service":
		err = session0SelfTestService(os.Args[2:])
	case "session0-self-test-child":
		privilegebroker.CapabilityLaunchSelfTestChild(os.Args[2:])
	default:
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func session0SelfTestService(args []string) error {
	fs := flag.NewFlagSet("steward-broker session0-self-test-service", flag.ContinueOnError)
	serviceName := fs.String("service-name", "MongojsonStewardBrokerSession0Smoke", "temporary Windows service name")
	resultFile := fs.String("result-file", "", "absolute JSON result path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*resultFile) == "" {
		return fmt.Errorf("session0 self-test requires --result-file")
	}
	path, err := filepath.Abs(*resultFile)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	return servicecontrol.Run(*serviceName, func(ctx context.Context) error {
		testCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		secretProbe := path + ".broker-secret"
		if err := os.WriteFile(secretProbe, []byte("must-not-be-readable-by-capability"), 0o600); err != nil {
			return err
		}
		defer os.Remove(secretProbe)
		if err := servicecontrol.ProtectServicePaths(*serviceName, []string{secretProbe}); err != nil {
			return fmt.Errorf("protect Session 0 secret probe: %w", err)
		}
		if _, err := os.ReadFile(secretProbe); err != nil {
			return fmt.Errorf("Broker lost access to service-SID secret probe: %w", err)
		}
		result := privilegebroker.RunCapabilityLaunchSelfTestSuiteWithDeniedPath(testCtx, executable, secretProbe)
		payload, marshalErr := json.MarshalIndent(result, "", "  ")
		if marshalErr != nil {
			return marshalErr
		}
		if err := os.WriteFile(path, payload, 0o600); err != nil {
			return fmt.Errorf("write Session 0 self-test result: %w", err)
		}
		return nil
	})
}

func runBroker(args []string) error {
	fs := flag.NewFlagSet("steward-broker run", flag.ContinueOnError)
	serviceName := fs.String("service-name", defaultBrokerServiceName(runtime.GOOS), "platform service name")
	workDir := fs.String("workdir", "", "working directory used before reading broker environment")
	privateEnvironmentFile := fs.String("private-environment-file", "", "protected JSON file containing service secrets")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*workDir) != "" {
		if err := os.Chdir(*workDir); err != nil {
			return fmt.Errorf("change broker workdir: %w", err)
		}
	}
	if strings.TrimSpace(*privateEnvironmentFile) != "" {
		if err := servicecontrol.LoadPrivateEnvironmentFile(*privateEnvironmentFile); err != nil {
			return err
		}
	}
	config, err := privilegebroker.ServerConfigFromEnv()
	if err != nil {
		return err
	}
	server, err := privilegebroker.NewServer(config)
	if err != nil {
		return err
	}
	log.Printf("steward privilege broker listening on %s", config.ListenAddress)
	return servicecontrol.Run(*serviceName, func(ctx context.Context) error {
		return server.Run(ctx, config.ListenAddress)
	})
}

func keygen() error {
	keys, err := privilegebroker.GenerateKeys()
	if err != nil {
		return err
	}
	return printJSON(map[string]any{
		"keys": keys,
		"broker_env": map[string]string{
			"STEWARD_BROKER_CLIENT_KEY":          keys.ClientKey,
			"STEWARD_BROKER_CONTROL_KEY":         keys.ControlKey,
			"STEWARD_BROKER_SIGNING_PRIVATE_KEY": keys.SigningPrivateKey,
		},
		"steward_env": map[string]string{
			"STEWARD_BROKER_CLIENT_KEY": keys.ClientKey,
			"STEWARD_BROKER_PUBLIC_KEY": keys.SigningPublicKey,
		},
	})
}

type systemToolCatalog struct {
	Protocol string `json:"protocol"`
	Tools    []struct {
		Name           string         `json:"name"`
		Description    string         `json:"description"`
		InputSchema    map[string]any `json:"input_schema"`
		TimeoutSeconds int            `json:"timeout_seconds"`
	} `json:"tools"`
}

func argsAfterCommand() []string { return os.Args[2:] }

func bootstrap(args []string) (returnErr error) {
	fs := flag.NewFlagSet("steward-broker bootstrap", flag.ContinueOnError)
	outputDir := fs.String("output-dir", "", "new protected bootstrap directory")
	systemToolHost := fs.String("system-tool-host", "", "absolute steward-system-tool-host executable")
	force := fs.Bool("force", false, "replace an existing bootstrap directory while retaining a rollback backup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*outputDir) == "" || strings.TrimSpace(*systemToolHost) == "" {
		return fmt.Errorf("bootstrap requires --output-dir and --system-tool-host")
	}
	host, err := filepath.Abs(strings.TrimSpace(*systemToolHost))
	if err != nil {
		return err
	}
	info, err := os.Stat(host)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("system tool host is unavailable: %s", host)
	}
	hostDigest, err := hashBootstrapFile(host)
	if err != nil {
		return err
	}
	catalogPayload, err := exec.Command(host, "catalog").Output()
	if err != nil {
		return fmt.Errorf("read system tool catalog: %w", err)
	}
	var catalog systemToolCatalog
	if err := json.Unmarshal(catalogPayload, &catalog); err != nil || catalog.Protocol != "steward-system-tool-catalog/1" || len(catalog.Tools) == 0 {
		return fmt.Errorf("system tool host returned an invalid catalog")
	}
	dir, err := filepath.Abs(strings.TrimSpace(*outputDir))
	if err != nil {
		return err
	}
	backup := ""
	if _, err := os.Stat(dir); err == nil {
		if !*force {
			return fmt.Errorf("bootstrap directory already exists: %s", dir)
		}
		backup = fmt.Sprintf("%s.backup-%s", dir, time.Now().UTC().Format("20060102-150405"))
		if err := os.Rename(dir, backup); err != nil {
			return fmt.Errorf("backup existing bootstrap directory: %w", err)
		}
	}
	defer func() {
		if returnErr != nil && backup != "" {
			_ = os.RemoveAll(dir)
			_ = os.Rename(backup, dir)
		}
	}()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	brokerKeys, err := privilegebroker.GenerateKeys()
	if err != nil {
		return err
	}
	approvalKeys, err := privilegebroker.GenerateApprovalAuthorityKeys()
	if err != nil {
		return err
	}
	capabilities := make([]privilegebroker.Capability, 0, len(catalog.Tools))
	for _, item := range catalog.Tools {
		capabilities = append(capabilities, privilegebroker.Capability{
			Name: "tool:" + strings.ToLower(strings.TrimSpace(item.Name)), Description: item.Description,
			Executable: host, ExecutableSHA256: hostDigest,
			Arguments: []string{"run"}, WorkingDirectory: filepath.Dir(host), TimeoutSeconds: item.TimeoutSeconds,
			MaxOutputBytes: 8 << 20, Enabled: true, InputSchema: item.InputSchema,
		})
	}
	policy := privilegebroker.Policy{Version: 3, ApprovalAuthorities: []privilegebroker.ApprovalAuthority{{
		Name: "local-operator", Algorithm: privilegebroker.ApprovalEd25519, PublicKey: approvalKeys.PublicKey, Enabled: true,
	}}, Capabilities: capabilities}
	policyPath := filepath.Join(dir, "policy.json")
	if err := writeBootstrapJSON(policyPath, policy); err != nil {
		return err
	}
	brokerSecrets := map[string]string{
		"STEWARD_BROKER_CLIENT_KEY": brokerKeys.ClientKey, "STEWARD_BROKER_CONTROL_KEY": brokerKeys.ControlKey,
		"STEWARD_BROKER_SIGNING_PRIVATE_KEY": brokerKeys.SigningPrivateKey,
	}
	if err := writeBootstrapJSON(filepath.Join(dir, "broker-secrets.json"), brokerSecrets); err != nil {
		return err
	}
	if err := writeBootstrapJSON(filepath.Join(dir, "steward-broker-client.json"), map[string]string{
		"STEWARD_BROKER_CLIENT_KEY": brokerKeys.ClientKey, "STEWARD_BROKER_PUBLIC_KEY": brokerKeys.SigningPublicKey,
	}); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "approval-private-key.txt"), []byte(approvalKeys.PrivateKey+"\n"), 0o600); err != nil {
		return err
	}
	manifest := map[string]any{"version": "steward-broker-bootstrap/1", "created_at": time.Now().UTC(),
		"system_tool_host": host, "system_tool_host_sha256": hostDigest, "capability_count": len(capabilities),
		"policy": policyPath, "rollback_backup": backup}
	if err := writeBootstrapJSON(filepath.Join(dir, "bootstrap-manifest.json"), manifest); err != nil {
		return err
	}
	return printJSON(map[string]any{"ok": true, "output_dir": dir, "policy": policyPath,
		"broker_secrets": filepath.Join(dir, "broker-secrets.json"), "steward_client": filepath.Join(dir, "steward-broker-client.json"),
		"approval_private_key": filepath.Join(dir, "approval-private-key.txt"), "capabilities": len(capabilities), "rollback_backup": backup})
}

func refreshSystemPolicy(args []string) error {
	fs := flag.NewFlagSet("steward-broker refresh-system-policy", flag.ContinueOnError)
	policyPath := fs.String("policy", "", "existing protected Broker policy")
	systemToolHost := fs.String("system-tool-host", "", "new installed System Tool Host")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*policyPath) == "" || strings.TrimSpace(*systemToolHost) == "" {
		return fmt.Errorf("refresh-system-policy requires --policy and --system-tool-host")
	}
	host, err := filepath.Abs(*systemToolHost)
	if err != nil {
		return err
	}
	digest, err := hashBootstrapFile(host)
	if err != nil {
		return err
	}
	payload, err := os.ReadFile(*policyPath)
	if err != nil {
		return err
	}
	var policy privilegebroker.Policy
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&policy); err != nil {
		return fmt.Errorf("decode existing policy: %w", err)
	}
	catalogPayload, err := exec.Command(host, "catalog").Output()
	if err != nil {
		return err
	}
	var catalog systemToolCatalog
	if err := json.Unmarshal(catalogPayload, &catalog); err != nil || catalog.Protocol != "steward-system-tool-catalog/1" {
		return fmt.Errorf("invalid System Tool Host catalog")
	}
	preserved := make([]privilegebroker.Capability, 0, len(policy.Capabilities)+len(catalog.Tools))
	for _, capability := range policy.Capabilities {
		if capability.InputSchema == nil || !strings.HasPrefix(strings.ToLower(capability.Name), "tool:") {
			preserved = append(preserved, capability)
		}
	}
	for _, item := range catalog.Tools {
		preserved = append(preserved, privilegebroker.Capability{Name: "tool:" + strings.ToLower(item.Name), Description: item.Description, Executable: host, ExecutableSHA256: digest, Arguments: []string{"run"}, WorkingDirectory: filepath.Dir(host), TimeoutSeconds: item.TimeoutSeconds, MaxOutputBytes: 8 << 20, Enabled: true, InputSchema: item.InputSchema})
	}
	policy.Capabilities = preserved
	loaded, err := privilegebroker.ValidatePolicy(policy)
	if err != nil {
		return fmt.Errorf("validate refreshed policy: %w", err)
	}
	backup := fmt.Sprintf("%s.backup-%s", *policyPath, time.Now().UTC().Format("20060102-150405"))
	if err := os.WriteFile(backup, payload, 0o600); err != nil {
		return err
	}
	if err := writeBootstrapJSON(*policyPath, loaded.Policy); err != nil {
		_ = os.Rename(backup, *policyPath)
		return err
	}
	return printJSON(map[string]any{"ok": true, "policy": *policyPath, "backup": backup, "policy_digest": loaded.Digest, "system_capabilities": len(catalog.Tools)})
}

func hashBootstrapFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 1<<30)); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func writeBootstrapJSON(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	temp := path + ".tmp"
	if err := os.WriteFile(temp, payload, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

func validatePolicy(args []string) error {
	fs := flag.NewFlagSet("steward-broker validate-policy", flag.ContinueOnError)
	path := fs.String("policy", envOrDefault("STEWARD_BROKER_POLICY", ""), "absolute broker policy path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	policy, err := privilegebroker.LoadPolicy(*path)
	if err != nil {
		return err
	}
	return printJSON(map[string]any{"valid": true, "policy_digest": policy.Digest, "approval_authorities": policy.PublicApprovalAuthorities(), "capabilities": policy.PublicCapabilities()})
}

func initializeCheckpoint(args []string) error {
	fs := flag.NewFlagSet("steward-broker initialize-checkpoint", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	config, err := privilegebroker.ServerConfigFromEnv()
	if err != nil {
		return err
	}
	if err := privilegebroker.InitializeCheckpoint(config); err != nil {
		return err
	}
	return printJSON(map[string]any{"initialized": true, "checkpoint": config.CheckpointPath})
}

func brokerStatus() error {
	client, err := privilegebroker.NewClientFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := client.Status(ctx)
	if err != nil {
		return err
	}
	return printJSON(status)
}

func brokerToolExecute(args []string) error {
	fs := flag.NewFlagSet("steward-broker tool-execute", flag.ContinueOnError)
	capability := fs.String("capability", "", "parameterized policy capability")
	argumentsJSON := fs.String("arguments-json", "{}", "one JSON object matching the capability schema")
	invocationID := fs.String("invocation-id", fmt.Sprintf("acceptance-%d", time.Now().UnixNano()), "unique idempotency and receipt id")
	subject := fs.String("subject", "local-admin-acceptance", "audited caller identity")
	if err := fs.Parse(args); err != nil {
		return err
	}
	var arguments map[string]any
	decoder := json.NewDecoder(strings.NewReader(*argumentsJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&arguments); err != nil {
		return fmt.Errorf("decode arguments-json: %w", err)
	}
	client, err := privilegebroker.NewClientFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	status, err := client.Status(ctx)
	if err != nil {
		return err
	}
	result, err := client.ExecuteTool(ctx, privilegebroker.ToolAuthorization{Capability: *capability, Subject: *subject, InvocationID: *invocationID, Arguments: arguments, ControlGeneration: status.Generation})
	if err != nil {
		return err
	}
	return printJSON(result)
}

func brokerControl(args []string) error {
	if len(args) == 0 || (args[0] != "stop" && args[0] != "resume") {
		return fmt.Errorf("control requires stop or resume")
	}
	stopped := args[0] == "stop"
	fs := flag.NewFlagSet("steward-broker control "+args[0], flag.ContinueOnError)
	generation := fs.Int64("generation", -1, "monotonic unified execution-control generation")
	reason := fs.String("reason", "", "audited control reason")
	changedBy := fs.String("changed-by", "local-admin", "audited operator identity")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if *generation < 0 || strings.TrimSpace(*reason) == "" {
		return fmt.Errorf("control requires --generation >= 0 and --reason")
	}
	client, err := privilegebroker.NewClientFromEnv()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := client.SetControl(ctx, stopped, privilegebroker.ControlRequest{Generation: *generation, Reason: *reason, ChangedBy: *changedBy})
	if err != nil {
		return err
	}
	return printJSON(status)
}

func brokerService(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("service requires install, uninstall, start, stop, restart, or status")
	}
	switch args[0] {
	case "install":
		return brokerServiceInstall(args[1:])
	case "uninstall", "start", "stop", "restart", "status":
		return brokerServiceAction(args[0], args[1:])
	default:
		return fmt.Errorf("unknown broker service action %q", args[0])
	}
}

func brokerServiceInstall(args []string) error {
	name := defaultBrokerServiceName(runtime.GOOS)
	scope := servicecontrol.ScopeSystem
	binary, _ := os.Executable()
	workDir, _ := os.Getwd()
	dataDir := envOrDefault("STEWARD_BROKER_DATA_DIR", filepath.Join(workDir, "data", "privilege-broker"))
	installDir := ""
	privateEnvironmentFile := ""
	if runtime.GOOS == "windows" {
		installDir = filepath.Join(os.Getenv("ProgramFiles"), "MongoJSON", "StewardBroker")
		dataDir = envOrDefault("STEWARD_BROKER_DATA_DIR", filepath.Join(os.Getenv("ProgramData"), "MongoJSON", "StewardBroker"))
		workDir = dataDir
		privateEnvironmentFile = filepath.Join(dataDir, "service-secrets.json")
	}
	listen := envOrDefault("STEWARD_BROKER_LISTEN", "127.0.0.1:18100")
	policyPath := envOrDefault("STEWARD_BROKER_POLICY", filepath.Join(dataDir, "policy.json"))
	statePath := envOrDefault("STEWARD_BROKER_STATE", filepath.Join(dataDir, "state.json"))
	auditPath := envOrDefault("STEWARD_BROKER_AUDIT", filepath.Join(dataDir, "audit.jsonl"))
	checkpointPath := envOrDefault("STEWARD_BROKER_CHECKPOINT", filepath.Join(dataDir, "checkpoint.json"))
	clientKey := envOrDefault("STEWARD_BROKER_CLIENT_KEY", "")
	controlKey := envOrDefault("STEWARD_BROKER_CONTROL_KEY", "")
	privateKey := envOrDefault("STEWARD_BROKER_SIGNING_PRIVATE_KEY", "")
	grantTTL := envOrDefault("STEWARD_BROKER_GRANT_TTL", "30s")
	requestSkew := envOrDefault("STEWARD_BROKER_REQUEST_SKEW", "30s")
	deviceID := envOrDefault("STEWARD_BROKER_DEVICE_ID", "")
	fs := flag.NewFlagSet("steward-broker service install", flag.ContinueOnError)
	fs.StringVar(&name, "name", name, "service name")
	fs.StringVar(&scope, "scope", scope, "service scope; system is required for privilege separation")
	fs.StringVar(&binary, "binary", binary, "steward-broker executable")
	fs.StringVar(&workDir, "workdir", workDir, "broker working directory")
	fs.StringVar(&installDir, "install-dir", installDir, "protected Windows service executable directory")
	fs.StringVar(&privateEnvironmentFile, "private-environment-file", privateEnvironmentFile, "protected Windows service secret file")
	fs.StringVar(&listen, "listen", listen, "explicit loopback broker address")
	fs.StringVar(&policyPath, "policy", policyPath, "root-owned broker policy file")
	fs.StringVar(&statePath, "state", statePath, "persistent emergency-stop state file")
	fs.StringVar(&auditPath, "audit", auditPath, "append-only broker audit file")
	fs.StringVar(&checkpointPath, "checkpoint", checkpointPath, "rollback-resistant broker checkpoint file")
	fs.StringVar(&clientKey, "client-key", clientKey, "base64 request-authentication key")
	fs.StringVar(&controlKey, "control-key", controlKey, "base64 administrator control key")
	fs.StringVar(&privateKey, "signing-private-key", privateKey, "base64 Ed25519 broker private key")
	fs.StringVar(&grantTTL, "grant-ttl", grantTTL, "short capability-token TTL")
	fs.StringVar(&requestSkew, "request-skew", requestSkew, "request signature clock-skew window")
	fs.StringVar(&deviceID, "device-id", deviceID, "stable Steward device id used for Broker federation")
	dryRun := fs.Bool("dry-run", false, "render service changes without applying")
	start := fs.Bool("start", false, "start the broker after installation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if scope != servicecontrol.ScopeSystem {
		return fmt.Errorf("privilege broker service must use system scope")
	}
	if _, err := privilegebroker.LoadPolicy(policyPath); err != nil {
		return err
	}
	if err := validateBase64Length(clientKey, 32, false); err != nil {
		return fmt.Errorf("client key: %w", err)
	}
	if err := validateBase64Length(controlKey, 32, false); err != nil {
		return fmt.Errorf("control key: %w", err)
	}
	if err := validateBase64Length(privateKey, 64, true); err != nil {
		return fmt.Errorf("signing private key: %w", err)
	}
	if *dryRun && *start {
		return fmt.Errorf("--start cannot be combined with --dry-run")
	}
	servicePolicyPath := policyPath
	protectedCopies := map[string]string{}
	serviceArgs := []string{"run", "--service-name", name, "--workdir", workDir}
	if runtime.GOOS == "windows" {
		servicePolicyPath = filepath.Join(dataDir, "policy.json")
		protectedCopies[servicePolicyPath] = policyPath
		serviceArgs = append(serviceArgs, "--private-environment-file", privateEnvironmentFile)
	}
	options := servicecontrol.InstallOptions{
		Name: name, Scope: scope, DisplayName: "MongoJSON Steward Privilege Broker",
		Description: "Isolated system-scope executor for approved steward capabilities",
		BinaryPath:  binary, WorkDir: workDir, HTTPAddr: listen, PeerHTTPAddr: "127.0.0.1:18101",
		DatabaseURL: "postgres://unused:unused@127.0.0.1:1/unused?sslmode=disable",
		StorageDir:  dataDir, DryRun: *dryRun, ServiceArgs: serviceArgs,
		WindowsHardened: runtime.GOOS == "windows", InstallDir: installDir,
		PrivateEnvironmentFile: privateEnvironmentFile,
		WindowsServiceAccount:  "localsystem", WindowsServiceSIDType: "unrestricted",
		ProtectedPaths: []string{dataDir, servicePolicyPath}, ProtectedFileCopies: protectedCopies,
		ExplicitEnvironment: map[string]string{
			"STEWARD_BROKER_LISTEN": listen, "STEWARD_BROKER_POLICY": servicePolicyPath,
			"STEWARD_BROKER_STATE": statePath, "STEWARD_BROKER_AUDIT": auditPath,
			"STEWARD_BROKER_CHECKPOINT": checkpointPath,
			"STEWARD_BROKER_DEVICE_ID":  deviceID,
			"STEWARD_BROKER_DATA_DIR":   dataDir, "STEWARD_BROKER_CLIENT_KEY": clientKey,
			"STEWARD_BROKER_CONTROL_KEY":         controlKey,
			"STEWARD_BROKER_SIGNING_PRIVATE_KEY": privateKey,
			"STEWARD_BROKER_GRANT_TTL":           grantTTL, "STEWARD_BROKER_REQUEST_SKEW": requestSkew,
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := servicecontrol.Install(ctx, options)
	if err != nil {
		return err
	}
	if !*dryRun {
		if _, err := privilegebroker.LoadPolicy(servicePolicyPath); err != nil {
			return fmt.Errorf("validate staged protected policy: %w", err)
		}
		clientKeyBytes, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(clientKey))
		controlKeyBytes, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(controlKey))
		privateKeyBytes, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(privateKey))
		grantDuration, err := time.ParseDuration(grantTTL)
		if err != nil {
			return fmt.Errorf("grant ttl: %w", err)
		}
		skewDuration, err := time.ParseDuration(requestSkew)
		if err != nil {
			return fmt.Errorf("request skew: %w", err)
		}
		config := privilegebroker.ServerConfig{
			DeviceID: deviceID, ListenAddress: listen, PolicyPath: servicePolicyPath, StatePath: statePath,
			AuditPath: auditPath, CheckpointPath: checkpointPath,
			ClientKey: clientKeyBytes, ControlKey: controlKeyBytes,
			SigningKey: ed25519.PrivateKey(privateKeyBytes), GrantTTL: grantDuration, RequestSkew: skewDuration,
		}
		if _, err := os.Stat(checkpointPath); os.IsNotExist(err) {
			if err := privilegebroker.InitializeCheckpoint(config); err != nil {
				return fmt.Errorf("initialize broker checkpoint before first start: %w", err)
			}
		} else if err != nil {
			return fmt.Errorf("inspect broker checkpoint: %w", err)
		}
		installedBroker := filepath.Join(installDir, filepath.Base(binary))
		paths := []string{installDir, installedBroker, dataDir, privateEnvironmentFile, servicePolicyPath, statePath, checkpointPath}
		if _, err := os.Stat(auditPath); err == nil {
			paths = append(paths, auditPath)
		}
		if err := servicecontrol.ProtectServicePaths(name, paths); err != nil {
			return fmt.Errorf("protect initialized broker files: %w", err)
		}
	}
	output := map[string]any{"install": result}
	if *start {
		started, err := servicecontrol.Start(ctx, result.Name, result.Scope, false)
		if err != nil {
			return err
		}
		output["start"] = started
	}
	return printJSON(output)
}

func brokerServiceAction(action string, args []string) error {
	fs := flag.NewFlagSet("steward-broker service "+action, flag.ContinueOnError)
	name := fs.String("name", defaultBrokerServiceName(runtime.GOOS), "service name")
	scope := fs.String("scope", servicecontrol.ScopeSystem, "service scope")
	dryRun := fs.Bool("dry-run", false, "render action without applying")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var value any
	var err error
	switch action {
	case "uninstall":
		value, err = servicecontrol.Uninstall(ctx, *name, *scope, *dryRun)
	case "start":
		value, err = servicecontrol.Start(ctx, *name, *scope, *dryRun)
	case "stop":
		value, err = servicecontrol.Stop(ctx, *name, *scope, *dryRun)
	case "restart":
		value, err = servicecontrol.Restart(ctx, *name, *scope, *dryRun)
	case "status":
		value, err = servicecontrol.Status(ctx, *name, *scope)
	}
	if err != nil {
		return err
	}
	return printJSON(value)
}

func defaultBrokerServiceName(platform string) string {
	switch platform {
	case "windows":
		return "MongojsonStewardBroker"
	case "darwin":
		return "com.mongojson.steward.broker"
	default:
		return "mongojson-steward-broker"
	}
}

func validateBase64Length(value string, minimum int, exact bool) error {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("must be base64")
	}
	if exact && len(decoded) != minimum {
		return fmt.Errorf("must decode to exactly %d bytes", minimum)
	}
	if !exact && len(decoded) < minimum {
		return fmt.Errorf("must decode to at least %d bytes", minimum)
	}
	return nil
}

func printJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func envOrDefault(key, fallback string) string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	return value
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: steward-broker <run|keygen|bootstrap|refresh-system-policy|validate-policy|initialize-checkpoint|status|tool-execute|control|service>")
}
