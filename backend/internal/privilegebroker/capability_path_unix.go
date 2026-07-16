//go:build !windows

package privilegebroker

import (
	"fmt"
	"os"
	"path/filepath"
)

func validateCapabilityPathSecurity(executable, workingDirectory string) error {
	for _, path := range []string{executable, workingDirectory} {
		for current := filepath.Clean(path); ; current = filepath.Dir(current) {
			info, err := os.Lstat(current)
			if err != nil {
				return fmt.Errorf("inspect capability path %q: %w", current, err)
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("capability path %q contains a symbolic link", current)
			}
			if info.Mode().Perm()&0o022 != 0 {
				return fmt.Errorf("capability path %q is group/other writable", current)
			}
			parent := filepath.Dir(current)
			if parent == current {
				break
			}
		}
	}
	return nil
}

func validateCredentialPathSecurity(path string) error {
	return validateCapabilityPathSecurity(path, filepath.Dir(path))
}
