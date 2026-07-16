package privilegebroker

// Go test executables live in user-writable temporary build directories and
// intentionally violate the production path policy. Individual platform tests
// call validateCapabilityPathSecurity directly when exercising that policy.
func init() {
	capabilityPathSecurityValidator = func(string, string) error { return nil }
}
