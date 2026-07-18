//go:build windows

package privilegebroker

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessTree struct{ job windows.Handle }

const (
	disableMaxPrivilege  = 0x1
	processSuspendResume = 0x0800
)

var (
	advapi32                     = windows.NewLazySystemDLL("advapi32.dll")
	createRestrictedTokenProc    = advapi32.NewProc("CreateRestrictedToken")
	ntdll                        = windows.NewLazySystemDLL("ntdll.dll")
	ntResumeProcessProc          = ntdll.NewProc("NtResumeProcess")
	user32                       = windows.NewLazySystemDLL("user32.dll")
	createDesktopProc            = user32.NewProc("CreateDesktopW")
	closeDesktopProc             = user32.NewProc("CloseDesktop")
	createWindowStationProc      = user32.NewProc("CreateWindowStationW")
	closeWindowStationProc       = user32.NewProc("CloseWindowStation")
	getProcessWindowStationProc  = user32.NewProc("GetProcessWindowStation")
	getUserObjectInformationProc = user32.NewProc("GetUserObjectInformationW")
	setProcessWindowStationProc  = user32.NewProc("SetProcessWindowStation")
	capabilityDesktopSequence    atomic.Uint64
	windowStationCreationMu      sync.Mutex
)

const userObjectName = 2

const createWindowStationOnly = 0x0001

// configureBrokerProcess gives each capability a restricted primary token and
// creates it suspended. The production profile removes optional privileges and
// disables Administrators plus every service SID. Broker secrets grant access
// only to Administrators and the Broker Service SID (never LocalSystem), so the
// capability cannot read them. We deliberately do not add a restricting-SID
// pass to the production token: Windows PowerShell's CLR fails to initialize in
// Session 0 under that extra access-check pass with HRESULT 0x80070005.
func configureBrokerProcess(command *exec.Cmd) (func(), error) {
	restricted, err := createCapabilityToken(capabilityTokenProfileProduction)
	if err != nil {
		return nil, err
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED,
		Token:         syscall.Token(restricted),
	}
	return func() { _ = restricted.Close() }, nil
}

func createCapabilityToken(profile capabilityTokenProfile) (windows.Token, error) {
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY, &source); err != nil {
		return 0, fmt.Errorf("open broker process token: %w", err)
	}
	defer source.Close()
	disabled, err := disabledCapabilitySIDs(source)
	if err != nil {
		return 0, err
	}
	restricting, err := capabilityRestrictingSIDs(source, profile)
	if err != nil {
		return 0, err
	}
	var disabledPointer uintptr
	if len(disabled) > 0 {
		disabledPointer = uintptr(unsafe.Pointer(&disabled[0]))
	}
	var restrictingPointer uintptr
	if len(restricting) > 0 {
		restrictingPointer = uintptr(unsafe.Pointer(&restricting[0]))
	}

	var restricted windows.Token
	result, _, callErr := createRestrictedTokenProc.Call(
		uintptr(source), disableMaxPrivilege,
		uintptr(len(disabled)), disabledPointer, 0, 0,
		uintptr(len(restricting)), restrictingPointer,
		uintptr(unsafe.Pointer(&restricted)),
	)
	if result == 0 {
		return 0, fmt.Errorf("create restricted capability token: %w", callErr)
	}
	return restricted, nil
}

func capabilityRestrictingSIDs(source windows.Token, profile capabilityTokenProfile) ([]windows.SIDAndAttributes, error) {
	restricting, err := restrictingCapabilitySIDs(source)
	if err != nil {
		return nil, err
	}
	switch profile {
	case capabilityTokenProfileDefault, capabilityTokenProfileSystem:
		systemSID, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
		if err != nil {
			return nil, fmt.Errorf("resolve LocalSystem SID for capability restriction: %w", err)
		}
		return append(restricting, windows.SIDAndAttributes{Sid: systemSID}), nil
	case capabilityTokenProfileProduction, capabilityTokenProfilePrivileges:
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown capability token profile %q", profile)
	}
}

func disabledCapabilitySIDs(source windows.Token) ([]windows.SIDAndAttributes, error) {
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, fmt.Errorf("resolve Administrators SID for restricted capability: %w", err)
	}
	items := []windows.SIDAndAttributes{{Sid: adminSID}}
	groups, err := source.GetTokenGroups()
	if err != nil {
		return nil, fmt.Errorf("read Broker token groups for capability isolation: %w", err)
	}
	for _, group := range groups.AllGroups() {
		if strings.HasPrefix(group.Sid.String(), "S-1-5-80-") {
			// Service SIDs are the Broker's private file identity. Capability
			// children retain LocalSystem for Windows initialization but may not
			// use the Broker service SID to read keys, policy, state or audit.
			items = append(items, windows.SIDAndAttributes{Sid: group.Sid})
		}
	}
	return items, nil
}

func restrictingCapabilitySIDs(source windows.Token) ([]windows.SIDAndAttributes, error) {
	usersSID, err := windows.CreateWellKnownSid(windows.WinBuiltinUsersSid)
	if err != nil {
		return nil, fmt.Errorf("resolve Users SID for restricted capability: %w", err)
	}
	restrictedCodeSID, err := windows.CreateWellKnownSid(windows.WinRestrictedCodeSid)
	if err != nil {
		return nil, fmt.Errorf("resolve Restricted Code SID for capability host: %w", err)
	}
	items := []windows.SIDAndAttributes{{Sid: usersSID}, {Sid: restrictedCodeSID}}
	groups, err := source.GetTokenGroups()
	if err != nil {
		return nil, fmt.Errorf("read Broker token groups for capability restriction: %w", err)
	}
	for _, group := range groups.AllGroups() {
		if group.Attributes&windows.SE_GROUP_LOGON_ID == windows.SE_GROUP_LOGON_ID {
			// Session 0 process initialization needs access to objects granted to
			// the service logon SID (window station, desktop and related kernel
			// objects). This SID is per-logon and is not present on Broker secret
			// files, so adding it does not cross the trust boundary.
			items = append(items, windows.SIDAndAttributes{Sid: group.Sid})
		}
	}
	// Users permits loading ordinary Windows system DLLs. Restricted Code is
	// granted only to the immutable System Tool Host executable. Broker policy,
	// keys, state, audit and checkpoint paths grant none of these SIDs.
	return items, nil
}

func attachBrokerProcessTree(command *exec.Cmd) (processTreeGuard, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("create broker process job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("configure broker process job: %w", err)
	}
	process, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE|processSuspendResume, false, uint32(command.Process.Pid))
	if err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("open broker process for job assignment: %w", err)
	}
	defer windows.CloseHandle(process)
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("assign broker process tree to job: %w", err)
	}
	// os/exec closes the primary thread handle after CreateProcessAsUser. Resume
	// the suspended process only after Job assignment, eliminating the previous
	// Start -> Assign escape window (including children spawned at entry point).
	status, _, callErr := ntResumeProcessProc.Call(uintptr(process))
	if int32(status) < 0 {
		_ = windows.TerminateJobObject(job, 1)
		_ = windows.CloseHandle(job)
		return nil, fmt.Errorf("resume contained broker process (ntstatus %#x): %w", uint32(status), callErr)
	}
	return &windowsProcessTree{job: job}, nil
}

// brokerEnvironment is derived from trusted OS APIs, never from the service
// environment. PATH, PATHEXT, HOME and USERPROFILE are not inherited. TEMP and
// TMP point at the OS-owned system temp directory so immutable capability hosts
// can create their own random, short-lived working directories without falling
// back to the Windows directory root.
func brokerEnvironment() []string {
	root, err := windows.GetSystemWindowsDirectory()
	if err != nil || root == "" {
		root = `C:\Windows`
	}
	temp := filepath.Join(root, "Temp")
	return []string{"SYSTEMROOT=" + root, "WINDIR=" + root, "TEMP=" + temp, "TMP=" + temp}
}

func capabilityRuntimeLaunchSelfTest() error {
	root, err := windows.GetSystemWindowsDirectory()
	if err != nil || root == "" {
		root = `C:\Windows`
	}
	powerShell := filepath.Join(root, "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	command := exec.Command(powerShell, "-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `[Console]::Out.Write('steward-powershell-launch-ok')`)
	command.Env = brokerEnvironment()
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("launch trusted PowerShell runtime: %w; output=%q", err, output)
	}
	if string(output) != "steward-powershell-launch-ok" {
		return fmt.Errorf("trusted PowerShell runtime returned %q", output)
	}
	return nil
}

func (g *windowsProcessTree) Terminate() error {
	if g == nil || g.job == 0 {
		return nil
	}
	return windows.TerminateJobObject(g.job, 1)
}

func (g *windowsProcessTree) Close() error {
	if g == nil || g.job == 0 {
		return nil
	}
	err := windows.CloseHandle(g.job)
	g.job = 0
	return err
}

// runBrokerCommand deliberately bypasses os/exec.Start on Windows. The Go
// launcher does not expose STARTUPINFO.lpDesktop, but a service in Session 0
// must place a restricted child on a desktop whose ACL admits the restricting
// SID. Otherwise CreateProcessAsUser succeeds and the Windows loader exits
// before main with STATUS_DLL_INIT_FAILED (0xC0000142).
func runBrokerCommand(ctx context.Context, command *exec.Cmd) (brokerCommandResult, error) {
	return runBrokerCommandWithProfile(ctx, command, capabilityTokenProfileProduction)
}

func runBrokerCommandWithProfile(ctx context.Context, command *exec.Cmd, profile capabilityTokenProfile) (brokerCommandResult, error) {
	if err := ctx.Err(); err != nil {
		return brokerCommandResult{exitCode: -1}, err
	}
	token, err := createCapabilityToken(profile)
	if err != nil {
		return brokerCommandResult{exitCode: -1}, err
	}
	defer token.Close()

	desktop, err := createCapabilityDesktop()
	if err != nil {
		return brokerCommandResult{exitCode: -1}, err
	}
	defer desktop.close()

	stdio, err := prepareBrokerStdio(command)
	if err != nil {
		return brokerCommandResult{exitCode: -1}, err
	}
	defer stdio.closeAll()

	app, err := windows.UTF16PtrFromString(command.Path)
	if err != nil {
		return brokerCommandResult{exitCode: -1}, fmt.Errorf("encode capability executable: %w", err)
	}
	line, err := windows.UTF16PtrFromString(windowsCommandLine(command.Args))
	if err != nil {
		return brokerCommandResult{exitCode: -1}, fmt.Errorf("encode capability command line: %w", err)
	}
	environment, err := windowsEnvironmentBlock(command.Env)
	if err != nil {
		return brokerCommandResult{exitCode: -1}, fmt.Errorf("encode capability environment: %w", err)
	}
	var directory *uint16
	if command.Dir != "" {
		directory, err = windows.UTF16PtrFromString(command.Dir)
		if err != nil {
			return brokerCommandResult{exitCode: -1}, fmt.Errorf("encode capability working directory: %w", err)
		}
	}
	desktopPointer, err := windows.UTF16PtrFromString(desktop.fullName)
	if err != nil {
		return brokerCommandResult{exitCode: -1}, fmt.Errorf("encode capability desktop: %w", err)
	}
	startup := windows.StartupInfo{
		Cb:        uint32(unsafe.Sizeof(windows.StartupInfo{})),
		Desktop:   desktopPointer,
		Flags:     windows.STARTF_USESTDHANDLES,
		StdInput:  windows.Handle(stdio.stdinChild.Fd()),
		StdOutput: windows.Handle(stdio.stdoutChild.Fd()),
		StdErr:    windows.Handle(stdio.stderrChild.Fd()),
	}
	processInfo := windows.ProcessInformation{}
	flags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED | windows.CREATE_UNICODE_ENVIRONMENT)
	if err := windows.CreateProcessAsUser(token, app, line, nil, nil, true, flags,
		&environment[0], directory, &startup, &processInfo); err != nil {
		return brokerCommandResult{exitCode: -1}, fmt.Errorf("create restricted capability process on %s: %w", desktop.fullName, err)
	}
	defer windows.CloseHandle(processInfo.Process)
	defer windows.CloseHandle(processInfo.Thread)
	stdio.closeChildEnds()
	copyDone := stdio.startCopies(command)

	job, err := createBrokerJob(processInfo.Process)
	if err != nil {
		_ = windows.TerminateProcess(processInfo.Process, 1)
		waitForProcess(processInfo.Process)
		<-copyDone
		return brokerCommandResult{exitCode: -1}, err
	}
	defer windows.CloseHandle(job)
	if _, err := windows.ResumeThread(processInfo.Thread); err != nil {
		_ = windows.TerminateJobObject(job, 1)
		waitForProcess(processInfo.Process)
		<-copyDone
		return brokerCommandResult{exitCode: -1}, fmt.Errorf("resume contained capability process: %w", err)
	}

	waited := make(chan error, 1)
	go func() { waited <- waitForProcess(processInfo.Process) }()
	select {
	case waitErr := <-waited:
		<-copyDone
		var code uint32
		if err := windows.GetExitCodeProcess(processInfo.Process, &code); err != nil {
			return brokerCommandResult{exitCode: -1}, fmt.Errorf("read capability exit code: %w", err)
		}
		result := brokerCommandResult{exitCode: int(int32(code))}
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		if waitErr != nil {
			return result, waitErr
		}
		if code != 0 {
			return result, fmt.Errorf("capability exited with code %d", code)
		}
		return result, nil
	case <-ctx.Done():
		_ = windows.TerminateJobObject(job, 1)
		<-waited
		<-copyDone
		return brokerCommandResult{exitCode: -1}, ctx.Err()
	}
}

func createBrokerJob(process windows.Handle) (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create broker process job: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("configure broker process job: %w", err)
	}
	if err := windows.AssignProcessToJobObject(job, process); err != nil {
		windows.CloseHandle(job)
		return 0, fmt.Errorf("assign restricted capability to job: %w", err)
	}
	return job, nil
}

func waitForProcess(process windows.Handle) error {
	status, err := windows.WaitForSingleObject(process, windows.INFINITE)
	if err != nil {
		return err
	}
	if status != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("unexpected capability wait status %#x", status)
	}
	return nil
}

func windowsCommandLine(args []string) string {
	escaped := make([]string, len(args))
	for index, arg := range args {
		escaped[index] = syscall.EscapeArg(arg)
	}
	return strings.Join(escaped, " ")
}

func windowsEnvironmentBlock(environment []string) ([]uint16, error) {
	block := make([]uint16, 0, 256)
	for _, item := range environment {
		if strings.IndexByte(item, 0) >= 0 {
			return nil, fmt.Errorf("environment entry contains NUL")
		}
		block = append(block, utf16.Encode([]rune(item))...)
		block = append(block, 0)
	}
	block = append(block, 0)
	return block, nil
}

type brokerStdio struct {
	stdinParent, stdinChild   *os.File
	stdoutParent, stdoutChild *os.File
	stderrParent, stderrChild *os.File
}

func prepareBrokerStdio(command *exec.Cmd) (*brokerStdio, error) {
	stdio := &brokerStdio{}
	var err error
	if stdio.stdinChild, stdio.stdinParent, err = inheritableChildPipe(true); err != nil {
		return nil, fmt.Errorf("create capability stdin: %w", err)
	}
	if stdio.stdoutChild, stdio.stdoutParent, err = inheritableChildPipe(false); err != nil {
		stdio.closeAll()
		return nil, fmt.Errorf("create capability stdout: %w", err)
	}
	if stdio.stderrChild, stdio.stderrParent, err = inheritableChildPipe(false); err != nil {
		stdio.closeAll()
		return nil, fmt.Errorf("create capability stderr: %w", err)
	}
	return stdio, nil
}

func inheritableChildPipe(childReads bool) (*os.File, *os.File, error) {
	readFile, writeFile, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	child := writeFile
	parent := readFile
	if childReads {
		child, parent = readFile, writeFile
	}
	if err := windows.SetHandleInformation(windows.Handle(child.Fd()), windows.HANDLE_FLAG_INHERIT, windows.HANDLE_FLAG_INHERIT); err != nil {
		readFile.Close()
		writeFile.Close()
		return nil, nil, err
	}
	if err := windows.SetHandleInformation(windows.Handle(parent.Fd()), windows.HANDLE_FLAG_INHERIT, 0); err != nil {
		readFile.Close()
		writeFile.Close()
		return nil, nil, err
	}
	return child, parent, nil
}

func (stdio *brokerStdio) closeChildEnds() {
	stdio.stdinChild.Close()
	stdio.stdoutChild.Close()
	stdio.stderrChild.Close()
}

func (stdio *brokerStdio) startCopies(command *exec.Cmd) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		var copies sync.WaitGroup
		copies.Add(3)
		go func() {
			defer copies.Done()
			if command.Stdin != nil {
				_, _ = io.Copy(stdio.stdinParent, command.Stdin)
			}
			_ = stdio.stdinParent.Close()
		}()
		go func() {
			defer copies.Done()
			if command.Stdout != nil {
				_, _ = io.Copy(command.Stdout, stdio.stdoutParent)
			} else {
				_, _ = io.Copy(io.Discard, stdio.stdoutParent)
			}
			_ = stdio.stdoutParent.Close()
		}()
		go func() {
			defer copies.Done()
			if command.Stderr != nil {
				_, _ = io.Copy(command.Stderr, stdio.stderrParent)
			} else {
				_, _ = io.Copy(io.Discard, stdio.stderrParent)
			}
			_ = stdio.stderrParent.Close()
		}()
		copies.Wait()
	}()
	return done
}

func (stdio *brokerStdio) closeAll() {
	for _, file := range []*os.File{stdio.stdinParent, stdio.stdinChild, stdio.stdoutParent, stdio.stdoutChild, stdio.stderrParent, stdio.stderrChild} {
		if file != nil {
			_ = file.Close()
		}
	}
}

type capabilityDesktop struct {
	station  windows.Handle
	desktop  windows.Handle
	fullName string
}

func createCapabilityDesktop() (*capabilityDesktop, error) {
	sequence := capabilityDesktopSequence.Add(1)
	stationName := fmt.Sprintf("MongojsonSteward-%d-%d", os.Getpid(), sequence)
	desktopName := "Capability"
	stationNamePointer, err := windows.UTF16PtrFromString(stationName)
	if err != nil {
		return nil, err
	}
	desktopNamePointer, err := windows.UTF16PtrFromString(desktopName)
	if err != nil {
		return nil, err
	}
	owner, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read Broker identity for capability window station: %w", err)
	}
	descriptor, err := windows.SecurityDescriptorFromString(fmt.Sprintf("D:P(A;;GA;;;%s)(A;;GA;;;RC)", owner.User.Sid.String()))
	if err != nil {
		return nil, fmt.Errorf("build capability window-station ACL: %w", err)
	}
	attributes := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: descriptor}
	stationValue, _, callErr := createWindowStationProc.Call(uintptr(unsafe.Pointer(stationNamePointer)), createWindowStationOnly,
		windows.GENERIC_ALL, uintptr(unsafe.Pointer(&attributes)))
	if stationValue == 0 && !owner.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		// Only Administrators may choose a window-station name. Unit tests often
		// run unelevated, so let Windows name a station there. The production
		// LocalSystem Broker must never take this fallback.
		stationValue, _, callErr = createWindowStationProc.Call(0, 0, windows.GENERIC_ALL, uintptr(unsafe.Pointer(&attributes)))
		if stationValue != 0 {
			stationName, err = windowStationName(windows.Handle(stationValue))
			if err != nil {
				closeWindowStation(windows.Handle(stationValue))
				return nil, err
			}
		}
	}
	if stationValue == 0 {
		return nil, fmt.Errorf("create private capability window station: %w", callErr)
	}
	station := windows.Handle(stationValue)

	// CreateDesktop targets the process window station. This change is
	// process-wide, so serialize it and restore the service station before the
	// child is launched or any other Broker work resumes.
	windowStationCreationMu.Lock()
	defer windowStationCreationMu.Unlock()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	originalValue, _, callErr := getProcessWindowStationProc.Call()
	if originalValue == 0 {
		closeWindowStation(station)
		return nil, fmt.Errorf("get Broker window station: %w", callErr)
	}
	result, _, callErr := setProcessWindowStationProc.Call(uintptr(station))
	if result == 0 {
		closeWindowStation(station)
		return nil, fmt.Errorf("enter private capability window station: %w", callErr)
	}
	desktopValue, _, desktopErr := createDesktopProc.Call(uintptr(unsafe.Pointer(desktopNamePointer)), 0, 0, 0,
		windows.GENERIC_ALL, uintptr(unsafe.Pointer(&attributes)))
	restoreResult, _, restoreErr := setProcessWindowStationProc.Call(originalValue)
	if desktopValue == 0 {
		closeWindowStation(station)
		return nil, fmt.Errorf("create private capability desktop: %w", desktopErr)
	}
	desktop := windows.Handle(desktopValue)
	if restoreResult == 0 {
		closeDesktop(desktop)
		closeWindowStation(station)
		return nil, fmt.Errorf("restore Broker service window station: %w", restoreErr)
	}
	return &capabilityDesktop{station: station, desktop: desktop, fullName: stationName + `\` + desktopName}, nil
}

func (desktop *capabilityDesktop) close() {
	if desktop == nil {
		return
	}
	closeDesktop(desktop.desktop)
	closeWindowStation(desktop.station)
	desktop.desktop = 0
	desktop.station = 0
}

func windowStationName(station windows.Handle) (string, error) {
	var required uint32
	getUserObjectInformationProc.Call(uintptr(station), userObjectName, 0, 0, uintptr(unsafe.Pointer(&required)))
	if required == 0 {
		return "", fmt.Errorf("measure capability window station name")
	}
	buffer := make([]uint16, (required+1)/2)
	result, _, callErr := getUserObjectInformationProc.Call(uintptr(station), userObjectName,
		uintptr(unsafe.Pointer(&buffer[0])), uintptr(required), uintptr(unsafe.Pointer(&required)))
	if result == 0 {
		return "", fmt.Errorf("read capability window station name: %w", callErr)
	}
	return windows.UTF16ToString(buffer), nil
}

func closeWindowStation(handle windows.Handle) {
	if handle == 0 {
		return
	}
	closeWindowStationProc.Call(uintptr(handle))
}

func closeDesktop(handle windows.Handle) {
	if handle != 0 {
		closeDesktopProc.Call(uintptr(handle))
	}
}
