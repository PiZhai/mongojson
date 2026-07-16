package privilegebroker

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
)

func webAuthnSignCount(assertion WebAuthnAssertion) (uint32, error) {
	authenticatorData, err := decodeCanonicalRawURL(assertion.AuthenticatorData, "authenticator data", 37, 16<<10)
	if err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint32(authenticatorData[33:37]), nil
}

func validateWebAuthnSignCount(previous, current uint32) error {
	// WebAuthn permits authenticators that do not implement a signature counter
	// to return zero. Clone detection is available only when both observations
	// are non-zero.
	if previous != 0 && current != 0 && current <= previous {
		return fmt.Errorf("WebAuthn authenticator counter did not advance")
	}
	return nil
}

type approvalChallengeClaims struct {
	Version           string `json:"version"`
	ProofID           string `json:"proof_id"`
	Subject           string `json:"subject"`
	PlanHash          string `json:"plan_hash"`
	Capability        string `json:"capability"`
	ControlGeneration int64  `json:"control_generation"`
	GrantedBy         string `json:"granted_by"`
	Reason            string `json:"reason"`
	IssuedAtUnixMS    int64  `json:"issued_at_unix_ms"`
	ExpiresAtUnixMS   int64  `json:"expires_at_unix_ms"`
}

type collectedClientData struct {
	Type        string `json:"type"`
	Challenge   string `json:"challenge"`
	Origin      string `json:"origin"`
	CrossOrigin bool   `json:"crossOrigin"`
}

func ApprovalProofChallenge(claims ApprovalProofClaims) ([]byte, error) {
	normalized, err := normalizeApprovalProofClaims(claims)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(approvalChallengeClaims{
		Version: normalized.Version, ProofID: normalized.ProofID, Subject: normalized.Subject,
		PlanHash: normalized.PlanHash, Capability: normalized.Capability,
		ControlGeneration: normalized.ControlGeneration, GrantedBy: normalized.GrantedBy, Reason: normalized.Reason,
		IssuedAtUnixMS: normalized.IssuedAt.UnixMilli(), ExpiresAtUnixMS: normalized.ExpiresAt.UnixMilli(),
	})
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	return digest[:], nil
}

func verifyWebAuthnApproval(authority PublicApprovalAuthority, claims ApprovalProofClaims, assertion WebAuthnAssertion) error {
	credentialID, err := decodeCanonicalRawURL(assertion.CredentialID, "credential id", 1, 1023)
	if err != nil {
		return err
	}
	authorityCredentialID, err := decodeCanonicalRawURL(authority.CredentialID, "authority credential id", 1, 1023)
	if err != nil {
		return err
	}
	if !bytes.Equal(credentialID, authorityCredentialID) {
		return fmt.Errorf("credential id does not match the approval authority")
	}

	clientDataJSON, err := decodeCanonicalRawURL(assertion.ClientDataJSON, "client data", 2, 16<<10)
	if err != nil {
		return err
	}
	var clientData collectedClientData
	if err := json.Unmarshal(clientDataJSON, &clientData); err != nil {
		return fmt.Errorf("decode client data JSON: %w", err)
	}
	if clientData.Type != "webauthn.get" {
		return fmt.Errorf("client data type must be webauthn.get")
	}
	challenge, err := ApprovalProofChallenge(claims)
	if err != nil {
		return err
	}
	if clientData.Challenge != base64.RawURLEncoding.EncodeToString(challenge) {
		return fmt.Errorf("client data challenge does not bind the approval claims")
	}
	if clientData.CrossOrigin {
		return fmt.Errorf("cross-origin WebAuthn approval is not allowed")
	}
	originAllowed := false
	for _, allowed := range authority.AllowedOrigins {
		if clientData.Origin == allowed {
			originAllowed = true
			break
		}
	}
	if !originAllowed {
		return fmt.Errorf("client data origin is not allowed")
	}

	authenticatorData, err := decodeCanonicalRawURL(assertion.AuthenticatorData, "authenticator data", 37, 16<<10)
	if err != nil {
		return err
	}
	rpIDHash := sha256.Sum256([]byte(authority.RPID))
	if !bytes.Equal(authenticatorData[:32], rpIDHash[:]) {
		return fmt.Errorf("authenticator RP ID hash does not match policy")
	}
	flags := authenticatorData[32]
	if flags&0x01 == 0 {
		return fmt.Errorf("authenticator did not prove user presence")
	}
	if flags&0x04 == 0 {
		return fmt.Errorf("authenticator did not prove user verification")
	}

	publicKeyDER, publicKey, err := decodeWebAuthnES256PublicKey(authority.PublicKey)
	if err != nil {
		return err
	}
	if keyMaterialID(publicKeyDER) != authority.KeyID {
		return fmt.Errorf("approval authority key id is invalid")
	}
	signature, err := decodeCanonicalRawURL(assertion.Signature, "assertion signature", 8, 256)
	if err != nil {
		return err
	}
	clientDataHash := sha256.Sum256(clientDataJSON)
	signed := make([]byte, 0, len(authenticatorData)+len(clientDataHash))
	signed = append(signed, authenticatorData...)
	signed = append(signed, clientDataHash[:]...)
	signedHash := sha256.Sum256(signed)
	if !ecdsa.VerifyASN1(publicKey, signedHash[:], signature) {
		return fmt.Errorf("invalid ES256 assertion signature")
	}
	return nil
}

func decodeWebAuthnES256PublicKey(value string) ([]byte, *ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(der) == 0 || len(der) > 4096 {
		return nil, nil, fmt.Errorf("WebAuthn public key must be a base64 SubjectPublicKeyInfo value")
	}
	parsed, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, nil, fmt.Errorf("parse WebAuthn SubjectPublicKeyInfo: %w", err)
	}
	publicKey, ok := parsed.(*ecdsa.PublicKey)
	if !ok || publicKey.Curve != elliptic.P256() {
		return nil, nil, fmt.Errorf("WebAuthn approval authority must use ES256 with P-256")
	}
	canonical, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil || !bytes.Equal(canonical, der) {
		return nil, nil, fmt.Errorf("WebAuthn public key is not canonical SubjectPublicKeyInfo")
	}
	return der, publicKey, nil
}

func decodeCanonicalRawURL(value, label string, minBytes, maxBytes int) ([]byte, error) {
	value = strings.TrimSpace(value)
	decoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || len(decoded) < minBytes || len(decoded) > maxBytes || base64.RawURLEncoding.EncodeToString(decoded) != value {
		return nil, fmt.Errorf("%s must be canonical base64url without padding", label)
	}
	return decoded, nil
}
