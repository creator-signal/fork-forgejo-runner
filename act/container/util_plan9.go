package container

import (
	"errors"
	"os"
	"syscall"
)

func runCmdInGroup(cmd *exec.Cmd, cmdline string, tty bool) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Rfork: syscall.RFNOTEG,
	}
	return cmd.Run()
}

func openPty() (*os.File, *os.File, error) {
	return nil, nil, errors.New("Unsupported")
}
