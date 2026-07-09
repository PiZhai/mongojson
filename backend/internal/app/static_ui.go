package app

import (
	"fmt"
	"net/http"
	"os"
	urlpath "path"
	"path/filepath"
	"strings"
)

func withStaticWorkspace(api http.Handler, uiDir string) (http.Handler, error) {
	uiDir = strings.TrimSpace(uiDir)
	if uiDir == "" {
		return api, nil
	}
	root, err := filepath.Abs(uiDir)
	if err != nil {
		return nil, fmt.Errorf("resolve STEWARD_UI_DIR: %w", err)
	}
	indexPath := filepath.Join(root, "index.html")
	info, err := os.Stat(indexPath)
	if err != nil {
		return nil, fmt.Errorf("STEWARD_UI_DIR must contain index.html: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("STEWARD_UI_DIR index.html is a directory")
	}

	fileServer := http.FileServer(http.Dir(root))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if routeToManagementAPI(r) || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			api.ServeHTTP(w, r)
			return
		}

		requestPath := cleanRequestPath(r.URL.Path)
		filePath, ok := workspaceFilePath(root, requestPath)
		if ok {
			if stat, statErr := os.Stat(filePath); statErr == nil && !stat.IsDir() {
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		if filepath.Ext(requestPath) != "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, indexPath)
	}), nil
}

func routeToManagementAPI(r *http.Request) bool {
	if r == nil || r.URL == nil {
		return true
	}
	requestPath := cleanRequestPath(r.URL.Path)
	return requestPath == "/api" ||
		strings.HasPrefix(requestPath, "/api/") ||
		requestPath == "/healthz" ||
		requestPath == "/readyz"
}

func cleanRequestPath(value string) string {
	return urlpath.Clean("/" + strings.TrimSpace(value))
}

func workspaceFilePath(root string, requestPath string) (string, bool) {
	relative := strings.TrimPrefix(cleanRequestPath(requestPath), "/")
	if relative == "" {
		return "", false
	}
	target := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." {
		return "", false
	}
	return target, true
}
