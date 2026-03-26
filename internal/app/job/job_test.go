package job

import (
	"context"
	"fmt"
	"testing"
	"time"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/runner/v12/internal/app/poll"
	pollmocks "code.forgejo.org/forgejo/runner/v12/internal/app/poll/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	mock_runner "code.forgejo.org/forgejo/runner/v12/internal/app/run/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"connectrpc.com/connect"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestNewJob(t *testing.T) {
	j := NewJob(&config.Config{}, mocks.NewClient(t), mock_runner.NewRunnerInterface(t))
	assert.NotNil(t, j)
}

func setTrace(t *testing.T) {
	t.Helper()
	log.SetReportCaller(true)
	log.SetLevel(log.TraceLevel)
}

func TestJob_fetchTask(t *testing.T) {
	setTrace(t)
	for _, testCase := range []struct {
		name    string
		noTask  bool
		sleep   time.Duration
		err     error
		cancel  bool
		success bool
	}{
		{
			name:    "Success",
			success: true,
		},
		{
			name:   "Canceled",
			cancel: true,
		},
		{
			name:   "NoTask",
			noTask: true,
		},
		{
			name: "Error",
			err:  fmt.Errorf("random error"),
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			configRunner := config.Runner{
				FetchTimeout: 1 * time.Millisecond,
			}
			client := mocks.NewClient(t)
			if testCase.cancel {
				client.On("FetchTask", mock.Anything, mock.Anything).Return(nil, context.Canceled)
			} else if testCase.err != nil {
				client.On("FetchTask", mock.Anything, mock.Anything).Return(nil, testCase.err)
			} else if testCase.noTask {
				client.On("FetchTask", mock.Anything, mock.Anything).Return(connect.NewResponse(&runnerv1.FetchTaskResponse{
					Task:         nil,
					TasksVersion: int64(1),
				}), nil)
			} else {
				client.On("FetchTask", mock.Anything, mock.Anything).Return(connect.NewResponse(&runnerv1.FetchTaskResponse{
					Task:         &runnerv1.Task{},
					TasksVersion: int64(1),
				}), nil)
			}

			j := NewJob(
				&config.Config{
					Runner: configRunner,
				},
				client,
				mock_runner.NewRunnerInterface(t),
			)

			task, ok := j.fetchTask(context.Background())
			if testCase.success {
				assert.True(t, ok)
				assert.NotNil(t, task)
			} else {
				assert.False(t, ok)
				assert.Nil(t, task)
			}
		})
	}
}

func TestJob_Run_Wait(t *testing.T) {
	setTrace(t)

	mockPoller := pollmocks.NewPoller(t)
	mockPoller.On("PollOnce").Return()

	originalNewPoller := NewPoller
	NewPoller = func(ctx context.Context, cfg *config.Config, client []client.Client, runner []run.RunnerInterface) poll.Poller {
		return mockPoller
	}
	defer func() { NewPoller = originalNewPoller }()

	j := NewJob(&config.Config{}, mocks.NewClient(t), mock_runner.NewRunnerInterface(t))

	err := j.Run(t.Context(), true)
	assert.NoError(t, err)
	mockPoller.AssertCalled(t, "PollOnce")
}

func TestJob_Run_NoWait(t *testing.T) {
	setTrace(t)

	for _, testCase := range []struct {
		name          string
		noTask        bool
		clientErr     error
		expectError   bool
		errorContains string
	}{
		{
			name:        "Success",
			expectError: false,
		},
		{
			name:          "FetchTask fails - no task",
			noTask:        true,
			expectError:   true,
			errorContains: "could not fetch task",
		},
		{
			name:          "FetchTask fails - client error",
			clientErr:     fmt.Errorf("fetch error"),
			expectError:   true,
			errorContains: "could not fetch task",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runner := mock_runner.NewRunnerInterface(t)
			if !testCase.expectError {
				runner.On("Run", mock.Anything, mock.Anything)
			}

			configRunner := config.Runner{
				FetchTimeout: 1 * time.Second,
				Timeout:      10 * time.Millisecond,
			}
			client := mocks.NewClient(t)
			if testCase.clientErr != nil {
				client.On("FetchTask", mock.Anything, mock.Anything).Return(nil, testCase.clientErr)
			} else if testCase.noTask {
				client.On("FetchTask", mock.Anything, mock.Anything).Return(connect.NewResponse(&runnerv1.FetchTaskResponse{
					Task:         nil,
					TasksVersion: int64(1),
				}), nil)
			} else {
				client.On("FetchTask", mock.Anything, mock.Anything).Return(connect.NewResponse(&runnerv1.FetchTaskResponse{
					Task:         &runnerv1.Task{},
					TasksVersion: int64(1),
				}), nil)
			}

			j := NewJob(
				&config.Config{
					Runner: configRunner,
				},
				client,
				runner,
			)

			err := j.Run(t.Context(), false)

			if testCase.expectError {
				assert.Error(t, err)
				if testCase.errorContains != "" {
					assert.Contains(t, err.Error(), testCase.errorContains)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
