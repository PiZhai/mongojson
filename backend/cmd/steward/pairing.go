package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/nacl/box"

	"mongojson/backend/internal/platform/servicecontrol"
)

const pairingSchema = "mongojson.steward.pairing"
const pairingSharedSyncEncryptionAlgorithm = "nacl.box.seal_anonymous.x25519-xsalsa20-poly1305"
const pairingBundleSignatureAlgorithm = "ed25519.pairing-bundle.v1"

type pairingBundle struct {
	Schema              string                      `json:"schema"`
	Version             int                         `json:"version"`
	CreatedAt           time.Time                   `json:"created_at"`
	Device              pairingDevice               `json:"device"`
	SharedSync          *pairingSharedSync          `json:"shared_sync,omitempty"`
	SharedSyncEncrypted *pairingEncryptedSharedSync `json:"shared_sync_encrypted,omitempty"`
	SuggestedEnv        map[string]string           `json:"suggested_env,omitempty"`
	Signature           *pairingBundleSignature     `json:"signature,omitempty"`
	Notes               []string                    `json:"notes,omitempty"`
}

type pairingDevice struct {
	ID              string `json:"id"`
	DeviceName      string `json:"device_name"`
	Platform        string `json:"platform"`
	APIBaseURL      string `json:"api_base_url"`
	PublicKey       string `json:"public_key"`
	PermissionLevel string `json:"permission_level"`
}

type pairingSharedSync struct {
	SyncSecret                 string `json:"sync_secret,omitempty"`
	SyncEncryptionKey          string `json:"sync_encryption_key,omitempty"`
	SyncEncryptionKeyID        string `json:"sync_encryption_key_id,omitempty"`
	SyncEncryptionPreviousKeys string `json:"sync_encryption_previous_keys,omitempty"`
}

type pairingEncryptedSharedSync struct {
	Algorithm          string `json:"algorithm"`
	RecipientPublicKey string `json:"recipient_public_key"`
	Ciphertext         string `json:"ciphertext"`
}

type pairingBundleSignature struct {
	Algorithm string    `json:"algorithm"`
	DeviceID  string    `json:"device_id"`
	PublicKey string    `json:"public_key"`
	SignedAt  time.Time `json:"signed_at"`
	Signature string    `json:"signature,omitempty"`
}

type pairingExportOptions struct {
	ID                         string
	DeviceName                 string
	Platform                   string
	APIBaseURL                 string
	PublicKey                  string
	PrivateKey                 string
	PermissionLevel            string
	IncludeSyncSecret          bool
	SyncSecret                 string
	IncludeSyncEncryptionKey   bool
	SyncEncryptionKey          string
	SyncEncryptionKeyID        string
	IncludeSyncPreviousKeys    bool
	SyncEncryptionPreviousKeys string
	EncryptSharedSyncFor       string
}

type pairingBootstrapOptions struct {
	File                 string
	ServiceName          string
	ServiceScope         string
	CurrentEnvFile       string
	DecryptSharedSyncKey string
	RequireSignature     bool
	SyncEnabled          bool
	PermissionLevel      string
	StrictSecurity       bool
}

type pairingBootstrapOutput struct {
	OK               bool                          `json:"ok"`
	Device           map[string]any                `json:"device"`
	SuggestedEnvKeys []string                      `json:"suggested_env_keys,omitempty"`
	SuggestedEnv     map[string]string             `json:"suggested_env_redacted,omitempty"`
	ServiceEnvPlan   *servicecontrol.Result        `json:"service_env_plan,omitempty"`
	Verification     *serviceEnvVerificationAdvice `json:"verification,omitempty"`
	Commands         pairingBootstrapCommands      `json:"commands"`
	Notes            []string                      `json:"notes"`
}

type pairingBootstrapCommands struct {
	Import       []string `json:"import"`
	ServicePlan  []string `json:"service_env_plan,omitempty"`
	ServiceApply []string `json:"service_env_apply,omitempty"`
}

func (c cli) pairing(args []string) error {
	if len(args) == 0 || isHelpArg(args[0]) {
		printPairingUsage()
		return nil
	}
	switch args[0] {
	case "export":
		return c.exportPairing(args[1:])
	case "import":
		return c.importPairing(args[1:])
	case "bootstrap":
		return c.bootstrapPairing(args[1:])
	case "keygen":
		return pairingKeygen(args[1:])
	case "verify":
		return c.verifyPairing(args[1:])
	default:
		return fmt.Errorf("unknown pairing command %q", args[0])
	}
}

func (c cli) bootstrapPairing(args []string) error {
	fs := flag.NewFlagSet("steward pairing bootstrap", flag.ExitOnError)
	opts := pairingBootstrapOptions{
		ServiceName:          servicecontrol.DefaultName(),
		ServiceScope:         servicecontrol.DefaultScope(),
		DecryptSharedSyncKey: envOrDefault("STEWARD_PAIRING_PRIVATE_KEY", ""),
		SyncEnabled:          true,
	}
	fs.StringVar(&opts.File, "file", "", "Pairing bundle file path, or '-' for stdin")
	fs.StringVar(&opts.ServiceName, "service-name", opts.ServiceName, "Service name, launchd label, or systemd unit name")
	fs.StringVar(&opts.ServiceScope, "service-scope", opts.ServiceScope, "Service manager scope used by generated service env commands")
	fs.StringVar(&opts.CurrentEnvFile, "current-env-file", "", "Optional current service environment JSON used to render a non-mutating service env plan")
	fs.StringVar(&opts.DecryptSharedSyncKey, "decrypt-shared-sync-key", opts.DecryptSharedSyncKey, "Recipient X25519 private key used to decrypt encrypted shared sync material")
	fs.BoolVar(&opts.RequireSignature, "require-signature", false, "Reject pairing bundles that do not carry a valid Ed25519 bundle signature")
	fs.BoolVar(&opts.SyncEnabled, "sync-enabled", opts.SyncEnabled, "Whether the imported peer can participate in sync")
	fs.StringVar(&opts.PermissionLevel, "permission-level", "", "Override imported peer permission ceiling")
	fs.BoolVar(&opts.StrictSecurity, "strict-security", false, "Validate the target service environment when --current-env-file is provided")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(opts.File) == "" && fs.NArg() > 0 {
		opts.File = fs.Arg(0)
	}
	if strings.TrimSpace(opts.File) == "" {
		return fmt.Errorf("pairing bootstrap requires --file or a bundle path")
	}
	bundle, err := readPairingBundle(opts.File)
	if err != nil {
		return err
	}
	var currentEnv map[string]string
	if strings.TrimSpace(opts.CurrentEnvFile) != "" {
		currentEnv, err = readCurrentServiceEnvFile(opts.CurrentEnvFile)
		if err != nil {
			return err
		}
	}
	output, err := buildPairingBootstrapPlan(bundle, opts, currentEnv)
	if err != nil {
		return err
	}
	return printJSON(output)
}

func (c cli) verifyPairing(args []string) error {
	fs := flag.NewFlagSet("steward pairing verify", flag.ExitOnError)
	id := fs.String("id", "", "Registered peer device id")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" && fs.NArg() > 0 {
		*id = fs.Arg(0)
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("pairing verify requires a device id")
	}
	return c.printRequest(http.MethodPost, "/steward/devices/"+url.PathEscape(strings.TrimSpace(*id))+"/verify", nil)
}

func (c cli) exportPairing(args []string) error {
	fs := flag.NewFlagSet("steward pairing export", flag.ExitOnError)
	opts := pairingExportOptions{
		ID:                         envOrDefault("STEWARD_AGENT_ID", "local-s1"),
		DeviceName:                 envOrDefault("STEWARD_DEVICE_NAME", ""),
		Platform:                   runtime.GOOS,
		APIBaseURL:                 envOrDefault("STEWARD_PUBLIC_API_BASE", ""),
		PublicKey:                  envOrDefault("STEWARD_DEVICE_PUBLIC_KEY", ""),
		PrivateKey:                 envOrDefault("STEWARD_DEVICE_PRIVATE_KEY", ""),
		PermissionLevel:            "A3",
		SyncSecret:                 envOrDefault("STEWARD_SYNC_SECRET", ""),
		SyncEncryptionKey:          envOrDefault("STEWARD_SYNC_ENCRYPTION_KEY", ""),
		SyncEncryptionKeyID:        envOrDefault("STEWARD_SYNC_ENCRYPTION_KEY_ID", "default"),
		SyncEncryptionPreviousKeys: envOrDefault("STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS", ""),
		EncryptSharedSyncFor:       envOrDefault("STEWARD_PAIRING_RECIPIENT_PUBLIC_KEY", ""),
	}
	output := fs.String("output", "", "Optional file path to write the pairing bundle")
	fs.StringVar(&opts.ID, "id", opts.ID, "Local device id")
	fs.StringVar(&opts.DeviceName, "name", opts.DeviceName, "Local device name")
	fs.StringVar(&opts.Platform, "platform", opts.Platform, "Local platform")
	fs.StringVar(&opts.APIBaseURL, "api-base-url", opts.APIBaseURL, "Reachable restricted Peer API base URL for this device")
	fs.StringVar(&opts.PublicKey, "public-key", opts.PublicKey, "Ed25519 public key for this device")
	fs.StringVar(&opts.PrivateKey, "private-key", opts.PrivateKey, "Optional Ed25519 private key used only to derive the public key")
	fs.StringVar(&opts.PermissionLevel, "permission-level", opts.PermissionLevel, "Suggested peer permission ceiling")
	fs.BoolVar(&opts.IncludeSyncSecret, "include-sync-secret", false, "Explicitly include STEWARD_SYNC_SECRET in the bundle")
	fs.StringVar(&opts.SyncSecret, "sync-secret", opts.SyncSecret, "STEWARD_SYNC_SECRET to include only when --include-sync-secret is set")
	fs.BoolVar(&opts.IncludeSyncEncryptionKey, "include-sync-encryption-key", false, "Explicitly include STEWARD_SYNC_ENCRYPTION_KEY in the bundle")
	fs.StringVar(&opts.SyncEncryptionKey, "sync-encryption-key", opts.SyncEncryptionKey, "STEWARD_SYNC_ENCRYPTION_KEY to include only when --include-sync-encryption-key is set")
	fs.StringVar(&opts.SyncEncryptionKeyID, "sync-encryption-key-id", opts.SyncEncryptionKeyID, "STEWARD_SYNC_ENCRYPTION_KEY_ID for the included sync encryption key")
	fs.BoolVar(&opts.IncludeSyncPreviousKeys, "include-sync-encryption-previous-keys", false, "Explicitly include STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS in the bundle")
	fs.StringVar(&opts.SyncEncryptionPreviousKeys, "sync-encryption-previous-keys", opts.SyncEncryptionPreviousKeys, "Comma-separated key_id:base64 entries to include only when --include-sync-encryption-previous-keys is set")
	fs.StringVar(&opts.EncryptSharedSyncFor, "encrypt-shared-sync-for", opts.EncryptSharedSyncFor, "Recipient X25519 public key used to encrypt included shared sync material")
	if err := fs.Parse(args); err != nil {
		return err
	}
	bundle, err := buildPairingBundle(opts, time.Now().UTC())
	if err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if strings.TrimSpace(*output) != "" {
		return os.WriteFile(*output, encoded, 0o600)
	}
	_, err = os.Stdout.Write(encoded)
	return err
}

func (c cli) importPairing(args []string) error {
	fs := flag.NewFlagSet("steward pairing import", flag.ExitOnError)
	file := fs.String("file", "", "Pairing bundle file path, or '-' for stdin")
	dryRun := fs.Bool("dry-run", false, "Print the device registration payload without changing local state")
	syncEnabled := fs.Bool("sync-enabled", true, "Whether the imported peer can participate in sync")
	permissionLevel := fs.String("permission-level", "", "Override imported peer permission ceiling")
	decryptSharedSyncKey := fs.String("decrypt-shared-sync-key", envOrDefault("STEWARD_PAIRING_PRIVATE_KEY", ""), "Recipient X25519 private key used to decrypt encrypted shared sync material")
	requireSignature := fs.Bool("require-signature", false, "Reject pairing bundles that do not carry a valid Ed25519 bundle signature")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*file) == "" && fs.NArg() > 0 {
		*file = fs.Arg(0)
	}
	if strings.TrimSpace(*file) == "" {
		return fmt.Errorf("pairing import requires --file or a bundle path")
	}
	bundle, err := readPairingBundle(*file)
	if err != nil {
		return err
	}
	payload, suggestedEnv, err := pairingImportPayload(bundle, *syncEnabled, *permissionLevel, *decryptSharedSyncKey, *requireSignature)
	if err != nil {
		return err
	}
	if *dryRun {
		return printJSON(map[string]any{
			"device":        payload,
			"suggested_env": suggestedEnv,
		})
	}
	body, err := c.request(http.MethodPost, "/steward/devices", payload)
	if err != nil {
		return err
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	if len(suggestedEnv) > 0 {
		decoded["suggested_env"] = suggestedEnv
	}
	return printJSON(decoded)
}

func pairingKeygen(args []string) error {
	fs := flag.NewFlagSet("steward pairing keygen", flag.ExitOnError)
	label := fs.String("label", "", "Optional device id or label echoed in the output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	publicKey, privateKey, err := box.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate pairing box keypair: %w", err)
	}
	publicKeyText := base64.StdEncoding.EncodeToString(publicKey[:])
	privateKeyText := base64.StdEncoding.EncodeToString(privateKey[:])
	payload := map[string]any{
		"algorithm":   pairingSharedSyncEncryptionAlgorithm,
		"public_key":  publicKeyText,
		"private_key": privateKeyText,
		"env": map[string]string{
			"STEWARD_PAIRING_PUBLIC_KEY":  publicKeyText,
			"STEWARD_PAIRING_PRIVATE_KEY": privateKeyText,
		},
	}
	if strings.TrimSpace(*label) != "" {
		payload["label"] = strings.TrimSpace(*label)
	}
	return printJSON(payload)
}

func buildPairingBundle(opts pairingExportOptions, now time.Time) (pairingBundle, error) {
	id := strings.TrimSpace(opts.ID)
	if id == "" {
		return pairingBundle{}, fmt.Errorf("pairing export requires a device id")
	}
	apiBaseURL := strings.TrimRight(strings.TrimSpace(opts.APIBaseURL), "/")
	if apiBaseURL == "" {
		return pairingBundle{}, fmt.Errorf("pairing export requires --api-base-url or STEWARD_PUBLIC_API_BASE")
	}
	publicKey := strings.TrimSpace(opts.PublicKey)
	privateKey := strings.TrimSpace(opts.PrivateKey)
	if privateKey != "" {
		derived, err := publicKeyFromPrivateKey(privateKey)
		if err != nil {
			return pairingBundle{}, err
		}
		if publicKey == "" {
			publicKey = derived
		} else {
			normalizedPublicKey, err := normalizeEd25519PublicKey(publicKey)
			if err != nil {
				return pairingBundle{}, err
			}
			if normalizedPublicKey != derived {
				return pairingBundle{}, fmt.Errorf("--public-key does not match --private-key")
			}
			publicKey = normalizedPublicKey
		}
	}
	publicKey, err := normalizeEd25519PublicKey(publicKey)
	if err != nil {
		return pairingBundle{}, err
	}
	bundle := pairingBundle{
		Schema:    pairingSchema,
		Version:   1,
		CreatedAt: now.UTC(),
		Device: pairingDevice{
			ID:              id,
			DeviceName:      defaultString(opts.DeviceName, id),
			Platform:        defaultString(opts.Platform, runtime.GOOS),
			APIBaseURL:      apiBaseURL,
			PublicKey:       publicKey,
			PermissionLevel: defaultString(opts.PermissionLevel, "A3"),
		},
		Notes: []string{
			"Importing this bundle only registers the peer device; it does not silently install or update local service secrets.",
		},
	}
	shared := pairingSharedSync{}
	if opts.IncludeSyncSecret {
		shared.SyncSecret = strings.TrimSpace(opts.SyncSecret)
		if shared.SyncSecret == "" {
			return pairingBundle{}, fmt.Errorf("--include-sync-secret requires --sync-secret or STEWARD_SYNC_SECRET")
		}
	}
	if opts.IncludeSyncEncryptionKey {
		shared.SyncEncryptionKey = strings.TrimSpace(opts.SyncEncryptionKey)
		if shared.SyncEncryptionKey == "" {
			return pairingBundle{}, fmt.Errorf("--include-sync-encryption-key requires --sync-encryption-key or STEWARD_SYNC_ENCRYPTION_KEY")
		}
		if _, err := decodeBase64Material(shared.SyncEncryptionKey, 32, "sync encryption key"); err != nil {
			return pairingBundle{}, err
		}
		shared.SyncEncryptionKeyID = defaultString(opts.SyncEncryptionKeyID, "default")
	}
	if opts.IncludeSyncPreviousKeys {
		shared.SyncEncryptionPreviousKeys = strings.TrimSpace(opts.SyncEncryptionPreviousKeys)
		if shared.SyncEncryptionPreviousKeys == "" {
			return pairingBundle{}, fmt.Errorf("--include-sync-encryption-previous-keys requires --sync-encryption-previous-keys or STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS")
		}
		if err := validatePreviousSyncKeys(shared.SyncEncryptionPreviousKeys); err != nil {
			return pairingBundle{}, err
		}
	}
	if shared.SyncSecret != "" || shared.SyncEncryptionKey != "" || shared.SyncEncryptionPreviousKeys != "" {
		recipientPublicKey := strings.TrimSpace(opts.EncryptSharedSyncFor)
		if recipientPublicKey != "" {
			encrypted, err := encryptPairingSharedSync(shared, recipientPublicKey)
			if err != nil {
				return pairingBundle{}, err
			}
			bundle.SharedSyncEncrypted = encrypted
			bundle.Notes = append(bundle.Notes, "This bundle contains encrypted shared sync material; decrypt it on the recipient device before applying suggested service environment values.")
		} else {
			bundle.SharedSync = &shared
			bundle.SuggestedEnv = suggestedEnvFromSharedSync(shared)
			bundle.Notes = append(bundle.Notes, "This bundle contains shared sync secret material because an include flag was set explicitly; handle it as sensitive.")
		}
	} else if strings.TrimSpace(opts.EncryptSharedSyncFor) != "" {
		return pairingBundle{}, fmt.Errorf("--encrypt-shared-sync-for requires at least one included shared sync secret flag")
	}
	if privateKey != "" {
		if err := signPairingBundle(&bundle, privateKey, now.UTC()); err != nil {
			return pairingBundle{}, err
		}
		bundle.Notes = append(bundle.Notes, "This bundle is signed with the device Ed25519 private key; import verifies the signature before using device or shared sync fields.")
	}
	return bundle, nil
}

func pairingImportPayload(bundle pairingBundle, syncEnabled bool, permissionOverride string, decryptSharedSyncKey string, requireSignature bool) (map[string]any, map[string]string, error) {
	if bundle.Schema != pairingSchema {
		return nil, nil, fmt.Errorf("unsupported pairing schema %q", bundle.Schema)
	}
	if bundle.Version != 1 {
		return nil, nil, fmt.Errorf("unsupported pairing version %d", bundle.Version)
	}
	if strings.TrimSpace(bundle.Device.ID) == "" {
		return nil, nil, fmt.Errorf("pairing bundle device id is required")
	}
	if err := verifyPairingBundleSignature(bundle, requireSignature); err != nil {
		return nil, nil, err
	}
	publicKey, err := normalizeEd25519PublicKey(bundle.Device.PublicKey)
	if err != nil {
		return nil, nil, err
	}
	permission := defaultString(permissionOverride, defaultString(bundle.Device.PermissionLevel, "A3"))
	payload := map[string]any{
		"id":               strings.TrimSpace(bundle.Device.ID),
		"device_name":      defaultString(bundle.Device.DeviceName, strings.TrimSpace(bundle.Device.ID)),
		"platform":         defaultString(bundle.Device.Platform, "unknown"),
		"role":             "peer",
		"sync_enabled":     syncEnabled,
		"permission_level": permission,
		"public_key":       publicKey,
		"api_base_url":     strings.TrimRight(strings.TrimSpace(bundle.Device.APIBaseURL), "/"),
	}
	suggestedEnv, err := pairingSuggestedEnv(bundle, decryptSharedSyncKey)
	if err != nil {
		return nil, nil, err
	}
	return payload, suggestedEnv, nil
}

func buildPairingBootstrapPlan(bundle pairingBundle, opts pairingBootstrapOptions, currentEnv map[string]string) (pairingBootstrapOutput, error) {
	payload, suggestedEnv, err := pairingImportPayload(bundle, opts.SyncEnabled, opts.PermissionLevel, opts.DecryptSharedSyncKey, opts.RequireSignature)
	if err != nil {
		return pairingBootstrapOutput{}, err
	}
	output := pairingBootstrapOutput{
		OK:               true,
		Device:           payload,
		SuggestedEnvKeys: sortedEnvKeys(suggestedEnv),
		SuggestedEnv:     redactPairingSuggestedEnv(suggestedEnv),
		Commands:         pairingBootstrapCommandAdvice(opts, bundle, len(suggestedEnv) > 0),
		Notes: []string{
			"bootstrap is a non-mutating plan: it does not register the peer, write service environment, restart a service, or call a model endpoint",
			"run the suggested import and service env apply commands only after reviewing the device identity and redacted target environment",
		},
	}
	if len(output.SuggestedEnv) == 0 {
		output.SuggestedEnv = nil
	}
	if len(output.SuggestedEnvKeys) == 0 {
		output.SuggestedEnvKeys = nil
		output.Notes = append(output.Notes, "the pairing bundle did not include shared sync material, so no service environment patch was planned")
		return output, nil
	}
	if currentEnv == nil {
		output.Notes = append(output.Notes, "pass --current-env-file to render and strict-check the full target service environment without reading the service manager")
		return output, nil
	}
	patchOptions := servicecontrol.EnvPatchOptions{
		Name:   defaultString(opts.ServiceName, servicecontrol.DefaultName()),
		Scope:  opts.ServiceScope,
		Set:    suggestedEnv,
		DryRun: true,
		ValidateTarget: func(target map[string]string) error {
			if !opts.StrictSecurity {
				return nil
			}
			options := serviceInstallOptionsFromEnv(defaultString(opts.ServiceName, servicecontrol.DefaultName()), target)
			options.Scope = opts.ServiceScope
			return validateStrictServiceSecurity(options)
		},
	}
	plan, err := servicecontrol.PlanEnvironmentPatch(currentEnv, patchOptions)
	if err != nil {
		return pairingBootstrapOutput{}, err
	}
	output.ServiceEnvPlan = &plan
	verification := serviceEnvVerificationAdviceFromEnvironmentForPlatform(defaultString(opts.ServiceName, servicecontrol.DefaultName()), plan.Scope, plan.Environment, plan.Platform)
	output.Verification = verification
	return output, nil
}

func pairingBootstrapCommandAdvice(opts pairingBootstrapOptions, bundle pairingBundle, hasSuggestedEnv bool) pairingBootstrapCommands {
	file := strings.TrimSpace(opts.File)
	if file == "" {
		file = "<pairing-bundle.json>"
	}
	importArgs := []string{"pairing", "import", "--file", file}
	if opts.RequireSignature {
		importArgs = append(importArgs, "--require-signature")
	}
	if !opts.SyncEnabled {
		importArgs = append(importArgs, "--sync-enabled=false")
	}
	if strings.TrimSpace(opts.PermissionLevel) != "" {
		importArgs = append(importArgs, "--permission-level", strings.TrimSpace(opts.PermissionLevel))
	}
	commands := pairingBootstrapCommands{Import: importArgs}
	if !hasSuggestedEnv {
		return commands
	}
	decryptArgs := []string{}
	if bundle.SharedSyncEncrypted != nil {
		decryptArgs = append(decryptArgs, "--decrypt-shared-sync-key", "<recipient pairing private_key>")
	}
	signatureArgs := []string{}
	if opts.RequireSignature {
		signatureArgs = append(signatureArgs, "--require-signature")
	}
	serviceNameArgs := []string{}
	if strings.TrimSpace(opts.ServiceName) != "" && strings.TrimSpace(opts.ServiceName) != servicecontrol.DefaultName() {
		serviceNameArgs = append(serviceNameArgs, "--name", strings.TrimSpace(opts.ServiceName))
	}
	serviceScopeArgs := []string{"--scope", defaultString(opts.ServiceScope, servicecontrol.DefaultScope())}
	planArgs := append([]string{"service", "env", "plan"}, serviceNameArgs...)
	planArgs = append(planArgs, serviceScopeArgs...)
	if strings.TrimSpace(opts.CurrentEnvFile) != "" {
		planArgs = append(planArgs, "--current-env-file", strings.TrimSpace(opts.CurrentEnvFile))
	}
	planArgs = append(planArgs, "--from-pairing", file)
	planArgs = append(planArgs, decryptArgs...)
	planArgs = append(planArgs, signatureArgs...)
	if opts.StrictSecurity {
		planArgs = append(planArgs, "--strict-security")
	}
	commands.ServicePlan = planArgs

	applyArgs := append([]string{"service", "env", "apply"}, serviceNameArgs...)
	applyArgs = append(applyArgs, serviceScopeArgs...)
	applyArgs = append(applyArgs, "--from-pairing", file)
	applyArgs = append(applyArgs, decryptArgs...)
	applyArgs = append(applyArgs, signatureArgs...)
	if opts.StrictSecurity {
		applyArgs = append(applyArgs, "--strict-security")
	}
	applyArgs = append(applyArgs, "--confirm", "--restart", "--verify")
	commands.ServiceApply = applyArgs
	return commands
}

func sortedEnvKeys(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func redactPairingSuggestedEnv(env map[string]string) map[string]string {
	if len(env) == 0 {
		return map[string]string{}
	}
	redacted := map[string]string{}
	for _, key := range sortedEnvKeys(env) {
		value := env[key]
		if pairingBootstrapSensitiveEnvKey(key) {
			redacted[key] = "<redacted>"
			continue
		}
		redacted[key] = value
	}
	return redacted
}

func pairingBootstrapSensitiveEnvKey(key string) bool {
	upper := strings.ToUpper(strings.TrimSpace(key))
	if strings.HasSuffix(upper, "_KEY_ID") {
		return false
	}
	return strings.Contains(upper, "SECRET") ||
		strings.Contains(upper, "PRIVATE") ||
		strings.Contains(upper, "PASSWORD") ||
		strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "CREDENTIAL") ||
		strings.Contains(upper, "API_KEY") ||
		strings.Contains(upper, "ENCRYPTION_KEY") ||
		strings.Contains(upper, "ENCRYPTION_PREVIOUS_KEYS")
}

func pairingSuggestedEnv(bundle pairingBundle, decryptSharedSyncKey string) (map[string]string, error) {
	if bundle.SharedSync != nil && bundle.SharedSyncEncrypted != nil {
		return nil, fmt.Errorf("pairing bundle cannot contain both shared_sync and shared_sync_encrypted")
	}
	if bundle.SharedSyncEncrypted != nil {
		if strings.TrimSpace(decryptSharedSyncKey) == "" {
			return nil, fmt.Errorf("encrypted shared sync material requires --decrypt-shared-sync-key or STEWARD_PAIRING_PRIVATE_KEY")
		}
		shared, err := decryptPairingSharedSync(*bundle.SharedSyncEncrypted, decryptSharedSyncKey)
		if err != nil {
			return nil, err
		}
		return suggestedEnvFromSharedSync(shared), nil
	}
	return suggestedEnvFromSharedSyncValue(bundle.SharedSync), nil
}

func signPairingBundle(bundle *pairingBundle, privateKeyValue string, signedAt time.Time) error {
	if bundle == nil {
		return fmt.Errorf("pairing bundle is required")
	}
	privateKey, err := decodeBase64Material(privateKeyValue, ed25519.PrivateKeySize, "private key")
	if err != nil {
		return err
	}
	publicKey, ok := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("derive public key from private key")
	}
	publicKeyText := base64.StdEncoding.EncodeToString(publicKey)
	if publicKeyText != bundle.Device.PublicKey {
		return fmt.Errorf("pairing bundle public key does not match signing key")
	}
	signature := pairingBundleSignature{
		Algorithm: pairingBundleSignatureAlgorithm,
		DeviceID:  bundle.Device.ID,
		PublicKey: bundle.Device.PublicKey,
		SignedAt:  signedAt.UTC(),
	}
	canonical, err := pairingBundleCanonicalPayload(*bundle, signature)
	if err != nil {
		return err
	}
	signature.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(privateKey), canonical))
	bundle.Signature = &signature
	return nil
}

func verifyPairingBundleSignature(bundle pairingBundle, requireSignature bool) error {
	if bundle.Signature == nil {
		if requireSignature {
			return fmt.Errorf("pairing bundle signature is required")
		}
		return nil
	}
	signature := *bundle.Signature
	if signature.Algorithm != pairingBundleSignatureAlgorithm {
		return fmt.Errorf("unsupported pairing bundle signature algorithm %q", signature.Algorithm)
	}
	if strings.TrimSpace(signature.DeviceID) != strings.TrimSpace(bundle.Device.ID) {
		return fmt.Errorf("pairing bundle signature device mismatch")
	}
	publicKey, err := normalizeEd25519PublicKey(bundle.Device.PublicKey)
	if err != nil {
		return err
	}
	signaturePublicKey, err := normalizeEd25519PublicKey(signature.PublicKey)
	if err != nil {
		return fmt.Errorf("pairing bundle signature public key: %w", err)
	}
	if signaturePublicKey != publicKey {
		return fmt.Errorf("pairing bundle signature public key mismatch")
	}
	if signature.SignedAt.IsZero() {
		return fmt.Errorf("pairing bundle signature signed_at is required")
	}
	signatureBytes, err := decodeBase64Material(signature.Signature, ed25519.SignatureSize, "pairing bundle signature")
	if err != nil {
		return err
	}
	canonical, err := pairingBundleCanonicalPayload(bundle, signature)
	if err != nil {
		return err
	}
	publicKeyBytes, err := decodeBase64Material(publicKey, ed25519.PublicKeySize, "public key")
	if err != nil {
		return err
	}
	if !ed25519.Verify(ed25519.PublicKey(publicKeyBytes), canonical, signatureBytes) {
		return fmt.Errorf("invalid pairing bundle signature")
	}
	return nil
}

func pairingBundleCanonicalPayload(bundle pairingBundle, signature pairingBundleSignature) ([]byte, error) {
	signature.Signature = ""
	payload := struct {
		Schema              string                      `json:"schema"`
		Version             int                         `json:"version"`
		CreatedAt           time.Time                   `json:"created_at"`
		Device              pairingDevice               `json:"device"`
		SharedSync          *pairingSharedSync          `json:"shared_sync,omitempty"`
		SharedSyncEncrypted *pairingEncryptedSharedSync `json:"shared_sync_encrypted,omitempty"`
		Signature           pairingBundleSignature      `json:"signature"`
	}{
		Schema:              bundle.Schema,
		Version:             bundle.Version,
		CreatedAt:           bundle.CreatedAt.UTC(),
		Device:              bundle.Device,
		SharedSync:          bundle.SharedSync,
		SharedSyncEncrypted: bundle.SharedSyncEncrypted,
		Signature:           signature,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal pairing bundle canonical payload: %w", err)
	}
	return encoded, nil
}

func readPairingBundle(path string) (pairingBundle, error) {
	path = strings.TrimSpace(path)
	var data []byte
	var err error
	switch path {
	case "", "-":
		data, err = io.ReadAll(os.Stdin)
	default:
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return pairingBundle{}, err
	}
	var bundle pairingBundle
	if err := json.Unmarshal(data, &bundle); err != nil {
		return pairingBundle{}, fmt.Errorf("decode pairing bundle: %w", err)
	}
	return bundle, nil
}

func suggestedEnvFromSharedSyncValue(shared *pairingSharedSync) map[string]string {
	if shared == nil {
		return map[string]string{}
	}
	return suggestedEnvFromSharedSync(*shared)
}

func suggestedEnvFromSharedSync(shared pairingSharedSync) map[string]string {
	env := map[string]string{}
	if strings.TrimSpace(shared.SyncSecret) != "" {
		env["STEWARD_SYNC_SECRET"] = strings.TrimSpace(shared.SyncSecret)
	}
	if strings.TrimSpace(shared.SyncEncryptionKey) != "" {
		env["STEWARD_SYNC_ENCRYPTION_KEY"] = strings.TrimSpace(shared.SyncEncryptionKey)
		env["STEWARD_SYNC_ENCRYPTION_KEY_ID"] = defaultString(shared.SyncEncryptionKeyID, "default")
	}
	if strings.TrimSpace(shared.SyncEncryptionPreviousKeys) != "" {
		env["STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS"] = strings.TrimSpace(shared.SyncEncryptionPreviousKeys)
	}
	return env
}

func encryptPairingSharedSync(shared pairingSharedSync, recipientPublicKey string) (*pairingEncryptedSharedSync, error) {
	recipient, err := decodePairingBoxKey(recipientPublicKey, "pairing recipient public key")
	if err != nil {
		return nil, err
	}
	plain, err := json.Marshal(shared)
	if err != nil {
		return nil, fmt.Errorf("marshal shared sync material: %w", err)
	}
	sealed, err := box.SealAnonymous(nil, plain, recipient, rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("encrypt shared sync material: %w", err)
	}
	return &pairingEncryptedSharedSync{
		Algorithm:          pairingSharedSyncEncryptionAlgorithm,
		RecipientPublicKey: base64.StdEncoding.EncodeToString(recipient[:]),
		Ciphertext:         base64.StdEncoding.EncodeToString(sealed),
	}, nil
}

func decryptPairingSharedSync(encrypted pairingEncryptedSharedSync, privateKeyValue string) (pairingSharedSync, error) {
	if encrypted.Algorithm != pairingSharedSyncEncryptionAlgorithm {
		return pairingSharedSync{}, fmt.Errorf("unsupported shared sync encryption algorithm %q", encrypted.Algorithm)
	}
	publicKey, err := decodePairingBoxKey(encrypted.RecipientPublicKey, "pairing recipient public key")
	if err != nil {
		return pairingSharedSync{}, err
	}
	privateKey, err := decodePairingBoxKey(privateKeyValue, "pairing recipient private key")
	if err != nil {
		return pairingSharedSync{}, err
	}
	ciphertext, err := decodeBase64Material(encrypted.Ciphertext, 0, "encrypted shared sync material")
	if err != nil {
		return pairingSharedSync{}, err
	}
	plain, ok := box.OpenAnonymous(nil, ciphertext, publicKey, privateKey)
	if !ok {
		return pairingSharedSync{}, fmt.Errorf("decrypt shared sync material: recipient key mismatch or ciphertext corrupted")
	}
	var shared pairingSharedSync
	if err := json.Unmarshal(plain, &shared); err != nil {
		return pairingSharedSync{}, fmt.Errorf("decode shared sync material: %w", err)
	}
	if shared.SyncEncryptionKey != "" {
		if _, err := decodeBase64Material(shared.SyncEncryptionKey, 32, "sync encryption key"); err != nil {
			return pairingSharedSync{}, err
		}
	}
	if shared.SyncEncryptionPreviousKeys != "" {
		if err := validatePreviousSyncKeys(shared.SyncEncryptionPreviousKeys); err != nil {
			return pairingSharedSync{}, err
		}
	}
	return shared, nil
}

func decodePairingBoxKey(value string, label string) (*[32]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "x25519:")
	value = strings.TrimPrefix(value, "nacl-box:")
	value = strings.TrimPrefix(value, "base64:")
	key, err := decodeBase64Material(value, 32, label)
	if err != nil {
		return nil, err
	}
	var out [32]byte
	copy(out[:], key)
	return &out, nil
}

func validatePreviousSyncKeys(value string) error {
	entries := strings.Split(strings.TrimSpace(value), ",")
	valid := 0
	for _, entry := range entries {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		keyID, material, ok := strings.Cut(entry, ":")
		if !ok || strings.TrimSpace(keyID) == "" || strings.TrimSpace(material) == "" {
			return fmt.Errorf("previous sync encryption key must be key_id:base64")
		}
		if _, err := decodeBase64Material(material, 32, "previous sync encryption key "+strings.TrimSpace(keyID)); err != nil {
			return err
		}
		valid++
	}
	if valid == 0 {
		return fmt.Errorf("previous sync encryption key must include at least one key_id:base64 entry")
	}
	return nil
}

func publicKeyFromPrivateKey(value string) (string, error) {
	privateKey, err := decodeBase64Material(value, ed25519.PrivateKeySize, "private key")
	if err != nil {
		return "", err
	}
	publicKey, ok := ed25519.PrivateKey(privateKey).Public().(ed25519.PublicKey)
	if !ok {
		return "", fmt.Errorf("derive public key from private key")
	}
	return base64.StdEncoding.EncodeToString(publicKey), nil
}

func normalizeEd25519PublicKey(value string) (string, error) {
	key, err := decodeBase64Material(value, ed25519.PublicKeySize, "public key")
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(key), nil
}

func decodeBase64Material(value string, expectedLen int, label string) ([]byte, error) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "ed25519:")
	value = strings.TrimPrefix(value, "base64:")
	if value == "" {
		return nil, fmt.Errorf("%s is required", label)
	}
	encodings := []*base64.Encoding{
		base64.StdEncoding,
		base64.RawStdEncoding,
		base64.URLEncoding,
		base64.RawURLEncoding,
	}
	for _, encoding := range encodings {
		decoded, err := encoding.DecodeString(value)
		if err == nil {
			if expectedLen > 0 && len(decoded) != expectedLen {
				return nil, fmt.Errorf("%s must be %d bytes, got %d", label, expectedLen, len(decoded))
			}
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("%s must be base64-encoded", label)
}
