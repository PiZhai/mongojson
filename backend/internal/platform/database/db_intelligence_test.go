package database

import "testing"

func TestDefaultIntelligenceCaptureProfile(t *testing.T) {
	if got := defaultIntelligenceCaptureProfile("windows"); got != "deep" {
		t.Fatalf("Windows capture profile = %q, want deep", got)
	}
	if got := defaultIntelligenceCaptureProfile("linux"); got != "hybrid" {
		t.Fatalf("Linux capture profile = %q, want hybrid", got)
	}
}
