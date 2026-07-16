//go:build windows

package privilegebroker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestRunBrokerCommandStartsRestrictedProcessInsideJob(t *testing.T) {
	command := exec.Command(os.Args[0], "-test.run=TestPrivilegeBrokerHelper", "--", "broker-child")
	command.Env = brokerEnvironment()
	var stdout bytes.Buffer
	command.Stdout = &stdout
	if err := runBrokerCommand(context.Background(), command); err != nil {
		t.Fatal(err)
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

func TestBrokerEnvironmentIgnoresAmbientWindowsPaths(t *testing.T) {
	t.Setenv("PATH", `C:\attacker\bin`)
	t.Setenv("PATHEXT", ".PWN")
	t.Setenv("TEMP", `C:\attacker\temp`)
	t.Setenv("HOME", `C:\attacker\home`)
	t.Setenv("USERPROFILE", `C:\attacker\profile`)

	environment := strings.Join(brokerEnvironment(), "\n")
	for _, forbidden := range []string{"PATH=", "PATHEXT=", "TEMP=", "HOME=", "USERPROFILE=", `C:\attacker`} {
		if strings.Contains(strings.ToUpper(environment), strings.ToUpper(forbidden)) {
			t.Fatalf("broker environment inherited %q: %s", forbidden, environment)
		}
	}
	if !strings.Contains(strings.ToUpper(environment), "SYSTEMROOT=") {
		t.Fatalf("broker environment omitted OS-derived SYSTEMROOT: %s", environment)
	}
}
