// Copyright 2025 The Forgejo Authors
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"testing"
	"time"

	"code.forgejo.org/forgejo/runner/v12/internal/app/poll"
	mock_poller "code.forgejo.org/forgejo/runner/v12/internal/app/poll/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	mock_runner "code.forgejo.org/forgejo/runner/v12/internal/app/run/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	mock_client "code.forgejo.org/forgejo/runner/v12/internal/pkg/client/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	"code.forgejo.org/forgejo/runner/v12/testutils"
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

	defer testutils.MockVariable(&initializeConfig, func(configFile *string) (*config.Config, error) {
		return &config.Config{
			Runner: config.Runner{
				// Default ShutdownTimeout of 0s won't work for the graceful shutdown test.
				ShutdownTimeout: 30 * time.Second,
			},
		}, nil
	})()
	defer testutils.MockVariable(&initLogging, func(cfg *config.Config) {})()
	defer testutils.MockVariable(&loadRegistration, func(cfg *config.Config) (*config.Registration, error) {
		return &config.Registration{}, nil
	})()
	defer testutils.MockVariable(&extractLabels, func(cfg *config.Config, reg *config.Registration) labels.Labels {
		return labels.Labels{}
	})()
	defer testutils.MockVariable(&configCheck, func(ctx context.Context, cfg *config.Config, ls labels.Labels) error {
		return nil
	})()
	defer testutils.MockVariable(&createClient, func(cfg *config.Config, reg *config.Registration) client.Client {
		return mockClient
	})()
	var runnerContext context.Context
	defer testutils.MockVariable(&createRunner, func(ctx context.Context, cfg *config.Config, reg *config.Registration, cli client.Client, ls labels.Labels) (run.RunnerInterface, string, error) {
		runnerContext = ctx
		return mockRunner, "runner", nil
	})()
	var pollerContext context.Context
	defer testutils.MockVariable(&createPoller, func(ctx context.Context, cfg *config.Config, cli client.Client, runner run.RunnerInterface) poll.Poller {
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
		err := runDaemon(mockSignalContext, &configFile)
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
