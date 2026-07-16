//go:build windows

package privilegebroker

import (
	"fmt"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

type windowsProcessTree struct{ job windows.Handle }

const (
	disableMaxPrivilege  = 0x1
	processSuspendResume = 0x0800
)

var (
	advapi32                  = windows.NewLazySystemDLL("advapi32.dll")
	createRestrictedTokenProc = advapi32.NewProc("CreateRestrictedToken")
	ntdll                     = windows.NewLazySystemDLL("ntdll.dll")
	ntResumeProcessProc       = ntdll.NewProc("NtResumeProcess")
)

// configureBrokerProcess gives each capability a restricted primary token and
// creates it suspended. The caller must keep the token alive through Start.
// DISABLE_MAX_PRIVILEGE removes every optional privilege (except the traversal
// privilege Windows retains by design) while preserving the service identity's
// ACL-based access needed by an explicitly authorised capability.
func configureBrokerProcess(command *exec.Cmd) (func(), error) {
	var source windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|windows.TOKEN_ASSIGN_PRIMARY, &source); err != nil {
		return nil, fmt.Errorf("open broker process token: %w", err)
	}
	defer source.Close()
	disabled, err := restrictedCapabilitySIDs(source)
	if err != nil {
		return nil, err
	}
	var disabledPointer uintptr
	if len(disabled) > 0 {
		disabledPointer = uintptr(unsafe.Pointer(&disabled[0]))
	}

	var restricted windows.Token
	result, _, callErr := createRestrictedTokenProc.Call(
		uintptr(source), disableMaxPrivilege,
		uintptr(len(disabled)), disabledPointer, 0, 0, 0, 0,
		uintptr(unsafe.Pointer(&restricted)),
	)
	if result == 0 {
		return nil, fmt.Errorf("create restricted capability token: %w", callErr)
	}
	command.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_SUSPENDED,
		Token:         syscall.Token(restricted),
	}
	return func() { _ = restricted.Close() }, nil
}

func restrictedCapabilitySIDs(source windows.Token) ([]windows.SIDAndAttributes, error) {
	items := make([]windows.SIDAndAttributes, 0, 2)
	adminSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, fmt.Errorf("resolve Administrators SID for restricted capability: %w", err)
	}
	items = append(items, windows.SIDAndAttributes{Sid: adminSID})
	user, err := source.GetTokenUser()
	if err != nil {
		return nil, fmt.Errorf("read broker token user: %w", err)
	}
	if user.User.Sid.IsWellKnown(windows.WinLocalSystemSid) {
		// Mark the LocalSystem user SID deny-only in the capability token. The
		// child can use resources granted to ordinary principals, but SYSTEM-only
		// Broker policy, key, state, audit and checkpoint paths are inaccessible.
		items = append(items, windows.SIDAndAttributes{Sid: user.User.Sid})
	}
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
// environment. In particular PATH, PATHEXT, TEMP, HOME and USERPROFILE are not
// inherited. SYSTEMROOT is required by a subset of native Windows programs.
func brokerEnvironment() []string {
	root, err := windows.GetSystemWindowsDirectory()
	if err != nil || root == "" {
		root = `C:\Windows`
	}
	return []string{"SYSTEMROOT=" + root, "WINDIR=" + root}
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
