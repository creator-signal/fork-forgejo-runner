// SPDX-License-Identifier: MIT

package firecracker

import (
	"fmt"
	"strings"
	"time"
)

// Config holds configuration for Firecracker VM management.
type Config struct {
	KernelPath      string
	RootFSTemplate  string
	FirecrackerBin  string
	MemoryMB        int
	VCPUs           int
	NetworkPrefix   string
	SSHTimeout      time.Duration
	OutputInterface string

	// Jailer configuration for per-VM cgroup isolation
	UseJailer     bool
	JailerBin     string
	JailerUID     int
	JailerGID     int
	ChrootBaseDir string
}

// DefaultConfig returns sensible defaults for testing.
// In production, configs come from profiles defined in config.yaml.
func DefaultConfig() Config {
	return Config{
		KernelPath:     "/opt/firecracker/vmlinux",
		RootFSTemplate: "/opt/firecracker/rootfs.ext4",
		FirecrackerBin: "/usr/local/bin/firecracker",
		MemoryMB:       2048,
		VCPUs:          2,
		NetworkPrefix:  "172.16",
		SSHTimeout:     60 * time.Second,
	}
}

// ConnectionInfo contains SSH connection details for a running VM.
type ConnectionInfo struct {
	Host   string // VM's IP address (guest)
	HostIP string // Host's TAP interface IP (gateway for the VM)
	Port   string
	Key    string
}

// BuildSSHCommand constructs an SSH command for executing in the VM.
// This is a pure function for easy testing.
func BuildSSHCommand(keyPath, host, port, user, workdir string, env map[string]string, command []string) []string {
	// Build env command arguments for all environment variables.
	// We use the `env` command instead of bash `export` because GitHub Actions
	// creates INPUT_<param-name> variables that may contain hyphens
	// (e.g., INPUT_KEEP-STATE), which are not valid bash variable names
	// but can be passed via the `env` command.
	var envArgs strings.Builder
	for k, v := range env {
		// Shell-quote both key and value to handle special characters
		escapedK := strings.ReplaceAll(k, "'", "'\"'\"'")
		escapedV := strings.ReplaceAll(v, "'", "'\"'\"'")
		envArgs.WriteString(fmt.Sprintf("'%s=%s' ", escapedK, escapedV))
	}

	// Build the remote command with working directory and env-wrapped command
	escapedWd := strings.ReplaceAll(workdir, "'", "'\"'\"'")
	var remoteCmd string
	if envArgs.Len() > 0 {
		remoteCmd = fmt.Sprintf("cd '%s' && env %s%s",
			escapedWd,
			envArgs.String(),
			strings.Join(command, " "))
	} else {
		remoteCmd = fmt.Sprintf("cd '%s' && %s",
			escapedWd,
			strings.Join(command, " "))
	}

	return []string{
		"/usr/bin/ssh",
		"-i", keyPath,
		"-p", port,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-T",
		fmt.Sprintf("%s@%s", user, host),
		remoteCmd,
	}
}
