package privilegebroker

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestApprovalProofChallengeDeterministicVector(t *testing.T) {
	claims := webAuthnTestClaims()
	first, err := ApprovalProofChallenge(claims)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ApprovalProofChallenge(claims)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(first), "3ecff483c9d79e17be8c24683b699de8e1ff1cc2ab0d8fe1d0b3a0edefbf26b6"; got != want {
		t.Fatalf("challenge vector = %s, want %s", got, want)
	}
	if string(first) != string(second) {
		t.Fatal("identical approval claims produced different challenges")
	}
}

func TestApprovalProofChallengeMatchesFrontendVector(t *testing.T) {
	issuedAt := time.Date(2025, time.January, 1, 0, 0, 0, 123000000, time.UTC)
	challenge, err := ApprovalProofChallenge(ApprovalProofClaims{
		Version: ApprovalProofVersion, ProofID: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		Subject: "runtime:run-1", PlanHash: strings.Repeat("a", 64), Capability: "tool:whoami",
		ControlGeneration: 7, GrantedBy: "local-user", Reason: "approve once",
		IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(challenge), "d642a460b9decde3a070488517a4944ac4efb98342d06e47c197ee79783db3e2"; got != want {
		t.Fatalf("frontend challenge vector = %s, want %s", got, want)
	}
}

func TestWebAuthnApprovalProofAcceptsValidES256Assertion(t *testing.T) {
	authority, proof, expected, now := newWebAuthnApprovalFixture(t)
	if err := VerifyApprovalProof([]PublicApprovalAuthority{authority}, proof, expected, now); err != nil {
		t.Fatalf("valid WebAuthn approval proof was rejected: %v", err)
	}
}

func TestWebAuthnApprovalProofRejectsTampering(t *testing.T) {
	authority, proof, expected, now := newWebAuthnApprovalFixture(t)
	tests := []struct {
		name    string
		wantErr string
		mutate  func(*SignedApprovalProof)
	}{
		{
			name: "credential", wantErr: "credential id does not match",
			mutate: func(candidate *SignedApprovalProof) {
				candidate.WebAuthn.CredentialID = base64.RawURLEncoding.EncodeToString([]byte("different-credential"))
			},
		},
		{
			name: "challenge", wantErr: "challenge does not bind",
			mutate: func(candidate *SignedApprovalProof) {
				data := decodeClientDataForTest(t, candidate.WebAuthn.ClientDataJSON)
				data.Challenge = base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("B", sha256.Size)))
				candidate.WebAuthn.ClientDataJSON = encodeRawURLJSONForTest(t, data)
			},
		},
		{
			name: "origin", wantErr: "origin is not allowed",
			mutate: func(candidate *SignedApprovalProof) {
				data := decodeClientDataForTest(t, candidate.WebAuthn.ClientDataJSON)
				data.Origin = "https://phishing.invalid"
				candidate.WebAuthn.ClientDataJSON = encodeRawURLJSONForTest(t, data)
			},
		},
		{
			name: "rp hash", wantErr: "RP ID hash does not match",
			mutate: func(candidate *SignedApprovalProof) {
				data := decodeRawURLForTest(t, candidate.WebAuthn.AuthenticatorData)
				data[0] ^= 0xff
				candidate.WebAuthn.AuthenticatorData = base64.RawURLEncoding.EncodeToString(data)
			},
		},
		{
			name: "user presence", wantErr: "user presence",
			mutate: func(candidate *SignedApprovalProof) {
				data := decodeRawURLForTest(t, candidate.WebAuthn.AuthenticatorData)
				data[32] &^= 0x01
				candidate.WebAuthn.AuthenticatorData = base64.RawURLEncoding.EncodeToString(data)
			},
		},
		{
			name: "user verification", wantErr: "user verification",
			mutate: func(candidate *SignedApprovalProof) {
				data := decodeRawURLForTest(t, candidate.WebAuthn.AuthenticatorData)
				data[32] &^= 0x04
				candidate.WebAuthn.AuthenticatorData = base64.RawURLEncoding.EncodeToString(data)
			},
		},
		{
			name: "signature", wantErr: "invalid ES256 assertion signature",
			mutate: func(candidate *SignedApprovalProof) {
				signature := decodeRawURLForTest(t, candidate.WebAuthn.Signature)
				signature[len(signature)-1] ^= 0x01
				candidate.WebAuthn.Signature = base64.RawURLEncoding.EncodeToString(signature)
			},
		},
		{
			name: "non-canonical base64url", wantErr: "canonical base64url without padding",
			mutate: func(candidate *SignedApprovalProof) {
				candidate.WebAuthn.CredentialID += "="
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := proof
			assertion := *proof.WebAuthn
			candidate.WebAuthn = &assertion
			test.mutate(&candidate)
			err := VerifyApprovalProof([]PublicApprovalAuthority{authority}, candidate, expected, now)
			if err == nil || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("tampered proof error = %v, want substring %q", err, test.wantErr)
			}
		})
	}
}

func TestPolicyNormalizesWebAuthnApprovalAuthority(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	executable, digest := testExecutable(t)
	credentialID := base64.RawURLEncoding.EncodeToString([]byte("policy-credential"))
	loaded, err := ValidatePolicy(Policy{
		Version: 2,
		ApprovalAuthorities: []ApprovalAuthority{{
			Name: "hardware key", Algorithm: " WebAuthn-ES256 ", PublicKey: base64.StdEncoding.EncodeToString(publicKeyDER),
			CredentialID: " " + credentialID + " ", RPID: "Example.COM.",
			AllowedOrigins: []string{"https://login.example.com:8443", "HTTPS://EXAMPLE.COM"}, Enabled: true,
		}},
		Capabilities: []Capability{{
			Name: "tool:test", PermissionLevel: "A4", RiskLevel: "high", Executable: executable,
			ExecutableSHA256: digest, Enabled: true,
		}},
	})
	if err != nil {
		t.Fatalf("validate WebAuthn policy: %v", err)
	}
	authorities := loaded.PublicApprovalAuthorities()
	if len(authorities) != 1 {
		t.Fatalf("public authorities = %+v", authorities)
	}
	authority := authorities[0]
	if authority.Algorithm != ApprovalWebAuthnES256 || authority.CredentialID != credentialID || authority.RPID != "example.com" {
		t.Fatalf("authority was not normalized: %+v", authority)
	}
	if got, want := strings.Join(authority.AllowedOrigins, ","), "https://example.com,https://login.example.com:8443"; got != want {
		t.Fatalf("normalized origins = %q, want %q", got, want)
	}
	if authority.KeyID != keyMaterialID(publicKeyDER) {
		t.Fatalf("authority key id = %q", authority.KeyID)
	}
}

func TestWebAuthnSignCountRejectsAuthenticatorRollback(t *testing.T) {
	for _, test := range []struct {
		name              string
		previous, current uint32
		wantErr           bool
	}{
		{name: "first observation", current: 1},
		{name: "counter advances", previous: 1, current: 2},
		{name: "unsupported zero counter", previous: 8, current: 0},
		{name: "counter repeated", previous: 8, current: 8, wantErr: true},
		{name: "counter rolled back", previous: 8, current: 7, wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateWebAuthnSignCount(test.previous, test.current)
			if (err != nil) != test.wantErr {
				t.Fatalf("validate counter (%d, %d) error = %v", test.previous, test.current, err)
			}
		})
	}
}

func TestAuditRestoresWebAuthnSignCount(t *testing.T) {
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "audit.jsonl")
	log, err := newAuditLog(path, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := log.Append(AuditRecord{Type: "grant.issued", Outcome: "issued", Details: map[string]any{
		"approval_proof_id": strings.Repeat("a", 64), "approval_key_id": "hardware-key", "webauthn_sign_count": uint32(12),
	}}); err != nil {
		t.Fatal(err)
	}
	restored, err := newAuditLog(path, privateKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored.WebAuthnSignCounts()["hardware-key"]; got != 12 {
		t.Fatalf("restored WebAuthn sign count = %d, want 12", got)
	}
}

func newWebAuthnApprovalFixture(t *testing.T) (PublicApprovalAuthority, SignedApprovalProof, ApprovalProofExpectation, time.Time) {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	publicKeyDER, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	claims := webAuthnTestClaims()
	challenge, err := ApprovalProofChallenge(claims)
	if err != nil {
		t.Fatal(err)
	}
	clientDataJSON, err := json.Marshal(collectedClientData{
		Type: "webauthn.get", Challenge: base64.RawURLEncoding.EncodeToString(challenge), Origin: "https://approve.example.com",
	})
	if err != nil {
		t.Fatal(err)
	}
	rpIDHash := sha256.Sum256([]byte("example.com"))
	authenticatorData := make([]byte, 37)
	copy(authenticatorData, rpIDHash[:])
	authenticatorData[32] = 0x05
	authenticatorData[36] = 1
	clientDataHash := sha256.Sum256(clientDataJSON)
	signed := append(append([]byte(nil), authenticatorData...), clientDataHash[:]...)
	signedHash := sha256.Sum256(signed)
	signature, err := ecdsa.SignASN1(rand.Reader, privateKey, signedHash[:])
	if err != nil {
		t.Fatal(err)
	}
	credentialID := base64.RawURLEncoding.EncodeToString([]byte("test-hardware-credential"))
	authority := PublicApprovalAuthority{
		Name: "test authenticator", Algorithm: ApprovalWebAuthnES256,
		PublicKey: base64.StdEncoding.EncodeToString(publicKeyDER), KeyID: keyMaterialID(publicKeyDER),
		CredentialID: credentialID, RPID: "example.com", AllowedOrigins: []string{"https://approve.example.com"},
	}
	proof := SignedApprovalProof{
		Claims: claims, KeyID: authority.KeyID, Algorithm: ApprovalWebAuthnES256,
		WebAuthn: &WebAuthnAssertion{
			CredentialID: credentialID, ClientDataJSON: base64.RawURLEncoding.EncodeToString(clientDataJSON),
			AuthenticatorData: base64.RawURLEncoding.EncodeToString(authenticatorData), Signature: base64.RawURLEncoding.EncodeToString(signature),
		},
	}
	expected := ApprovalProofExpectation{
		Subject: claims.Subject, PlanHash: claims.PlanHash, Capability: claims.Capability,
		ControlGeneration: claims.ControlGeneration, Reason: claims.Reason,
	}
	return authority, proof, expected, claims.IssuedAt.Add(time.Minute)
}

func webAuthnTestClaims() ApprovalProofClaims {
	issuedAt := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)
	return ApprovalProofClaims{
		Version: ApprovalProofVersion, ProofID: strings.Repeat("1", 64), Subject: "runtime:run-123",
		PlanHash: strings.Repeat("2", 64), Capability: "tool:restart", ControlGeneration: 7,
		GrantedBy: "local-operator", Reason: "approved maintenance", IssuedAt: issuedAt, ExpiresAt: issuedAt.Add(5 * time.Minute),
	}
}

func decodeClientDataForTest(t *testing.T, encoded string) collectedClientData {
	t.Helper()
	payload := decodeRawURLForTest(t, encoded)
	var data collectedClientData
	if err := json.Unmarshal(payload, &data); err != nil {
		t.Fatal(err)
	}
	return data
}

func encodeRawURLJSONForTest(t *testing.T, value any) string {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeRawURLForTest(t *testing.T, encoded string) []byte {
	t.Helper()
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}
