//go:build windows

package servicecontrol

import (
	"testing"

	"golang.org/x/sys/windows"
)

func TestSplitPrivateEnvironmentKeepsBrokerKeysOutOfRegistry(t *testing.T) {
	public, private := splitPrivateEnvironment(map[string]string{
		"STEWARD_BROKER_LISTEN":              "127.0.0.1:18100",
		"STEWARD_BROKER_CLIENT_KEY":          "client",
		"STEWARD_BROKER_CONTROL_KEY":         "control",
		"STEWARD_BROKER_SIGNING_PRIVATE_KEY": "signing",
	})
	if len(public) != 1 || public["STEWARD_BROKER_LISTEN"] == "" {
		t.Fatalf("public service environment = %+v", public)
	}
	for _, key := range []string{"STEWARD_BROKER_CLIENT_KEY", "STEWARD_BROKER_CONTROL_KEY", "STEWARD_BROKER_SIGNING_PRIVATE_KEY"} {
		if _, ok := public[key]; ok {
			t.Fatalf("%s leaked to public service environment", key)
		}
		if private[key] == "" {
			t.Fatalf("%s missing from private service environment", key)
		}
	}
}

func TestWindowsServiceConfigSupportsRestrictedLocalService(t *testing.T) {
	config := windowsServiceConfig(InstallOptions{
		DisplayName: "Steward", Description: "test",
		WindowsServiceAccount: "localservice", WindowsServiceSIDType: "restricted",
	})
	if config.ServiceStartName != `NT AUTHORITY\LocalService` {
		t.Fatalf("service account = %q", config.ServiceStartName)
	}
	if config.SidType != windows.SERVICE_SID_TYPE_RESTRICTED {
		t.Fatalf("SID type = %d, want restricted", config.SidType)
	}
}

func TestRequirePathWithinRejectsSiblingPrefix(t *testing.T) {
	if err := requirePathWithin(`C:\Program Files Evil\Broker`, `C:\Program Files`, "install dir"); err == nil {
		t.Fatal("expected sibling path to be rejected")
	}
	if err := requirePathWithin(`C:\Program Files\MongoJSON\Broker`, `C:\Program Files`, "install dir"); err != nil {
		t.Fatal(err)
	}
}
