package steward

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mongojson/backend/internal/domain"
)

const runtimeMaxTextBytes = 1 << 20

type runtimeListDirectoryTool struct{ service *Service }
type runtimeReadTextTool struct{ service *Service }
type runtimeCreateTextTool struct{ service *Service }

func newRuntimeListDirectoryTool(service *Service) RuntimeTool {
	return runtimeListDirectoryTool{service: service}
}
func newRuntimeReadTextTool(service *Service) RuntimeTool {
	return runtimeReadTextTool{service: service}
}
func newRuntimeCreateTextTool(service *Service) RuntimeTool {
	return runtimeCreateTextTool{service: service}
}

func (runtimeListDirectoryTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "fs.list", Version: "2.0.0", Description: "List one allowlisted local directory without recursion.",
		InputSchema:     map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "max_entries": map[string]any{"type": "integer"}}},
		OutputSchema:    map[string]any{"type": "object", "required": []string{"path", "entries", "count"}},
		PermissionLevel: PermissionA1, RiskLevel: "low", SideEffect: RuntimeSideEffectNone,
		ApprovalMode: RuntimeApprovalNever, IdempotencyMode: RuntimeIdempotencyInherent,
		Deterministic: false, SupportsCancel: true, DefaultTimeoutSec: 15,
	}
}

func (t runtimeListDirectoryTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "path", "max_entries"); err != nil {
		return err
	}
	path, err := runtimeRequiredString(input, "path")
	if err != nil {
		return err
	}
	maxEntries, err := runtimeInt(input, "max_entries", 200)
	if err != nil || maxEntries < 1 || maxEntries > 2000 {
		return fmt.Errorf("max_entries must be between 1 and 2000")
	}
	resolved, err := t.service.resolveRuntimePath(path, true)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("path must be an existing directory")
	}
	return nil
}

func (t runtimeListDirectoryTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	path, _ := runtimeRequiredString(input, "path")
	maxEntries, _ := runtimeInt(input, "max_entries", 200)
	resolved, _ := t.service.resolveRuntimePath(path, true)
	entries, err := os.ReadDir(resolved)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })
	truncated := len(entries) > maxEntries
	if truncated {
		entries = entries[:maxEntries]
	}
	items := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		select {
		case <-ctx.Done():
			return RuntimeToolResult{}, ctx.Err()
		default:
		}
		item := map[string]any{"name": entry.Name(), "is_dir": entry.IsDir(), "type": entry.Type().String()}
		if info, infoErr := entry.Info(); infoErr == nil {
			item["size_bytes"] = info.Size()
			item["modified_at"] = info.ModTime().UTC().Format(time.RFC3339Nano)
		}
		items = append(items, item)
	}
	output := map[string]any{"path": resolved, "entries": items, "count": len(items), "truncated": truncated}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "directory_listing", Summary: fmt.Sprintf("listed %d entries", len(items)), Payload: map[string]any{"path": resolved, "count": len(items), "truncated": truncated}}}}, nil
}

func (t runtimeListDirectoryTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	if _, ok := output["entries"]; !ok {
		return fmt.Errorf("directory listing output is missing entries")
	}
	return runtimeOutputMatchesExpected(output, expected)
}

func (runtimeReadTextTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "fs.read_text", Version: "2.0.0", Description: "Read one UTF-8-compatible text file under an allowlisted root.",
		InputSchema:     map[string]any{"type": "object", "required": []string{"path"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "max_bytes": map[string]any{"type": "integer"}}},
		OutputSchema:    map[string]any{"type": "object", "required": []string{"path", "content", "sha256", "bytes"}},
		PermissionLevel: PermissionA1, RiskLevel: "low", SideEffect: RuntimeSideEffectNone,
		ApprovalMode: RuntimeApprovalNever, IdempotencyMode: RuntimeIdempotencyInherent,
		Deterministic: false, SupportsCancel: true, DefaultTimeoutSec: 15,
	}
}

func (t runtimeReadTextTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "path", "max_bytes"); err != nil {
		return err
	}
	path, err := runtimeRequiredString(input, "path")
	if err != nil {
		return err
	}
	maxBytes, err := runtimeInt(input, "max_bytes", 256<<10)
	if err != nil || maxBytes < 1 || maxBytes > runtimeMaxTextBytes {
		return fmt.Errorf("max_bytes must be between 1 and %d", runtimeMaxTextBytes)
	}
	resolved, err := t.service.resolveRuntimePath(path, true)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("path must be an existing regular file")
	}
	return nil
}

func (t runtimeReadTextTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	path, _ := runtimeRequiredString(input, "path")
	maxBytes, _ := runtimeInt(input, "max_bytes", 256<<10)
	resolved, _ := t.service.resolveRuntimePath(path, true)
	file, err := os.Open(resolved)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(maxBytes)+1))
	if err != nil {
		return RuntimeToolResult{}, err
	}
	if len(data) > maxBytes {
		return RuntimeToolResult{}, fmt.Errorf("file exceeds max_bytes=%d", maxBytes)
	}
	if strings.IndexByte(string(data), 0) >= 0 {
		return RuntimeToolResult{}, fmt.Errorf("file appears to be binary")
	}
	select {
	case <-ctx.Done():
		return RuntimeToolResult{}, ctx.Err()
	default:
	}
	digest := sha256.Sum256(data)
	info, _ := file.Stat()
	output := map[string]any{"path": resolved, "content": string(data), "bytes": len(data), "sha256": hex.EncodeToString(digest[:])}
	if info != nil {
		output["modified_at"] = info.ModTime().UTC().Format(time.RFC3339Nano)
	}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "file_read", Summary: "read text file and captured content hash", Payload: map[string]any{"path": resolved, "bytes": len(data), "sha256": output["sha256"]}}}}, nil
}

func (t runtimeReadTextTool) Verify(_ context.Context, _ map[string]any, output map[string]any, expected map[string]any) error {
	content, ok := output["content"].(string)
	if !ok {
		return fmt.Errorf("read output is missing content")
	}
	digest := sha256.Sum256([]byte(content))
	if output["sha256"] != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("read output hash does not match content")
	}
	return runtimeOutputMatchesExpected(output, expected)
}

func (runtimeCreateTextTool) Spec() domain.StewardToolSpec {
	return domain.StewardToolSpec{
		Name: "fs.create_text", Version: "2.0.0", Description: "Atomically create a new text file under an allowlisted root; existing different content is never overwritten.",
		InputSchema:     map[string]any{"type": "object", "required": []string{"path", "content"}, "properties": map[string]any{"path": map[string]any{"type": "string"}, "content": map[string]any{"type": "string"}, "create_parents": map[string]any{"type": "boolean"}}},
		OutputSchema:    map[string]any{"type": "object", "required": []string{"path", "sha256", "bytes", "created", "reconciled"}},
		PermissionLevel: PermissionA2, RiskLevel: "low", SideEffect: RuntimeSideEffectWrite,
		ApprovalMode: RuntimeApprovalAlways, IdempotencyMode: RuntimeIdempotencyKeyed,
		Deterministic: true, SupportsCancel: true, DefaultTimeoutSec: 20,
	}
}

func (t runtimeCreateTextTool) Validate(input map[string]any) error {
	if err := runtimeRejectUnknownFields(input, "path", "content", "create_parents"); err != nil {
		return err
	}
	path, err := runtimeRequiredString(input, "path")
	if err != nil {
		return err
	}
	contentValue, exists := input["content"]
	content, ok := contentValue.(string)
	if !exists || !ok {
		return fmt.Errorf("content must be a string")
	}
	if len([]byte(content)) > runtimeMaxTextBytes {
		return fmt.Errorf("content exceeds %d bytes", runtimeMaxTextBytes)
	}
	if _, err := runtimeBool(input, "create_parents", false); err != nil {
		return err
	}
	resolved, err := t.service.resolveRuntimePath(path, false)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(resolved); statErr == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("existing path is not a regular file")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	return nil
}

func (t runtimeCreateTextTool) Execute(ctx context.Context, input map[string]any) (RuntimeToolResult, error) {
	if err := t.Validate(input); err != nil {
		return RuntimeToolResult{}, err
	}
	path, _ := runtimeRequiredString(input, "path")
	content := input["content"].(string)
	createParents, _ := runtimeBool(input, "create_parents", false)
	resolved, err := t.service.resolveRuntimePath(path, false)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	data := []byte(content)
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	if existing, err := os.ReadFile(resolved); err == nil {
		existingDigest := sha256.Sum256(existing)
		if hex.EncodeToString(existingDigest[:]) != hash {
			return RuntimeToolResult{}, fmt.Errorf("target already exists with different content; R2 never overwrites files")
		}
		output := map[string]any{"path": resolved, "sha256": hash, "bytes": len(data), "created": false, "reconciled": true}
		return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "file_reconciled", Summary: "existing file already matched requested content", Payload: output}}}, nil
	} else if !os.IsNotExist(err) {
		return RuntimeToolResult{}, err
	}
	parent := filepath.Dir(resolved)
	if createParents {
		if err := os.MkdirAll(parent, 0o700); err != nil {
			return RuntimeToolResult{}, err
		}
	}
	// Re-resolve after parent creation so a pre-existing or concurrently
	// introduced directory symlink cannot silently redirect this operation.
	resolved, err = t.service.resolveRuntimePath(path, false)
	if err != nil {
		return RuntimeToolResult{}, err
	}
	parent = filepath.Dir(resolved)
	if _, err := os.Stat(parent); err != nil {
		return RuntimeToolResult{}, fmt.Errorf("parent directory does not exist; set create_parents=true: %w", err)
	}
	select {
	case <-ctx.Done():
		return RuntimeToolResult{}, ctx.Err()
	default:
	}
	temporary, err := os.CreateTemp(parent, ".steward-create-*")
	if err != nil {
		return RuntimeToolResult{}, err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return RuntimeToolResult{}, err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return RuntimeToolResult{}, err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return RuntimeToolResult{}, err
	}
	if err := temporary.Close(); err != nil {
		return RuntimeToolResult{}, err
	}
	// A hard link publishes the fully flushed temporary file atomically and
	// fails if the destination appeared after preflight, so R2 never overwrites.
	if err := os.Link(temporaryPath, resolved); err != nil {
		if existing, readErr := os.ReadFile(resolved); readErr == nil {
			existingDigest := sha256.Sum256(existing)
			if hex.EncodeToString(existingDigest[:]) == hash {
				output := map[string]any{"path": resolved, "sha256": hash, "bytes": len(data), "created": false, "reconciled": true}
				return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "file_reconciled", Summary: "concurrent file creation already produced the requested content", Payload: output}}}, nil
			}
		}
		return RuntimeToolResult{}, fmt.Errorf("atomically create target: %w", err)
	}
	output := map[string]any{"path": resolved, "sha256": hash, "bytes": len(data), "created": true, "reconciled": false}
	return RuntimeToolResult{Output: output, Evidence: []RuntimeEvidence{{Kind: "file_created", Summary: "atomically created text file", Payload: output}}}, nil
}

func (t runtimeCreateTextTool) Verify(_ context.Context, input map[string]any, output map[string]any, expected map[string]any) error {
	path, _ := runtimeRequiredString(input, "path")
	resolved, err := t.service.resolveRuntimePath(path, true)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(resolved)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	if output["sha256"] != hex.EncodeToString(digest[:]) {
		return fmt.Errorf("created file hash does not match execution output")
	}
	return runtimeOutputMatchesExpected(output, expected)
}

func (s *Service) resolveRuntimePath(rawPath string, mustExist bool) (string, error) {
	if s == nil || len(s.runtimeAllowedRoots) == 0 {
		return "", fmt.Errorf("%w: no allowed roots are configured", ErrRuntimePathDenied)
	}
	absolute, err := filepath.Abs(strings.TrimSpace(rawPath))
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	for _, root := range s.runtimeAllowedRoots {
		if !runtimePathWithin(root, absolute) {
			continue
		}
		probe := absolute
		if !mustExist {
			probe = filepath.Dir(absolute)
			for {
				if _, statErr := os.Lstat(probe); statErr == nil {
					break
				}
				parent := filepath.Dir(probe)
				if parent == probe {
					break
				}
				probe = parent
			}
		}
		resolvedProbe, evalErr := filepath.EvalSymlinks(probe)
		if evalErr != nil {
			if mustExist {
				continue
			}
			resolvedProbe = probe
		}
		if !runtimePathWithin(root, filepath.Clean(resolvedProbe)) {
			continue
		}
		if mustExist {
			resolvedTarget, evalErr := filepath.EvalSymlinks(absolute)
			if evalErr != nil || !runtimePathWithin(root, filepath.Clean(resolvedTarget)) {
				continue
			}
			return filepath.Clean(resolvedTarget), nil
		}
		return absolute, nil
	}
	return "", fmt.Errorf("%w: %s", ErrRuntimePathDenied, absolute)
}

func runtimePathWithin(root string, target string) bool {
	relative, err := filepath.Rel(filepath.Clean(root), filepath.Clean(target))
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) && !filepath.IsAbs(relative))
}
