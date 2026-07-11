package steward

import "testing"

func TestNormalizePeerDeviceRegistrationProtectsLocalIdentity(t *testing.T) {
	tests := []struct {
		name  string
		input RegisterDeviceInput
	}{
		{name: "missing id", input: RegisterDeviceInput{}},
		{name: "local identity", input: RegisterDeviceInput{ID: "windows-main"}},
		{name: "local role", input: RegisterDeviceInput{ID: "macbook-main", Role: DeviceRoleLocal}},
		{name: "unknown role", input: RegisterDeviceInput{ID: "macbook-main", Role: "admin"}},
		{name: "unknown platform", input: RegisterDeviceInput{ID: "macbook-main", Platform: "macos"}},
		{name: "invalid permission", input: RegisterDeviceInput{ID: "macbook-main", PermissionLevel: "root"}},
		{name: "URL credentials", input: RegisterDeviceInput{ID: "macbook-main", APIBaseURL: "https://user:pass@peer.example/api"}},
		{name: "management-like path", input: RegisterDeviceInput{ID: "macbook-main", APIBaseURL: "https://peer.example/management"}},
		{name: "URL query", input: RegisterDeviceInput{ID: "macbook-main", APIBaseURL: "https://peer.example/api?token=value"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := normalizePeerDeviceRegistration("windows-main", tt.input); err == nil {
				t.Fatal("expected unsafe device registration to fail")
			}
		})
	}
}

func TestNormalizePeerDeviceRegistrationCanonicalizesPeer(t *testing.T) {
	syncEnabled := true
	input, err := normalizePeerDeviceRegistration("windows-main", RegisterDeviceInput{
		ID: " macbook-main ", Platform: " DARWIN ", PermissionLevel: " a2 ",
		APIBaseURL: "https://peer.example/api/", SyncEnabled: &syncEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if input.ID != "macbook-main" || input.DeviceName != "macbook-main" || input.Role != DeviceRolePeer ||
		input.Platform != "darwin" || input.PermissionLevel != PermissionA2 || input.APIBaseURL != "https://peer.example/api" {
		t.Fatalf("unexpected normalized peer: %+v", input)
	}
}
