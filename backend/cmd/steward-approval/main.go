package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	"mongojson/backend/internal/privilegebroker"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "keygen":
		err = keygen()
	case "issue":
		err = issue(os.Args[2:])
	case "verify":
		err = verify(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		log.Fatal(err)
	}
}

func keygen() error {
	keys, err := privilegebroker.GenerateApprovalAuthorityKeys()
	if err != nil {
		return err
	}
	return writeJSON(map[string]any{
		"keys":             keys,
		"policy_authority": map[string]any{"name": "local-operator", "public_key": keys.PublicKey, "enabled": true},
	})
}

func issue(args []string) error {
	fs := flag.NewFlagSet("steward-approval issue", flag.ContinueOnError)
	privateKeyFile := fs.String("private-key-file", "", "file containing the base64 Ed25519 private key; prefer an OS-protected file")
	subject := fs.String("subject", "", "exact execution subject, for example runtime:<run-id>")
	planHash := fs.String("plan-hash", "", "immutable plan SHA-256")
	capability := fs.String("capability", "", "exact Broker capability")
	generation := fs.Int64("generation", -1, "current unified execution-control generation")
	grantedBy := fs.String("granted-by", "local-operator", "human operator identity")
	reason := fs.String("reason", "", "human approval reason")
	ttl := fs.Duration("ttl", 5*time.Minute, "proof lifetime, maximum 15m")
	approved := fs.Bool("approve", false, "required explicit confirmation that the displayed execution is approved")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*approved {
		return fmt.Errorf("issue requires --approve after the operator reviews subject, plan hash, capability, generation, and reason")
	}
	privateKey := strings.TrimSpace(os.Getenv("STEWARD_APPROVAL_PRIVATE_KEY"))
	if strings.TrimSpace(*privateKeyFile) != "" {
		if privateKey != "" {
			return fmt.Errorf("use either STEWARD_APPROVAL_PRIVATE_KEY or --private-key-file, not both")
		}
		payload, err := os.ReadFile(strings.TrimSpace(*privateKeyFile))
		if err != nil {
			return fmt.Errorf("read approval private key file: %w", err)
		}
		privateKey = strings.TrimSpace(string(payload))
	}
	if privateKey == "" {
		return fmt.Errorf("STEWARD_APPROVAL_PRIVATE_KEY or --private-key-file is required")
	}
	if *generation < 0 || *ttl <= 0 || *ttl > privilegebroker.MaxApprovalProofTTL {
		return fmt.Errorf("generation must be non-negative and ttl must be between 1ns and %s", privilegebroker.MaxApprovalProofTTL)
	}
	proofIDBytes := make([]byte, 32)
	if _, err := rand.Read(proofIDBytes); err != nil {
		return err
	}
	now := time.Now().UTC()
	proof, err := privilegebroker.IssueApprovalProof(privateKey, privilegebroker.ApprovalProofClaims{
		ProofID: hex.EncodeToString(proofIDBytes), Subject: *subject, PlanHash: *planHash, Capability: *capability,
		ControlGeneration: *generation, GrantedBy: *grantedBy, Reason: *reason,
		IssuedAt: now, ExpiresAt: now.Add(*ttl),
	})
	if err != nil {
		return err
	}
	return writeJSON(proof)
}

func verify(args []string) error {
	fs := flag.NewFlagSet("steward-approval verify", flag.ContinueOnError)
	publicKey := fs.String("public-key", "", "base64 Ed25519 approval authority public key")
	proofPath := fs.String("proof", "-", "signed proof JSON path, or - for stdin")
	subject := fs.String("subject", "", "expected execution subject")
	planHash := fs.String("plan-hash", "", "expected plan SHA-256")
	capability := fs.String("capability", "", "expected Broker capability")
	generation := fs.Int64("generation", -1, "expected control generation")
	reason := fs.String("reason", "", "expected approval reason")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *generation < 0 {
		return fmt.Errorf("verify requires --generation >= 0")
	}
	var reader io.Reader = os.Stdin
	if strings.TrimSpace(*proofPath) != "-" {
		file, err := os.Open(*proofPath)
		if err != nil {
			return err
		}
		defer file.Close()
		reader = file
	}
	var proof privilegebroker.SignedApprovalProof
	decoder := json.NewDecoder(io.LimitReader(reader, 128<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&proof); err != nil {
		return fmt.Errorf("decode approval proof: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("approval proof must contain exactly one JSON object")
	}
	authority := privilegebroker.PublicApprovalAuthority{Name: "verification", Algorithm: "ed25519", PublicKey: *publicKey, KeyID: proof.KeyID}
	if err := privilegebroker.VerifyApprovalProof([]privilegebroker.PublicApprovalAuthority{authority}, proof, privilegebroker.ApprovalProofExpectation{
		Subject: *subject, PlanHash: *planHash, Capability: *capability, ControlGeneration: *generation, Reason: *reason,
	}, time.Now().UTC()); err != nil {
		return err
	}
	return writeJSON(map[string]any{"valid": true, "claims": proof.Claims, "key_id": proof.KeyID})
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: steward-approval <keygen|issue|verify>")
}
