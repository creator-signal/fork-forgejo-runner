package container

import (
	"errors"
	"os"
	"os/exec"
	"unsafe"

	syscall "golang.org/x/sys/windows"
)

func runCmdInGroup(cmd *exec.Cmd, cmdline string, tty bool) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CmdLine: cmdline,
		// We pass `CREATE_SUSPENDED` here to avoid a race condition: we want to assign
		// the child to a job object to ensure we can terminate the whole process tree,
		// but we can only do that once they've spawned, and we don't want them to spawn
		// any children before we do it. So we start the process suspended, and resume it
		// after assigning it to a job object.
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP | syscall.CREATE_SUSPENDED,
	}
	jobHandle, err := syscall.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(jobHandle)

	cmd.Cancel = func() error {
		syscall.TerminateJobObject(jobHandle, 1)
		return cmd.Process.Kill()
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	childResumed := false
	defer func() {
		if !childResumed {
			// If the child is still suspended (we're erroring for some reason), just
			// kill it. This won't orphan any processes because the child hasn't had
			// a chance to spawn children of its own yet.
			cmd.Process.Kill()
		}
	}()

	// The Go standard library actually does have a handle to the child process in
	// the `Cmd`, but it's inaccessible to us, so we must use `OpenProcess`.
	procHandle, err := syscall.OpenProcess(syscall.PROCESS_TERMINATE|syscall.PROCESS_SET_QUOTA, false, uint32(cmd.Process.Pid))
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(procHandle)

	// The Go standard library gets a handle to the main thread from `CreateProcess`.
	// However, it immediately closes it, which is a problem: we need that handle to
	// resume the initially-suspended thread. Our only option here is to iterate the
	// threads on the system to find the one in that process and open another handle
	// to it. If it's any consolation, the Go standard library actually has a test
	// which does just this, and for exactly the same reason too:
	// https://cs.opensource.google/go/go/+/refs/tags/go1.25.4:src/runtime/syscall_windows_test.go;l=975-1022
	snapshot, err := syscall.CreateToolhelp32Snapshot(syscall.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(snapshot)
	var threadEntry syscall.ThreadEntry32
	threadEntry.Size = uint32(unsafe.Sizeof(threadEntry))
	err = syscall.Thread32First(snapshot, &threadEntry)
	if err != nil {
		return err
	}
	for threadEntry.OwnerProcessID != uint32(cmd.Process.Pid) {
		err = syscall.Thread32Next(snapshot, &threadEntry)
		if err != nil {
			return err
		}
	}
	threadHandle, err := syscall.OpenThread(syscall.THREAD_SUSPEND_RESUME, false, threadEntry.ThreadID)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(threadHandle)

	// *Now* we can do what we actually want to do. Assign the child process to the
	// job object we created, then resume the child.
	err = syscall.AssignProcessToJobObject(jobHandle, procHandle)
	if err != nil {
		return err
	}
	_, err = syscall.ResumeThread(threadHandle)
	if err != nil {
		return err
	}
	childResumed = true

	return cmd.Wait()
}

func openPty() (*os.File, *os.File, error) {
	return nil, nil, errors.New("Unsupported")
}
