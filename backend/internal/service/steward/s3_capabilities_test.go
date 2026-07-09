package steward

import (
	"testing"
	"time"

	"mongojson/backend/internal/domain"
)

func TestDeviceCapabilitySyncIDsAreStableAndScoped(t *testing.T) {
	first := deviceCapabilitySyncEntityID("windows-main", AutonomyActionCreateLocalTask)
	if first != deviceCapabilitySyncEntityID("windows-main", AutonomyActionCreateLocalTask) {
		t.Fatalf("device capability entity id must be stable")
	}
	if first == deviceCapabilitySyncEntityID("macbook-main", AutonomyActionCreateLocalTask) {
		t.Fatalf("device capability entity id must include the owning device")
	}
	if deviceCapabilitySyncChangeID("windows-main", AutonomyActionCreateLocalTask, 1) ==
		deviceCapabilitySyncChangeID("windows-main", AutonomyActionCreateLocalTask, 2) {
		t.Fatalf("device capability change id must include the declaration version")
	}
}

func TestSameDeviceCapabilityIgnoresTransportTimestamp(t *testing.T) {
	left := domain.StewardDeviceCapability{
		DeviceID: "windows-main", Capability: AutonomyActionCreateLocalTask,
		Description: "create task", TargetType: "task", RiskLevel: "low",
		MaxPermissionLevel: PermissionA3, Version: 2,
		UpdatedAt: time.Date(2026, 7, 5, 10, 0, 0, 0, time.UTC),
	}
	right := left
	right.UpdatedAt = left.UpdatedAt.Add(time.Hour)
	if !sameDeviceCapability(left, right) {
		t.Fatalf("transport timestamp must not create an equal-version capability conflict")
	}
	right.Description = "changed"
	if sameDeviceCapability(left, right) {
		t.Fatalf("capability metadata change must be detected")
	}
}
