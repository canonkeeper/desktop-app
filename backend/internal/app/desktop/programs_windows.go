// SPDX-License-Identifier: AGPL-3.0-only

package desktop

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// The job object of the backend (specs/2026-11-16-desk-restore-orphans.md RO1.1): Windows ends every process in it once
// its last handle closes, and the only handle is the backend's own, which Windows closes when the backend ends, however it
// ends. The calls are those of kernel32, which the standard library loads from the system directory.
var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procCreateJobObjectW         = kernel32.NewProc("CreateJobObjectW")
	procSetInformationJobObject  = kernel32.NewProc("SetInformationJobObject")
	procAssignProcessToJobObject = kernel32.NewProc("AssignProcessToJobObject")
)

const (
	jobObjectExtendedLimitInformation = 9
	jobObjectLimitKillOnJobClose      = 0x00002000
)

// jobBasicLimits and jobExtendedLimits are JOBOBJECT_BASIC_LIMIT_INFORMATION and JOBOBJECT_EXTENDED_LIMIT_INFORMATION.
type jobBasicLimits struct {
	PerProcessUserTimeLimit int64
	PerJobUserTimeLimit     int64
	LimitFlags              uint32
	MinimumWorkingSetSize   uintptr
	MaximumWorkingSetSize   uintptr
	ActiveProcessLimit      uint32
	Affinity                uintptr
	PriorityClass           uint32
	SchedulingClass         uint32
}

type jobExtendedLimits struct {
	BasicLimitInformation jobBasicLimits
	IoInfo                [6]uint64
	ProcessMemoryLimit    uintptr
	JobMemoryLimit        uintptr
	PeakProcessMemoryUsed uintptr
	PeakJobMemoryUsed     uintptr
}

// programsJob is the job of the backend, made once and never closed; 0 when Windows did not make it. It lets nothing
// break away: what a program starts stays in it.
var programsJob = sync.OnceValue(func() uintptr {
	job, _, _ := procCreateJobObjectW.Call(0, 0)
	if job == 0 {
		return 0
	}
	limits := jobExtendedLimits{BasicLimitInformation: jobBasicLimits{LimitFlags: jobObjectLimitKillOnJobClose}}
	if set, _, _ := procSetInformationJobObject.Call(job, jobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&limits)), unsafe.Sizeof(limits)); set == 0 {
		_ = syscall.CloseHandle(syscall.Handle(job))
		return 0
	}
	return job
})

// startBound starts a program of PostgreSQL bound to the backend (RO1.1): right after its start it joins the job of the
// backend, which ends it when the backend ends, together with what it started — initdb runs itself again as its own
// child with a restricted token, and that child does the work, with the postgres of its steps. pg_ctl start stays out of
// the job (startsServer): the server it starts is the cluster, which outlives the backend as before — postmaster.pid names
// it to stopLeftover, and pg_ctl gives it a job of its own. A program the job cannot hold runs as before: the undo of the
// next start waits out what it still writes (removeForGood). A cancel ends the program alone (Process.Kill); what it
// started ends with its work or with the backend.
func startBound(command *exec.Cmd, startsServer bool) error {
	if err := command.Start(); err != nil {
		return err
	}
	if job := programsJob(); job != 0 && !startsServer {
		_ = command.Process.WithHandle(func(process uintptr) {
			_, _, _ = procAssignProcessToJobObject.Call(job, process)
		})
	}
	return nil
}
