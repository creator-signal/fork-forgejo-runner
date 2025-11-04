package job

import (
	"context"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"

	"code.forgejo.org/forgejo/actions-proto/ping/v1/pingv1connect"
	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/actions-proto/runner/v1/runnerv1connect"
	"code.forgejo.org/forgejo/runner/v12/internal/app/poll"
	pollmocks "code.forgejo.org/forgejo/runner/v12/internal/app/poll/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type mockClient struct {
	pingv1connect.PingServiceClient
	runnerv1connect.RunnerServiceClient

	sleep  time.Duration
	cancel bool
	err    error
	noTask bool
}

func (o mockClient) Address() string {
	return ""
}

func (o mockClient) Insecure() bool {
	return true
}

func (o mockClient) FetchInterval() time.Duration {
	return time.Second
}

func (o *mockClient) FetchTask(ctx context.Context, _ *connect.Request[runnerv1.FetchTaskRequest]) (*connect.Response[runnerv1.FetchTaskResponse], error) {
	if o.sleep > 0 {
		select {
		case <-ctx.Done():
			log.Trace("fetch task done")
			return nil, context.DeadlineExceeded
		case <-time.After(o.sleep):
			log.Trace("slept")
			return nil, fmt.Errorf("unexpected")
		}
	}
	if o.cancel {
		return nil, context.Canceled
	}
	if o.err != nil {
		return nil, o.err
	}
	task := &runnerv1.Task{}
	if o.noTask {
		task = nil
		o.noTask = false
	}

	return connect.NewResponse(&runnerv1.FetchTaskResponse{
		Task:         task,
		TasksVersion: int64(1),
	}), nil
}

type mockRunner struct {
	cfg    *config.Runner
	log    chan string
	panics bool
	err    error
}

func (o *mockRunner) Run(ctx context.Context, _ *runnerv1.Task) error {
	o.log <- "runner starts"
	if o.panics {
		log.Trace("panics")
		o.log <- "runner panics"
		o.panics = false
		panic("whatever")
	}
	if o.err != nil {
		log.Trace("error")
		o.log <- "runner error"
		err := o.err
		o.err = nil
		return err
	}
	for {
		select {
		case <-ctx.Done():
			log.Trace("shutdown")
			o.log <- "runner shutdown"
			return nil
		case <-time.After(o.cfg.Timeout):
			log.Trace("after")
			o.log <- "runner timeout"
			return nil
		}
	}
}

func TestNewJob(t *testing.T) {
	j := NewJob(&config.Config{}, &mockClient{}, &mockRunner{})
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

			j := NewJob(
				&config.Config{
					Runner: configRunner,
				},
				&mockClient{
					sleep:  testCase.sleep,
					cancel: testCase.cancel,
					noTask: testCase.noTask,
					err:    testCase.err,
				},
				&mockRunner{},
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

	j := NewJob(&config.Config{}, &mockClient{}, &mockRunner{})

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
		runnerErr     error
		runnerPanics  bool
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
		{
			name:        "Runner error",
			runnerErr:   fmt.Errorf("runner failed"),
			expectError: true,
		},
		{
			name:         "Runner panics",
			runnerPanics: true,
			expectError:  false,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			logChan := make(chan string, 10)
			configRunner := config.Runner{
				FetchTimeout: 1 * time.Second,
				Timeout:      10 * time.Millisecond,
			}

			j := NewJob(
				&config.Config{
					Runner: configRunner,
				},
				&mockClient{
					noTask: testCase.noTask,
					err:    testCase.clientErr,
				},
				&mockRunner{
					cfg:    &configRunner,
					log:    logChan,
					panics: testCase.runnerPanics,
					err:    testCase.runnerErr,
				},
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
