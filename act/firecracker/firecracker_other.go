//go:build !linux

// SPDX-License-Identifier: MIT

package firecracker

import (
	"context"
	"fmt"
	"runtime"
)

// VM is a stub for non-Linux platforms.
// Firecracker requires Linux with KVM support.
type VM struct {
	Name     string
	RootDir  string
	Config   Config
	SubnetID int
	TAPName  string
	GuestIP  string
	HostIP   string
	SSHKey   string
	PID      int
}

var errNotSupported = fmt.Errorf("firecracker is only supported on Linux (current OS: %s)", runtime.GOOS)

// NewVM returns a stub VM that will error on any operation.
func NewVM(name, rootDir string, config Config) *VM {
	return &VM{
		Name:    name,
		RootDir: rootDir,
		Config:  config,
	}
}

// Create returns an error on non-Linux platforms.
func (vm *VM) Create(ctx context.Context) error {
	return errNotSupported
}

// Start returns an error on non-Linux platforms.
func (vm *VM) Start(ctx context.Context) (*ConnectionInfo, error) {
	return nil, errNotSupported
}

// Stop returns an error on non-Linux platforms.
func (vm *VM) Stop(ctx context.Context) error {
	return errNotSupported
}

// Destroy returns an error on non-Linux platforms.
func (vm *VM) Destroy(ctx context.Context) error {
	return errNotSupported
}

// CreateVMDirectory returns an error on non-Linux platforms.
func (vm *VM) CreateVMDirectory(ctx context.Context, path string) error {
	return errNotSupported
}

// LogStats is a no-op on non-Linux platforms.
func (vm *VM) LogStats(ctx context.Context) {
}

// BuildConfig returns nil on non-Linux platforms.
func (vm *VM) BuildConfig(rootfsPath string) []byte {
	return nil
}
