//go:build windows

package privilegebroker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRunBrokerCommandStartsRestrictedProcessInsideJob(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestPrivilegeBrokerHelper", "--", "broker-child")
	command.Env = brokerEnvironment()
	var stdout bytes.Buffer
	command.Stdout = &stdout
	result, err := runBrokerCommand(context.Background(), command)
	if err != nil {
		t.Fatal(err)
	}
	if result.exitCode != 0 {
		t.Fatalf("restricted child exit code = %d", result.exitCode)
	}
	if !strings.Contains(stdout.String(), "broker-helper-ok") {
		t.Fatalf("restricted child output = %q", stdout.String())
	}
}

func TestConfigureBrokerProcessUsesRestrictedSuspendedToken(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=^$")
	cleanup, err := configureBrokerProcess(command)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if command.SysProcAttr == nil || command.SysProcAttr.Token == 0 {
		t.Fatal("broker process did not receive a restricted primary token")
	}
	want := uint32(windows.CREATE_SUSPENDED | windows.CREATE_NEW_PROCESS_GROUP)
	if command.SysProcAttr.CreationFlags&want != want {
		t.Fatalf("creation flags = %#x, want suspended process group %#x", command.SysProcAttr.CreationFlags, want)
	}
}

func TestCapabilityTokenDoesNotDisableLocalSystemAndUsesRestrictingSIDs(t *testing.T) {
	disabled, err := disabledCapabilitySIDs(windows.GetCurrentProcessToken())
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range disabled {
		if item.Sid.IsWellKnown(windows.WinLocalSystemSid) {
			t.Fatal("LocalSystem must remain enabled so the Windows loader can initialize the child")
		}
	}
	restricting, err := capabilityRestrictingSIDs(windows.GetCurrentProcessToken(), capabilityTokenProfileDefault)
	if err != nil {
		t.Fatal(err)
	}
	var users, restrictedCode, localSystem bool
	for _, item := range restricting {
		users = users || item.Sid.IsWellKnown(windows.WinBuiltinUsersSid)
		restrictedCode = restrictedCode || item.Sid.IsWellKnown(windows.WinRestrictedCodeSid)
		localSystem = localSystem || item.Sid.IsWellKnown(windows.WinLocalSystemSid)
	}
	groups, err := windows.GetCurrentProcessToken().GetTokenGroups()
	if err != nil {
		t.Fatal(err)
	}
	logon := false
	for _, group := range groups.AllGroups() {
		if group.Attributes&windows.SE_GROUP_LOGON_ID != windows.SE_GROUP_LOGON_ID {
			continue
		}
		for _, item := range restricting {
			logon = logon || item.Sid.Equals(group.Sid)
		}
	}
	if !users || !restrictedCode || !localSystem || !logon {
		t.Fatalf("restricting SIDs = %#v, want Users, Restricted Code, LocalSystem and source Logon SID", restricting)
	}
}

func TestProductionCapabilityTokenUsesPrivilegeAndIdentityReductionWithoutRestrictingSIDs(t *testing.T) {
	restricting, err := capabilityRestrictingSIDs(windows.GetCurrentProcessToken(), capabilityTokenProfileProduction)
	if err != nil {
		t.Fatal(err)
	}
	if len(restricting) != 0 {
		t.Fatalf("production restricting SIDs = %#v, want none for CLR compatibility", restricting)
	}
	disabled, err := disabledCapabilitySIDs(windows.GetCurrentProcessToken())
	if err != nil {
		t.Fatal(err)
	}
	if len(disabled) == 0 || !disabled[0].Sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		t.Fatalf("production disabled SIDs = %#v, want Administrators and service identities", disabled)
	}
}

func TestBrokerEnvironmentIgnoresAmbientWindowsPaths(t *testing.T) {
	t.Setenv("PATH", `C:\attacker\bin`)
	t.Setenv("PATHEXT", ".PWN")
	t.Setenv("TEMP", `C:\attacker\temp`)
	t.Setenv("HOME", `C:\attacker\home`)
	t.Setenv("USERPROFILE", `C:\attacker\profile`)

	environment := strings.Join(brokerEnvironment(), "\n")
	for _, forbidden := range []string{"PATH=", "PATHEXT=", "HOME=", "USERPROFILE=", `C:\attacker`} {
		if strings.Contains(strings.ToUpper(environment), strings.ToUpper(forbidden)) {
			t.Fatalf("broker environment inherited %q: %s", forbidden, environment)
		}
	}
	root, err := windows.GetSystemWindowsDirectory()
	if err != nil || root == "" {
		root = `C:\Windows`
	}
	if !strings.Contains(strings.ToUpper(environment), "SYSTEMROOT="+strings.ToUpper(root)) {
		t.Fatalf("broker environment omitted OS-derived SYSTEMROOT: %s", environment)
	}
	trustedTemp := strings.ToUpper(filepath.Join(root, "Temp"))
	for _, key := range []string{"TEMP=", "TMP="} {
		if !strings.Contains(strings.ToUpper(environment), key+trustedTemp) {
			t.Fatalf("broker environment omitted trusted %s: %s", key, environment)
		}
	}
}
