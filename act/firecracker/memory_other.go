//go:build !linux

package firecracker

import "errors"

// ErrUnsupportedPlatform is returned on non-Linux platforms where memory scheduling is unavailable.
var ErrUnsupportedPlatform = errors.New("memory scheduling is only supported on Linux")

type realMemorySystem struct{}

// GetAvailableMemoryMB is not supported on non-Linux platforms.
func (s *realMemorySystem) GetAvailableMemoryMB() (int64, error) {
	return 0, ErrUnsupportedPlatform
}

// GetTotalMemoryMB is not supported on non-Linux platforms.
func (s *realMemorySystem) GetTotalMemoryMB() (int64, error) {
	return 0, ErrUnsupportedPlatform
}
