package container

import (
	"errors"
	"os"
	"syscall"
)

func runCmdInGroup(cmd *exec.Cmd, cmdline string, tty bool) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}
	cmd.Cancel = func() error {
		pgid := cmd.Process.Pid
		return syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return cmd.Run()
}

func openPty() (*os.File, *os.File, error) {
	return nil, nil, errors.New("Unsupported")
}
