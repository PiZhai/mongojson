//go:build windows

package privilegebroker

// Atomic replacement is performed only inside the protected NTFS directory.
// The temporary file itself is flushed before MoveFileEx-backed os.Rename.
// Windows does not expose portable directory fsync through os.File.
func syncParentDirectory(string) error { return nil }
