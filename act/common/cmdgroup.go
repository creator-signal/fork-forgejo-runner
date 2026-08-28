//go:build (!windows && !plan9 && !openbsd) || (!windows && !plan9 && !mips64)

package common

import (
	"os"
	"os/exec"
	"syscall"

	"github.com/creack/pty"
)

// Execute `cmd` in such a way that `cmd.Cancel` will kill `cmd` and all of its children.
func RunCmdInGroup(cmd *exec.Cmd, cmdline string) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Cancel = func() error {
		// The `Setpgid` (or `Setsid`) flag means that the process is leader of a
		// process group whose PGID matches the process' PID.
		pgid := cmd.Process.Pid
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return cmd.Run()
}

func OpenPty() (*os.File, *os.File, error) {
	return pty.Open()
}
