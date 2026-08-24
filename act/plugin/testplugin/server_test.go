// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package testplugin

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"runtime"
	"testing"

	pluginv1alpha "code.forgejo.org/forgejo/runner/v13/act/plugin/proto/v1alpha"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

const bufSize = 1024 * 1024

func startServer(t *testing.T) (pluginv1alpha.BackendPluginClient, *Server) {
	t.Helper()

	lis := bufconn.Listen(bufSize)
	srv := grpc.NewServer()
	ts := New()
	pluginv1alpha.RegisterBackendPluginServer(srv, ts)
	t.Cleanup(func() {
		srv.Stop()
		ts.Cleanup()
	})

	go func() { _ = srv.Serve(lis) }()

	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })

	return pluginv1alpha.NewBackendPluginClient(conn), ts
}

func createEnv(t *testing.T, client pluginv1alpha.BackendPluginClient) string {
	t.Helper()
	resp, err := client.Create(t.Context(), &pluginv1alpha.CreateRequest{
		Image: "test:latest",
		Name:  "test",
	})
	require.NoError(t, err)
	_, err = client.Start(t.Context(), &pluginv1alpha.StartRequest{EnvironmentId: resp.GetEnvironmentId()})
	require.NoError(t, err)
	assert.Equal(t, "/shared", resp.GetRootPath())
	assert.Equal(t, "/shared/act", resp.GetActPath())
	assert.Equal(t, "/shared/toolcache", resp.GetToolCachePath())
	assert.Equal(t, "/tmp", resp.GetTempPath())
	return resp.GetEnvironmentId()
}

func TestCapabilities(t *testing.T) {
	client, _ := startServer(t)
	caps, err := client.Capabilities(t.Context(), &pluginv1alpha.CapabilitiesRequest{})
	require.NoError(t, err)

	assert.Equal(t, "test", caps.GetName())
}

func TestCreateAndRemove(t *testing.T) {
	client, ts := startServer(t)
	envID := createEnv(t, client)
	assert.Contains(t, ts.envs, envID)

	_, err := client.Remove(t.Context(), &pluginv1alpha.RemoveRequest{EnvironmentId: envID})
	require.NoError(t, err)
	assert.NotContains(t, ts.envs, envID)
}

func TestExec_Echo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("echo command differs on Windows")
	}
	client, _ := startServer(t)
	envID := createEnv(t, client)

	stream, err := client.Exec(t.Context(), &pluginv1alpha.ExecRequest{
		EnvironmentId: envID,
		Command:       []string{"echo", "hello world"},
	})
	require.NoError(t, err)

	var stdout bytes.Buffer
	exitCode := int32(-1) // initialize to non-zero so that assert.Equal validates it is set to 0
	for {
		out, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		breakLoop := false
		switch v := out.GetOutput().(type) {
		case *pluginv1alpha.ExecOutput_Data:
			if v.Data.GetStream() == pluginv1alpha.DataChunk_STDOUT {
				stdout.Write(v.Data.GetData())
			}
		case *pluginv1alpha.ExecOutput_ExecComplete:
			exitCode = v.ExecComplete.GetExitCode()
			breakLoop = true
		default:
			assert.Failf(t, "unexpected output type", "did not expect %#v", v)
		}
		if breakLoop {
			break
		}
	}

	assert.Equal(t, int32(0), exitCode)
	assert.Equal(t, "hello world\n", stdout.String())
}

func TestExec_FailingCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("false command differs on Windows")
	}
	client, _ := startServer(t)
	envID := createEnv(t, client)

	stream, err := client.Exec(t.Context(), &pluginv1alpha.ExecRequest{
		EnvironmentId: envID,
		Command:       []string{"false"},
	})
	require.NoError(t, err)

	exitCode := int32(-1) // initialize to non-zero so that assert.Equal validates it is set to 0
	for {
		out, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		breakLoop := false
		switch v := out.GetOutput().(type) {
		case *pluginv1alpha.ExecOutput_Data:
		case *pluginv1alpha.ExecOutput_ExecComplete:
			exitCode = v.ExecComplete.GetExitCode()
			breakLoop = true
		default:
			assert.Failf(t, "unexpected output type", "did not expect %#v", v)
		}
		if breakLoop {
			break
		}
	}
	assert.NotEqual(t, int32(0), exitCode)
}

func TestExec_EnvVars(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("env command differs on Windows")
	}
	client, _ := startServer(t)
	envID := createEnv(t, client)

	stream, err := client.Exec(t.Context(), &pluginv1alpha.ExecRequest{
		EnvironmentId: envID,
		Command:       []string{"sh", "-c", "echo $TEST_VAR $EXTRA"},
		Env: map[string]string{
			"TEST_VAR": "hello",
			"EXTRA":    "world",
		},
	})
	require.NoError(t, err)

	var stdout bytes.Buffer
	for {
		out, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		breakLoop := false
		switch v := out.GetOutput().(type) {
		case *pluginv1alpha.ExecOutput_Data:
			if v.Data.GetStream() == pluginv1alpha.DataChunk_STDOUT {
				stdout.Write(v.Data.GetData())
			}
		case *pluginv1alpha.ExecOutput_ExecComplete:
			breakLoop = true
		default:
			assert.Failf(t, "unexpected output type", "did not expect %#v", v)
		}
		if breakLoop {
			break
		}
	}
	assert.Equal(t, "hello world\n", stdout.String())
}

func TestExec_TranslatesEnvironmentPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command differs on Windows")
	}
	client, _ := startServer(t)
	envID := createEnv(t, client)

	stream, err := client.Exec(t.Context(), &pluginv1alpha.ExecRequest{
		EnvironmentId: envID,
		Command:       []string{"sh", "-c", `mkdir -p "$(dirname "$FORGEJO_OUTPUT")" && echo "result=value" >> "$FORGEJO_OUTPUT"`},
		Env: map[string]string{
			"FORGEJO_OUTPUT": "/shared/act/workflow/outputcmd.txt",
		},
	})
	require.NoError(t, err)
	for {
		out, err := stream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)

		breakLoop := false
		switch v := out.GetOutput().(type) {
		case *pluginv1alpha.ExecOutput_Data:
		case *pluginv1alpha.ExecOutput_ExecComplete:
			assert.Equal(t, int32(0), v.ExecComplete.GetExitCode())
			breakLoop = true
		default:
			assert.Failf(t, "unexpected output type", "did not expect %#v", v)
		}
		if breakLoop {
			break
		}
	}

	copyOutStream, err := client.CopyOut(t.Context(), &pluginv1alpha.CopyOutRequest{
		EnvironmentId: envID,
		SrcPath:       "/shared/act/workflow/outputcmd.txt",
	})
	require.NoError(t, err)
	var tarBuf bytes.Buffer
	for {
		chunk, err := copyOutStream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		tarBuf.Write(chunk.GetData())
	}
	tr := tar.NewReader(&tarBuf)
	_, err = tr.Next()
	require.NoError(t, err)
	content, err := io.ReadAll(tr)
	require.NoError(t, err)
	assert.Equal(t, "result=value\n", string(content))
}

func TestCopyIn_AndCopyOut(t *testing.T) {
	client, _ := startServer(t)
	envID := createEnv(t, client)

	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	content := []byte("test content 123")
	_ = tw.WriteHeader(&tar.Header{Name: "test.txt", Mode: 0o644, Size: int64(len(content))})
	_, _ = tw.Write(content)
	tw.Close()

	copyInStream, err := client.CopyIn(t.Context())
	require.NoError(t, err)
	err = copyInStream.Send(&pluginv1alpha.CopyInChunk{
		EnvironmentId: proto.String(envID),
		DestPath:      proto.String("/shared/workdir"),
		Data:          tarBuf.Bytes(),
	})
	require.NoError(t, err)
	_, err = copyInStream.CloseAndRecv()
	require.NoError(t, err)

	copyOutStream, err := client.CopyOut(t.Context(), &pluginv1alpha.CopyOutRequest{
		EnvironmentId: envID,
		SrcPath:       "/shared/workdir/test.txt",
	})
	require.NoError(t, err)

	var outBuf bytes.Buffer
	for {
		chunk, err := copyOutStream.Recv()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		outBuf.Write(chunk.GetData())
	}

	tr := tar.NewReader(&outBuf)
	hdr, err := tr.Next()
	require.NoError(t, err)
	assert.Equal(t, "test.txt", hdr.Name)
	data, _ := io.ReadAll(tr)
	assert.Equal(t, "test content 123", string(data))
}

func TestCopyPathsAreScopedToEnvironment(t *testing.T) {
	client, _ := startServer(t)
	firstEnvID := createEnv(t, client)
	secondEnvID := createEnv(t, client)

	writeFile := func(envID, content string) {
		t.Helper()
		var tarBuf bytes.Buffer
		tw := tar.NewWriter(&tarBuf)
		require.NoError(t, tw.WriteHeader(&tar.Header{Name: "value.txt", Mode: 0o644, Size: int64(len(content))}))
		_, err := tw.Write([]byte(content))
		require.NoError(t, err)
		require.NoError(t, tw.Close())

		stream, err := client.CopyIn(t.Context())
		require.NoError(t, err)
		require.NoError(t, stream.Send(&pluginv1alpha.CopyInChunk{
			EnvironmentId: proto.String(envID),
			DestPath:      proto.String("/shared/workdir"),
			Data:          tarBuf.Bytes(),
		}))
		_, err = stream.CloseAndRecv()
		require.NoError(t, err)
	}
	readFile := func(envID string) string {
		t.Helper()
		stream, err := client.CopyOut(t.Context(), &pluginv1alpha.CopyOutRequest{
			EnvironmentId: envID,
			SrcPath:       "/shared/workdir/value.txt",
		})
		require.NoError(t, err)
		var tarBuf bytes.Buffer
		for {
			chunk, err := stream.Recv()
			if err == io.EOF {
				break
			}
			require.NoError(t, err)
			tarBuf.Write(chunk.GetData())
		}
		tr := tar.NewReader(&tarBuf)
		_, err = tr.Next()
		require.NoError(t, err)
		content, err := io.ReadAll(tr)
		require.NoError(t, err)
		return string(content)
	}

	writeFile(firstEnvID, "first")
	writeFile(secondEnvID, "second")

	assert.Equal(t, "first", readFile(firstEnvID))
	assert.Equal(t, "second", readFile(secondEnvID))
}

func TestRemove_NotFound(t *testing.T) {
	client, _ := startServer(t)
	_, err := client.Remove(t.Context(), &pluginv1alpha.RemoveRequest{EnvironmentId: "bogus"})
	assert.Error(t, err)
}

func TestRemove_CleansUpTempDir(t *testing.T) {
	client, ts := startServer(t)
	envID := createEnv(t, client)

	ts.mu.Lock()
	root := ts.envs[envID].root
	ts.mu.Unlock()

	_, err := os.Stat(root)
	require.NoError(t, err)

	_, err = client.Remove(t.Context(), &pluginv1alpha.RemoveRequest{EnvironmentId: envID})
	require.NoError(t, err)

	_, err = os.Stat(root)
	assert.True(t, os.IsNotExist(err))
}
