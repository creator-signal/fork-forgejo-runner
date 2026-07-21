package container

import (
	"context"
	"io"
	"time"
)

// tenkiClient is the narrow seam over the Tenki sandbox SDK that
// TenkiEnvironment depends on. Defining it here (consumer side) keeps the SDK
// import isolated to tenki_client_sdk.go and lets tests substitute a fake,
// so unit tests never create real cloud sandboxes.
type tenkiClient interface {
	// CreateSession provisions a microVM and waits until it is ready.
	CreateSession(ctx context.Context, opts tenkiCreateOptions) (tenkiSession, error)
	// Close releases client-side handles. It does not terminate sessions.
	Close() error
}

// tenkiSession is a single running microVM tied to one CI job.
type tenkiSession interface {
	// ID is the Tenki session identifier.
	ID() string
	// Run starts a command and returns a handle streaming its output. The
	// command keeps running until it exits or ctx is cancelled (which kills it).
	Run(ctx context.Context, argv []string, env map[string]string, workdir string) (tenkiProcess, error)
	// WriteFile streams r into path inside the sandbox.
	WriteFile(ctx context.Context, path string, r io.Reader) error
	// ReadFile streams the file at path out of the sandbox.
	ReadFile(ctx context.Context, path string) (io.ReadCloser, error)
	// Terminate destroys the sandbox. Terminating an already-gone session
	// returns nil (idempotent).
	Terminate(ctx context.Context) error
}

// tenkiProcess is a command executing inside a sandbox. Stdout/Stderr stream
// incrementally; Wait blocks for the exit code. A non-zero exit is reported via
// the returned exit code, not an error — err is reserved for transport/exec
// faults.
type tenkiProcess interface {
	Stdout() io.Reader
	Stderr() io.Reader
	Stdin() io.WriteCloser
	Kill() error
	Wait() (exitCode int, err error)
}

// tenkiCreateOptions are the sandbox parameters resolved from runner config and
// the job's runs-on label.
type tenkiCreateOptions struct {
	Name        string
	ProjectID   string
	Image       string
	CPUCores    int32
	MemoryMB    int32
	DiskSizeGB  int
	MaxLifetime time.Duration
	Metadata    map[string]string
}
