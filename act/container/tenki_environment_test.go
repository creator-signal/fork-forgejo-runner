package container

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- fakes implementing the tenki seam ---

type fakeProcess struct {
	stdout   io.Reader
	stderr   io.Reader
	stdin    *bytesWriteCloser
	exitCode int
	waitErr  error
	killed   bool
}

func newFakeProcess(stdout, stderr string, exit int) *fakeProcess {
	return &fakeProcess{
		stdout:   strings.NewReader(stdout),
		stderr:   strings.NewReader(stderr),
		stdin:    &bytesWriteCloser{},
		exitCode: exit,
	}
}

func (p *fakeProcess) Stdout() io.Reader     { return p.stdout }
func (p *fakeProcess) Stderr() io.Reader     { return p.stderr }
func (p *fakeProcess) Stdin() io.WriteCloser { return p.stdin }
func (p *fakeProcess) Kill() error           { p.killed = true; return nil }
func (p *fakeProcess) Wait() (int, error)    { return p.exitCode, p.waitErr }

type bytesWriteCloser struct {
	bytes.Buffer
	closed bool
}

func (b *bytesWriteCloser) Close() error { b.closed = true; return nil }

type runCall struct {
	argv    []string
	env     map[string]string
	workdir string
}

type fakeSession struct {
	id         string
	runs       []runCall
	procFor    func(call runCall) *fakeProcess
	writes     map[string]string
	terminated int
	termErr    error
}

func newFakeSession() *fakeSession {
	return &fakeSession{id: "sess-1", writes: map[string]string{}}
}

func (s *fakeSession) ID() string { return s.id }

func (s *fakeSession) Run(_ context.Context, argv []string, env map[string]string, workdir string) (tenkiProcess, error) {
	call := runCall{argv: argv, env: env, workdir: workdir}
	s.runs = append(s.runs, call)
	if s.procFor != nil {
		return s.procFor(call), nil
	}
	return newFakeProcess("", "", 0), nil
}

func (s *fakeSession) WriteFile(_ context.Context, path string, r io.Reader) error {
	b, _ := io.ReadAll(r)
	s.writes[path] = string(b)
	return nil
}

func (s *fakeSession) ReadFile(_ context.Context, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("")), nil
}

func (s *fakeSession) Terminate(_ context.Context) error {
	s.terminated++
	return s.termErr
}

type fakeClient struct {
	session   *fakeSession
	createErr error
	created   int
	closed    int
}

func (c *fakeClient) CreateSession(_ context.Context, _ tenkiCreateOptions) (tenkiSession, error) {
	c.created++
	if c.createErr != nil {
		return nil, c.createErr
	}
	return c.session, nil
}

func (c *fakeClient) Close() error { c.closed++; return nil }

// newTestEnv wires an env to a fake client/session.
func newTestEnv() (*TenkiEnvironment, *fakeClient, *fakeSession) {
	sess := newFakeSession()
	client := &fakeClient{session: sess}
	env := &TenkiEnvironment{
		Name:   "job-1",
		Path:   "/home/tenki/act",
		StdOut: &bytes.Buffer{},
		client: client,
	}
	return env, client, sess
}

// --- tests ---

func TestTenkiEnvironment_ImplementsInterface(t *testing.T) {
	var _ ExecutionsEnvironment = (*TenkiEnvironment)(nil)
}

func TestTenkiEnvironment_CreateStoresSession(t *testing.T) {
	env, client, sess := newTestEnv()
	require.NoError(t, env.Create(nil, nil)(context.Background()))
	assert.Equal(t, 1, client.created)
	got, err := env.currentSession()
	require.NoError(t, err)
	assert.Equal(t, sess, got)
}

func TestTenkiEnvironment_ExecBeforeCreateFails(t *testing.T) {
	env, _, _ := newTestEnv()
	err := env.Exec([]string{"true"}, nil, "", "")(context.Background())
	require.Error(t, err)
}

func TestTenkiEnvironment_Exec(t *testing.T) {
	tests := []struct {
		name     string
		stdout   string
		stderr   string
		exit     int
		wantErr  bool
		wantLogs string
	}{
		{name: "success streams stdout", stdout: "hello\n", exit: 0, wantErr: false, wantLogs: "hello\n"},
		{name: "nonzero exit is error", stdout: "", stderr: "boom\n", exit: 7, wantErr: true, wantLogs: "boom\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, client, sess := newTestEnv()
			require.NoError(t, env.Create(nil, nil)(context.Background()))
			_ = client
			sess.procFor = func(_ runCall) *fakeProcess {
				return newFakeProcess(tt.stdout, tt.stderr, tt.exit)
			}
			buf := &bytes.Buffer{}
			env.StdOut = buf

			err := env.Exec([]string{"echo", "hi"}, map[string]string{"A": "B"}, "", "sub")(context.Background())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantLogs, buf.String())
			// workdir resolved under Path
			require.Len(t, sess.runs, 1)
			assert.Equal(t, "/home/tenki/act/sub", sess.runs[0].workdir)
			assert.Equal(t, map[string]string{"A": "B"}, sess.runs[0].env)
		})
	}
}

func TestTenkiEnvironment_ExecCancelKills(t *testing.T) {
	env, _, sess := newTestEnv()
	require.NoError(t, env.Create(nil, nil)(context.Background()))

	// A process whose Wait reports the transport failure that a killed remote
	// command surfaces.
	sess.procFor = func(_ runCall) *fakeProcess {
		p := newFakeProcess("", "", 0)
		p.waitErr = context.Canceled
		return p
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled
	err := env.Exec([]string{"sleep", "100"}, nil, "", "")(ctx)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cancelled")
}

func TestTenkiEnvironment_CopyWritesFiles(t *testing.T) {
	env, _, sess := newTestEnv()
	require.NoError(t, env.Create(nil, nil)(context.Background()))
	err := env.Copy("/home/tenki/act", &FileEntry{Name: "sub/x.sh", Mode: 0o755, Body: "echo hi"})(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "echo hi", sess.writes["/home/tenki/act/sub/x.sh"])
}

func TestTenkiEnvironment_RemoveTerminatesIdempotent(t *testing.T) {
	env, _, sess := newTestEnv()
	require.NoError(t, env.Create(nil, nil)(context.Background()))
	require.NoError(t, env.Remove()(context.Background()))
	assert.Equal(t, 1, sess.terminated)
	// second remove is a no-op (session already cleared)
	require.NoError(t, env.Remove()(context.Background()))
	assert.Equal(t, 1, sess.terminated)
}

func TestTenkiEnvironment_CloseClosesClient(t *testing.T) {
	env, client, _ := newTestEnv()
	require.NoError(t, env.Close()(context.Background()))
	assert.Equal(t, 1, client.closed)
}

func TestTenkiEnvironment_Capabilities(t *testing.T) {
	env, _, _ := newTestEnv()
	assert.Equal(t, "tenki", env.BackendID())
	assert.False(t, env.SupportsDockerContainerActions())
	assert.True(t, env.ManagesOwnNetworking())
	assert.False(t, env.IsEnvironmentCaseInsensitive())
	assert.Equal(t, "PATH", env.GetPathVariableName())
	assert.Equal(t, "a:b", env.JoinPathVariable("a", "b"))
}

func TestTenkiEnvironment_RunnerContextIsLinux(t *testing.T) {
	env, _, _ := newTestEnv()
	env.ToolCache = "/tc"
	env.TmpDir = "/tmp/x"
	ctx := env.GetRunnerContext(context.Background())
	assert.Equal(t, "Linux", ctx["os"])
	assert.Equal(t, "X64", ctx["arch"])
	assert.Equal(t, "/tc", ctx["tool_cache"])
	assert.Equal(t, "/tmp/x", ctx["temp"])
}

func TestTenkiEnvironment_ResolveWorkdir(t *testing.T) {
	env, _, _ := newTestEnv()
	assert.Equal(t, "/home/tenki/act", env.resolveWorkdir(""))
	assert.Equal(t, "/home/tenki/act/sub", env.resolveWorkdir("sub"))
	assert.Equal(t, "/abs", env.resolveWorkdir("/abs"))
}

func TestTenkiEnvironment_CopyTarStreamPipesAndExtracts(t *testing.T) {
	env, _, sess := newTestEnv()
	require.NoError(t, env.Create(nil, nil)(context.Background()))

	// Capture what gets streamed into the tar process's stdin.
	var tarProc *fakeProcess
	sess.procFor = func(call runCall) *fakeProcess {
		p := newFakeProcess("", "", 0)
		if len(call.argv) > 0 && call.argv[0] == "tar" {
			tarProc = p
		}
		return p
	}

	payload := []byte("fake-tar-bytes")
	err := env.CopyTarStream(context.Background(), "/home/tenki/act", bytes.NewReader(payload))
	require.NoError(t, err)
	require.NotNil(t, tarProc)
	assert.Equal(t, string(payload), tarProc.stdin.String())
	assert.True(t, tarProc.stdin.closed)
	// A mkdir/rm housekeeping command ran before the tar command.
	require.GreaterOrEqual(t, len(sess.runs), 2)
	assert.Equal(t, "sh", sess.runs[0].argv[0])
	assert.Equal(t, "tar", sess.runs[1].argv[0])
}

func TestTenkiEnvironment_ExecRunError(t *testing.T) {
	env, _, sess := newTestEnv()
	require.NoError(t, env.Create(nil, nil)(context.Background()))
	sess.procFor = nil
	// override Run to fail
	failing := &failingSession{}
	env.session = failing
	err := env.Exec([]string{"x"}, nil, "", "")(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "run failed")
}

type failingSession struct{ fakeSession }

func (s *failingSession) Run(_ context.Context, _ []string, _ map[string]string, _ string) (tenkiProcess, error) {
	return nil, errors.New("run failed")
}
