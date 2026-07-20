//go:build windows

package servicecontrol

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc/mgr"
)

type recoveryConfigurerStub struct {
	actions        []mgr.RecoveryAction
	resetPeriod    uint32
	nonCrash       bool
	actionsErr     error
	nonCrashErr    error
	nonCrashCalled bool
}

func (s *recoveryConfigurerStub) SetRecoveryActions(actions []mgr.RecoveryAction, resetPeriod uint32) error {
	s.actions = actions
	s.resetPeriod = resetPeriod
	return s.actionsErr
}

func (s *recoveryConfigurerStub) SetRecoveryActionsOnNonCrashFailures(value bool) error {
	s.nonCrash = value
	s.nonCrashCalled = true
	return s.nonCrashErr
}

func TestWindowsInstallRejectsSensitiveSCMEnvironment(t *testing.T) {
	_, err := installPlatform(context.Background(), InstallOptions{
		Name:        "MongojsonStewardUnsafeTest",
		Scope:       ScopeSystem,
		BinaryPath:  os.Args[0],
		WorkDir:     ".",
		HTTPAddr:    "127.0.0.1:18080",
		DatabaseURL: "postgres://user:password@127.0.0.1:5432/database",
		StorageDir:  ".",
		DryRun:      true,
	})
	if err == nil || !strings.Contains(err.Error(), "protected private environment") {
		t.Fatalf("expected sensitive SCM environment rejection, got %v", err)
	}
}

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
		WindowsHardened: true, WindowsServiceAccount: "localservice", WindowsServiceSIDType: "restricted",
	})
	if config.ServiceStartName != `NT AUTHORITY\LocalService` {
		t.Fatalf("service account = %q", config.ServiceStartName)
	}
	if config.SidType != windows.SERVICE_SID_TYPE_RESTRICTED {
		t.Fatalf("SID type = %d, want restricted", config.SidType)
	}
	if !config.DelayedAutoStart {
		t.Fatal("hardened LocalService must use delayed automatic start")
	}
	for name, options := range map[string]InstallOptions{
		"broker":       {WindowsHardened: true, WindowsServiceAccount: "localsystem"},
		"non-hardened": {WindowsServiceAccount: "localservice"},
	} {
		if windowsServiceConfig(options).DelayedAutoStart {
			t.Fatalf("%s must not use delayed automatic start", name)
		}
	}
}

func TestHardenedWindowsServiceRecoveryPolicy(t *testing.T) {
	for _, test := range []struct {
		name    string
		account string
		delays  []time.Duration
	}{
		{name: "main", account: "localservice", delays: []time.Duration{15 * time.Second, 30 * time.Second, 60 * time.Second}},
		{name: "broker", account: "localsystem", delays: []time.Duration{5 * time.Second, 15 * time.Second, 30 * time.Second}},
	} {
		t.Run(test.name, func(t *testing.T) {
			actions := hardenedWindowsServiceRecoveryActions(InstallOptions{WindowsServiceAccount: test.account})
			if len(actions) != len(test.delays) {
				t.Fatalf("recovery action count = %d, want %d", len(actions), len(test.delays))
			}
			for index, action := range actions {
				if action.Type != mgr.ServiceRestart || action.Delay != test.delays[index] {
					t.Fatalf("recovery action %d = %+v, want restart after %s", index, action, test.delays[index])
				}
			}
		})
	}
	if hardenedWindowsServiceRecoveryResetSeconds != 86400 {
		t.Fatalf("recovery reset period = %d, want 86400", hardenedWindowsServiceRecoveryResetSeconds)
	}
}

func TestConfigureHardenedWindowsServiceRecovery(t *testing.T) {
	stub := &recoveryConfigurerStub{}
	if err := configureHardenedWindowsServiceRecovery(stub, InstallOptions{WindowsServiceAccount: "localservice"}); err != nil {
		t.Fatal(err)
	}
	if stub.resetPeriod != 86400 || len(stub.actions) != 3 || !stub.nonCrashCalled || !stub.nonCrash {
		t.Fatalf("recovery configuration = %+v", stub)
	}

	actionsFailure := &recoveryConfigurerStub{actionsErr: errors.New("actions")}
	if err := configureHardenedWindowsServiceRecovery(actionsFailure, InstallOptions{}); err == nil || actionsFailure.nonCrashCalled {
		t.Fatalf("expected recovery actions failure before non-crash flag, got %v", err)
	}
	nonCrashFailure := &recoveryConfigurerStub{nonCrashErr: errors.New("non-crash")}
	if err := configureHardenedWindowsServiceRecovery(nonCrashFailure, InstallOptions{}); err == nil || !nonCrashFailure.nonCrashCalled {
		t.Fatalf("expected non-crash recovery failure, got %v", err)
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
