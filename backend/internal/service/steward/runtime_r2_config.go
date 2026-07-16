package steward

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func splitRuntimeCSV(value string) []string {
	value = strings.ReplaceAll(value, "\r", "\n")
	var items []string
	for _, line := range strings.Split(value, "\n") {
		for _, item := range strings.Split(line, ",") {
			if item = strings.TrimSpace(item); item != "" {
				items = append(items, item)
			}
		}
	}
	return items
}

func runtimeAllowedRootsFromEnv(storageDir string) []string {
	roots := splitRuntimeCSV(os.Getenv("STEWARD_RUNTIME_ALLOWED_ROOTS"))
	return normalizeRuntimeAllowedRoots(roots, storageDir)
}

func normalizeRuntimeAllowedRoots(roots []string, storageDir string) []string {
	if len(roots) == 0 {
		roots = []string{storageDir}
	}
	seen := map[string]bool{}
	result := []string{}
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root == "" {
			continue
		}
		absolute, err := filepath.Abs(root)
		if err != nil {
			continue
		}
		absolute = filepath.Clean(absolute)
		if resolved, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = filepath.Clean(resolved)
		}
		key := strings.ToLower(absolute)
		if !seen[key] {
			seen[key] = true
			result = append(result, absolute)
		}
	}
	return result
}

func resolveRuntimeExecutables(values []string) map[string]string {
	result := map[string]string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		resolved := value
		if !filepath.IsAbs(resolved) {
			path, err := exec.LookPath(resolved)
			if err != nil {
				continue
			}
			resolved = path
		}
		absolute, err := filepath.Abs(resolved)
		if err != nil {
			continue
		}
		absolute = filepath.Clean(absolute)
		if evaluated, err := filepath.EvalSymlinks(absolute); err == nil {
			absolute = filepath.Clean(evaluated)
		}
		result[strings.ToLower(absolute)] = absolute
	}
	return result
}

func runtimeHostSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value != "" {
			result[value] = true
		}
	}
	return result
}

func (s *Service) registerRuntimeR2Tools() {
	if s == nil || s.runtimeTools == nil {
		return
	}
	s.runtimeTools.registerIfAbsent(newRuntimeListDirectoryTool(s))
	s.runtimeTools.registerIfAbsent(newRuntimeReadTextTool(s))
	s.runtimeTools.registerIfAbsent(newRuntimeCreateTextTool(s))
	s.runtimeTools.registerIfAbsent(newRuntimeShellExecTool(s))
	s.runtimeTools.registerIfAbsent(newRuntimeWebFetchTool(s))
	if s.runtimeBrowserOpen {
		s.runtimeTools.registerIfAbsent(newRuntimeBrowserOpenTool(s))
	}
}
