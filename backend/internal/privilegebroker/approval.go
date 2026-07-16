package privilegebroker

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"time"
)

const (
	ApprovalProofVersion = "steward-approval-proof/v1"
	MaxApprovalProofTTL  = 15 * time.Minute
	ApprovalEd25519      = "ed25519"
	ApprovalWebAuthnES256 = "webauthn-es256"
)

type ApprovalAuthorityKeys struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	KeyID      string `json:"key_id"`
}

type PublicApprovalAuthority struct {
	Name           string   `json:"name"`
	Algorithm      string   `json:"algorithm"`
	PublicKey      string   `json:"public_key"`
	KeyID          string   `json:"key_id"`
	CredentialID   string   `json:"credential_id,omitempty"`
	RPID           string   `json:"rp_id,omitempty"`
	AllowedOrigins []string `json:"allowed_origins,omitempty"`
}

type ApprovalProofClaims struct {
	Version           string    `json:"version"`
	ProofID           string    `json:"proof_id"`
	Subject           string    `json:"subject"`
	PlanHash          string    `json:"plan_hash"`
	Capability        string    `json:"capability"`
	ControlGeneration int64     `json:"control_generation"`
	GrantedBy         string    `json:"granted_by"`
	Reason            string    `json:"reason"`
	IssuedAt          time.Time `json:"issued_at"`
	ExpiresAt         time.Time `json:"expires_at"`
}

type SignedApprovalProof struct {
	Claims    ApprovalProofClaims `json:"claims"`
	KeyID     string              `json:"key_id"`
	Algorithm string              `json:"algorithm,omitempty"`
	Signature string              `json:"signature,omitempty"`
	WebAuthn  *WebAuthnAssertion  `json:"webauthn,omitempty"`
}

type WebAuthnAssertion struct {
	CredentialID     string `json:"credential_id"`
	ClientDataJSON   string `json:"client_data_json"`
	AuthenticatorData string `json:"authenticator_data"`
	Signature        string `json:"signature"`
}

type ApprovalProofExpectation struct {
	Subject           string
	PlanHash          string
	Capability        string
	ControlGeneration int64
	Reason            string
}

func GenerateApprovalAuthorityKeys() (ApprovalAuthorityKeys, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ApprovalAuthorityKeys{}, err
	}
	return ApprovalAuthorityKeys{
		PublicKey: base64.StdEncoding.EncodeToString(publicKey), PrivateKey: base64.StdEncoding.EncodeToString(privateKey),
		KeyID: publicKeyID(publicKey),
	}, nil
}

func IssueApprovalProof(encodedPrivateKey string, claims ApprovalProofClaims) (SignedApprovalProof, error) {
	privateKey, err := decodeApprovalPrivateKey(encodedPrivateKey)
	if err != nil {
		return SignedApprovalProof{}, err
	}
	normalized, err := normalizeApprovalProofClaims(claims)
	if err != nil {
		return SignedApprovalProof{}, err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	signature, err := signValue(privateKey, normalized)
	if err != nil {
		return SignedApprovalProof{}, err
	}
	return SignedApprovalProof{Claims: normalized, KeyID: publicKeyID(publicKey), Algorithm: ApprovalEd25519, Signature: signature}, nil
}

func VerifyApprovalProof(authorities []PublicApprovalAuthority, proof SignedApprovalProof, expected ApprovalProofExpectation, now time.Time) error {
	var authority *PublicApprovalAuthority
	for index := range authorities {
		if authorities[index].KeyID == strings.TrimSpace(proof.KeyID) {
			authority = &authorities[index]
			break
		}
	}
	if authority == nil {
		return fmt.Errorf("approval proof authority is not trusted")
	}
	if proof.KeyID != strings.TrimSpace(proof.KeyID) {
		return fmt.Errorf("approval proof key id is not canonical")
	}
	normalized, err := normalizeApprovalProofClaims(proof.Claims)
	if err != nil {
		return err
	}
	if normalized != proof.Claims {
		return fmt.Errorf("approval proof claims are not canonical")
	}
	algorithm := strings.ToLower(strings.TrimSpace(proof.Algorithm))
	if algorithm == "" {
		algorithm = ApprovalEd25519
	}
	if algorithm != strings.ToLower(strings.TrimSpace(authority.Algorithm)) {
		return fmt.Errorf("approval proof algorithm does not match its authority")
	}
	switch algorithm {
	case ApprovalEd25519:
		if proof.WebAuthn != nil {
			return fmt.Errorf("Ed25519 approval proof must not contain a WebAuthn assertion")
		}
		publicKey, decodeErr := decodeApprovalPublicKey(authority.PublicKey)
		if decodeErr != nil {
			return decodeErr
		}
		if publicKeyID(publicKey) != authority.KeyID {
			return fmt.Errorf("approval authority key id is invalid")
		}
		if err := verifyValue(publicKey, proof.Claims, proof.Signature); err != nil {
			return fmt.Errorf("verify approval proof: %w", err)
		}
	case ApprovalWebAuthnES256:
		if proof.Signature != "" {
			return fmt.Errorf("WebAuthn approval proof must not contain a detached signature")
		}
		if proof.WebAuthn == nil {
			return fmt.Errorf("WebAuthn approval proof assertion is required")
		}
		if err := verifyWebAuthnApproval(*authority, proof.Claims, *proof.WebAuthn); err != nil {
			return fmt.Errorf("verify WebAuthn approval proof: %w", err)
		}
	default:
		return fmt.Errorf("approval proof authority algorithm is not supported")
	}
	now = now.UTC()
	if proof.Claims.IssuedAt.After(now.Add(time.Minute)) || !proof.Claims.ExpiresAt.After(now) {
		return fmt.Errorf("approval proof is expired or not yet valid")
	}
	expected.Subject = strings.TrimSpace(expected.Subject)
	expected.PlanHash = strings.ToLower(strings.TrimSpace(expected.PlanHash))
	expected.Capability = strings.ToLower(strings.TrimSpace(expected.Capability))
	expected.Reason = strings.TrimSpace(expected.Reason)
	if proof.Claims.Subject != expected.Subject || proof.Claims.PlanHash != expected.PlanHash ||
		proof.Claims.Capability != expected.Capability || proof.Claims.ControlGeneration != expected.ControlGeneration {
		return fmt.Errorf("approval proof does not match the requested execution")
	}
	if expected.Reason != "" && proof.Claims.Reason != expected.Reason {
		return fmt.Errorf("approval proof reason does not match the approval decision")
	}
	return nil
}

func normalizeApprovalProofClaims(input ApprovalProofClaims) (ApprovalProofClaims, error) {
	out := input
	out.Version = strings.TrimSpace(out.Version)
	out.ProofID = strings.ToLower(strings.TrimSpace(out.ProofID))
	out.Subject = strings.TrimSpace(out.Subject)
	out.PlanHash = strings.ToLower(strings.TrimSpace(out.PlanHash))
	out.Capability = strings.ToLower(strings.TrimSpace(out.Capability))
	out.GrantedBy = strings.TrimSpace(out.GrantedBy)
	out.Reason = strings.TrimSpace(out.Reason)
	out.IssuedAt = out.IssuedAt.UTC()
	out.ExpiresAt = out.ExpiresAt.UTC()
	if out.Version == "" {
		out.Version = ApprovalProofVersion
	}
	if out.Version != ApprovalProofVersion {
		return out, fmt.Errorf("unsupported approval proof version")
	}
	if !hexDigestPattern.MatchString(out.ProofID) {
		return out, fmt.Errorf("approval proof_id must be a lowercase SHA-256 value")
	}
	if out.Subject == "" || len([]rune(out.Subject)) > 200 {
		return out, fmt.Errorf("approval proof subject is invalid")
	}
	if !hexDigestPattern.MatchString(out.PlanHash) {
		return out, fmt.Errorf("approval proof plan_hash must be lowercase SHA-256")
	}
	if !capabilityNamePattern.MatchString(out.Capability) {
		return out, fmt.Errorf("approval proof capability is invalid")
	}
	if out.ControlGeneration < 0 {
		return out, fmt.Errorf("approval proof control generation must not be negative")
	}
	if out.GrantedBy == "" || len([]rune(out.GrantedBy)) > 200 {
		return out, fmt.Errorf("approval proof granted_by is invalid")
	}
	if out.Reason == "" || len([]rune(out.Reason)) > 1000 {
		return out, fmt.Errorf("approval proof reason is required and must not exceed 1000 characters")
	}
	if out.IssuedAt.IsZero() || !out.ExpiresAt.After(out.IssuedAt) || out.ExpiresAt.Sub(out.IssuedAt) > MaxApprovalProofTTL {
		return out, fmt.Errorf("approval proof TTL must be greater than zero and no more than %s", MaxApprovalProofTTL)
	}
	return out, nil
}

func decodeApprovalPrivateKey(value string) (ed25519.PrivateKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("approval authority private key must be a base64 Ed25519 private key")
	}
	return ed25519.PrivateKey(decoded), nil
}

func decodeApprovalPublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("approval authority public key must be a base64 Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}
