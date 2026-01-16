// Copyright The Forgejo Authors.
// SPDX-License-Identifier: MIT

package poll

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"

	"code.forgejo.org/forgejo/actions-proto/ping/v1/pingv1connect"
	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/actions-proto/runner/v1/runnerv1connect"
	mock_runner "code.forgejo.org/forgejo/runner/v12/internal/app/run/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type mockPoller struct {
	poller
}

func (o *mockPoller) Poll() {
	o.poller.Poll()
}

type mockClient struct {
	pingv1connect.PingServiceClient
	runnerv1connect.RunnerServiceClient

	sleep     time.Duration
	cancel    bool
	err       error
	noTask    bool
	addtTasks bool
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

func (o *mockClient) FetchTask(ctx context.Context, req *connect.Request[runnerv1.FetchTaskRequest]) (*connect.Response[runnerv1.FetchTaskResponse], error) {
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

	var addt []*runnerv1.Task
	if o.addtTasks {
		addt = append(addt, &runnerv1.Task{})
	}

	return connect.NewResponse(&runnerv1.FetchTaskResponse{
		Task:            task,
		TasksVersion:    int64(1),
		AdditionalTasks: addt,
	}), nil
}

type mockRunner struct {
	cfg    *config.Runner
	log    chan string
	panics bool
	err    error
}

func (o *mockRunner) Run(ctx context.Context, task *runnerv1.Task) error {
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

func setTrace(t *testing.T) {
	t.Helper()
	log.SetReportCaller(true)
	log.SetLevel(log.TraceLevel)
}

func TestPoller_New(t *testing.T) {
	p := New(t.Context(), &config.Config{}, []client.Client{&mockClient{}}, &mockRunner{})
	assert.NotNil(t, p)
}

func TestPoller_Runner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	setTrace(t)
	for _, testCase := range []struct {
		name           string
		timeout        time.Duration
		noTask         bool
		panics         bool
		err            error
		expected       string
		contextTimeout time.Duration
	}{
		{
			name:     "Simple",
			timeout:  10 * time.Second,
			expected: "runner shutdown",
		},
		{
			name:     "Panics",
			timeout:  10 * time.Second,
			panics:   true,
			expected: "runner panics",
		},
		{
			name:     "Error",
			timeout:  10 * time.Second,
			err:      fmt.Errorf("ERROR"),
			expected: "runner error",
		},
		{
			name:     "PollTaskError",
			timeout:  10 * time.Second,
			noTask:   true,
			expected: "runner shutdown",
		},
		{
			name:           "ShutdownTimeout",
			timeout:        1 * time.Second,
			contextTimeout: 1 * time.Minute,
			expected:       "runner timeout",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			runnerLog := make(chan string, 3)
			configRunner := config.Runner{
				FetchInterval: 1,
				Capacity:      1,
				Timeout:       testCase.timeout,
			}
			p := &mockPoller{}
			p.init(
				t.Context(),
				&config.Config{
					Runner: configRunner,
				},
				[]client.Client{&mockClient{
					noTask: testCase.noTask,
				}},
				&mockRunner{
					cfg:    &configRunner,
					log:    runnerLog,
					panics: testCase.panics,
					err:    testCase.err,
				})
			go p.Poll()
			assert.Equal(t, "runner starts", <-runnerLog)
			var ctx context.Context
			var cancel context.CancelFunc
			if testCase.contextTimeout > 0 {
				ctx, cancel = context.WithTimeout(context.Background(), testCase.contextTimeout)
				defer cancel()
			} else {
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			_ = p.Shutdown(ctx) // err not checked
			<-p.done
			assert.Equal(t, testCase.expected, <-runnerLog)
		})
	}
}

func TestPoller_Fetch(t *testing.T) {
	setTrace(t)
	for _, testCase := range []struct {
		name      string
		noTask    bool
		sleep     time.Duration
		err       error
		cancel    bool
		success   bool
		addtTasks bool
		taskCount int
	}{
		{
			name:      "Success",
			success:   true,
			taskCount: 1,
		},
		{
			name:  "Timeout",
			sleep: 100 * time.Millisecond,
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
			name:      "AdditionalTasks",
			success:   true,
			addtTasks: true,
			taskCount: 2,
		},
		{
			name:      "AdditionalTasks Only",
			noTask:    true,
			success:   true,
			addtTasks: true,
			taskCount: 1,
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
			p := &mockPoller{}
			p.init(
				t.Context(),
				&config.Config{
					Runner: configRunner,
				},
				[]client.Client{&mockClient{
					sleep:     testCase.sleep,
					cancel:    testCase.cancel,
					noTask:    testCase.noTask,
					err:       testCase.err,
					addtTasks: testCase.addtTasks,
				}},
				&mockRunner{},
			)
			task, ok := p.fetchTasks(context.Background(), p.clients[0], &atomic.Int64{}, 100)
			if testCase.success {
				assert.True(t, ok)
				assert.NotNil(t, task)
				assert.Len(t, task, testCase.taskCount)
			} else {
				assert.False(t, ok)
				assert.Nil(t, task)
			}
		})
	}
}

func TestPollerPoll(t *testing.T) {
	setup := func(t *testing.T, pollingCtx context.Context) (*mocks.Client, *mock_runner.RunnerInterface, Poller) {
		mockClient := mocks.NewClient(t)
		mockClient.On("FetchInterval").Return(1 * time.Second)
		mockClient.On("Address").Return("https://client")
		mockRunner := mock_runner.NewRunnerInterface(t)
		poller := New(pollingCtx, &config.Config{
			Runner: config.Runner{
				Capacity: 3,
			},
		}, []client.Client{mockClient}, mockRunner)
		return mockClient, mockRunner, poller
	}
	teardown := func(t *testing.T, mockClient *mocks.Client, mockRunner *mock_runner.RunnerInterface) {
		mockClient.AssertExpectations(t)
		mockRunner.AssertExpectations(t)
	}
	emptyResponse := connect.NewResponse(&runnerv1.FetchTaskResponse{
		Task:            nil,
		TasksVersion:    int64(1),
		AdditionalTasks: nil,
	})
	task1 := &runnerv1.Task{}
	task2 := &runnerv1.Task{}
	task3 := &runnerv1.Task{}
	twoTaskResponse := connect.NewResponse(&runnerv1.FetchTaskResponse{
		Task:            task1,
		TasksVersion:    int64(1),
		AdditionalTasks: []*runnerv1.Task{task2},
	})
	threeTaskResponse := connect.NewResponse(&runnerv1.FetchTaskResponse{
		Task:            task1,
		TasksVersion:    int64(1),
		AdditionalTasks: []*runnerv1.Task{task2, task3},
	})

	// invocations of `fetchTasks` are rate limited per configuration
	t.Run("fetchTasks rate limited", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			pollingCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockClient, mockRunner, poller := setup(t, pollingCtx)
			mockClient.On("FetchTask", mock.Anything, mock.Anything).Return(emptyResponse, nil)

			go poller.Poll()

			time.Sleep(1 * time.Millisecond) // should immediately FetchTask
			mockClient.AssertNumberOfCalls(t, "FetchTask", 1)

			time.Sleep(998 * time.Millisecond) // not again until all of FetchInterval (1s) passes
			mockClient.AssertNumberOfCalls(t, "FetchTask", 1)

			time.Sleep(3 * time.Millisecond) // but then it does FetchTask again
			mockClient.AssertNumberOfCalls(t, "FetchTask", 2)

			require.NoError(t, poller.Shutdown(t.Context()))

			teardown(t, mockClient, mockRunner)
		})
	})

	t.Run("available capacity is passed to fetchTask", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			pollingCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockClient, mockRunner, poller := setup(t, pollingCtx)
			mockClient.On("FetchTask", mock.Anything, mock.Anything).
				Once().
				Run(func(args mock.Arguments) {
					req := args.Get(1).(*connect.Request[runnerv1.FetchTaskRequest])
					assert.EqualValues(t, 0, req.Msg.GetTasksVersion(), "GetTasksVersion")
					assert.EqualValues(t, 3, req.Msg.GetTaskCapacity(), "GetTaskCapacity")
				}).
				Return(emptyResponse, nil)

			go poller.Poll()
			time.Sleep(1 * time.Millisecond)
			require.NoError(t, poller.Shutdown(t.Context()))

			teardown(t, mockClient, mockRunner)
		})
	})

	t.Run("available capacity is varied as tasks start and finish", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			pollingCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockClient, mockRunner, poller := setup(t, pollingCtx)
			// First fetch -- assert 3 capacity requested, return two tasks
			mockClient.On("FetchTask", mock.Anything, mock.Anything).
				Once().
				Run(func(args mock.Arguments) {
					req := args.Get(1).(*connect.Request[runnerv1.FetchTaskRequest])
					assert.EqualValues(t, 0, req.Msg.GetTasksVersion(), "GetTasksVersion Call 1")
					assert.EqualValues(t, 3, req.Msg.GetTaskCapacity(), "GetTaskCapacity Call 1")
				}).
				Return(twoTaskResponse, nil)
			// Second fetch -- assert 1 capacity, return no tasks
			mockClient.On("FetchTask", mock.Anything, mock.Anything).
				Once().
				Run(func(args mock.Arguments) {
					req := args.Get(1).(*connect.Request[runnerv1.FetchTaskRequest])
					assert.EqualValues(t, 0, req.Msg.GetTasksVersion(), "GetTasksVersion Call 2")
					assert.EqualValues(t, 1, req.Msg.GetTaskCapacity(), "GetTaskCapacity Call 2")
				}).
				Return(emptyResponse, nil)
			// Third fetch -- assert 3 capacity, return no tasks
			mockClient.On("FetchTask", mock.Anything, mock.Anything).
				Once().
				Run(func(args mock.Arguments) {
					req := args.Get(1).(*connect.Request[runnerv1.FetchTaskRequest])
					assert.EqualValues(t, 1, req.Msg.GetTasksVersion(), "GetTasksVersion Call 3")
					assert.EqualValues(t, 3, req.Msg.GetTaskCapacity(), "GetTaskCapacity Call 3")
				}).
				Return(emptyResponse, nil)
			mockRunner.On("Run", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					// Take some time to execute so that the 2nd FetchTask still has these tasks considered in-progress.
					time.Sleep(1500 * time.Millisecond)
				}).
				Return(nil)

			go poller.Poll()
			time.Sleep(1 * time.Millisecond)
			mockClient.AssertNumberOfCalls(t, "FetchTask", 1)
			time.Sleep(1 * time.Second)
			mockClient.AssertNumberOfCalls(t, "FetchTask", 2)
			time.Sleep(1 * time.Second)
			mockClient.AssertNumberOfCalls(t, "FetchTask", 3)
			require.NoError(t, poller.Shutdown(t.Context()))

			teardown(t, mockClient, mockRunner)
		})
	})

	t.Run("no FetchTask when available capacity is zero", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			pollingCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockClient, mockRunner, poller := setup(t, pollingCtx)
			mockClient.On("FetchTask", mock.Anything, mock.Anything).
				Return(threeTaskResponse, nil)
			mockRunner.On("Run", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					time.Sleep(1 * time.Hour)
				}).
				Return(nil)

			go poller.Poll()
			time.Sleep(1 * time.Millisecond)
			mockClient.AssertNumberOfCalls(t, "FetchTask", 1)
			time.Sleep(30 * time.Minute) // a long time later, but jobs are using up all the capacity...
			mockClient.AssertNumberOfCalls(t, "FetchTask", 1)
			require.NoError(t, poller.Shutdown(t.Context()))

			teardown(t, mockClient, mockRunner)
		})
	})

	t.Run("poll shutdown waits for task completion", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			pollingCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockClient, mockRunner, poller := setup(t, pollingCtx)
			mockClient.On("FetchTask", mock.Anything, mock.Anything).
				Return(twoTaskResponse, nil)
			mockRunner.On("Run", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					time.Sleep(1 * time.Hour)
				}).
				Return(nil)

			go poller.Poll()
			time.Sleep(1 * time.Millisecond) // let poll get started, fetch tasks, start them
			shutdownStart := time.Now()
			require.NoError(t, poller.Shutdown(t.Context()))
			shutdownEnd := time.Now()
			assert.EqualValues(t, 3599999000, shutdownEnd.Sub(shutdownStart).Microseconds())

			teardown(t, mockClient, mockRunner)
		})
	})
}

func TestPollerPollMultipleClients(t *testing.T) {
	setup := func(t *testing.T, pollingCtx context.Context) (*mocks.Client, *mocks.Client, *mock_runner.RunnerInterface, Poller) {
		mockClient1 := mocks.NewClient(t)
		mockClient1.On("FetchInterval").Return(1 * time.Second)
		mockClient1.On("Address").Return("https://client1")
		mockClient2 := mocks.NewClient(t)
		mockClient2.On("FetchInterval").Return(30 * time.Second)
		mockClient2.On("Address").Return("https://client2")
		mockRunner := mock_runner.NewRunnerInterface(t)
		poller := New(pollingCtx, &config.Config{
			Runner: config.Runner{
				Capacity: 3,
			},
		}, []client.Client{mockClient1, mockClient2}, mockRunner)
		return mockClient1, mockClient2, mockRunner, poller
	}
	teardown := func(t *testing.T, mockClient1, mockClient2 *mocks.Client, mockRunner *mock_runner.RunnerInterface) {
		mockClient1.AssertExpectations(t)
		mockClient2.AssertExpectations(t)
		mockRunner.AssertExpectations(t)
	}
	emptyResponse := connect.NewResponse(&runnerv1.FetchTaskResponse{
		Task:            nil,
		TasksVersion:    int64(1),
		AdditionalTasks: nil,
	})
	task1 := &runnerv1.Task{}
	task2 := &runnerv1.Task{}
	twoTaskResponse := connect.NewResponse(&runnerv1.FetchTaskResponse{
		Task:            task1,
		TasksVersion:    int64(100),
		AdditionalTasks: []*runnerv1.Task{task2},
	})

	t.Run("invocations of `fetchTasks` are rate limited per configuration", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			pollingCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockClient1, mockClient2, mockRunner, poller := setup(t, pollingCtx)
			mockClient1.On("FetchTask", mock.Anything, mock.Anything).Return(emptyResponse, nil)
			mockClient2.On("FetchTask", mock.Anything, mock.Anything).Return(emptyResponse, nil)

			go poller.Poll()

			time.Sleep(1 * time.Millisecond) // both clients should FetchTask ASAP
			mockClient1.AssertNumberOfCalls(t, "FetchTask", 1)
			mockClient2.AssertNumberOfCalls(t, "FetchTask", 1)

			time.Sleep(1 * time.Second) // only mockClient1 fetches every second
			mockClient1.AssertNumberOfCalls(t, "FetchTask", 2)
			mockClient2.AssertNumberOfCalls(t, "FetchTask", 1)

			time.Sleep(35 * time.Second) // mockClient2 fetches a while later
			mockClient1.AssertNumberOfCalls(t, "FetchTask", 37)
			mockClient2.AssertNumberOfCalls(t, "FetchTask", 2)

			require.NoError(t, poller.Shutdown(t.Context()))

			teardown(t, mockClient1, mockClient2, mockRunner)
		})
	})

	// if `FetchTask` occurred simultaneously on multiple clients, we could exceed available capacity -- should be
	// protected from this occurring.
	t.Run("never exceed available capacity", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			pollingCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockClient1, mockClient2, mockRunner, poller := setup(t, pollingCtx)

			// synctest doesn't provide a consistent ordering for whether mockClient1 or mockClient2 will be called
			// first since they both wake at time 0 in this test.  Ignore the first call, just give an empty task
			// response.
			mockClient1.On("FetchTask", mock.Anything, mock.Anything).
				Once().
				Return(emptyResponse, nil)
			mockClient2.On("FetchTask", mock.Anything, mock.Anything).
				Once().
				Return(emptyResponse, nil)

			// On the second call, make sure mockClient1 gets a capacity of 3, but then simulate the service call to the
			// client taking a *long* time.  Verify that by the time mockClient2 is invoked it doesn't incorrectly
			// report capacity 3, but rather capacity 1 -- in other words, the fetch to mockClient2 is blocked while
			// mockClient1's fetch is running.
			mockClient1.On("FetchTask", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					req := args.Get(1).(*connect.Request[runnerv1.FetchTaskRequest])
					assert.EqualValues(t, 3, req.Msg.GetTaskCapacity(), "GetTaskCapacity")
					time.Sleep(45 * time.Second) // long enough for mockClient2's 30-s fetch interval to trigger
				}).
				Return(twoTaskResponse, nil)
			mockClient2.On("FetchTask", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					req := args.Get(1).(*connect.Request[runnerv1.FetchTaskRequest])
					assert.EqualValues(t, 1, req.Msg.GetTaskCapacity(), "GetTaskCapacity")
				}).
				Return(emptyResponse, nil)
			mockRunner.On("Run", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					// Ensure tasks returned from mockClient1 "run" for a little bit so that the capacity isn't
					// immediately freed when mockClient2 can make its service call.
					time.Sleep(1500 * time.Millisecond)
				}).
				Return(nil)

			go poller.Poll()

			time.Sleep(35 * time.Second)
			require.NoError(t, poller.Shutdown(t.Context()))

			mockClient2.AssertNumberOfCalls(t, "FetchTask", 2) // ensure the second call where task capacity is asserted was completed

			teardown(t, mockClient1, mockClient2, mockRunner)
		})
	})

	// each client should have separate tracking for TasksVersion field
	t.Run("independent task versions", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			pollingCtx, cancel := context.WithCancel(t.Context())
			defer cancel()

			mockClient1, mockClient2, mockRunner, poller := setup(t, pollingCtx)

			// Return 123 task version for mockClient1, 456 for mockClient2, verify that the same value is used in
			// follow-up FetchTask calls.
			mockClient1.On("FetchTask", mock.Anything, mock.Anything).
				Once().
				Return(connect.NewResponse(&runnerv1.FetchTaskResponse{
					Task:            nil,
					TasksVersion:    int64(123),
					AdditionalTasks: nil,
				}), nil)
			mockClient1.On("FetchTask", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					req := args.Get(1).(*connect.Request[runnerv1.FetchTaskRequest])
					assert.EqualValues(t, 123, req.Msg.GetTasksVersion(), "GetTasksVersion")
				}).
				Return(emptyResponse, nil)

			mockClient2.On("FetchTask", mock.Anything, mock.Anything).
				Once().
				Return(connect.NewResponse(&runnerv1.FetchTaskResponse{
					Task:            nil,
					TasksVersion:    int64(456),
					AdditionalTasks: nil,
				}), nil)
			mockClient2.On("FetchTask", mock.Anything, mock.Anything).
				Run(func(args mock.Arguments) {
					req := args.Get(1).(*connect.Request[runnerv1.FetchTaskRequest])
					assert.EqualValues(t, 456, req.Msg.GetTasksVersion(), "GetTasksVersion")
				}).
				Return(emptyResponse, nil)

			go poller.Poll()

			time.Sleep(35 * time.Second)
			require.NoError(t, poller.Shutdown(t.Context()))

			// Ensure second calls (or more) happened where assertions are.
			mockClient1.AssertNumberOfCalls(t, "FetchTask", 36)
			mockClient2.AssertNumberOfCalls(t, "FetchTask", 2)

			teardown(t, mockClient1, mockClient2, mockRunner)
		})
	})
}
