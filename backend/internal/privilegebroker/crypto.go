package privilegebroker

import (
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

func requestSignature(key []byte, method, path, timestamp, nonce string, body []byte) string {
	digest := sha256.Sum256(body)
	canonical := strings.Join([]string{method, path, timestamp, nonce, hex.EncodeToString(digest[:])}, "\n")
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(canonical))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func verifyRequestSignature(key []byte, method, path, timestamp, nonce, signature string, body []byte) bool {
	expected := requestSignature(key, method, path, timestamp, nonce, body)
	return hmac.Equal([]byte(expected), []byte(strings.TrimSpace(signature)))
}

func signValue(privateKey ed25519.PrivateKey, value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload)), nil
}

func verifyValue(publicKey ed25519.PublicKey, value any, signature string) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(signature))
	if err != nil || !ed25519.Verify(publicKey, payload, decoded) {
		return fmt.Errorf("invalid broker signature")
	}
	return nil
}

func encodeSignedToken(privateKey ed25519.PrivateKey, claims CapabilityTokenClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signature := ed25519.Sign(privateKey, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func decodeSignedToken(publicKey ed25519.PublicKey, token string) (CapabilityTokenClaims, error) {
	var claims CapabilityTokenClaims
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 2 {
		return claims, fmt.Errorf("invalid capability token format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, fmt.Errorf("decode capability token: %w", err)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || !ed25519.Verify(publicKey, payload, signature) {
		return claims, fmt.Errorf("invalid capability token signature")
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return claims, fmt.Errorf("decode capability token claims: %w", err)
	}
	return claims, nil
}

func publicKeyID(publicKey ed25519.PublicKey) string {
	return keyMaterialID(publicKey)
}

func keyMaterialID(publicKey []byte) string {
	digest := sha256.Sum256(publicKey)
	return hex.EncodeToString(digest[:8])
}

func randomID() (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func decodeSharedKey(value string) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) < 32 {
		return nil, fmt.Errorf("broker client key must be base64 with at least 32 decoded bytes")
	}
	return decoded, nil
}

func decodePrivateKey(value string) (ed25519.PrivateKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("broker signing private key must be base64 Ed25519 private key")
	}
	return ed25519.PrivateKey(decoded), nil
}

func decodePublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("broker public key must be base64 Ed25519 public key")
	}
	return ed25519.PublicKey(decoded), nil
}
