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
