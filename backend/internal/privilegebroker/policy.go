package privilegebroker

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var capabilityNamePattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{1,79}$`)
var credentialIDPattern = regexp.MustCompile(`^[a-z][a-z0-9._:-]{1,79}$`)
var webAuthnRPIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,251}[a-z0-9])?$`)
var capabilityPathSecurityValidator = validateCapabilityPathSecurity
var credentialPathSecurityValidator = validateCredentialPathSecurity

type Policy struct {
	Version             int                 `json:"version"`
	ApprovalAuthorities []ApprovalAuthority `json:"approval_authorities"`
	Capabilities        []Capability        `json:"capabilities"`
	BrokerPeers         []BrokerPeer        `json:"broker_peers,omitempty"`
	Credentials         []BrokerCredential  `json:"credentials,omitempty"`
}

type BrokerPeer struct {
	DeviceID            string   `json:"device_id"`
	Name                string   `json:"name"`
	PublicKey           string   `json:"public_key"`
	AllowedCapabilities []string `json:"allowed_capabilities"`
	AllowedCredentials  []string `json:"allowed_credentials,omitempty"`
	Enabled             bool     `json:"enabled"`

	keyID string
}

type BrokerCredential struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	MaxBytes int    `json:"max_bytes,omitempty"`
	Enabled  bool   `json:"enabled"`
}

type ApprovalAuthority struct {
	Name           string   `json:"name"`
	Algorithm      string   `json:"algorithm,omitempty"`
	PublicKey      string   `json:"public_key"`
	CredentialID   string   `json:"credential_id,omitempty"`
	RPID           string   `json:"rp_id,omitempty"`
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
	Enabled        bool     `json:"enabled"`

	keyID string
}

type Capability struct {
	Name             string   `json:"name"`
	Description      string   `json:"description"`
	PermissionLevel  string   `json:"permission_level"`
	RiskLevel        string   `json:"risk_level"`
	Executable       string   `json:"executable"`
	ExecutableSHA256 string   `json:"executable_sha256"`
	Arguments        []string `json:"arguments"`
	WorkingDirectory string   `json:"working_directory"`
	TimeoutSeconds   int      `json:"timeout_seconds"`
	MaxOutputBytes   int      `json:"max_output_bytes"`
	Enabled          bool     `json:"enabled"`
	CredentialIDs    []string `json:"credential_ids,omitempty"`
	// InputSchema enables parameterized execution through a hash-pinned tool
	// host. An omitted schema preserves the legacy fixed-command capability.
	InputSchema map[string]any `json:"input_schema,omitempty"`

	digest string
}

type LoadedPolicy struct {
	Policy       Policy
	Digest       string
	capabilities map[string]Capability
	authorities  map[string]PublicApprovalAuthority
	peers        map[string]BrokerPeer
	credentials  map[string]BrokerCredential
}

func LoadPolicy(path string) (*LoadedPolicy, error) {
	file, err := os.Open(strings.TrimSpace(path))
	if err != nil {
		return nil, fmt.Errorf("open broker policy: %w", err)
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, 1<<20))
	decoder.DisallowUnknownFields()
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return nil, fmt.Errorf("decode broker policy: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return nil, fmt.Errorf("broker policy must contain exactly one JSON object")
	}
	return ValidatePolicy(policy)
}

func ValidatePolicy(policy Policy) (*LoadedPolicy, error) {
	if policy.Version != 2 && policy.Version != 3 {
		return nil, fmt.Errorf("broker policy version must be 2 or 3")
	}
	if len(policy.Capabilities) == 0 || len(policy.Capabilities) > 128 {
		return nil, fmt.Errorf("broker policy must define between 1 and 128 capabilities")
	}
	if len(policy.ApprovalAuthorities) == 0 || len(policy.ApprovalAuthorities) > 16 {
		return nil, fmt.Errorf("R3.1 broker policy must define between 1 and 16 approval authorities")
	}
	loaded := &LoadedPolicy{Policy: policy, capabilities: map[string]Capability{}, authorities: map[string]PublicApprovalAuthority{}, peers: map[string]BrokerPeer{}, credentials: map[string]BrokerCredential{}}
	for index := range loaded.Policy.ApprovalAuthorities {
		authority := loaded.Policy.ApprovalAuthorities[index]
		authority.Name = strings.TrimSpace(authority.Name)
		authority.PublicKey = strings.TrimSpace(authority.PublicKey)
		if authority.Name == "" || len([]rune(authority.Name)) > 100 {
			return nil, fmt.Errorf("approval authority %d has an invalid name", index)
		}
		authority.Algorithm = strings.ToLower(strings.TrimSpace(authority.Algorithm))
		if authority.Algorithm == "" {
			authority.Algorithm = ApprovalEd25519
		}
		var publicAuthority PublicApprovalAuthority
		switch authority.Algorithm {
		case ApprovalEd25519:
			if authority.CredentialID != "" || authority.RPID != "" || len(authority.AllowedOrigins) != 0 {
				return nil, fmt.Errorf("approval authority %d: Ed25519 authority must not define WebAuthn fields", index)
			}
			publicKey, err := decodeApprovalPublicKey(authority.PublicKey)
			if err != nil {
				return nil, fmt.Errorf("approval authority %d: %w", index, err)
			}
			authority.PublicKey = base64.StdEncoding.EncodeToString(publicKey)
			authority.keyID = publicKeyID(publicKey)
			publicAuthority = PublicApprovalAuthority{Name: authority.Name, Algorithm: authority.Algorithm, PublicKey: authority.PublicKey, KeyID: authority.keyID}
		case ApprovalWebAuthnES256:
			publicKeyDER, _, err := decodeWebAuthnES256PublicKey(authority.PublicKey)
			if err != nil {
				return nil, fmt.Errorf("approval authority %d: %w", index, err)
			}
			authority.PublicKey = base64.StdEncoding.EncodeToString(publicKeyDER)
			authority.CredentialID, authority.RPID, authority.AllowedOrigins, err = normalizeWebAuthnAuthority(authority.CredentialID, authority.RPID, authority.AllowedOrigins)
			if err != nil {
				return nil, fmt.Errorf("approval authority %d: %w", index, err)
			}
			authority.keyID = keyMaterialID(publicKeyDER)
			publicAuthority = PublicApprovalAuthority{
				Name: authority.Name, Algorithm: authority.Algorithm, PublicKey: authority.PublicKey, KeyID: authority.keyID,
				CredentialID: authority.CredentialID, RPID: authority.RPID, AllowedOrigins: append([]string(nil), authority.AllowedOrigins...),
			}
		default:
			return nil, fmt.Errorf("approval authority %d: unsupported algorithm %q", index, authority.Algorithm)
		}
		loaded.Policy.ApprovalAuthorities[index] = authority
		if authority.Enabled {
			if _, exists := loaded.authorities[authority.keyID]; exists {
				return nil, fmt.Errorf("duplicate approval authority key %q", authority.keyID)
			}
			loaded.authorities[authority.keyID] = publicAuthority
		}
	}
	if len(loaded.authorities) == 0 {
		return nil, fmt.Errorf("R3.2 broker policy must enable at least one approval authority")
	}
	if policy.Version == 2 && (len(policy.BrokerPeers) > 0 || len(policy.Credentials) > 0) {
		return nil, fmt.Errorf("Broker federation and credential proxy require policy version 3")
	}
	if len(policy.BrokerPeers) > 64 || len(policy.Credentials) > 64 {
		return nil, fmt.Errorf("broker policy supports at most 64 peers and 64 credentials")
	}
	for index := range loaded.Policy.Credentials {
		credential := loaded.Policy.Credentials[index]
		credential.ID = strings.ToLower(strings.TrimSpace(credential.ID))
		if !credentialIDPattern.MatchString(credential.ID) {
			return nil, fmt.Errorf("credential %d has an invalid id", index)
		}
		credential.Path = filepath.Clean(strings.TrimSpace(credential.Path))
		if !filepath.IsAbs(credential.Path) {
			return nil, fmt.Errorf("credential %q path must be absolute", credential.ID)
		}
		resolved, err := filepath.EvalSymlinks(credential.Path)
		if err != nil {
			return nil, fmt.Errorf("credential %q path: %w", credential.ID, err)
		}
		credential.Path = filepath.Clean(resolved)
		info, err := os.Stat(credential.Path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("credential %q path is unavailable or not a regular file", credential.ID)
		}
		if credential.MaxBytes == 0 {
			credential.MaxBytes = 16 << 10
		}
		if credential.MaxBytes < 1 || credential.MaxBytes > 64<<10 || info.Size() > int64(credential.MaxBytes) {
			return nil, fmt.Errorf("credential %q exceeds its protected size limit", credential.ID)
		}
		if err := credentialPathSecurityValidator(credential.Path); err != nil {
			return nil, fmt.Errorf("credential %q: %w", credential.ID, err)
		}
		loaded.Policy.Credentials[index] = credential
		if credential.Enabled {
			if _, exists := loaded.credentials[credential.ID]; exists {
				return nil, fmt.Errorf("duplicate credential %q", credential.ID)
			}
			loaded.credentials[credential.ID] = credential
		}
	}
	for index := range loaded.Policy.BrokerPeers {
		peer := loaded.Policy.BrokerPeers[index]
		peer.DeviceID = strings.TrimSpace(peer.DeviceID)
		peer.Name = strings.TrimSpace(peer.Name)
		if peer.DeviceID == "" || len(peer.DeviceID) > 200 || peer.Name == "" || len([]rune(peer.Name)) > 120 {
			return nil, fmt.Errorf("broker peer %d has invalid identity metadata", index)
		}
		key, err := decodePublicKey(peer.PublicKey)
		if err != nil {
			return nil, fmt.Errorf("broker peer %q public key: %w", peer.DeviceID, err)
		}
		peer.PublicKey = base64.StdEncoding.EncodeToString(key)
		peer.keyID = publicKeyID(key)
		peer.AllowedCapabilities, err = normalizePolicyNames(peer.AllowedCapabilities, capabilityNamePattern, "capability")
		if err != nil || len(peer.AllowedCapabilities) == 0 {
			return nil, fmt.Errorf("broker peer %q must allow at least one valid capability: %v", peer.DeviceID, err)
		}
		peer.AllowedCredentials, err = normalizePolicyNames(peer.AllowedCredentials, credentialIDPattern, "credential")
		if err != nil {
			return nil, fmt.Errorf("broker peer %q: %w", peer.DeviceID, err)
		}
		loaded.Policy.BrokerPeers[index] = peer
		if peer.Enabled {
			if _, exists := loaded.peers[peer.DeviceID]; exists {
				return nil, fmt.Errorf("duplicate enabled broker peer %q", peer.DeviceID)
			}
			loaded.peers[peer.DeviceID] = peer
		}
	}
	for index := range loaded.Policy.Capabilities {
		capability, err := normalizeCapability(loaded.Policy.Capabilities[index])
		if err != nil {
			return nil, fmt.Errorf("capability %d: %w", index, err)
		}
		if _, exists := loaded.capabilities[capability.Name]; exists {
			return nil, fmt.Errorf("duplicate broker capability %q", capability.Name)
		}
		loaded.Policy.Capabilities[index] = capability
		for _, credentialID := range capability.CredentialIDs {
			if _, ok := loaded.credentials[credentialID]; !ok {
				return nil, fmt.Errorf("capability %q references unavailable credential %q", capability.Name, credentialID)
			}
		}
		if capability.Enabled {
			loaded.capabilities[capability.Name] = capability
		}
	}
	sort.Slice(loaded.Policy.Capabilities, func(i, j int) bool {
		return loaded.Policy.Capabilities[i].Name < loaded.Policy.Capabilities[j].Name
	})
	payload, _ := json.Marshal(loaded.Policy)
	digest := sha256.Sum256(payload)
	loaded.Digest = hex.EncodeToString(digest[:])
	return loaded, nil
}

func normalizeWebAuthnAuthority(credentialID, rpID string, origins []string) (string, string, []string, error) {
	credentialID = strings.TrimSpace(credentialID)
	if _, err := decodeCanonicalRawURL(credentialID, "credential_id", 1, 1023); err != nil {
		return "", "", nil, err
	}
	rpID = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(rpID), "."))
	if rpID == "" || len(rpID) > 253 || net.ParseIP(rpID) != nil || !webAuthnRPIDPattern.MatchString(rpID) {
		return "", "", nil, fmt.Errorf("rp_id must be a canonical DNS name")
	}
	if len(origins) == 0 || len(origins) > 8 {
		return "", "", nil, fmt.Errorf("allowed_origins must contain between 1 and 8 origins")
	}
	normalizedOrigins := make([]string, 0, len(origins))
	seen := map[string]struct{}{}
	for _, value := range origins {
		parsed, err := url.Parse(strings.TrimSpace(value))
		if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", "", nil, fmt.Errorf("allowed origin %q is not an origin without path, query, or fragment", value)
		}
		scheme := strings.ToLower(parsed.Scheme)
		hostname := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
		if scheme != "https" && !(scheme == "http" && rpID == "localhost") {
			return "", "", nil, fmt.Errorf("allowed origin %q must use HTTPS; HTTP is limited to localhost", value)
		}
		if hostname != rpID && !strings.HasSuffix(hostname, "."+rpID) {
			return "", "", nil, fmt.Errorf("allowed origin %q is outside rp_id %q", value, rpID)
		}
		host := hostname
		if port := parsed.Port(); port != "" {
			host = net.JoinHostPort(hostname, port)
		}
		origin := scheme + "://" + host
		if _, exists := seen[origin]; exists {
			return "", "", nil, fmt.Errorf("duplicate allowed origin %q", origin)
		}
		seen[origin] = struct{}{}
		normalizedOrigins = append(normalizedOrigins, origin)
	}
	sort.Strings(normalizedOrigins)
	return credentialID, rpID, normalizedOrigins, nil
}

func (p *LoadedPolicy) PublicApprovalAuthorities() []PublicApprovalAuthority {
	items := make([]PublicApprovalAuthority, 0, len(p.authorities))
	for _, authority := range p.authorities {
		items = append(items, authority)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].KeyID < items[j].KeyID })
	return items
}

func normalizeCapability(input Capability) (Capability, error) {
	out := input
	out.Name = strings.ToLower(strings.TrimSpace(out.Name))
	out.Description = strings.TrimSpace(out.Description)
	out.PermissionLevel = strings.ToUpper(strings.TrimSpace(out.PermissionLevel))
	out.RiskLevel = strings.ToLower(strings.TrimSpace(out.RiskLevel))
	out.ExecutableSHA256 = strings.ToLower(strings.TrimSpace(out.ExecutableSHA256))
	var err error
	out.CredentialIDs, err = normalizePolicyNames(out.CredentialIDs, credentialIDPattern, "credential")
	if err != nil {
		return out, err
	}
	if !capabilityNamePattern.MatchString(out.Name) {
		return out, fmt.Errorf("name must be a stable lowercase capability identifier")
	}
	if len([]rune(out.Description)) > 1000 {
		return out, fmt.Errorf("description must not exceed 1000 characters")
	}
	if out.PermissionLevel != "" && out.PermissionLevel != "A4" && out.PermissionLevel != "A5" && out.PermissionLevel != "A6" && out.PermissionLevel != "A7" {
		return out, fmt.Errorf("legacy permission_level, when present, must be A4-A7")
	}
	if out.RiskLevel != "" && out.RiskLevel != "high" && out.RiskLevel != "critical" {
		return out, fmt.Errorf("legacy risk_level, when present, must be high or critical")
	}
	out.Executable = filepath.Clean(strings.TrimSpace(out.Executable))
	if out.Executable == "." || !filepath.IsAbs(out.Executable) {
		return out, fmt.Errorf("executable must be an absolute path")
	}
	evaluated, err := filepath.EvalSymlinks(out.Executable)
	if err != nil {
		return out, fmt.Errorf("resolve executable path without reparse ambiguity: %w", err)
	}
	out.Executable = filepath.Clean(evaluated)
	if brokerShellInterpreter(out.Executable) {
		return out, fmt.Errorf("shell interpreters are not valid broker capabilities")
	}
	info, err := os.Stat(out.Executable)
	if err != nil || !info.Mode().IsRegular() {
		return out, fmt.Errorf("executable is unavailable or not a regular file")
	}
	if info.Size() > 1<<30 {
		return out, fmt.Errorf("executable exceeds the 1 GiB broker hashing limit")
	}
	if len(out.ExecutableSHA256) != 64 {
		return out, fmt.Errorf("executable_sha256 is required")
	}
	actualDigest, err := hashFile(out.Executable)
	if err != nil {
		return out, err
	}
	if !strings.EqualFold(actualDigest, out.ExecutableSHA256) {
		return out, fmt.Errorf("executable_sha256 does not match the current binary")
	}
	out.ExecutableSHA256 = actualDigest
	if len(out.Arguments) > 100 {
		return out, fmt.Errorf("fixed arguments must not exceed 100 entries")
	}
	for _, argument := range out.Arguments {
		if len([]rune(argument)) > 4000 {
			return out, fmt.Errorf("one fixed argument exceeds 4000 characters")
		}
	}
	out.Arguments = append([]string(nil), out.Arguments...)
	if out.InputSchema != nil {
		if err := validateBrokerInputSchemaDefinition(out.InputSchema); err != nil {
			return out, fmt.Errorf("input_schema: %w", err)
		}
		// Canonicalize through JSON so policy digests and schema validation do
		// not retain caller-owned mutable maps.
		raw, _ := json.Marshal(out.InputSchema)
		out.InputSchema = map[string]any{}
		_ = json.Unmarshal(raw, &out.InputSchema)
	}
	out.WorkingDirectory = filepath.Clean(strings.TrimSpace(out.WorkingDirectory))
	if out.WorkingDirectory == "." || out.WorkingDirectory == "" {
		out.WorkingDirectory = filepath.Dir(out.Executable)
	}
	if !filepath.IsAbs(out.WorkingDirectory) {
		return out, fmt.Errorf("working_directory must be absolute")
	}
	workingInfo, err := os.Stat(out.WorkingDirectory)
	if err != nil || !workingInfo.IsDir() {
		return out, fmt.Errorf("working_directory is unavailable")
	}
	if err := capabilityPathSecurityValidator(out.Executable, out.WorkingDirectory); err != nil {
		return out, err
	}
	if out.TimeoutSeconds == 0 {
		out.TimeoutSeconds = 60
	}
	if out.TimeoutSeconds < 1 || out.TimeoutSeconds > 3600 {
		return out, fmt.Errorf("timeout_seconds must be between 1 and 3600")
	}
	if out.MaxOutputBytes == 0 {
		out.MaxOutputBytes = 64 << 10
	}
	if out.MaxOutputBytes < 1024 || out.MaxOutputBytes > 8<<20 {
		return out, fmt.Errorf("max_output_bytes must be between 1024 and 8388608")
	}
	canonical := out
	canonical.digest = ""
	payload, _ := json.Marshal(canonical)
	digest := sha256.Sum256(payload)
	out.digest = hex.EncodeToString(digest[:])
	return out, nil
}

func (p *LoadedPolicy) Capability(name string) (Capability, bool) {
	if p == nil {
		return Capability{}, false
	}
	capability, ok := p.capabilities[strings.ToLower(strings.TrimSpace(name))]
	return capability, ok
}

func (p *LoadedPolicy) PublicCapabilities() []PublicCapability {
	items := make([]PublicCapability, 0, len(p.capabilities))
	for _, capability := range p.capabilities {
		items = append(items, capability.Public())
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
	return items
}

func (c Capability) Public() PublicCapability {
	schemaDigest := ""
	if c.InputSchema != nil {
		payload, _ := json.Marshal(c.InputSchema)
		digest := sha256.Sum256(payload)
		schemaDigest = hex.EncodeToString(digest[:])
	}
	return PublicCapability{
		Name: c.Name, Description: c.Description, PermissionLevel: c.PermissionLevel,
		RiskLevel: c.RiskLevel, ExecutableName: filepath.Base(c.Executable),
		ArgumentCount: len(c.Arguments), TimeoutSeconds: c.TimeoutSeconds,
		MaxOutputBytes: c.MaxOutputBytes, CapabilityDigest: c.digest,
		CredentialIDs: append([]string(nil), c.CredentialIDs...),
		AcceptsInput:  c.InputSchema != nil, InputSchemaDigest: schemaDigest,
	}
}

func normalizePolicyNames(values []string, pattern *regexp.Regexp, label string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !pattern.MatchString(value) {
			return nil, fmt.Errorf("invalid %s %q", label, value)
		}
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	sort.Strings(result)
	return result, nil
}

func (p *LoadedPolicy) BrokerPeer(deviceID string) (BrokerPeer, bool) {
	peer, ok := p.peers[strings.TrimSpace(deviceID)]
	return peer, ok
}

func (p *LoadedPolicy) PublicBrokerPeers() []PublicBrokerPeer {
	items := make([]PublicBrokerPeer, 0, len(p.peers))
	for _, peer := range p.peers {
		items = append(items, PublicBrokerPeer{DeviceID: peer.DeviceID, Name: peer.Name, PublicKey: peer.PublicKey, KeyID: peer.keyID,
			AllowedCapabilities: append([]string(nil), peer.AllowedCapabilities...), AllowedCredentials: append([]string(nil), peer.AllowedCredentials...)})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].DeviceID < items[j].DeviceID })
	return items
}

func (p *LoadedPolicy) Credential(id string) (BrokerCredential, bool) {
	credential, ok := p.credentials[strings.ToLower(strings.TrimSpace(id))]
	return credential, ok
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open executable for digest: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", fmt.Errorf("stat executable for digest: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > 1<<30 {
		return "", fmt.Errorf("executable is not a regular file within the 1 GiB hashing limit")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("hash executable: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func brokerShellInterpreter(path string) bool {
	name := strings.ToLower(filepath.Base(path))
	for _, denied := range []string{"cmd", "cmd.exe", "powershell", "powershell.exe", "pwsh", "pwsh.exe", "sh", "bash", "zsh", "fish", "wscript.exe", "cscript.exe"} {
		if name == denied {
			return true
		}
	}
	return false
}
