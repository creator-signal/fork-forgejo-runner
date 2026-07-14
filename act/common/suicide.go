//go:build !windows

package common

import "syscall"

func Suicide(exitCode RunnerExitCode) error {
	SetPendingExitCode(exitCode)
	return syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
}
