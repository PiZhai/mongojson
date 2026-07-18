//go:build windows

package main

import (
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCompanionPipeSDDLUsesExactUserAndServiceSIDs(t *testing.T) {
	descriptor := companionPipeSDDL("S-1-5-21-1-2-3-1001", "S-1-5-80-1-2-3-4-5")
	if strings.Contains(descriptor, ";;;IU)") {
		t.Fatal("broad Interactive Users ACE must not be present")
	}
	if !strings.Contains(descriptor, ";;;S-1-5-21-1-2-3-1001)") || !strings.Contains(descriptor, ";;;S-1-5-80-1-2-3-4-5)") {
		t.Fatalf("descriptor does not contain exact SIDs: %s", descriptor)
	}
	if _, err := windows.SecurityDescriptorFromString(descriptor); err != nil {
		t.Fatalf("descriptor is invalid: %v", err)
	}
}
