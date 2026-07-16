//go:build windows

package privilegebroker

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const capabilityWriteMask = windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA |
	windows.FILE_WRITE_ATTRIBUTES | windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER |
	windows.GENERIC_WRITE | windows.GENERIC_ALL

func validateCapabilityPathSecurity(executable, workingDirectory string) error {
	trustedRoots := []string{os.Getenv("WINDIR"), os.Getenv("ProgramFiles")}
	for _, path := range []string{executable, workingDirectory} {
		root, ok := containedTrustedRoot(path, trustedRoots)
		if !ok {
			return fmt.Errorf("capability path %q must be under Windows or Program Files protected roots", path)
		}
		for current := filepath.Clean(path); ; current = filepath.Dir(current) {
			if err := validateProtectedWindowsPathComponent(current); err != nil {
				return err
			}
			if strings.EqualFold(current, root) {
				break
			}
			parent := filepath.Dir(current)
			if parent == current {
				return fmt.Errorf("capability path %q escaped its trusted root", path)
			}
		}
	}
	return nil
}

func containedTrustedRoot(path string, roots []string) (string, bool) {
	path = filepath.Clean(path)
	for _, candidate := range roots {
		candidate = filepath.Clean(strings.TrimSpace(candidate))
		if candidate == "." || candidate == "" {
			continue
		}
		relative, err := filepath.Rel(candidate, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return candidate, true
		}
	}
	return "", false
}

func validateProtectedWindowsPathComponent(path string) error {
	attributes, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(path))
	if err != nil {
		return fmt.Errorf("read capability path attributes %q: %w", path, err)
	}
	if attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("capability path %q contains a reparse point", path)
	}
	descriptor, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("read capability path security %q: %w", path, err)
	}
	owner, _, err := descriptor.Owner()
	if err != nil || !trustedCapabilityOwner(owner) {
		return fmt.Errorf("capability path %q owner is not SYSTEM, Administrators, or a protected service identity", path)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		return fmt.Errorf("capability path %q must have an explicit DACL", path)
	}
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			return fmt.Errorf("read capability path ACE %q: %w", path, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || uint32(ace.Mask)&capabilityWriteMask == 0 {
			continue
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !trustedCapabilityOwner(sid) {
			return fmt.Errorf("capability path %q grants write access to untrusted SID %s", path, sid.String())
		}
	}
	return nil
}

func trustedCapabilityOwner(sid *windows.SID) bool {
	if sid == nil {
		return false
	}
	if sid.IsWellKnown(windows.WinLocalSystemSid) || sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) {
		return true
	}
	if sid.IsWellKnown(windows.WinCreatorOwnerSid) || sid.IsWellKnown(windows.WinCreatorOwnerRightsSid) {
		return true
	}
	// S-1-5-80 covers Windows service identities, including TrustedInstaller.
	return strings.HasPrefix(sid.String(), "S-1-5-80-")
}
