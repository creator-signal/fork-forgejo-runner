// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package testplugin

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	pluginv1alpha "code.forgejo.org/forgejo/runner/v13/act/plugin/proto/v1alpha"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

type environment struct {
	root    string // temp directory root
	workdir string
}

type Server struct {
	pluginv1alpha.UnimplementedBackendPluginServer

	mu   sync.Mutex
	envs map[string]*environment
	seq  int
}

func New() *Server {
	return &Server{
		envs: make(map[string]*environment),
	}
}

// Register wires this server plus a SERVING grpc.health.v1 onto grpcServer.
func (s *Server) Register(grpcServer *grpc.Server) {
	pluginv1alpha.RegisterBackendPluginServer(grpcServer, s)
	healthSrv := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthSrv)
	healthSrv.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
}

func (s *Server) getEnv(id string) (*environment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	env, ok := s.envs[id]
	if !ok {
		return nil, status.Errorf(codes.NotFound, "environment %q not found", id)
	}
	return env, nil
}

// resolvePath maps canonical environment paths into the temp root.
func resolvePath(env *environment, p string) string {
	return filepath.Join(env.root, p)
}

// resolveValue translates canonical paths embedded in command arguments and
// environment values for this host-backed reference implementation.
func resolveValue(env *environment, value string) string {
	return strings.ReplaceAll(value, "/shared", filepath.Join(env.root, "shared"))
}

func (s *Server) Capabilities(_ context.Context, _ *pluginv1alpha.CapabilitiesRequest) (*pluginv1alpha.CapabilitiesResponse, error) {
	return &pluginv1alpha.CapabilitiesResponse{
		Name:                       "test",
		EnvironmentCaseInsensitive: runtime.GOOS == "windows",
		Os:                         "Linux",
		Arch:                       "x86_64",
	}, nil
}

func (s *Server) Create(_ context.Context, req *pluginv1alpha.CreateRequest) (*pluginv1alpha.CreateResponse, error) {
	root, err := os.MkdirTemp("", "testplugin-*")
	if err != nil {
		return nil, status.Errorf(codes.Internal, "create tmpdir: %v", err)
	}

	workdir := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		os.RemoveAll(root)
		return nil, status.Errorf(codes.Internal, "create workdir: %v", err)
	}

	for _, dir := range []string{"shared/act", "shared/toolcache", "shared/workdir"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			os.RemoveAll(root)
			return nil, status.Errorf(codes.Internal, "create %s: %v", dir, err)
		}
	}

	s.mu.Lock()
	s.seq++
	envID := fmt.Sprintf("test-env-%d", s.seq)
	s.envs[envID] = &environment{
		root:    root,
		workdir: workdir,
	}
	s.mu.Unlock()

	return &pluginv1alpha.CreateResponse{
		EnvironmentId: envID,
		RootPath:      "/shared",
		ActPath:       "/shared/act",
		ToolCachePath: "/shared/toolcache",
		TempPath:      "/tmp",
	}, nil
}

func (s *Server) Start(_ context.Context, req *pluginv1alpha.StartRequest) (*pluginv1alpha.StartResponse, error) {
	_, err := s.getEnv(req.GetEnvironmentId())
	if err != nil {
		return nil, err
	}

	imageEnv := map[string]string{
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	return &pluginv1alpha.StartResponse{ImageEnv: imageEnv}, nil
}

func (s *Server) Exec(req *pluginv1alpha.ExecRequest, stream grpc.ServerStreamingServer[pluginv1alpha.ExecOutput]) error {
	env, err := s.getEnv(req.GetEnvironmentId())
	if err != nil {
		return err
	}

	command := make([]string, len(req.GetCommand()))
	for i, arg := range req.GetCommand() {
		command[i] = resolveValue(env, arg)
	}
	if len(command) == 0 {
		return sendExecDone(stream, 1, "empty command")
	}

	wd := req.GetWorkdir()
	if wd == "" {
		wd = env.workdir
	} else {
		wd = resolvePath(env, wd)
	}
	if err := os.MkdirAll(wd, 0o755); err != nil {
		return sendExecDone(stream, 1, fmt.Sprintf("create workdir: %v", err))
	}

	envList := os.Environ()
	for k, v := range req.GetEnv() {
		envList = append(envList, k+"="+resolveValue(env, v))
	}

	cmd := exec.CommandContext(stream.Context(), command[0], command[1:]...)
	cmd.Dir = wd
	cmd.Env = envList

	var stdoutMu sync.Mutex
	cmd.Stdout = &execStreamWriter{mu: &stdoutMu, stream: stream, streamType: pluginv1alpha.ExecOutput_STDOUT}
	cmd.Stderr = &execStreamWriter{mu: &stdoutMu, stream: stream, streamType: pluginv1alpha.ExecOutput_STDERR}

	runErr := cmd.Run()

	exitCode := int32(0)
	errorMsg := ""
	if runErr != nil {
		exitCode = 1
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = int32(exitErr.ExitCode())
		} else {
			errorMsg = runErr.Error()
		}
	}

	return sendExecDone(stream, exitCode, errorMsg)
}

func sendExecDone(stream grpc.ServerStreamingServer[pluginv1alpha.ExecOutput], exitCode int32, errorMsg string) error {
	out := &pluginv1alpha.ExecOutput{Done: true, ExitCode: &exitCode}
	if errorMsg != "" {
		out.ErrorMessage = &errorMsg
	}
	return stream.Send(out)
}

func (s *Server) CopyIn(stream grpc.ClientStreamingServer[pluginv1alpha.CopyInChunk, pluginv1alpha.CopyInResponse]) error {
	first, err := stream.Recv()
	if err != nil {
		return status.Errorf(codes.InvalidArgument, "copyin recv: %v", err)
	}

	env, err := s.getEnv(first.GetEnvironmentId())
	if err != nil {
		return err
	}

	destPath := resolvePath(env, first.GetDestPath())

	var buf bytes.Buffer
	if len(first.GetData()) > 0 {
		buf.Write(first.GetData())
	}
	for {
		chunk, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return status.Errorf(codes.Internal, "copyin recv: %v", err)
		}
		buf.Write(chunk.GetData())
	}

	if err := extractTar(destPath, &buf); err != nil {
		return status.Errorf(codes.Internal, "copyin extract: %v", err)
	}

	return stream.SendAndClose(&pluginv1alpha.CopyInResponse{})
}

func (s *Server) CopyOut(req *pluginv1alpha.CopyOutRequest, stream grpc.ServerStreamingServer[pluginv1alpha.CopyOutChunk]) error {
	env, err := s.getEnv(req.GetEnvironmentId())
	if err != nil {
		return err
	}

	srcPath := resolvePath(env, req.GetSrcPath())

	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	fi, err := os.Stat(srcPath)
	if err != nil {
		return status.Errorf(codes.NotFound, "copyout stat: %v", err)
	}

	if fi.IsDir() {
		err = filepath.WalkDir(srcPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(srcPath, path)
			info, err := d.Info()
			if err != nil {
				return err
			}
			hdr, err := tar.FileInfoHeader(info, "")
			if err != nil {
				return err
			}
			hdr.Name = rel
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = io.Copy(tw, f)
			return err
		})
	} else {
		hdr, hdrErr := tar.FileInfoHeader(fi, "")
		if hdrErr != nil {
			return status.Errorf(codes.Internal, "copyout header: %v", hdrErr)
		}
		hdr.Name = fi.Name()
		if err := tw.WriteHeader(hdr); err != nil {
			return status.Errorf(codes.Internal, "copyout header: %v", err)
		}
		f, err := os.Open(srcPath)
		if err != nil {
			return status.Errorf(codes.Internal, "copyout open: %v", err)
		}
		defer f.Close()
		if _, err := io.Copy(tw, f); err != nil {
			return status.Errorf(codes.Internal, "copyout copy: %v", err)
		}
	}
	if err != nil {
		return status.Errorf(codes.Internal, "copyout walk: %v", err)
	}
	tw.Close()

	data := buf.Bytes()
	const chunkSize = 256 * 1024
	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}
		if err := stream.Send(&pluginv1alpha.CopyOutChunk{Data: data[i:end]}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Server) Remove(_ context.Context, req *pluginv1alpha.RemoveRequest) (*pluginv1alpha.RemoveResponse, error) {
	envID := req.GetEnvironmentId()

	s.mu.Lock()
	env, ok := s.envs[envID]
	if ok {
		delete(s.envs, envID)
	}
	s.mu.Unlock()

	if !ok {
		return nil, status.Errorf(codes.NotFound, "environment %q not found", envID)
	}

	os.RemoveAll(env.root)
	return &pluginv1alpha.RemoveResponse{}, nil
}

func (s *Server) Cleanup() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, env := range s.envs {
		os.RemoveAll(env.root)
	}
	clear(s.envs)
}

type execStreamWriter struct {
	mu         *sync.Mutex
	stream     grpc.ServerStreamingServer[pluginv1alpha.ExecOutput]
	streamType pluginv1alpha.ExecOutput_Stream
}

func (w *execStreamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.stream.Send(&pluginv1alpha.ExecOutput{
		Stream: w.streamType,
		Data:   p,
	}); err != nil {
		return 0, err
	}
	return len(p), nil
}

func extractTar(destPath string, r io.Reader) error {
	if err := os.MkdirAll(destPath, 0o755); err != nil {
		return err
	}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(destPath, filepath.Clean(hdr.Name))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fs.FileMode(hdr.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		case tar.TypeSymlink:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			os.Remove(target)
			if err := os.Symlink(hdr.Linkname, target); err != nil {
				return err
			}
		}
	}
}

var _ pluginv1alpha.BackendPluginServer = (*Server)(nil)
