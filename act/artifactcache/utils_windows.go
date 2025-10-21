//go:build windows

package artifactcache

import "syscall"

func suicide() error {
	handle, err := syscall.GetCurrentProcess()
	if err != nil {
		return err
	}

	return syscall.TerminateProcess(handle, uint32(syscall.SIGTERM))
}
