// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"strings"
	"sync"
	"time"

	"code.forgejo.org/forgejo/runner/v13/act/common"
	"code.forgejo.org/forgejo/runner/v13/act/container"
	pluginv1alpha "code.forgejo.org/forgejo/runner/v13/act/plugin/proto/v1alpha"
	"google.golang.org/protobuf/types/known/durationpb"
)

const copyChunkSize = 256 * 1024 // 256 KB

// pluginEnvironment is not safe for concurrent use; one goroutine per env.
type pluginEnvironment struct {
	client      pluginv1alpha.BackendPluginClient
	caps        *pluginv1alpha.CapabilitiesResponse
	backendOpts map[string]string
	labelArg    string
	timeout     time.Duration
	input       *container.NewContainerInput
	services    []*pluginv1alpha.ServiceContainer
	imageEnv    map[string]string

	// State that is only initialized after an environment is created:
	envCreated *createdEnvironment

	// mu guards stdout/stderr swaps against in-flight Exec writes.
	mu     sync.Mutex
	stdout io.Writer
	stderr io.Writer
}

type createdEnvironment struct {
	envID                        string
	rootPath                     string
	actPath                      string
	toolCachePath                string
	tempPath                     string
	envVariables                 []string // env variables defined by the runner after an image is created, to be passed into each exec
	pathVariableName             string
	defaultPathVariable          string
	pathSeparator                string
	environmentOS                string
	environmentArch              string
	isEnvironmentCaseInsensitive bool
}

// ExecError carries the remote exit code and optional message from Exec.
type ExecError struct {
	ExitCode int32
	Message  string
}

func (e *ExecError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("plugin exec: %s", e.Message)
	}
	return fmt.Sprintf("plugin exec: exit code %d", e.ExitCode)
}

var (
	_ container.ExecutionsEnvironment = (*pluginEnvironment)(nil)
	_ container.ServiceAdder          = (*pluginEnvironment)(nil)
)

func (p *pluginEnvironment) AddServiceContainerRaw(name, image string, env map[string]string, ports []string) {
	p.services = append(p.services, &pluginv1alpha.ServiceContainer{
		Name:  name,
		Image: image,
		Env:   env,
		Ports: ports,
	})
}

func (p *pluginEnvironment) BackendID() string {
	return p.caps.GetName()
}

// SupportsDockerContainerActions is false: a plugin-managed environment does
// not run `uses: docker://` or container actions.
func (p *pluginEnvironment) SupportsDockerContainerActions() bool {
	return false
}

// ManagesOwnNetworking is true: a plugin owns connectivity for its environment,
// so the runner does not create or attach a Docker-style network.
func (p *pluginEnvironment) ManagesOwnNetworking() bool {
	return true
}

func (p *pluginEnvironment) GetName() string {
	return p.caps.GetName()
}

func (p *pluginEnvironment) GetRoot() string {
	if p.envCreated == nil {
		panic("accessed pluginEnvironment.GetRoot() before environment creation")
	}
	return p.envCreated.rootPath
}

func (p *pluginEnvironment) GetActPath() string {
	if p.envCreated == nil {
		panic("accessed pluginEnvironment.GetActPath() before environment creation")
	}
	return p.envCreated.actPath
}

func (p *pluginEnvironment) GetPathVariableName() string {
	if p.envCreated == nil {
		panic("accessed pluginEnvironment.GetPathVariableName() before environment creation")
	}
	if v := p.envCreated.pathVariableName; v != "" {
		return v
	}
	return "PATH"
}

func (p *pluginEnvironment) DefaultPathVariable() string {
	if p.envCreated == nil {
		panic("accessed pluginEnvironment.DefaultPathVariable() before environment creation")
	}
	if v := p.envCreated.defaultPathVariable; v != "" {
		return v
	}
	return "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
}

func (p *pluginEnvironment) JoinPathVariable(paths ...string) string {
	if p.envCreated == nil {
		panic("accessed pluginEnvironment.JoinPathVariable() before environment creation")
	}
	sep := p.envCreated.pathSeparator
	if sep == "" {
		sep = ":"
	}
	return strings.Join(paths, sep)
}

func (p *pluginEnvironment) GetRunnerContext(_ context.Context) map[string]any {
	if p.envCreated == nil {
		panic("accessed pluginEnvironment.GetRunnerContext() before environment creation")
	}
	return map[string]any{
		"os":         p.envCreated.environmentOS,
		"arch":       p.envCreated.environmentArch,
		"temp":       p.envCreated.tempPath,
		"tool_cache": p.envCreated.toolCachePath,
	}
}

func (p *pluginEnvironment) IsEnvironmentCaseInsensitive() bool {
	if p.envCreated == nil {
		panic("accessed IsEnvironmentCaseInsensitive before environment creation")
	}
	return p.envCreated.isEnvironmentCaseInsensitive
}

func (p *pluginEnvironment) ToContainerPath(path string) string {
	return path
}

// newCreateRequest maps the container input into a Create request. Callers set
// path-specific fields (cap add/drop, services) afterwards.
func newCreateRequest(input *container.NewContainerInput, backendOpts map[string]string, labelArg string, timeout time.Duration) *pluginv1alpha.CreateRequest {
	return &pluginv1alpha.CreateRequest{
		Image:              input.Image,
		Name:               input.Name,
		BackendOptions:     backendOpts,
		LabelArg:           labelArg,
		EnvironmentTimeout: durationpb.New(timeout),
	}
}

// envSliceToMap converts docker-style KEY=VALUE entries into a map, last value
// winning on duplicate keys. An entry without '=' maps the whole entry to "".
func envSliceToMap(env []string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	m := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		m[k] = v
	}
	return m
}

func (p *pluginEnvironment) Create(capAdd, capDrop []string) common.Executor {
	return func(ctx context.Context) error {
		if p.envCreated != nil {
			panic("Create()() invoked on a plugin environment that has already had Create invoked")
		}
		req := newCreateRequest(p.input, p.backendOpts, p.labelArg, p.timeout)
		req.CapAdd = capAdd
		req.CapDrop = capDrop
		req.Services = p.services
		resp, err := p.client.Create(ctx, req)
		if err != nil {
			return fmt.Errorf("plugin create: %w", err)
		}
		p.envCreated = &createdEnvironment{
			envID:         resp.GetEnvironmentId(),
			rootPath:      resp.GetRootPath(),
			actPath:       resp.GetActPath(),
			toolCachePath: resp.GetToolCachePath(),
			tempPath:      resp.GetTempPath(),
			envVariables: []string{
				fmt.Sprintf("RUNNER_TOOL_CACHE=%s", resp.GetToolCachePath()),
				fmt.Sprintf("RUNNER_TEMP=%s", resp.GetTempPath()),
				fmt.Sprintf("RUNNER_OS=%s", resp.GetOs()),
				fmt.Sprintf("RUNNER_ARCH=%s", resp.GetArch()),
			},
			pathVariableName:             resp.GetPathVariableName(),
			defaultPathVariable:          resp.GetDefaultPathVariable(),
			pathSeparator:                resp.GetPathSeparator(),
			environmentOS:                resp.GetOs(),
			environmentArch:              resp.GetArch(),
			isEnvironmentCaseInsensitive: resp.GetEnvironmentCaseInsensitive(),
		}
		return nil
	}
}

func (p *pluginEnvironment) Start(_ bool) common.Executor {
	return func(ctx context.Context) error {
		if p.envCreated == nil {
			panic("accessed pluginEnvironment.Start()(...) before environment creation")
		}

		// Cancel on early return so the client-side stream goroutines exit.
		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		stream, err := p.client.Start(streamCtx, &pluginv1alpha.StartRequest{
			EnvironmentId: p.envCreated.envID,
		})
		if err != nil {
			return fmt.Errorf("plugin start: request error %w", err)
		}

		done := false
		for {
			out, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return fmt.Errorf("plugin start: stream error %w", err)
			}

			switch v := out.GetOutput().(type) {
			case *pluginv1alpha.StartOutput_Data:
				p.mu.Lock()
				switch v.Data.GetStream() {
				case pluginv1alpha.DataChunk_STDOUT:
					_, _ = p.stdout.Write(v.Data.GetData())
				case pluginv1alpha.DataChunk_STDERR:
					_, _ = p.stderr.Write(v.Data.GetData())
				}
				p.mu.Unlock()

			case *pluginv1alpha.StartOutput_StartComplete:
				p.imageEnv = v.StartComplete.GetImageEnv()
				done = true

			default:
				return fmt.Errorf("plugin start: unexpected stream chunk %#v", v)
			}
			if done {
				break
			}
		}

		if !done {
			return fmt.Errorf("plugin start: stream ended before completion signal")
		}
		return nil
	}
}

func (p *pluginEnvironment) Pull(_ bool) common.Executor {
	return common.NewInfoExecutor("plugin manages image pull internally")
}

func (p *pluginEnvironment) ConnectToNetwork(_ string) common.Executor {
	return common.NewInfoExecutor("plugin manages networking internally")
}

func (p *pluginEnvironment) Exec(command []string, env map[string]string, user, workdir string) common.Executor {
	return func(ctx context.Context) error {
		if p.envCreated == nil {
			panic("accessed pluginEnvironment.Exec()(...) before environment creation")
		}

		// Cancel on early return so the client-side stream goroutines exit.
		streamCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		// Start with `envVariables` which are the static values the runner defines, and then add in the environment-level
		// env vars, and then the specific env settings requested for this exec.
		finalEnv := envSliceToMap(p.envCreated.envVariables)
		maps.Copy(finalEnv, envSliceToMap(p.input.Env))
		maps.Copy(finalEnv, env)

		req := &pluginv1alpha.ExecRequest{
			EnvironmentId: p.envCreated.envID,
			Command:       command,
			Env:           finalEnv,
		}
		if user != "" {
			req.User = &user
		}
		if workdir != "" {
			req.Workdir = workdir
		} else {
			req.Workdir = p.input.WorkingDir
		}
		stream, err := p.client.Exec(streamCtx, req)
		if err != nil {
			return fmt.Errorf("plugin exec: %w", err)
		}

		var (
			exitCode     int32
			errorMessage string
			done         bool
		)
		for {
			out, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return fmt.Errorf("plugin exec stream: %w", err)
			}

			switch v := out.GetOutput().(type) {
			case *pluginv1alpha.ExecOutput_Data:
				p.mu.Lock()
				switch v.Data.GetStream() {
				case pluginv1alpha.DataChunk_STDOUT:
					_, _ = p.stdout.Write(v.Data.GetData())
				case pluginv1alpha.DataChunk_STDERR:
					_, _ = p.stderr.Write(v.Data.GetData())
				}
				p.mu.Unlock()

			case *pluginv1alpha.ExecOutput_ExecComplete:
				exitCode = v.ExecComplete.GetExitCode()
				done = true

			case *pluginv1alpha.ExecOutput_ExecFailed:
				errorMessage = v.ExecFailed.GetErrorMessage()
				done = true

			default:
				return fmt.Errorf("plugin exec: unexpected stream chunk %#v", v)
			}
			if done {
				break
			}
		}

		if !done {
			return fmt.Errorf("plugin exec: stream ended before completion signal")
		}
		if exitCode != 0 || errorMessage != "" {
			return &ExecError{ExitCode: exitCode, Message: errorMessage}
		}
		return nil
	}
}

func (p *pluginEnvironment) Copy(destPath string, files ...*container.FileEntry) common.Executor {
	return func(ctx context.Context) error {
		// Stream the tar so large payloads do not stay buffered in memory.
		pr, pw := io.Pipe()
		defer pr.Close()
		go func() {
			tw := tar.NewWriter(pw)
			for _, f := range files {
				if err := tw.WriteHeader(&tar.Header{
					Name: f.Name,
					Mode: f.Mode,
					Size: int64(len(f.Body)),
				}); err != nil {
					pw.CloseWithError(fmt.Errorf("plugin copy tar header: %w", err))
					return
				}
				if _, err := tw.Write([]byte(f.Body)); err != nil {
					pw.CloseWithError(fmt.Errorf("plugin copy tar write: %w", err))
					return
				}
			}
			if err := tw.Close(); err != nil {
				pw.CloseWithError(err)
				return
			}
			pw.Close()
		}()
		return p.streamCopyIn(ctx, destPath, pr)
	}
}

func (p *pluginEnvironment) CopyDir(destPath, srcPath string, _ bool) common.Executor {
	return func(ctx context.Context) error {
		pr, pw := io.Pipe()
		defer pr.Close()
		go func() {
			tw := tar.NewWriter(pw)
			if err := tw.AddFS(os.DirFS(srcPath)); err != nil {
				pw.CloseWithError(err)
				return
			}
			if err := tw.Close(); err != nil {
				pw.CloseWithError(err)
				return
			}
			pw.Close()
		}()
		return p.streamCopyIn(ctx, destPath, pr)
	}
}

func (p *pluginEnvironment) CopyTarStream(ctx context.Context, destPath string, tarStream io.Reader) error {
	return p.streamCopyIn(ctx, destPath, tarStream)
}

func (p *pluginEnvironment) streamCopyIn(ctx context.Context, destPath string, r io.Reader) error {
	if p.envCreated == nil {
		panic("accessed pluginEnvironment.streamCopyIn() before environment creation")
	}

	stream, err := p.client.CopyIn(ctx)
	if err != nil {
		return fmt.Errorf("plugin copyin: %w", err)
	}

	// On client-streaming RPCs, Send returns io.EOF once the server has
	// closed the stream; the real error only surfaces via CloseAndRecv.
	sendChunk := func(chunk *pluginv1alpha.CopyInChunk) error {
		if err := stream.Send(chunk); err != nil {
			if errors.Is(err, io.EOF) {
				if _, recvErr := stream.CloseAndRecv(); recvErr != nil {
					return fmt.Errorf("plugin copyin: %w", recvErr)
				}
				return fmt.Errorf("plugin copyin: stream closed unexpectedly")
			}
			return fmt.Errorf("plugin copyin send: %w", err)
		}
		return nil
	}

	// Send the header unconditionally so empty payloads still convey envID/dest.
	if err := sendChunk(&pluginv1alpha.CopyInChunk{
		EnvironmentId: &p.envCreated.envID,
		DestPath:      &destPath,
	}); err != nil {
		return err
	}

	buf := make([]byte, copyChunkSize)
	for {
		n, readErr := r.Read(buf)
		if n > 0 {
			if err := sendChunk(&pluginv1alpha.CopyInChunk{Data: buf[:n]}); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("plugin copyin read: %w", readErr)
		}
	}

	if _, err := stream.CloseAndRecv(); err != nil {
		return fmt.Errorf("plugin copyin close: %w", err)
	}
	return nil
}

func (p *pluginEnvironment) GetContainerArchive(ctx context.Context, srcPath string) (io.ReadCloser, error) {
	if p.envCreated == nil {
		panic("accessed pluginEnvironment.streamCopyIn() before environment creation")
	}

	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := p.client.CopyOut(streamCtx, &pluginv1alpha.CopyOutRequest{
		EnvironmentId: p.envCreated.envID,
		SrcPath:       srcPath,
	})
	if err != nil {
		cancel()
		return nil, fmt.Errorf("plugin copyout: %w", err)
	}

	pr, pw := io.Pipe()
	go func() {
		defer cancel()
		for {
			chunk, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				pw.Close()
				return
			}
			if err != nil {
				pw.CloseWithError(fmt.Errorf("plugin copyout stream: %w", err))
				return
			}
			if _, err := pw.Write(chunk.GetData()); err != nil {
				// Reader closed: cancel via defer so stream.Recv unblocks.
				pw.CloseWithError(err)
				return
			}
		}
	}()

	return pr, nil
}

func (p *pluginEnvironment) UpdateFromEnv(srcPath string, env *map[string]string) common.Executor {
	// Read the env file out of the environment (CopyOut) and parse it here; the
	// runner owns the merge rather than the plugin.
	return container.ParseEnvFile(p, srcPath, env)
}

func (p *pluginEnvironment) UpdateFromImageEnv(env *map[string]string) common.Executor {
	return func(_ context.Context) error {
		if p.imageEnv == nil {
			return nil
		}
		envMap := *env
		pathVar := p.GetPathVariableName()
		sep := p.envCreated.pathSeparator
		if sep == "" {
			sep = ":"
		}
		for k, v := range p.imageEnv {
			if k == pathVar {
				if envMap[k] == "" {
					envMap[k] = v
				} else {
					envMap[k] += sep + v
				}
			} else if envMap[k] == "" {
				envMap[k] = v
			}
		}
		*env = envMap
		return nil
	}
}

// IsHealthy is a no-op for plugins: the v1alpha protocol has no health RPC, so
// the runner does not poll liveness of a plugin-managed environment.
func (p *pluginEnvironment) IsHealthy(_ context.Context) (time.Duration, error) {
	return 0, nil
}

func (p *pluginEnvironment) Remove() common.Executor {
	return func(ctx context.Context) error {
		if p.envCreated == nil {
			return nil
		}

		_, err := p.client.Remove(ctx, &pluginv1alpha.RemoveRequest{
			EnvironmentId: p.envCreated.envID,
		})
		if err != nil {
			return fmt.Errorf("plugin remove: %w", err)
		}
		return nil
	}
}

func (p *pluginEnvironment) Close() common.Executor {
	return func(_ context.Context) error {
		return nil
	}
}

func (p *pluginEnvironment) ReplaceLogWriter(stdout, stderr io.Writer) (io.Writer, io.Writer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	oldOut, oldErr := p.stdout, p.stderr
	p.stdout = stdout
	p.stderr = stderr
	return oldOut, oldErr
}
