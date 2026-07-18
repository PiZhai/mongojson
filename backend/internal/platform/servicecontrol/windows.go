//go:build windows

package servicecontrol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func runPlatform(name string, run func(context.Context) error) error {
	isService, err := svc.IsWindowsService()
	if err != nil {
		return err
	}
	if !isService {
		return runWithSignals(run)
	}
	return svc.Run(name, windowsService{run: run})
}

type windowsService struct {
	run func(context.Context) error
}

func (s windowsService) Execute(_ []string, requests <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
	changes <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- s.run(ctx)
	}()

	changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-errCh:
			if err != nil {
				return false, 1
			}
			return false, 0
		case req := <-requests:
			switch req.Cmd {
			case svc.Interrogate:
				changes <- req.CurrentStatus
			case svc.Stop, svc.Shutdown:
				changes <- svc.Status{State: svc.StopPending}
				cancel()
				err := <-errCh
				if err != nil {
					return false, 1
				}
				return false, 0
			default:
				changes <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
			}
		}
	}
}

func installPlatform(ctx context.Context, input InstallOptions) (Result, error) {
	options, err := NormalizeInstallOptions(input)
	if err != nil {
		return Result{}, err
	}
	sourceBinary := options.BinaryPath
	env := Environment(options)
	serviceEnv := env
	if options.WindowsHardened {
		if options.Scope != ScopeSystem || options.InstallDir == "" || options.PrivateEnvironmentFile == "" {
			return Result{}, fmt.Errorf("Windows service hardening requires system scope, install dir, and private environment file")
		}
		serviceEnv, _ = splitPrivateEnvironment(env)
		if options.InstallDir != "" {
			options.BinaryPath = filepath.Join(options.InstallDir, filepath.Base(options.BinaryPath))
		}
		if !options.DryRun {
			options, serviceEnv, err = stageHardenedWindowsService(options, sourceBinary, env)
			if err != nil {
				return Result{}, err
			}
		}
	}
	args := serviceRunArgs(options)
	result := Result{
		Platform:    runtime.GOOS,
		Name:        options.Name,
		Scope:       options.Scope,
		Environment: redactedEnvironment(serviceEnv),
		Commands: []string{
			commandString("sc.exe", "create", options.Name, "binPath=", commandString(options.BinaryPath, args...), "start=", "auto", "DisplayName=", options.DisplayName),
			"registry Environment=" + fmt.Sprintf("%q", redactedEnvList(serviceEnv)),
		},
	}
	if options.WindowsHardened {
		result.Files = append(result.Files, options.BinaryPath, options.PrivateEnvironmentFile)
		result.Commands = append(result.Commands,
			"configure protected DACL/owner on service install and data paths",
			"configure service account "+options.WindowsServiceAccount,
			"configure "+options.WindowsServiceSIDType+" per-service SID")
	}
	if options.DryRun {
		result.Message = "dry run: Windows service would be created"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	manager, err := mgr.Connect()
	if err != nil {
		return Result{}, fmt.Errorf("connect service manager: %w", err)
	}
	defer manager.Disconnect()
	config := windowsServiceConfig(options)
	service, err := manager.CreateService(options.Name, options.BinaryPath, config, args...)
	if err != nil {
		return Result{}, fmt.Errorf("create Windows service: %w", err)
	}
	defer service.Close()
	if err := setWindowsServiceEnv(options.Name, serviceEnv); err != nil {
		rollbackErr := service.Delete()
		if rollbackErr != nil {
			rollbackErr = fmt.Errorf("rollback Windows service after environment failure: %w", rollbackErr)
		}
		return Result{}, errors.Join(err, rollbackErr)
	}
	if options.WindowsHardened {
		if err := protectWindowsServicePaths(options); err != nil {
			rollbackErr := service.Delete()
			return Result{}, errors.Join(err, rollbackErr)
		}
	}
	result.Message = "Windows service installed"
	return result, nil
}

func windowsServiceConfig(options InstallOptions) mgr.Config {
	serviceStartName := "LocalSystem"
	if options.WindowsServiceAccount == "localservice" {
		serviceStartName = `NT AUTHORITY\LocalService`
	}
	sidType := uint32(windows.SERVICE_SID_TYPE_UNRESTRICTED)
	if options.WindowsServiceSIDType == "restricted" {
		sidType = windows.SERVICE_SID_TYPE_RESTRICTED
	}
	return mgr.Config{
		DisplayName: options.DisplayName, Description: options.Description,
		StartType: mgr.StartAutomatic, ServiceStartName: serviceStartName, SidType: sidType,
	}
}

// stageHardenedWindowsService places the executable and secrets outside
// user-writable locations before SCM is configured. ACLs are initially limited
// to SYSTEM and Administrators; the service SID is added after CreateService.
func stageHardenedWindowsService(options InstallOptions, source string, env map[string]string) (InstallOptions, map[string]string, error) {
	if options.Scope != ScopeSystem {
		return options, nil, fmt.Errorf("Windows service hardening requires system scope")
	}
	if options.InstallDir == "" || options.PrivateEnvironmentFile == "" {
		return options, nil, fmt.Errorf("Windows service hardening requires install dir and private environment file")
	}
	programFiles := os.Getenv("ProgramFiles")
	programData := os.Getenv("ProgramData")
	if err := requirePathWithin(options.InstallDir, programFiles, "install dir"); err != nil {
		return options, nil, err
	}
	if err := requirePathWithin(options.PrivateEnvironmentFile, programData, "private environment file"); err != nil {
		return options, nil, err
	}
	if err := rejectWindowsReparsePath(options.InstallDir, programFiles); err != nil {
		return options, nil, err
	}
	if err := rejectWindowsReparsePath(options.PrivateEnvironmentFile, programData); err != nil {
		return options, nil, err
	}
	if err := os.MkdirAll(options.InstallDir, 0o700); err != nil {
		return options, nil, fmt.Errorf("create protected install dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(options.PrivateEnvironmentFile), 0o700); err != nil {
		return options, nil, fmt.Errorf("create protected data dir: %w", err)
	}
	for path := range options.WindowsPathAccess {
		if strings.EqualFold(path, options.PrivateEnvironmentFile) || strings.EqualFold(path, options.BinaryPath) {
			continue
		}
		if err := requirePathWithinEither(path, programFiles, programData, "protected service path"); err != nil {
			return options, nil, err
		}
		if err := os.MkdirAll(path, 0o700); err != nil {
			return options, nil, fmt.Errorf("create protected service path %s: %w", path, err)
		}
	}
	if err := rejectWindowsReparsePath(options.InstallDir, programFiles); err != nil {
		return options, nil, err
	}
	if err := rejectWindowsReparsePath(options.PrivateEnvironmentFile, programData); err != nil {
		return options, nil, err
	}
	if err := setProtectedWindowsACL(options.InstallDir, ""); err != nil {
		return options, nil, fmt.Errorf("protect install dir: %w", err)
	}
	if err := setProtectedWindowsACL(filepath.Dir(options.PrivateEnvironmentFile), ""); err != nil {
		return options, nil, fmt.Errorf("protect data dir: %w", err)
	}
	for destination, copySource := range options.ProtectedFileCopies {
		if err := requirePathWithin(destination, programData, "protected file destination"); err != nil {
			return options, nil, err
		}
		if err := rejectWindowsReparsePath(destination, programData); err != nil {
			return options, nil, err
		}
		content, err := os.ReadFile(copySource)
		if err != nil {
			return options, nil, fmt.Errorf("read protected source %s: %w", copySource, err)
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
			return options, nil, fmt.Errorf("create protected file directory: %w", err)
		}
		if err := writePrivateFileAtomic(destination, content); err != nil {
			return options, nil, fmt.Errorf("stage protected file %s: %w", destination, err)
		}
		options.ProtectedPaths = append(options.ProtectedPaths, destination)
	}
	target := filepath.Join(options.InstallDir, filepath.Base(source))
	content, err := os.ReadFile(source)
	if err != nil {
		return options, nil, fmt.Errorf("read broker executable: %w", err)
	}
	if err := writePrivateFileAtomic(target, content); err != nil {
		return options, nil, fmt.Errorf("stage broker executable: %w", err)
	}
	options.BinaryPath = target
	publicEnv, privateEnv := splitPrivateEnvironment(env)
	encoded, err := json.Marshal(privateEnv)
	if err != nil {
		return options, nil, fmt.Errorf("encode private service environment: %w", err)
	}
	if err := writePrivateFileAtomic(options.PrivateEnvironmentFile, encoded); err != nil {
		return options, nil, fmt.Errorf("write private service environment: %w", err)
	}
	return options, publicEnv, nil
}

func rejectWindowsReparsePath(path, root string) error {
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if err := requirePathWithin(path, root, "protected path"); err != nil {
		return err
	}
	for current := path; ; current = filepath.Dir(current) {
		attributes, err := windows.GetFileAttributes(windows.StringToUTF16Ptr(current))
		if err == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return fmt.Errorf("protected path %q contains a reparse point at %q", path, current)
		}
		if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return fmt.Errorf("inspect protected path %q: %w", current, err)
		}
		if strings.EqualFold(current, root) {
			break
		}
		parent := filepath.Dir(current)
		if parent == current {
			return fmt.Errorf("protected path %q escaped root %q", path, root)
		}
	}
	return nil
}

func splitPrivateEnvironment(env map[string]string) (map[string]string, map[string]string) {
	publicEnv := make(map[string]string, len(env))
	privateEnv := map[string]string{}
	for key, value := range env {
		if isSensitiveEnvKey(key) {
			privateEnv[key] = value
		} else {
			publicEnv[key] = value
		}
	}
	return publicEnv, privateEnv
}

func writePrivateFileAtomic(path string, content []byte) error {
	tempFile, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temp := tempFile.Name()
	defer os.Remove(temp)
	if _, err := tempFile.Write(content); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Sync(); err != nil {
		_ = tempFile.Close()
		return err
	}
	if err := tempFile.Close(); err != nil {
		return err
	}
	if err := setProtectedWindowsACL(temp, ""); err != nil {
		return err
	}
	from, err := windows.UTF16PtrFromString(temp)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(from, to, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return err
	}
	return setProtectedWindowsACL(path, "")
}

func protectWindowsServicePaths(options InstallOptions) error {
	paths := []string{options.InstallDir, options.BinaryPath}
	if options.PrivateEnvironmentFile != "" {
		paths = append(paths, filepath.Dir(options.PrivateEnvironmentFile), options.PrivateEnvironmentFile)
	}
	paths = append(paths, options.ProtectedPaths...)
	serviceSID := ""
	if options.WindowsServiceAccount != "localsystem" || len(options.WindowsPathAccess) > 0 {
		var err error
		serviceSID, err = lookupWindowsServiceSID(options.Name)
		if err != nil {
			return err
		}
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("stat protected path %s: %w", path, err)
		}
		access := options.WindowsPathAccess[filepath.Clean(path)]
		if access == "" && serviceSID != "" {
			access = "read"
		}
		if err := setProtectedWindowsACLWithAccess(path, serviceSID, access); err != nil {
			return fmt.Errorf("protect %s: %w", path, err)
		}
	}
	return nil
}

func protectNamedWindowsServicePaths(name string, paths []string) error {
	serviceSID, err := lookupWindowsServiceSID(name)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("stat protected path %s: %w", path, err)
		}
		// Broker trust-domain files deliberately omit a LocalSystem ACE. The
		// Broker service accesses them through its per-service SID; restricted
		// capability children keep LocalSystem only for OS initialization and have
		// the Broker service SID disabled.
		if err := setExclusiveServiceWindowsACL(path, serviceSID); err != nil {
			return fmt.Errorf("protect %s: %w", path, err)
		}
	}
	return nil
}

func setExclusiveServiceWindowsACL(path string, serviceSID string) error {
	flags := ""
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		flags = "OICI"
	}
	sddl := "O:BAG:SYD:P(A;" + flags + ";FA;;;BA)(A;" + flags + ";FA;;;" + serviceSID + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
}

func protectServicePathsPlatform(name string, paths []string) error {
	return protectNamedWindowsServicePaths(name, paths)
}

func setProtectedWindowsACL(path string, serviceSID string) error {
	return setProtectedWindowsACLWithAccess(path, serviceSID, "full")
}

func setProtectedWindowsACLWithAccess(path string, serviceSID string, access string) error {
	flags := ""
	if info, err := os.Stat(path); err == nil && info.IsDir() {
		flags = "OICI"
	}
	sddl := "O:BAG:SYD:P(A;" + flags + ";FA;;;SY)(A;" + flags + ";FA;;;BA)"
	if serviceSID != "" {
		rights := "FA"
		if access == "read" {
			rights = "GRGX"
		}
		sddl += "(A;" + flags + ";" + rights + ";;;" + serviceSID + ")"
	}
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return err
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return err
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT,
		windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		owner, nil, dacl, nil)
}

func lookupWindowsServiceSID(name string) (string, error) {
	sid, _, _, err := windows.LookupSID("", `NT SERVICE\`+strings.TrimSpace(name))
	if err != nil {
		return "", fmt.Errorf("resolve Windows service SID for %s: %w", name, err)
	}
	return sid.String(), nil
}

func requirePathWithin(path, root, label string) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("cannot resolve Windows root for %s", label)
	}
	absPath, _ := filepath.Abs(path)
	absRoot, _ := filepath.Abs(root)
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, `..\`) || filepath.IsAbs(rel) {
		return fmt.Errorf("%s must be under %s", label, absRoot)
	}
	return nil
}

func requirePathWithinEither(path, firstRoot, secondRoot, label string) error {
	if err := requirePathWithin(path, firstRoot, label); err == nil {
		return nil
	}
	if err := requirePathWithin(path, secondRoot, label); err == nil {
		return nil
	}
	return fmt.Errorf("%s must be under %s or %s", label, firstRoot, secondRoot)
}

func envPatchPlatform(ctx context.Context, input EnvPatchOptions) (Result, error) {
	options, err := NormalizeEnvPatchOptions(input)
	if err != nil {
		return Result{}, err
	}
	current, err := getWindowsServiceEnv(options.Name)
	if err != nil {
		return Result{}, err
	}
	privatePath, err := getWindowsServicePrivateEnvironmentFile(options.Name)
	if err != nil {
		return Result{}, err
	}
	privateCurrent := map[string]string{}
	if privatePath != "" {
		privateCurrent, err = ReadPrivateEnvironmentFile(privatePath)
		if err != nil {
			return Result{}, err
		}
		for key, value := range privateCurrent {
			current[key] = value
		}
	}
	next, err := buildEnvPatchTarget(current, options)
	if err != nil {
		return Result{}, err
	}
	publicNext, privateNext := splitPrivateEnvironment(next)
	if privatePath == "" && len(privateNext) > 0 {
		return Result{}, fmt.Errorf("service is not configured with --private-environment-file; refusing to store sensitive values in SCM registry")
	}
	result := Result{
		Platform:    runtime.GOOS,
		Name:        options.Name,
		Scope:       options.Scope,
		Environment: redactedEnvironment(next),
		Commands: []string{
			"registry Environment=" + fmt.Sprintf("%q", redactedEnvList(publicNext)),
			commandString("sc.exe", "stop", options.Name),
			commandString("sc.exe", "start", options.Name),
		},
	}
	if options.DryRun {
		result.Message = "dry run: Windows service environment would be updated; restart the service for changes to take effect"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	if privatePath != "" {
		serviceSID, sidErr := lookupWindowsServiceSID(options.Name)
		if sidErr != nil {
			return Result{}, sidErr
		}
		encoded, encodeErr := json.Marshal(privateNext)
		if encodeErr != nil {
			return Result{}, encodeErr
		}
		if err := writePrivateFileAtomic(privatePath, encoded); err != nil {
			return Result{}, fmt.Errorf("update private service environment: %w", err)
		}
		if err := setProtectedWindowsACLWithAccess(privatePath, serviceSID, "read"); err != nil {
			if previous, rollbackEncodeErr := json.Marshal(privateCurrent); rollbackEncodeErr == nil {
				_ = writePrivateFileAtomic(privatePath, previous)
				_ = setProtectedWindowsACLWithAccess(privatePath, serviceSID, "read")
			}
			return Result{}, fmt.Errorf("restore private environment ACL; previous private environment was restored: %w", err)
		}
	}
	if err := setWindowsServiceEnv(options.Name, publicNext); err != nil {
		if privatePath != "" {
			if encoded, rollbackEncodeErr := json.Marshal(privateCurrent); rollbackEncodeErr == nil {
				_ = writePrivateFileAtomic(privatePath, encoded)
				if serviceSID, sidErr := lookupWindowsServiceSID(options.Name); sidErr == nil {
					_ = setProtectedWindowsACLWithAccess(privatePath, serviceSID, "read")
				}
			}
		}
		return Result{}, err
	}
	result.Message = "Windows service environment updated; restart the service for changes to take effect"
	return result, nil
}

func getWindowsServicePrivateEnvironmentFile(name string) (string, error) {
	service, manager, err := openWindowsService(name)
	if err != nil {
		return "", err
	}
	defer manager.Disconnect()
	defer service.Close()
	config, err := service.Config()
	if err != nil {
		return "", fmt.Errorf("read Windows service configuration: %w", err)
	}
	args, err := windows.DecomposeCommandLine(config.BinaryPathName)
	if err != nil {
		return "", fmt.Errorf("parse Windows service command line: %w", err)
	}
	for index := 0; index+1 < len(args); index++ {
		if args[index] == "--private-environment-file" {
			return filepath.Clean(args[index+1]), nil
		}
	}
	return "", nil
}

func uninstallPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Platform: runtime.GOOS,
		Name:     name,
		Scope:    scope,
		Commands: []string{
			commandString("sc.exe", "delete", name),
		},
	}
	if dryRun {
		result.Message = "dry run: Windows service would be deleted"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return Result{}, err
	}
	defer manager.Disconnect()
	defer service.Close()
	if err := service.Delete(); err != nil {
		return Result{}, fmt.Errorf("delete Windows service: %w", err)
	}
	result.Message = "Windows service deleted"
	return result, nil
}

func startPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{Platform: runtime.GOOS, Name: name, Scope: scope, Commands: []string{commandString("sc.exe", "start", name)}}
	if dryRun {
		result.Message = "dry run: Windows service would be started"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return Result{}, err
	}
	defer manager.Disconnect()
	defer service.Close()
	if err := service.Start(); err != nil {
		return Result{}, fmt.Errorf("start Windows service: %w", err)
	}
	result.Message = "Windows service start requested"
	return result, nil
}

func stopPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{Platform: runtime.GOOS, Name: name, Scope: scope, Commands: []string{commandString("sc.exe", "stop", name)}}
	if dryRun {
		result.Message = "dry run: Windows service would be stopped"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return Result{}, err
	}
	defer manager.Disconnect()
	defer service.Close()
	if _, err := service.Control(svc.Stop); err != nil {
		return Result{}, fmt.Errorf("stop Windows service: %w", err)
	}
	result.Message = "Windows service stop requested"
	return result, nil
}

func restartPlatform(ctx context.Context, name string, scope string, dryRun bool) (Result, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return Result{}, err
	}
	result := Result{
		Platform: runtime.GOOS,
		Name:     name,
		Scope:    scope,
		Commands: []string{
			commandString("sc.exe", "stop", name),
			commandString("sc.exe", "start", name),
		},
	}
	if dryRun {
		result.Message = "dry run: Windows service would be restarted"
		return result, nil
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return Result{}, err
	}
	defer manager.Disconnect()
	defer service.Close()

	status, err := service.Query()
	if err != nil {
		return Result{}, fmt.Errorf("query Windows service before restart: %w", err)
	}
	if status.State != svc.Stopped {
		if _, err := service.Control(svc.Stop); err != nil {
			return Result{}, fmt.Errorf("stop Windows service before restart: %w", err)
		}
		if err := waitWindowsServiceState(ctx, service, svc.Stopped); err != nil {
			return Result{}, err
		}
	}
	if err := service.Start(); err != nil {
		return Result{}, fmt.Errorf("start Windows service after restart: %w", err)
	}
	result.Message = "Windows service restart requested"
	return result, nil
}

func waitWindowsServiceState(ctx context.Context, service *mgr.Service, want svc.State) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		status, err := service.Query()
		if err != nil {
			return fmt.Errorf("query Windows service state: %w", err)
		}
		if status.State == want {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func statusPlatform(ctx context.Context, name string, scope string) (StatusResult, error) {
	name, scope, err := normalizeServiceActionTarget(runtime.GOOS, name, scope)
	if err != nil {
		return StatusResult{}, err
	}
	select {
	case <-ctx.Done():
		return StatusResult{}, ctx.Err()
	default:
	}
	service, manager, err := openWindowsService(name)
	if err != nil {
		return StatusResult{}, err
	}
	defer manager.Disconnect()
	defer service.Close()
	status, err := service.Query()
	if err != nil {
		return StatusResult{}, fmt.Errorf("query Windows service: %w", err)
	}
	return StatusResult{Platform: runtime.GOOS, Name: name, Scope: scope, Status: windowsServiceState(status.State), Detail: fmt.Sprintf("pid=%d", status.ProcessId)}, nil
}

func openWindowsService(name string) (*mgr.Service, *mgr.Mgr, error) {
	manager, err := mgr.Connect()
	if err != nil {
		return nil, nil, fmt.Errorf("connect service manager: %w", err)
	}
	service, err := manager.OpenService(name)
	if err != nil {
		manager.Disconnect()
		return nil, nil, fmt.Errorf("open Windows service: %w", err)
	}
	return service, manager, nil
}

func setWindowsServiceEnv(name string, env map[string]string) error {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("open service registry key: %w", err)
	}
	defer key.Close()
	if err := key.SetStringsValue("Environment", envList(env)); err != nil {
		return fmt.Errorf("set service environment: %w", err)
	}
	return nil
}

func getWindowsServiceEnv(name string) (map[string]string, error) {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\`+name, registry.QUERY_VALUE)
	if err != nil {
		return nil, fmt.Errorf("open service registry key: %w", err)
	}
	defer key.Close()
	values, _, err := key.GetStringsValue("Environment")
	if err != nil {
		if err == syscall.ERROR_FILE_NOT_FOUND {
			return map[string]string{}, nil
		}
		return nil, fmt.Errorf("read service environment: %w", err)
	}
	return parseEnvList(values), nil
}

func windowsServiceState(state svc.State) string {
	switch state {
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "start_pending"
	case svc.StopPending:
		return "stop_pending"
	case svc.Running:
		return "running"
	case svc.ContinuePending:
		return "continue_pending"
	case svc.PausePending:
		return "pause_pending"
	case svc.Paused:
		return "paused"
	default:
		return fmt.Sprintf("unknown_%d", state)
	}
}

func waitBriefly() {
	time.Sleep(500 * time.Millisecond)
}
