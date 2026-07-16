package steward

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if len(roots) == 0 {
		roots = append(roots, storageDir)
	}
	if boolEnv("STEWARD_RUNTIME_INCLUDE_KNOWN_FOLDERS", true) {
		for _, path := range runtimeKnownFolders() {
			roots = append(roots, path)
		}
	}
	return normalizeRuntimeAllowedRoots(roots, storageDir)
}

func runtimeKnownFolders() map[string]string {
	home, _ := os.UserHomeDir()
	candidates := map[string][]string{
		"home":      {home},
		"desktop":   {filepath.Join(home, "Desktop")},
		"downloads": {filepath.Join(home, "Downloads")},
		"documents": {filepath.Join(home, "Documents")},
		"pictures":  {filepath.Join(home, "Pictures")},
		"music":     {filepath.Join(home, "Music")},
		"videos":    {filepath.Join(home, "Videos")},
	}
	if runtime.GOOS == "windows" {
		for _, cloudRoot := range []string{os.Getenv("OneDrive"), os.Getenv("OneDriveConsumer"), os.Getenv("OneDriveCommercial")} {
			if strings.TrimSpace(cloudRoot) == "" {
				continue
			}
			candidates["desktop"] = append([]string{filepath.Join(cloudRoot, "Desktop")}, candidates["desktop"]...)
			candidates["documents"] = append([]string{filepath.Join(cloudRoot, "Documents")}, candidates["documents"]...)
			candidates["pictures"] = append([]string{filepath.Join(cloudRoot, "Pictures")}, candidates["pictures"]...)
		}
	}
	result := map[string]string{}
	for name, paths := range candidates {
		for _, path := range paths {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			if info, err := os.Stat(path); err == nil && info.IsDir() {
				result[name] = filepath.Clean(path)
				break
			}
		}
	}
	return result
}

func expandRuntimeKnownFolder(rawPath string) string {
	value := strings.TrimSpace(rawPath)
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	normalized := strings.ReplaceAll(value, "\\", "/")
	parts := strings.SplitN(normalized, "/", 2)
	aliases := map[string]string{
		"~": "home", "home": "home", "用户目录": "home", "主目录": "home",
		"desktop": "desktop", "桌面": "desktop",
		"downloads": "downloads", "download": "downloads", "下载": "downloads", "下载目录": "downloads",
		"documents": "documents", "document": "documents", "文档": "documents",
		"pictures": "pictures", "图片": "pictures",
		"music": "music", "音乐": "music",
		"videos": "videos", "video": "videos", "视频": "videos",
	}
	key, ok := aliases[strings.ToLower(strings.TrimSpace(parts[0]))]
	if !ok {
		return value
	}
	base := runtimeKnownFolders()[key]
	if base == "" {
		return value
	}
	if len(parts) == 1 || strings.TrimSpace(parts[1]) == "" {
		return base
	}
	return filepath.Join(base, filepath.FromSlash(parts[1]))
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
	s.runtimeTools.registerIfAbsent(newRuntimeCreateDirectoryTool(s))
	s.runtimeTools.registerIfAbsent(newRuntimeCreateTextTool(s))
	s.runtimeTools.registerIfAbsent(newRuntimeShellExecTool(s))
	s.runtimeTools.registerIfAbsent(newRuntimeWebFetchTool(s))
	if s.runtimeBrowserOpen {
		s.runtimeTools.registerIfAbsent(newRuntimeBrowserOpenTool(s))
	}
}
