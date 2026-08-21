package common

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

func RunCmdInGroup(cmd *exec.Cmd, cmdline string) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Rfork: syscall.RFNOTEG,
	}
	return cmd.Run()
}

func OpenPty() (*os.File, *os.File, error) {
	return nil, nil, errors.New("Unsupported")
}
