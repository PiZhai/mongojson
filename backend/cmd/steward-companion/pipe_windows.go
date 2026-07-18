//go:build windows

package main

import (
	"fmt"
	"net"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func companionPipeListener(path string, serviceName string) (net.Listener, error) {
	descriptor, err := companionPipeSecurityDescriptor(serviceName)
	if err != nil {
		return nil, err
	}
	return winio.ListenPipe(path, &winio.PipeConfig{
		SecurityDescriptor: descriptor,
		MessageMode:        false,
		InputBufferSize:    64 << 10,
		OutputBufferSize:   64 << 10,
	})
}

func companionPipeSecurityDescriptor(serviceName string) (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("resolve companion user SID: %w", err)
	}
	serviceSID, _, _, err := windows.LookupSID("", `NT SERVICE\`+strings.TrimSpace(serviceName))
	if err != nil {
		return "", fmt.Errorf("resolve Steward service SID %s: %w", serviceName, err)
	}
	userSID := user.User.Sid.String()
	return companionPipeSDDL(userSID, serviceSID.String()), nil
}

func companionPipeSDDL(userSID, serviceSID string) string {
	return "O:" + userSID + "G:SYD:P" +
		"(A;;GA;;;" + userSID + ")" +
		"(A;;GA;;;SY)" +
		"(A;;GA;;;" + serviceSID + ")"
}
