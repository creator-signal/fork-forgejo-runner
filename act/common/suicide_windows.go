//go:build windows

package common

import "syscall"

func Suicide(exitCode RunnerExitCode) error {
	SetPendingExitCode(exitCode)

	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return err
	}

	return syscall.TerminateProcess(handle, uint32(syscall.SIGTERM))
}
