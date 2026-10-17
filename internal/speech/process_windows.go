//go:build windows

package speech

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// killWithParent puts the process in a job object that Windows terminates when the job
// handle closes, which also happens if this app crashes or is killed. The returned
// function closes the handle.
func killWithParent(p *os.Process) func() {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return func() {}
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err == nil {
		if h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(p.Pid)); err == nil {
			windows.AssignProcessToJobObject(job, h)
			windows.CloseHandle(h)
		}
	}
	return func() { windows.CloseHandle(job) }
}
