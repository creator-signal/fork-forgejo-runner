package container

import (
	"context"
	"fmt"
	"io"
	"time"

	tenki "github.com/TenkiCloud/tenki-sdk-go/sandbox"
)

// createReadyTimeout bounds how long CreateSession waits for the sandbox to
// reach RUNNING before giving up.
const createReadyTimeout = 3 * time.Minute

// TenkiCreateOptions are the sandbox parameters callers pass to NewTenkiEnvironment.
// It mirrors the internal tenkiCreateOptions so the runner package can configure a
// sandbox without importing the SDK types.
type TenkiCreateOptions struct {
	Name        string
	ProjectID   string
	Image       string
	CPUCores    int32
	MemoryMB    int32
	DiskSizeGB  int
	MaxLifetime time.Duration
	Metadata    map[string]string
}

// NewTenkiEnvironment builds a TenkiEnvironment backed by the real Tenki SDK.
// token/endpoint fall back to the SDK's env resolution when empty. The returned
// environment has no session yet; Create provisions it.
func NewTenkiEnvironment(token, endpoint string, opts TenkiCreateOptions, paths TenkiPaths, stdout io.Writer) (*TenkiEnvironment, error) {
	client, err := newSDKTenkiClient(token, endpoint)
	if err != nil {
		return nil, err
	}
	return &TenkiEnvironment{
		Name:      opts.Name,
		Path:      paths.Path,
		TmpDir:    paths.TmpDir,
		ToolCache: paths.ToolCache,
		Workdir:   paths.Workdir,
		ActPath:   paths.ActPath,
		Root:      paths.Root,
		StdOut:    stdout,
		client:    client,
		createOpts: tenkiCreateOptions{
			Name:        opts.Name,
			ProjectID:   opts.ProjectID,
			Image:       opts.Image,
			CPUCores:    opts.CPUCores,
			MemoryMB:    opts.MemoryMB,
			DiskSizeGB:  opts.DiskSizeGB,
			MaxLifetime: opts.MaxLifetime,
			Metadata:    opts.Metadata,
		},
	}, nil
}

// TenkiPaths are the in-sandbox POSIX paths a TenkiEnvironment operates under.
type TenkiPaths struct {
	Root      string
	Path      string
	ActPath   string
	TmpDir    string
	ToolCache string
	Workdir   string
}

// sdkTenkiClient is the production tenkiClient backed by the Tenki Go SDK.
type sdkTenkiClient struct {
	client *tenki.Client
}

// newSDKTenkiClient builds a client. token/endpoint fall back to the SDK's own
// env resolution (TENKI_API_KEY / TENKI_API_URL) when empty.
func newSDKTenkiClient(token, endpoint string) (tenkiClient, error) {
	var opts []tenki.Option
	if token != "" {
		opts = append(opts, tenki.WithAuthToken(token))
	}
	if endpoint != "" {
		opts = append(opts, tenki.WithBaseURL(endpoint))
	}
	client, err := tenki.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("creating tenki client: %w", err)
	}
	return &sdkTenkiClient{client: client}, nil
}

func (c *sdkTenkiClient) CreateSession(ctx context.Context, opts tenkiCreateOptions) (tenkiSession, error) {
	createOpts := []tenki.CreateOption{
		tenki.WithProjectID(opts.ProjectID),
		tenki.WithAllowOutbound(true),
	}
	if opts.Name != "" {
		createOpts = append(createOpts, tenki.WithName(opts.Name))
	}
	if opts.Image != "" {
		createOpts = append(createOpts, tenki.WithImage(opts.Image))
	}
	if opts.CPUCores > 0 {
		createOpts = append(createOpts, tenki.WithCPUCores(opts.CPUCores))
	}
	if opts.MemoryMB > 0 {
		createOpts = append(createOpts, tenki.WithMemoryMB(opts.MemoryMB))
	}
	if opts.DiskSizeGB > 0 {
		createOpts = append(createOpts, tenki.WithDiskSizeGB(opts.DiskSizeGB))
	}
	if opts.MaxLifetime > 0 {
		createOpts = append(createOpts, tenki.WithMaxDuration(opts.MaxLifetime))
	}
	if len(opts.Metadata) > 0 {
		createOpts = append(createOpts, tenki.WithMetadata(opts.Metadata))
	}

	session, err := c.client.CreateAndWait(ctx, createReadyTimeout, createOpts...)
	if err != nil {
		return nil, fmt.Errorf("creating tenki sandbox: %w", err)
	}
	return &sdkTenkiSession{session: session}, nil
}

func (c *sdkTenkiClient) Close() error {
	return c.client.Close()
}

type sdkTenkiSession struct {
	session *tenki.Session
}

func (s *sdkTenkiSession) ID() string {
	return s.session.ID
}

func (s *sdkTenkiSession) Run(ctx context.Context, argv []string, env map[string]string, workdir string) (tenkiProcess, error) {
	handle, err := s.session.Command(argv, tenki.RunOptions{Env: env, Dir: workdir}).Stream(ctx)
	if err != nil {
		return nil, fmt.Errorf("starting command in sandbox: %w", err)
	}
	return &sdkTenkiProcess{handle: handle}, nil
}

func (s *sdkTenkiSession) WriteFile(ctx context.Context, path string, r io.Reader) error {
	if err := s.session.WriteFileStream(ctx, path, r); err != nil {
		return fmt.Errorf("writing %s to sandbox: %w", path, err)
	}
	return nil
}

func (s *sdkTenkiSession) ReadFile(ctx context.Context, path string) (io.ReadCloser, error) {
	rc, err := s.session.ReadFileStream(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("reading %s from sandbox: %w", path, err)
	}
	return rc, nil
}

func (s *sdkTenkiSession) Terminate(ctx context.Context) error {
	if err := s.session.Close(ctx); err != nil {
		return fmt.Errorf("terminating sandbox: %w", err)
	}
	return nil
}

type sdkTenkiProcess struct {
	handle *tenki.RunHandle
}

func (p *sdkTenkiProcess) Stdout() io.Reader     { return p.handle.Stdout }
func (p *sdkTenkiProcess) Stderr() io.Reader     { return p.handle.Stderr }
func (p *sdkTenkiProcess) Stdin() io.WriteCloser { return p.handle.Stdin }
func (p *sdkTenkiProcess) Kill() error           { return p.handle.Kill() }

func (p *sdkTenkiProcess) Wait() (int, error) {
	result, err := p.handle.Wait()
	if err != nil {
		return 0, fmt.Errorf("waiting for command: %w", err)
	}
	return int(result.ExitCode), nil
}
