//go:build !windows

package steward

func newRuntimeWindowsPathEnsureTool() RuntimeTool { return nil }
