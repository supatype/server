package deno

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// tieLifetime has nothing to set before the start on Windows; see adoptWorker.
func tieLifetime(*exec.Cmd) (release func()) { return func() {} }

// workerJob is a job object that kills every process in it when its last
// handle closes. Only this process holds the handle and it is never closed, so
// it closes when this process ends, however it ends, and the worker goes too.
var workerJob = sync.OnceValues(func() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("create job object: %w", err)
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	if _, err := windows.SetInformationJobObject(
		job,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), // #nosec G103 -- the Windows API takes the struct by pointer and size
		uint32(unsafe.Sizeof(info)),
	); err != nil {
		_ = windows.CloseHandle(job)
		return 0, fmt.Errorf("set job object limits: %w", err)
	}
	return job, nil
})

// adoptWorker puts the started worker in workerJob. Windows has no
// parent-death signal, and a job is how a process's children are made to end
// with it. Processes the worker starts from now on join the job with it.
func adoptWorker(p *os.Process) error {
	job, err := workerJob()
	if err != nil {
		return err
	}
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid)) // #nosec G115 -- a PID.
	if err != nil {
		return fmt.Errorf("open worker process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if err := windows.AssignProcessToJobObject(job, h); err != nil {
		return fmt.Errorf("assign worker to job object: %w", err)
	}
	return nil
}
