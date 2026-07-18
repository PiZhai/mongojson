package steward

import "testing"

func TestSuggestNextToolVersionUsesHighestImmutableVersion(t *testing.T) {
	got := suggestNextToolVersion("1.0.0", []string{"1.0.0", "1.0.4", "1.0.3-beta.1"})
	if got != "1.0.5" {
		t.Fatalf("suggestNextToolVersion = %s, want 1.0.5", got)
	}
}

func TestSuggestNextToolVersionAdvancesAcrossMinorVersions(t *testing.T) {
	got := suggestNextToolVersion("1.0.9", []string{"1.2.0", "2.0.0"})
	if got != "2.0.1" {
		t.Fatalf("suggestNextToolVersion = %s, want 2.0.1", got)
	}
}
