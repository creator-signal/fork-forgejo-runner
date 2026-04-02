// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/runner/v12/act/cacheproxy"
	"code.forgejo.org/forgejo/runner/v12/internal/app/poll"
	mock_poller "code.forgejo.org/forgejo/runner/v12/internal/app/poll/mocks"
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

func TestRunDaemonGracefulShutdown(t *testing.T) {
	// Key assertions for graceful shutdown test:
	//
	// - ctx passed to createRunner, createPoller, and Shutdown must outlive signalContext passed to runDaemon, allowing
	// the poller to operate without errors after termination signal is received: #1
	//
	// - When shutting down, the order of operations should be: close signalContext, which causes Shutdown mock to be
	// invoked, and Shutdown mock causes the Poll method to be stopped: #2

	mockClient := mock_client.NewClient(t)
	mockRunner := mock_runner.NewRunnerInterface(t)
	mockPoller := mock_poller.NewPoller(t)

	connectionURL, err := url.Parse("https://example.com")
	require.NoError(t, err)
	defer testutils.MockVariable(&initializeConfig, func(configFile *string, args *daemonArgs) (*config.Config, error) {
		return &config.Config{
			Runner: config.Runner{
				// Default ShutdownTimeout of 0s won't work for the graceful shutdown test.
				ShutdownTimeout: 30 * time.Second,
			},
			Server: config.Server{
				Connections: map[string]*config.Connection{
					"default": {
						URL: connectionURL,
					},
				},
			},
		}, nil
	})()
	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&configCheck, func(ctx context.Context, cfg *config.Config) error {
		return nil
	})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, conn *config.Connection) client.Client {
		return mockClient
	})()
	var runnerContext context.Context
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
		runnerContext = ctx
		return mockRunner, "runner", false, nil
	})()
	var pollerContext context.Context
	defer testutils.MockVariable(&createPoller, func(ctx context.Context, cfg *config.Config, clients []client.Client, runners []run.RunnerInterface) poll.Poller {
		pollerContext = ctx
		return mockPoller
	})()

	pollBegunChannel := make(chan interface{})
	shutdownChannel := make(chan interface{})
	mockPoller.On("Poll").Run(func(args mock.Arguments) {
		close(pollBegunChannel)
		// Simulate running the poll by waiting and doing nothing until shutdownChannel says Shutdown was invoked
		require.NotNil(t, pollerContext)
		select {
		case <-pollerContext.Done():
			assert.Fail(t, "pollerContext was closed before shutdownChannel") // #1
			return
		case <-shutdownChannel:
			return
		}
	})
	mockPoller.On("Shutdown", mock.Anything).Run(func(args mock.Arguments) {
		shutdownContext := args.Get(0).(context.Context)
		select {
		case <-shutdownContext.Done():
			assert.Fail(t, "shutdownContext was closed, but was expected to be open") // #1
			return
		case <-runnerContext.Done():
			assert.Fail(t, "runnerContext was closed, but was expected to be open") // #1
			return
		case <-time.After(time.Microsecond):
			close(shutdownChannel)
			return
		}
	}).Return(nil)

	// When runDaemon is begun, it will run "forever" until the passed-in context is done.  So, let's start that in a goroutine...
	mockSignalContext, cancelSignal := context.WithCancel(t.Context())
	runDaemonComplete := make(chan interface{})
	go func() {
		configFile := "config.yaml"
		err := runDaemon(mockSignalContext, &configFile, &daemonArgs{})
		close(runDaemonComplete)
		require.NoError(t, err)
	}()

	// Wait until runDaemon reaches poller.Poll(), where we expect graceful shutdown to trigger
	<-pollBegunChannel

	// Now we'll signal to the daemon to begin graceful shutdown; this begins the events described in #2
	cancelSignal()

	// Wait for the daemon goroutine to stop
	<-runDaemonComplete
}

func TestRunDaemon_pollTask_ReactsOnContext(t *testing.T) {
	osSignal, cancelOsSignal := context.WithCancel(t.Context())
	mockPoller := mock_poller.NewPoller(t)
	mockPoller.On("Poll").Run(func(args mock.Arguments) {
		cancelOsSignal()
		// block until test is done
		<-t.Context().Done()
	}).Return(nil)

	pollFinishedChan := make(chan interface{})
	go func() {
		defer close(pollFinishedChan)
		pollTask(osSignal, mockPoller)
	}()

	select {
	case <-pollFinishedChan:
		// good case
	case <-time.After(10 * time.Millisecond):
		assert.Fail(t, "routine 'pollTask' took too long to finish")
		// bad case
	}

	mockPoller.AssertNumberOfCalls(t, "Poll", 1)
}

func TestCreateRunner_PopulatesEphemeralFromClientResponse(t *testing.T) {
	ctx := t.Context()

	tempDir := t.TempDir()
	runnerFile := filepath.Join(tempDir, ".runner")

	cfg := &config.Config{
		Runner: config.Runner{
			File: runnerFile,
		},
	}
	ls := labels.Labels{}

	mockClient := mock_client.NewClient(t)
	mockClient.On("Address").Return("https://example.com")

	expectedEphemeral := true
	mockDeclareResponse := &connect.Response[runnerv1.DeclareResponse]{
		Msg: &runnerv1.DeclareResponse{
			Runner: &runnerv1.Runner{
				Name:      "test-runner",
				Version:   "v1.0.0",
				Labels:    []string{"test-label"},
				Ephemeral: expectedEphemeral,
			},
		},
	}

	mockClient.On("Declare", mock.Anything, mock.Anything).Return(mockDeclareResponse, nil)

	runner, runnerName, ephemeral, err := createRunner(ctx, "test-runner", cfg, mockClient, ls, nil)

	require.NoError(t, err)
	assert.NotNil(t, runner)
	assert.Equal(t, "test-runner", runnerName)

	assert.Equal(t, expectedEphemeral, ephemeral, "Ephemeral should be populated from the Declare response")

	mockClient.AssertCalled(t, "Declare", mock.Anything, mock.Anything)
}

func TestRunDaemon_MultipleServers(t *testing.T) {
	// When there are multiple servers to connect to, the key responsibilities of the daemon command that we need to
	// assert are:
	//
	// - each client is created with the right server connection config
	//
	// - each runner is created with the right client, name, and labels
	//
	// - clients and runners are passed into `createPoller` with the right index-by-index association between the two
	// objects

	serverURL1, err := url.Parse("https://example.com/forgejo1")
	require.NoError(t, err)
	serverURL2, err := url.Parse("https://example.com/forgejo2")
	require.NoError(t, err)

	mockClient1 := mock_client.NewClient(t)
	mockClient2 := mock_client.NewClient(t)

	mockRunner1 := mock_runner.NewRunnerInterface(t)
	mockRunner2 := mock_runner.NewRunnerInterface(t)

	mockPoller := mock_poller.NewPoller(t)

	require.NoError(t, err)
	defer testutils.MockVariable(&initializeConfig, func(configFile *string, args *daemonArgs) (*config.Config, error) {
		return &config.Config{
			Runner: config.Runner{
				// Default ShutdownTimeout of 0s won't work for the graceful shutdown test.
				ShutdownTimeout: 30 * time.Second,
			},
			Server: config.Server{
				Connections: map[string]*config.Connection{
					"forgejo1": {
						URL: serverURL1,
					},
					"forgejo2": {
						URL: serverURL2,
					},
				},
			},
		}, nil
	})()
	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&configCheck, func(ctx context.Context, cfg *config.Config) error {
		return nil
	})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, conn *config.Connection) client.Client {
		switch conn.URL.String() {
		case serverURL1.String():
			return mockClient1
		case serverURL2.String():
			return mockClient2
		}
		t.Fatalf("unexpected connection URL: %q", conn.URL.String())
		return nil
	})()
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
		switch name {
		case "forgejo1":
			return mockRunner1, "forgejo1", false, nil
		case "forgejo2":
			return mockRunner2, "forgejo2", false, nil
		}
		t.Fatalf("unexpected connection name: %q", name)
		return nil, "", false, nil
	})()
	var createPollerInvoked bool
	defer testutils.MockVariable(&createPoller, func(ctx context.Context, cfg *config.Config, clients []client.Client, runners []run.RunnerInterface) poll.Poller {
		createPollerInvoked = true
		require.Len(t, clients, 2)
		require.Len(t, runners, 2)
		// Order isn't important to the test, just equal identity between clients[0] and runners[0] -- so check both possible orders.
		if clients[0] == mockClient1 {
			assert.Same(t, mockClient1, clients[0])
			assert.Same(t, mockRunner1, runners[0])
			assert.Same(t, mockClient2, clients[1])
			assert.Same(t, mockRunner2, runners[1])
		} else {
			assert.Same(t, mockClient1, clients[1])
			assert.Same(t, mockRunner1, runners[1])
			assert.Same(t, mockClient2, clients[0])
			assert.Same(t, mockRunner2, runners[0])
		}
		return mockPoller
	})()

	pollBegunChannel := make(chan any)
	mockPoller.On("Poll").Run(func(args mock.Arguments) {
		// Indicate to test that Poll was invoked...
		close(pollBegunChannel)
	})
	mockPoller.On("Shutdown", mock.Anything).Run(func(args mock.Arguments) {}).Return(nil)

	// When runDaemon is begun, it will run "forever" until the passed-in context is done.  So, let's start that in a goroutine...
	mockSignalContext, cancelSignal := context.WithCancel(t.Context())
	runDaemonComplete := make(chan any)
	go func() {
		configFile := "config.yaml"
		err := runDaemon(mockSignalContext, &configFile, &daemonArgs{})
		require.NoError(t, err)
		close(runDaemonComplete)
	}()

	// Wait until runDaemon reaches poller.Poll(), and verify createPoller was run where our test assertions are.
	<-pollBegunChannel
	require.True(t, createPollerInvoked)

	// Shutdown the daemon by killing the signal context
	cancelSignal()

	// Wait for the daemon goroutine to stop so that we're sure the test goroutines are complete.
	<-runDaemonComplete
}

func TestRunDaemon_MultipleServersQuitsIfOneIsEphemeral(t *testing.T) {
	serverURL1, err := url.Parse("https://example.com/forgejo1")
	require.NoError(t, err)
	serverURL2, err := url.Parse("https://example.com/forgejo2")
	require.NoError(t, err)

	mockClient1 := mock_client.NewClient(t)
	mockClient2 := mock_client.NewClient(t)

	mockRunner1 := mock_runner.NewRunnerInterface(t)
	mockRunner2 := mock_runner.NewRunnerInterface(t)

	mockPoller := mock_poller.NewPoller(t)

	require.NoError(t, err)
	defer testutils.MockVariable(&initializeConfig, func(configFile *string, args *daemonArgs) (*config.Config, error) {
		return &config.Config{
			Server: config.Server{
				Connections: map[string]*config.Connection{
					"forgejo1": {
						URL: serverURL1,
					},
					"forgejo2": {
						URL: serverURL2,
					},
				},
			},
		}, nil
	})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, conn *config.Connection) client.Client {
		switch conn.URL.String() {
		case serverURL1.String():
			return mockClient1
		case serverURL2.String():
			return mockClient2
		}
		t.Fatalf("unexpected connection URL: %q", conn.URL.String())
		return nil
	})()
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
		switch name {
		case "forgejo1":
			return mockRunner1, "forgejo1", false, nil
		case "forgejo2":
			return mockRunner2, "forgejo2", true, nil
		}
		t.Fatalf("unexpected connection name: %q", name)
		return nil, "", false, nil
	})()
	defer testutils.MockVariable(&createPoller, func(ctx context.Context, cfg *config.Config, clients []client.Client, runners []run.RunnerInterface) poll.Poller {
		return mockPoller
	})()

	configFile := "config.yaml"
	err = runDaemon(t.Context(), &configFile, &daemonArgs{})
	require.ErrorContains(t, err, "connection \"forgejo2\" requires an ephemeral runner")
}

func TestRunDaemon_WithConnectionFromCommandOptions(t *testing.T) {
	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "token.txt")

	err := os.WriteFile(tokenPath, []byte("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"), 0o644)
	require.NoError(t, err)

	tokenURL, err := fileuri.FromFilePath(tokenPath)
	require.NoError(t, err)

	mockClient := mock_client.NewClient(t)
	mockRunner := mock_runner.NewRunnerInterface(t)
	mockPoller := mock_poller.NewPoller(t)

	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&configCheck, func(ctx context.Context, cfg *config.Config) error {
		return nil
	})()
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
	var createPollerInvoked bool
	defer testutils.MockVariable(&createPoller, func(ctx context.Context, cfg *config.Config, clients []client.Client, runners []run.RunnerInterface) poll.Poller {
		createPollerInvoked = true
		require.Len(t, clients, 1)
		require.Len(t, runners, 1)

		assert.Same(t, mockClient, clients[0])
		assert.Same(t, mockRunner, runners[0])

		return mockPoller
	})()

	pollBegunChannel := make(chan any)
	mockPoller.On("Poll").Run(func(args mock.Arguments) {
		// Indicate to test that Poll was invoked...
		close(pollBegunChannel)
	})
	mockPoller.On("Shutdown", mock.Anything).Run(func(args mock.Arguments) {}).Return(nil)

	// When runDaemon is begun, it will run "forever" until the passed-in context is done.
	// So, let's start that in a goroutine...
	mockSignalContext, cancelSignal := context.WithCancel(t.Context())
	runDaemonComplete := make(chan any)
	go func() {
		conn := connection{
			url:      "https://example.com/forgejo",
			uuid:     "41414141-4141-4141-4141-414141414141",
			tokenURL: tokenURL.String(),
			labels:   []string{"some-label"},
		}

		configPath := ""
		err := runDaemon(mockSignalContext, &configPath, &daemonArgs{connection: conn})
		require.NoError(t, err)
		close(runDaemonComplete)
	}()

	// Wait until runDaemon reaches poller.Poll(), and verify createPoller was run where our test assertions are.
	<-pollBegunChannel
	require.True(t, createPollerInvoked)

	// Shutdown the daemon by killing the signal context.
	cancelSignal()

	// Wait for the daemon goroutine to stop so that we're sure the test goroutines are complete.
	<-runDaemonComplete
}
