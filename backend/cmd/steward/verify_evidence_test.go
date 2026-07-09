package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteVerificationEvidencePersistsEnvelopeAndRedactsCommand(t *testing.T) {
	originalArgs := os.Args
	os.Args = []string{
		"steward",
		"verify",
		"runtime",
		"--llm-api-key",
		"secret-value",
		"--sync-encryption-key=sync-secret",
		"--expect-agent-id",
		"windows-main",
	}
	t.Cleanup(func() { os.Args = originalArgs })

	path, err := writeVerificationEvidence("runtime/check", t.TempDir(), map[string]any{
		"verification": runtimeVerificationResult{OK: true, APIBase: "http://127.0.0.1:18080/api"},
	}, true)
	if err != nil {
		t.Fatalf("write evidence: %v", err)
	}
	if filepath.Base(path) == path || !strings.Contains(filepath.Base(path), "runtime_check") {
		t.Fatalf("unexpected evidence path %q", path)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "secret-value") || strings.Contains(text, "sync-secret") {
		t.Fatalf("evidence leaked sensitive command arg: %s", text)
	}
	var envelope verificationEvidenceEnvelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		t.Fatalf("decode evidence envelope: %v", err)
	}
	if envelope.Kind != "runtime_check" || !envelope.OK || envelope.Payload == nil || envelope.CreatedAt.IsZero() {
		t.Fatalf("unexpected evidence envelope: %#v", envelope)
	}
	command := strings.Join(envelope.Command, " ")
	if !strings.Contains(command, "<redacted>") || !strings.Contains(command, "windows-main") {
		t.Fatalf("evidence command was not redacted as expected: %#v", envelope.Command)
	}
}

func TestPrintVerificationResultIncludesEvidencePath(t *testing.T) {
	output, err := captureStdoutText(t, func() error {
		return printVerificationResult("runtime", t.TempDir(), runtimeVerificationResult{OK: true}, true)
	})
	if err != nil {
		t.Fatalf("print verification result: %v", err)
	}
	if !strings.Contains(output, `"evidence_path"`) || !strings.Contains(output, `"verification"`) {
		t.Fatalf("verification output missing evidence path: %s", output)
	}
}

func TestUniqueEvidencePathAvoidsOverwrite(t *testing.T) {
	dir := t.TempDir()
	first, err := uniqueEvidencePath(dir, "steward-verify-runtime-20260706T000000.000000000Z", "pass")
	if err != nil {
		t.Fatalf("first path: %v", err)
	}
	if err := os.WriteFile(first, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed first path: %v", err)
	}
	second, err := uniqueEvidencePath(dir, "steward-verify-runtime-20260706T000000.000000000Z", "pass")
	if err != nil {
		t.Fatalf("second path: %v", err)
	}
	if first == second || !strings.Contains(filepath.Base(second), "-02-pass.json") {
		t.Fatalf("expected unique second evidence path, first=%s second=%s", first, second)
	}
}
