package main

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
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
	case "validate-policy":
		err = validatePolicy(os.Args[2:])
	case "initialize-checkpoint":
		err = initializeCheckpoint(os.Args[2:])
	case "status":
		err = brokerStatus()
	case "control":
		err = brokerControl(os.Args[2:])
	case "service":
		err = brokerService(os.Args[2:])
	default:
		printUsage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
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
		if err := loadPrivateEnvironment(*privateEnvironmentFile); err != nil {
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
		ProtectedPaths:         []string{dataDir, servicePolicyPath}, ProtectedFileCopies: protectedCopies,
		ExplicitEnvironment: map[string]string{
			"STEWARD_BROKER_LISTEN": listen, "STEWARD_BROKER_POLICY": servicePolicyPath,
			"STEWARD_BROKER_STATE": statePath, "STEWARD_BROKER_AUDIT": auditPath,
			"STEWARD_BROKER_CHECKPOINT": checkpointPath,
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
			ListenAddress: listen, PolicyPath: servicePolicyPath, StatePath: statePath,
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
		paths := []string{servicePolicyPath, statePath, checkpointPath}
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

func loadPrivateEnvironment(path string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read private service environment: %w", err)
	}
	values := map[string]string{}
	if err := json.Unmarshal(content, &values); err != nil {
		return fmt.Errorf("decode private service environment: %w", err)
	}
	allowed := map[string]bool{
		"DATABASE_URL":              true,
		"STEWARD_BROKER_CLIENT_KEY": true, "STEWARD_BROKER_CONTROL_KEY": true,
		"STEWARD_BROKER_SIGNING_PRIVATE_KEY": true,
	}
	for key, value := range values {
		if !allowed[key] {
			return fmt.Errorf("private service environment contains unsupported key %q", key)
		}
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("private service environment %s contains NUL", key)
		}
		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("set private service environment %s: %w", key, err)
		}
	}
	return nil
}

func printUsage() {
	fmt.Fprintln(os.Stderr, "usage: steward-broker <run|keygen|validate-policy|initialize-checkpoint|status|control|service>")
}
