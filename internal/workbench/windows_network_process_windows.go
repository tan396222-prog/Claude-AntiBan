//go:build windows

package workbench

import (
	"errors"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The packet helper keeps this non-inheritable handle until process exit.
// Windows then retires its relay descendants even if the controller kills
// the helper before it can run its normal pipe cleanup.
var windowsNetworkProcessJob windows.Handle

func containWindowsNetworkProcess() error {
	if windowsNetworkProcessJob != 0 {
		return nil
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return errors.New("Windows 网络进程组无法创建，外网保持关闭")
	}
	var limits windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, err = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits)))
	if err == nil {
		err = windows.AssignProcessToJobObject(job, windows.CurrentProcess())
	}
	if err != nil {
		_ = windows.CloseHandle(job)
		return errors.New("Windows 网络子进程无法隔离，外网保持关闭")
	}
	windowsNetworkProcessJob = job
	return nil
}
