package privilegebroker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const capabilityLaunchSelfTestMarker = "steward-capability-launch-ok"

type CapabilityLaunchSelfTestResult struct {
	OK       bool   `json:"ok"`
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output,omitempty"`
	Error    string `json:"error,omitempty"`
}

type CapabilityLaunchSelfTestSuite struct {
	OK    bool                                      `json:"ok"`
	Cases map[string]CapabilityLaunchSelfTestResult `json:"cases"`
}

// RunCapabilityLaunchSelfTest exercises the exact restricted-token, desktop,
// Job Object and stdio path used by real Broker capabilities. The executable
// must dispatch "session0-self-test-child" by printing the marker below.
func RunCapabilityLaunchSelfTest(ctx context.Context, executable string) CapabilityLaunchSelfTestResult {
	return runCapabilityLaunchSelfTestProfile(ctx, executable, capabilityTokenProfileProduction)
}

func RunCapabilityLaunchSelfTestSuite(ctx context.Context, executable string) CapabilityLaunchSelfTestSuite {
	return RunCapabilityLaunchSelfTestSuiteWithDeniedPath(ctx, executable, "")
}

func RunCapabilityLaunchSelfTestSuiteWithDeniedPath(ctx context.Context, executable, deniedPath string) CapabilityLaunchSelfTestSuite {
	profiles := []capabilityTokenProfile{capabilityTokenProfileProduction, capabilityTokenProfileDefault, capabilityTokenProfileSystem, capabilityTokenProfilePrivileges}
	suite := CapabilityLaunchSelfTestSuite{Cases: make(map[string]CapabilityLaunchSelfTestResult, len(profiles))}
	for _, profile := range profiles {
		suite.Cases[string(profile)] = runCapabilityLaunchSelfTestProfileWithDeniedPath(ctx, executable, deniedPath, profile)
	}
	suite.OK = suite.Cases[string(capabilityTokenProfileProduction)].OK
	return suite
}

func runCapabilityLaunchSelfTestProfile(ctx context.Context, executable string, profile capabilityTokenProfile) CapabilityLaunchSelfTestResult {
	return runCapabilityLaunchSelfTestProfileWithDeniedPath(ctx, executable, "", profile)
}

func runCapabilityLaunchSelfTestProfileWithDeniedPath(ctx context.Context, executable, deniedPath string, profile capabilityTokenProfile) CapabilityLaunchSelfTestResult {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := exec.Command(executable, "session0-self-test-child")
	if strings.TrimSpace(deniedPath) != "" {
		command.Args = append(command.Args, "--deny-read", deniedPath)
	}
	command.Env = brokerEnvironment()
	command.Stdout = &stdout
	command.Stderr = &stderr
	result, err := runBrokerCommandWithProfile(ctx, command, profile)
	output := strings.TrimSpace(stdout.String())
	response := CapabilityLaunchSelfTestResult{ExitCode: result.exitCode, Output: output}
	if err != nil {
		response.Error = err.Error()
		if value := strings.TrimSpace(stderr.String()); value != "" {
			response.Error = strings.TrimSpace(response.Error + "; stderr: " + value)
		}
		return response
	}
	if output != capabilityLaunchSelfTestMarker {
		response.Error = fmt.Sprintf("unexpected restricted child output %q", output)
		return response
	}
	response.OK = true
	return response
}

func CapabilityLaunchSelfTestChild(args []string) {
	temp, err := os.MkdirTemp("", "mongojson-steward-capability-selftest-")
	if err != nil {
		fmt.Printf("broker-temp-create-error:%v", err)
		return
	}
	if err := os.RemoveAll(temp); err != nil {
		fmt.Printf("broker-temp-cleanup-error:%v", err)
		return
	}
	if err := capabilityRuntimeLaunchSelfTest(); err != nil {
		fmt.Printf("broker-runtime-error:%v", err)
		return
	}
	if len(args) == 2 && args[0] == "--deny-read" {
		if _, err := os.ReadFile(args[1]); err == nil {
			fmt.Print("broker-secret-readable")
			return
		} else if !os.IsPermission(err) {
			fmt.Printf("broker-secret-probe-error:%v", err)
			return
		}
	}
	fmt.Print(capabilityLaunchSelfTestMarker)
}
