package container

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/common/gitignore"
	"code.forgejo.org/forgejo/runner/v12/act/filecollector"
)

// TenkiEnvironment runs a job inside a disposable Tenki microVM. It implements
// ExecutionsEnvironment against a remote sandbox: unlike HostEnvironment, every
// path is inside the sandbox (always POSIX Linux) and every file operation and
// command crosses the network through the tenkiSession seam.
type TenkiEnvironment struct {
	Name string

	// Remote paths inside the sandbox (POSIX). Workdir is the runner-side
	// directory that maps onto Path inside the sandbox.
	Path      string
	TmpDir    string
	ToolCache string
	Workdir   string
	ActPath   string
	Root      string

	// StdOut is the per-step log sink, swapped by ReplaceLogWriter.
	StdOut io.Writer

	client     tenkiClient
	createOpts tenkiCreateOptions

	mu      sync.Mutex
	session tenkiSession
}

func (e *TenkiEnvironment) Create(_, _ []string) common.Executor {
	return func(ctx context.Context) error {
		session, err := e.client.CreateSession(ctx, e.createOpts)
		if err != nil {
			return err
		}
		e.mu.Lock()
		e.session = session
		e.mu.Unlock()
		return nil
	}
}

func (e *TenkiEnvironment) currentSession() (tenkiSession, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.session == nil {
		return nil, errors.New("tenki sandbox is not created")
	}
	return e.session, nil
}

// Copy writes each in-memory file into the sandbox under destPath.
func (e *TenkiEnvironment) Copy(destPath string, files ...*FileEntry) common.Executor {
	return func(ctx context.Context) error {
		session, err := e.currentSession()
		if err != nil {
			return err
		}
		for _, f := range files {
			dst := path.Join(destPath, f.Name)
			if err := session.WriteFile(ctx, dst, strings.NewReader(f.Body)); err != nil {
				return err
			}
		}
		return nil
	}
}

// CopyTarStream untars a stream into destPath inside the sandbox by piping it
// into a `tar -x` running there.
func (e *TenkiEnvironment) CopyTarStream(ctx context.Context, destPath string, tarStream io.Reader) error {
	session, err := e.currentSession()
	if err != nil {
		return err
	}
	// Ensure a clean destination, mirroring HostEnvironment.CopyTarStream.
	if _, err := e.runToCompletion(ctx, session, []string{"sh", "-c", fmt.Sprintf("rm -rf %q && mkdir -p %q", destPath, destPath)}, nil, ""); err != nil {
		return err
	}
	proc, err := session.Run(ctx, []string{"tar", "-xf", "-", "-C", destPath}, nil, "")
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		io.Copy(&stderr, proc.Stderr())
	}()
	go io.Copy(io.Discard, proc.Stdout())
	if _, err := io.Copy(proc.Stdin(), tarStream); err != nil {
		proc.Stdin().Close()
		_, _ = proc.Wait()
		return fmt.Errorf("streaming tar into sandbox: %w", err)
	}
	if err := proc.Stdin().Close(); err != nil {
		return fmt.Errorf("closing tar stream: %w", err)
	}
	exit, err := proc.Wait()
	wg.Wait()
	if err != nil {
		return err
	}
	if exit != 0 {
		return fmt.Errorf("tar extract failed (exit %d): %s", exit, strings.TrimSpace(stderr.String()))
	}
	return nil
}

// CopyDir tars a local directory and streams it into the sandbox.
func (e *TenkiEnvironment) CopyDir(destPath, srcPath string, useGitIgnore bool) common.Executor {
	return func(ctx context.Context) error {
		tarStream, err := tarLocalDir(ctx, srcPath, useGitIgnore)
		if err != nil {
			return err
		}
		return e.CopyTarStream(ctx, destPath, tarStream)
	}
}

// tarLocalDir packs a runner-side directory into an in-memory tar, honoring
// .gitignore when requested. Mirrors HostEnvironment's collector usage.
func tarLocalDir(ctx context.Context, srcPath string, useGitIgnore bool) (io.Reader, error) {
	logger := common.Logger(ctx)
	srcPrefix := filepath.Dir(srcPath)
	if !strings.HasSuffix(srcPrefix, string(filepath.Separator)) {
		srcPrefix += string(filepath.Separator)
	}
	var ignorer gitignore.Matcher
	if useGitIgnore {
		ps, err := gitignore.ReadPatterns(srcPath)
		if err != nil {
			logger.Debugf("Error loading .gitignore: %v", err)
		}
		ignorer = gitignore.NewMatcher(ps)
	}
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	fc := &filecollector.FileCollector{
		Ignorer:   ignorer,
		SrcPath:   srcPath,
		SrcPrefix: srcPrefix,
		Handler:   &filecollector.TarCollector{TarWriter: tw},
	}
	if err := filepath.Walk(srcPath, fc.CollectFiles(ctx, []string{})); err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return buf, nil
}

// GetContainerArchive tars srcPath inside the sandbox and streams it back.
func (e *TenkiEnvironment) GetContainerArchive(ctx context.Context, srcPath string) (io.ReadCloser, error) {
	session, err := e.currentSession()
	if err != nil {
		return nil, err
	}
	srcPath = path.Clean(srcPath)
	// Pack the path's contents relative to its parent so the tar entries match
	// what CopyTarStream/consumers expect, mirroring the local collector.
	dir, base := path.Split(srcPath)
	if dir == "" {
		dir = "."
	}
	proc, err := session.Run(ctx, []string{"tar", "-cf", "-", "-C", dir, base}, nil, "")
	if err != nil {
		return nil, err
	}
	go io.Copy(io.Discard, proc.Stderr())
	return &procReadCloser{proc: proc}, nil
}

// procReadCloser exposes a running command's stdout as a ReadCloser that waits
// for the command to finish on Close.
type procReadCloser struct {
	proc tenkiProcess
}

func (r *procReadCloser) Read(p []byte) (int, error) { return r.proc.Stdout().Read(p) }

func (r *procReadCloser) Close() error {
	// Drain any unread stdout before waiting: the SDK streams over an
	// unbuffered pipe, so an undrained reader blocks Wait from ever observing
	// the exit frame. Callers (e.g. ParseEnvFile) often stop reading early.
	_, _ = io.Copy(io.Discard, r.proc.Stdout())
	_, err := r.proc.Wait()
	return err
}

func (e *TenkiEnvironment) Pull(_ bool) common.Executor {
	return func(ctx context.Context) error { return nil }
}

func (e *TenkiEnvironment) Start(_ bool) common.Executor {
	return func(ctx context.Context) error { return nil }
}

func (e *TenkiEnvironment) UpdateFromImageEnv(_ *map[string]string) common.Executor {
	return func(ctx context.Context) error { return nil }
}

// runToCompletion runs a command and discards its output, returning the exit
// code. Used for internal housekeeping commands (mkdir, rm).
func (e *TenkiEnvironment) runToCompletion(ctx context.Context, session tenkiSession, argv []string, env map[string]string, workdir string) (int, error) {
	proc, err := session.Run(ctx, argv, env, workdir)
	if err != nil {
		return 0, err
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(io.Discard, proc.Stdout()) }()
	go func() { defer wg.Done(); io.Copy(io.Discard, proc.Stderr()) }()
	exit, err := proc.Wait()
	wg.Wait()
	return exit, err
}

func (e *TenkiEnvironment) Exec(command []string, env map[string]string, user, workdir string) common.Executor {
	return func(ctx context.Context) error {
		session, err := e.currentSession()
		if err != nil {
			return err
		}
		wd := e.resolveWorkdir(workdir)
		proc, err := session.Run(ctx, command, env, wd)
		if err != nil {
			return err
		}

		// Kill the remote command if the step is cancelled.
		killCtx, stopKiller := context.WithCancel(ctx)
		defer stopKiller()
		go func() {
			<-killCtx.Done()
			if errors.Is(killCtx.Err(), context.Canceled) && ctx.Err() != nil {
				_ = proc.Kill()
			}
		}()

		// Stream stdout and stderr into the (combined) log sink.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); io.Copy(e.StdOut, proc.Stdout()) }()
		go func() { defer wg.Done(); io.Copy(e.StdOut, proc.Stderr()) }()

		exit, waitErr := proc.Wait()
		wg.Wait()

		if waitErr != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("this step has been cancelled: ctx: %w, exec: %w", ctx.Err(), waitErr)
			}
			return waitErr
		}
		if exit != 0 {
			return fmt.Errorf("exit with `FAILURE`: %d", exit)
		}
		return nil
	}
}

// resolveWorkdir maps an act workdir into an in-sandbox absolute path.
func (e *TenkiEnvironment) resolveWorkdir(workdir string) string {
	if workdir == "" {
		return e.Path
	}
	if path.IsAbs(workdir) {
		return workdir
	}
	return path.Join(e.Path, workdir)
}

func (e *TenkiEnvironment) UpdateFromEnv(srcPath string, env *map[string]string) common.Executor {
	return ParseEnvFile(e, srcPath, env)
}

func (e *TenkiEnvironment) Remove() common.Executor {
	return func(ctx context.Context) error {
		e.mu.Lock()
		session := e.session
		e.session = nil
		e.mu.Unlock()
		if session == nil {
			return nil
		}
		return session.Terminate(ctx)
	}
}

func (e *TenkiEnvironment) Close() common.Executor {
	return func(ctx context.Context) error {
		if e.client == nil {
			return nil
		}
		return e.client.Close()
	}
}

func (e *TenkiEnvironment) ToContainerPath(p string) string {
	if bp, err := filepath.Rel(e.Workdir, p); err == nil {
		return path.Join(e.Path, filepath.ToSlash(bp))
	} else if filepath.Clean(e.Workdir) == filepath.Clean(p) {
		return e.Path
	}
	return p
}

func (*TenkiEnvironment) BackendID() string { return "tenki" }

// SupportsDockerContainerActions is false: a remote microVM cannot host the
// runner's local Docker container actions.
func (*TenkiEnvironment) SupportsDockerContainerActions() bool { return false }

func (*TenkiEnvironment) ManagesOwnNetworking() bool { return true }

func (e *TenkiEnvironment) GetName() string { return e.Name }

func (e *TenkiEnvironment) GetRoot() string { return e.Root }

func (e *TenkiEnvironment) GetActPath() string { return e.ActPath }

// The sandbox is always Linux, so paths are POSIX regardless of the runner OS.
func (*TenkiEnvironment) GetPathVariableName() string { return "PATH" }

func (e *TenkiEnvironment) DefaultPathVariable() string {
	return "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
}

func (*TenkiEnvironment) JoinPathVariable(paths ...string) string {
	return strings.Join(paths, ":")
}

func (e *TenkiEnvironment) GetRunnerContext(_ context.Context) map[string]any {
	return map[string]any{
		"os":         "Linux",
		"arch":       "X64",
		"tool_cache": e.ToolCache,
		"temp":       e.TmpDir,
	}
}

func (e *TenkiEnvironment) IsHealthy(_ context.Context) (time.Duration, error) {
	return 0, nil
}

func (e *TenkiEnvironment) ReplaceLogWriter(stdout, _ io.Writer) (io.Writer, io.Writer) {
	org := e.StdOut
	e.StdOut = stdout
	return org, org
}

func (*TenkiEnvironment) IsEnvironmentCaseInsensitive() bool { return false }

// compile-time assertion that TenkiEnvironment satisfies the interface.
var _ ExecutionsEnvironment = (*TenkiEnvironment)(nil)
