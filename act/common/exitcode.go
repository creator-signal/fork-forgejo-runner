package common

import (
	"os"
)

type RunnerExitCode int

const (
	Success RunnerExitCode = iota
	CacheUnrecoverableError
)

var PendingExitCode RunnerExitCode

// When the program exits, store an exit code to be used at that time.  If `SetPendingExitCode` is called multiple times
// only the first invocation will be saved.
func SetPendingExitCode(exitCode RunnerExitCode) {
	if PendingExitCode == 0 {
		PendingExitCode = exitCode
	}
}

// Exit with an exit code that was previously set in `SetPendingExitCode`, or success if it was never set.
func Exit() {
	os.Exit(int(PendingExitCode))
}
