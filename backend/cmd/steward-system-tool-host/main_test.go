package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestExecuteRejectsInvalidInput(t *testing.T) {
	if _, err := execute(strings.NewReader("{")); err == nil || !strings.Contains(err.Error(), "decode request") {
		t.Fatalf("execute invalid input error = %v", err)
	}
}

func TestRunCLIReportsToolHostFailureWithNonzeroExit(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := runCLI([]string{"run"}, strings.NewReader("{"), &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runCLI exit code = %d, want 1", code)
	}
	var response struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		t.Fatalf("decode error response: %v; output=%q", err, stdout.String())
	}
	if response.OK || !strings.Contains(response.Error, "decode request") {
		t.Fatalf("failure response = %+v", response)
	}
}

func TestExecuteRejectsMultipleObjects(t *testing.T) {
	input := `{"protocol":"steward-system-tool/1","invocation_id":"test","capability":"tool:system.uptime","arguments":{},"input_sha256":"44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"} {}`
	if _, err := execute(strings.NewReader(input)); err == nil || !strings.Contains(err.Error(), "exactly one JSON object") {
		t.Fatalf("execute multiple objects error = %v", err)
	}
}
