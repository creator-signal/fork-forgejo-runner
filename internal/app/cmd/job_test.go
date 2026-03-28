// Copyright 2026 The Forgejo Authors
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/runner/v12/act/cacheproxy"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	mock_runner "code.forgejo.org/forgejo/runner/v12/internal/app/run/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	mock_client "code.forgejo.org/forgejo/runner/v12/internal/pkg/client/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	"code.forgejo.org/forgejo/runner/v12/testutils"
	"connectrpc.com/connect"
	"github.com/powerman/fileuri"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestRunJob(t *testing.T) {
	rawConfig := `
cache:
  enabled: false
server:
  connections:
    example:
      url: https://example.com/forgejo
      uuid: 41414141-4141-4141-4141-414141414141
      token: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
`

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	err := os.WriteFile(configPath, []byte(rawConfig), 0o644)
	require.NoError(t, err)

	mockClient := mock_client.NewClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchTask", mock.Anything, connect.NewRequest(&runnerv1.FetchTaskRequest{})).
		Return(connect.NewResponse(&runnerv1.FetchTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil)

	mockRunner := mock_runner.NewRunnerInterface(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, conn *config.Connection) client.Client {
		assert.Equal(t, "https://example.com/forgejo", conn.URL.String())
		assert.Equal(t, "41414141-4141-4141-4141-414141414141", conn.UUID.String())
		assert.Equal(t, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", conn.Token)

		return mockClient
	})()
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
		if name == "example" {
			return mockRunner, "example", false, nil
		}
		t.Fatalf("unexpected connection name: %q", name)
		return nil, "", false, nil
	})()

	runJobCompleted := make(chan any)
	go func() {
		err := runJob(t.Context(), &configPath, &runJobArgs{})
		require.NoError(t, err)

		// Signal that runJob() has completed.
		close(runJobCompleted)
	}()

	mockRunner.On("Run", mock.Anything, mock.Anything)

	// Wait for the goroutine that executes runJob() to stop.
	<-runJobCompleted
}

func TestRunJob_WithConnectionFromCommandOptions(t *testing.T) {
	rawConfig := `
cache:
  enabled: false
`
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")
	tokenPath := filepath.Join(tempDir, "token.txt")

	err := os.WriteFile(configPath, []byte(rawConfig), 0o644)
	require.NoError(t, err)

	err = os.WriteFile(tokenPath, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), 0o644)
	require.NoError(t, err)

	tokenURL, err := fileuri.FromFilePath(tokenPath)
	require.NoError(t, err)

	mockClient := mock_client.NewClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchTask", mock.Anything, connect.NewRequest(&runnerv1.FetchTaskRequest{})).
		Return(connect.NewResponse(&runnerv1.FetchTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil)

	mockRunner := mock_runner.NewRunnerInterface(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, conn *config.Connection) client.Client {
		assert.Equal(t, "https://example.com/forgejo", conn.URL.String())
		assert.Equal(t, "41414141-4141-4141-4141-414141414141", conn.UUID.String())
		assert.Equal(t, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", conn.Token)
		assert.Equal(t, labels.Labels{labels.MustParse("some-label")}, conn.Labels)

		return mockClient
	})()
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
		if name == "default" {
			return mockRunner, "default", false, nil
		}
		t.Fatalf("unexpected connection name: %q", name)
		return nil, "", false, nil
	})()

	runJobCompleted := make(chan any)
	go func() {
		conn := connection{
			url:      "https://example.com/forgejo",
			uuid:     "41414141-4141-4141-4141-414141414141",
			tokenURL: tokenURL.String(),
			labels:   []string{"some-label"},
		}

		err := runJob(t.Context(), &configPath, &runJobArgs{connection: conn})
		require.NoError(t, err)

		// Signal that runJob() has completed.
		close(runJobCompleted)
	}()

	mockRunner.On("Run", mock.Anything, mock.Anything)

	// Wait for the goroutine that executes runJob() to stop.
	<-runJobCompleted
}

func TestRunJob_WaitingForJob(t *testing.T) {
	rawConfig := `
cache:
  enabled: false
server:
  connections:
    example:
      url: https://example.com/forgejo
      uuid: 41414141-4141-4141-4141-414141414141
      token: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
`

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	err := os.WriteFile(configPath, []byte(rawConfig), 0o644)
	require.NoError(t, err)

	mockClient := mock_client.NewClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchTask", mock.Anything, connect.NewRequest(&runnerv1.FetchTaskRequest{})).
		Return(connect.NewResponse(&runnerv1.FetchTaskResponse{Task: nil, TasksVersion: int64(1)}), nil).Once().
		On("FetchTask", mock.Anything, connect.NewRequest(&runnerv1.FetchTaskRequest{})).
		Return(connect.NewResponse(&runnerv1.FetchTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil).Once()

	mockRunner := mock_runner.NewRunnerInterface(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, conn *config.Connection) client.Client {
		assert.Equal(t, "https://example.com/forgejo", conn.URL.String())
		assert.Equal(t, "41414141-4141-4141-4141-414141414141", conn.UUID.String())
		assert.Equal(t, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", conn.Token)

		return mockClient
	})()
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
		if name == "example" {
			return mockRunner, "example", false, nil
		}
		t.Fatalf("unexpected connection name: %q", name)
		return nil, "", false, nil
	})()

	runJobCompleted := make(chan any)
	go func() {
		err := runJob(t.Context(), &configPath, &runJobArgs{wait: true})
		require.NoError(t, err)

		// Signal that runJob() has completed.
		close(runJobCompleted)
	}()

	mockRunner.On("Run", mock.Anything, mock.Anything)

	// Wait for the goroutine that executes runJob() to stop.
	<-runJobCompleted
}

func TestRunJob_WithRequestedJob(t *testing.T) {
	rawConfig := `
cache:
  enabled: false
server:
  connections:
    example:
      url: https://example.com/forgejo
      uuid: 41414141-4141-4141-4141-414141414141
      token: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
`

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	err := os.WriteFile(configPath, []byte(rawConfig), 0o644)
	require.NoError(t, err)

	handle := "1bd04a66-e7ae-4e8a-9edb-c3344d6deec6"

	mockClient := mock_client.NewClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchSingleTask", mock.Anything, connect.NewRequest(&runnerv1.FetchSingleTaskRequest{Handle: &handle})).
		Return(connect.NewResponse(&runnerv1.FetchSingleTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil)

	mockRunner := mock_runner.NewRunnerInterface(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, conn *config.Connection) client.Client {
		assert.Equal(t, "https://example.com/forgejo", conn.URL.String())
		assert.Equal(t, "41414141-4141-4141-4141-414141414141", conn.UUID.String())
		assert.Equal(t, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", conn.Token)

		return mockClient
	})()
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
		if name == "example" {
			return mockRunner, "example", false, nil
		}
		t.Fatalf("unexpected connection name: %q", name)
		return nil, "", false, nil
	})()

	runJobCompleted := make(chan any)
	go func() {
		err := runJob(t.Context(), &configPath, &runJobArgs{handle: handle})
		require.NoError(t, err)

		// Signal that runJob() has completed.
		close(runJobCompleted)
	}()

	mockRunner.On("Run", mock.Anything, mock.Anything)

	// Wait for the goroutine that executes runJob() to stop.
	<-runJobCompleted
}

func TestRunJob_WithWaitingForRequestedJob(t *testing.T) {
	rawConfig := `
cache:
  enabled: false
server:
  connections:
    example:
      url: https://example.com/forgejo
      uuid: 41414141-4141-4141-4141-414141414141
      token: AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
`

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.yaml")

	err := os.WriteFile(configPath, []byte(rawConfig), 0o644)
	require.NoError(t, err)

	handle := "1bd04a66-e7ae-4e8a-9edb-c3344d6deec6"

	mockClient := mock_client.NewClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchSingleTask", mock.Anything, connect.NewRequest(&runnerv1.FetchSingleTaskRequest{Handle: &handle})).
		Return(connect.NewResponse(&runnerv1.FetchSingleTaskResponse{Task: nil, TasksVersion: int64(1)}), nil).Once().
		On("FetchSingleTask", mock.Anything, connect.NewRequest(&runnerv1.FetchSingleTaskRequest{Handle: &handle})).
		Return(connect.NewResponse(&runnerv1.FetchSingleTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil).Once()

	mockRunner := mock_runner.NewRunnerInterface(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, conn *config.Connection) client.Client {
		assert.Equal(t, "https://example.com/forgejo", conn.URL.String())
		assert.Equal(t, "41414141-4141-4141-4141-414141414141", conn.UUID.String())
		assert.Equal(t, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", conn.Token)

		return mockClient
	})()
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
		if name == "example" {
			return mockRunner, "example", false, nil
		}
		t.Fatalf("unexpected connection name: %q", name)
		return nil, "", false, nil
	})()

	runJobCompleted := make(chan any)
	go func() {
		err := runJob(t.Context(), &configPath, &runJobArgs{wait: true, handle: handle})
		require.NoError(t, err)

		// Signal that runJob() has completed.
		close(runJobCompleted)
	}()

	mockRunner.On("Run", mock.Anything, mock.Anything)

	// Wait for the goroutine that executes runJob() to stop.
	<-runJobCompleted
}
