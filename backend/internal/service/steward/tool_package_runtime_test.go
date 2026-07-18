package steward

import (
	"strings"
	"testing"
)

func TestDecodeToolHostResponseRequiresProtocolEnvelope(t *testing.T) {
	_, err := decodeToolHostResponse([]byte(`{"entries":[],"count":0}`))
	if err == nil || !strings.Contains(err.Error(), `missing required boolean field "ok"`) {
		t.Fatalf("expected actionable missing ok error, got %v", err)
	}
}

func TestDecodeToolHostResponseRequiresOutputOnSuccess(t *testing.T) {
	_, err := decodeToolHostResponse([]byte(`{"ok":true,"evidence":[]}`))
	if err == nil || !strings.Contains(err.Error(), `require an object field "output"`) {
		t.Fatalf("expected actionable missing output error, got %v", err)
	}
}

func TestDecodeToolHostResponseRequiresErrorOnFailure(t *testing.T) {
	_, err := decodeToolHostResponse([]byte(`{"ok":false,"output":{}}`))
	if err == nil || !strings.Contains(err.Error(), `require a non-empty string field "error"`) {
		t.Fatalf("expected actionable missing error error, got %v", err)
	}
}

func TestDecodeToolHostResponseRequiresEvidenceArray(t *testing.T) {
	_, err := decodeToolHostResponse([]byte(`{"ok":true,"output":{}}`))
	if err == nil || !strings.Contains(err.Error(), `missing required array field "evidence"`) {
		t.Fatalf("expected actionable missing evidence error, got %v", err)
	}
}

func TestDecodeToolHostResponseAcceptsLastProtocolLine(t *testing.T) {
	response, err := decodeToolHostResponse([]byte("diagnostic line\n" + `{"ok":true,"output":{"count":2},"evidence":[]}` + "\n"))
	if err != nil {
		t.Fatalf("decode valid response: %v", err)
	}
	if !response.OK || response.Output["count"] != float64(2) || len(response.Evidence) != 0 {
		t.Fatalf("unexpected decoded response: %#v", response)
	}
}
