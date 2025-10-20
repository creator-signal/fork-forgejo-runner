//go:build !windows

package artifactcache

import "syscall"

func suicide() error {
	return syscall.Kill(syscall.Getpid(), syscall.SIGTERM)
}
