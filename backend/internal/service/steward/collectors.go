package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const maxWatchedDirectoryEntries = 1000

func normalizeCollectorSettings(name string, input map[string]any) (map[string]any, error) {
	result := map[string]any{}
	switch name {
	case "watched-directory":
		paths, err := collectorPaths(input["paths"])
		if err != nil {
			return nil, err
		}
		if len(paths) > 8 {
			return nil, fmt.Errorf("watched-directory permits at most 8 paths")
		}
		for _, path := range paths {
			if err := validateWatchedDirectory(path); err != nil {
				return nil, err
			}
		}
		depth := collectorInt(input["max_depth"], 1)
		if depth < 0 || depth > 3 {
			return nil, fmt.Errorf("watched-directory max_depth must be between 0 and 3")
		}
		result["paths"] = paths
		result["max_depth"] = depth
	case "screenpipe-bridge", "activitywatch-bridge":
		endpoint := strings.TrimSpace(fmt.Sprint(input["endpoint"]))
		if endpoint == "" {
			if name == "screenpipe-bridge" {
				endpoint = "http://127.0.0.1:3030"
			} else {
				endpoint = "http://127.0.0.1:5600"
			}
		}
		if err := validateLocalAdapterEndpoint(endpoint); err != nil {
			return nil, err
		}
		result["endpoint"] = strings.TrimRight(endpoint, "/")
		result["limit"] = collectorInt(input["limit"], 100)
		if result["limit"].(int) < 1 || result["limit"].(int) > 500 {
			return nil, fmt.Errorf("%s limit must be between 1 and 500", name)
		}
		if name == "screenpipe-bridge" {
			pinnedVersion := strings.TrimSpace(fmt.Sprint(input["pinned_version"]))
			if pinnedVersion == "" {
				return nil, fmt.Errorf("screenpipe-bridge requires a pinned_version release or commit")
			}
			if collectorBool(input["keyboard_content"], false) {
				return nil, fmt.Errorf("screenpipe keyboard content collection is permanently disabled")
			}
			result["pinned_version"] = pinnedVersion
			result["keyboard_content"] = false
		}
	case "system-status", "manual-input", "browser-link", "clipboard-summary":
		return result, nil
	default:
		return nil, fmt.Errorf("unsupported collector %q", name)
	}
	return result, nil
}

func (s *Service) RunEnabledCollectors(ctx context.Context) error {
	collectors, err := s.ListCollectors(ctx)
	if err != nil {
		return err
	}
	errorsFound := []string{}
	for _, collector := range collectors {
		if !collector.Enabled {
			continue
		}
		var runErr error
		switch collector.Name {
		case "system-status":
			runErr = s.collectSystemStatus(ctx)
		case "watched-directory":
			runErr = s.collectWatchedDirectories(ctx, collector.Settings)
		case "screenpipe-bridge":
			runErr = s.collectScreenpipe(ctx, collector.Settings)
		case "activitywatch-bridge":
			runErr = s.collectActivityWatch(ctx, collector.Settings)
		default:
			continue
		}
		if updateErr := s.recordCollectorRun(ctx, collector.Name, runErr); updateErr != nil {
			runErr = updateErr
		}
		if runErr != nil {
			errorsFound = append(errorsFound, collector.Name+": "+runErr.Error())
		}
	}
	if len(errorsFound) > 0 {
		return fmt.Errorf("collector run completed with errors: %s", strings.Join(errorsFound, "; "))
	}
	return nil
}

func (s *Service) collectSystemStatus(ctx context.Context) error {
	hostname, _ := os.Hostname()
	value := strings.Join([]string{hostname, runtime.GOOS, runtime.GOARCH, runtime.Version(), strconv.Itoa(runtime.NumCPU())}, "|")
	changed, err := s.observeCollectorValue(ctx, "system-status", "local-runtime", value, time.Now().UTC())
	if err != nil || !changed {
		return err
	}
	_, err = s.CreateObservation(ctx, CreateObservationInput{
		Type: "system_status", ContextKey: "system-status",
		Summary: fmt.Sprintf("设备 %s，平台 %s/%s，CPU %d 核，运行时 %s。", defaultString(hostname, "local-device"), runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version()),
		Source:  "collector:system-status", DataLevel: DataD2, PermissionLevel: PermissionA1,
		Payload:     map[string]any{"platform": runtime.GOOS, "architecture": runtime.GOARCH, "cpu_count": runtime.NumCPU(), "runtime": runtime.Version()},
		EntityHints: []ObservationEntityHint{{Type: "device", CanonicalKey: s.agentIDValue(), DisplayName: defaultString(hostname, "local-device")}},
	})
	return err
}

func (s *Service) collectWatchedDirectories(ctx context.Context, settings map[string]any) error {
	paths, err := collectorPaths(settings["paths"])
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no watched directories configured")
	}
	depth := collectorInt(settings["max_depth"], 1)
	startedAt := time.Now().UTC()
	changedNames := []string{}
	changedCount := 0
	for _, root := range paths {
		if err := validateWatchedDirectory(root); err != nil {
			return err
		}
		entries, scanErr := scanDirectoryMetadata(root, depth, maxWatchedDirectoryEntries)
		if scanErr != nil {
			return scanErr
		}
		for _, entry := range entries {
			key := filepath.Clean(root) + "|" + entry.RelativePath
			changed, observeErr := s.observeCollectorValue(ctx, "watched-directory", key, entry.Fingerprint, startedAt)
			if observeErr != nil {
				return observeErr
			}
			if changed {
				changedCount++
				if len(changedNames) < 12 {
					changedNames = append(changedNames, filepath.Base(root)+"/"+entry.RelativePath)
				}
			}
		}
	}
	deletedRows, err := s.db.Pool.Query(ctx, `
		delete from steward_collector_observations
		where collector_name = 'watched-directory' and last_seen_at < $1
		returning observation_key
	`, startedAt)
	if err != nil {
		return err
	}
	deletedCount := 0
	for deletedRows.Next() {
		var key string
		if err := deletedRows.Scan(&key); err != nil {
			deletedRows.Close()
			return err
		}
		deletedCount++
		if len(changedNames) < 12 {
			parts := strings.SplitN(key, "|", 2)
			if len(parts) == 2 {
				changedNames = append(changedNames, filepath.Base(parts[0])+"/"+parts[1]+" (deleted)")
			}
		}
	}
	deletedRows.Close()
	if changedCount == 0 && deletedCount == 0 {
		return nil
	}
	sort.Strings(changedNames)
	_, err = s.CreateObservation(ctx, CreateObservationInput{
		Type: "directory_metadata_change", ContextKey: strings.Join(paths, ";"),
		Summary: fmt.Sprintf("新增或修改 %d 项，删除 %d 项。样例：%s", changedCount, deletedCount, strings.Join(changedNames, "；")),
		Source:  "collector:watched-directory", DataLevel: DataD2, PermissionLevel: PermissionA1,
		Payload: map[string]any{"changed_count": changedCount, "deleted_count": deletedCount},
	})
	return err
}

type directoryMetadata struct {
	RelativePath string
	Fingerprint  string
}

func scanDirectoryMetadata(root string, maxDepth, limit int) ([]directoryMetadata, error) {
	root = filepath.Clean(root)
	items := make([]directoryMetadata, 0)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		depth := strings.Count(filepath.ToSlash(relative), "/")
		if entry.IsDir() {
			if depth >= maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		if len(items) >= limit {
			return fmt.Errorf("watched directory %s exceeds %d entries", root, limit)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		fingerprint := fmt.Sprintf("%d|%d|%s", info.Size(), info.ModTime().UTC().UnixNano(), info.Mode().String())
		items = append(items, directoryMetadata{RelativePath: filepath.ToSlash(relative), Fingerprint: fingerprint})
		return nil
	})
	return items, err
}

func (s *Service) observeCollectorValue(ctx context.Context, collector, key, value string, seenAt time.Time) (bool, error) {
	hash := sha256.Sum256([]byte(value))
	fingerprint := hex.EncodeToString(hash[:])
	var current string
	err := s.db.Pool.QueryRow(ctx, `select fingerprint from steward_collector_observations where collector_name = $1 and observation_key = $2`, collector, key).Scan(&current)
	if err != nil && err != pgx.ErrNoRows {
		return false, err
	}
	changed := err == pgx.ErrNoRows || current != fingerprint
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_collector_observations (collector_name, observation_key, fingerprint, last_seen_at)
		values ($1,$2,$3,$4)
		on conflict (collector_name, observation_key) do update set fingerprint = excluded.fingerprint, last_seen_at = excluded.last_seen_at
	`, collector, key, fingerprint, seenAt)
	return changed, err
}

func (s *Service) recordCollectorRun(ctx context.Context, name string, runErr error) error {
	now := time.Now().UTC()
	var errorText *string
	if runErr != nil {
		value := truncateAdvisorText(runErr.Error(), 500)
		errorText = &value
	}
	_, err := s.db.Pool.Exec(ctx, `update steward_collector_configs set last_run_at = $1, last_error = $2, updated_at = $1 where name = $3`, now, errorText, name)
	return err
}

func collectorPaths(value any) ([]string, error) {
	var raw []string
	switch typed := value.(type) {
	case nil:
		return []string{}, nil
	case string:
		for _, item := range strings.FieldsFunc(typed, func(r rune) bool { return r == ';' || r == '\n' }) {
			raw = append(raw, item)
		}
	case []string:
		raw = append(raw, typed...)
	case []any:
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("watched-directory paths must contain strings")
			}
			raw = append(raw, text)
		}
	default:
		return nil, fmt.Errorf("watched-directory paths must be an array or semicolon-separated string")
	}
	result := make([]string, 0, len(raw))
	seen := map[string]bool{}
	for _, item := range raw {
		path := filepath.Clean(strings.TrimSpace(item))
		if path == "." || path == "" || seen[strings.ToLower(path)] {
			continue
		}
		seen[strings.ToLower(path)] = true
		result = append(result, path)
	}
	return result, nil
}

func validateWatchedDirectory(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("watched-directory path must be absolute: %s", path)
	}
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	root := string(filepath.Separator)
	if volume != "" {
		root = volume + string(filepath.Separator)
	}
	if strings.EqualFold(clean, root) {
		return fmt.Errorf("watched-directory cannot monitor a filesystem root: %s", clean)
	}
	info, err := os.Stat(clean)
	if err != nil {
		return fmt.Errorf("watched-directory path unavailable %s: %w", clean, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("watched-directory path is not a directory: %s", clean)
	}
	return nil
}

func collectorInt(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return fallback
}

func collectorBool(value any, fallback bool) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		if parsed, err := strconv.ParseBool(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return fallback
}
