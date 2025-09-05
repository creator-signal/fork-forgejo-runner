package run

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	pingv1 "code.forgejo.org/forgejo/actions-proto/ping/v1"
	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/labels"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/report"
	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestExplainFailedGenerateWorkflow(t *testing.T) {
	logged := ""
	log := func(message string, args ...any) {
		logged += fmt.Sprintf(message, args...) + "\n"
	}
	task := &runnerv1.Task{
		WorkflowPayload: []byte("on: [push]\njobs:\n"),
	}
	generateWorkflowError := errors.New("message 1\nmessage 2")
	err := explainFailedGenerateWorkflow(task, log, generateWorkflowError)
	assert.Error(t, err)
	assert.Equal(t, "    1: on: [push]\n    2: jobs:\n    3: \nErrors were found and although they tend to be cryptic the line number they refer to gives a hint as to where the problem might be.\nmessage 1\nmessage 2\n", logged)
}

func TestLabelUpdate(t *testing.T) {
	ctx := context.Background()
	ls := labels.Labels{}

	initialLabel, err := labels.Parse("testlabel:docker://alpine")
	assert.NoError(t, err)
	ls = append(ls, initialLabel)

	newLs := labels.Labels{}

	newLabel, err := labels.Parse("next label:host")
	assert.NoError(t, err)
	newLs = append(newLs, initialLabel)
	newLs = append(newLs, newLabel)

	runner := Runner{
		labels: ls,
	}

	assert.Contains(t, runner.labels, initialLabel)
	assert.NotContains(t, runner.labels, newLabel)

	runner.Update(ctx, newLs)

	assert.Contains(t, runner.labels, initialLabel)
	assert.Contains(t, runner.labels, newLabel)
}

type forgejoClientMock struct {
	mock.Mock
}

func (m *forgejoClientMock) Address() string {
	args := m.Called()
	return args.String(0)
}

func (m *forgejoClientMock) Insecure() bool {
	args := m.Called()
	return args.Bool(0)
}

func (m *forgejoClientMock) Ping(ctx context.Context, request *connect.Request[pingv1.PingRequest]) (*connect.Response[pingv1.PingResponse], error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*connect.Response[pingv1.PingResponse]), args.Error(1)
}

func (m *forgejoClientMock) Register(ctx context.Context, request *connect.Request[runnerv1.RegisterRequest]) (*connect.Response[runnerv1.RegisterResponse], error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*connect.Response[runnerv1.RegisterResponse]), args.Error(1)
}

func (m *forgejoClientMock) Declare(ctx context.Context, request *connect.Request[runnerv1.DeclareRequest]) (*connect.Response[runnerv1.DeclareResponse], error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*connect.Response[runnerv1.DeclareResponse]), args.Error(1)
}

func (m *forgejoClientMock) FetchTask(ctx context.Context, request *connect.Request[runnerv1.FetchTaskRequest]) (*connect.Response[runnerv1.FetchTaskResponse], error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*connect.Response[runnerv1.FetchTaskResponse]), args.Error(1)
}

func (m *forgejoClientMock) UpdateTask(ctx context.Context, request *connect.Request[runnerv1.UpdateTaskRequest]) (*connect.Response[runnerv1.UpdateTaskResponse], error) {
	args := m.Called(ctx, request)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*connect.Response[runnerv1.UpdateTaskResponse]), args.Error(1)
}

func (m *forgejoClientMock) UpdateLog(ctx context.Context, request *connect.Request[runnerv1.UpdateLogRequest]) (*connect.Response[runnerv1.UpdateLogResponse], error) {
	// Enable for log output from runs if needed.
	// for _, row := range request.Msg.Rows {
	// 	println(fmt.Sprintf("UpdateLog: %q", row.Content))
	// }
	args := m.Called(ctx, request)
	mockRetval := args.Get(0)
	mockError := args.Error(1)
	if mockRetval != nil {
		return mockRetval.(*connect.Response[runnerv1.UpdateLogResponse]), mockError
	} else if mockError != nil {
		return nil, mockError
	}
	// Unless overridden by mock, default to returning indication that logs were received successfully
	return connect.NewResponse(&runnerv1.UpdateLogResponse{
		AckIndex: request.Msg.Index + int64(len(request.Msg.Rows)),
	}), nil
}

func TestRunnerCacheConfiguration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	forgejoClient := &forgejoClientMock{}

	forgejoClient.On("Address").Return("https://127.0.0.1:8080") // not expected to be used in this test
	forgejoClient.On("UpdateLog", mock.Anything, mock.Anything).Return(nil, nil)
	forgejoClient.On("UpdateTask", mock.Anything, mock.Anything).
		Return(connect.NewResponse(&runnerv1.UpdateTaskResponse{}), nil)

	runner := NewRunner(
		&config.Config{
			Cache: config.Cache{
				// Note that this test requires that containers on the local dev environment can access localhost to
				// reach the cache proxy, and that the cache proxy can access localhost to reach the cache, both of
				// which are embedded servers that the Runner will start.  If any specific firewall config is needed
				// it's easier to do that with statically configured ports, so...
				Port:      40713,
				ProxyPort: 40714,
				Dir:       t.TempDir(),
			},
			Host: config.Host{
				WorkdirParent: t.TempDir(),
			},
		},
		&config.Registration{
			Labels: []string{"ubuntu-latest:docker://code.forgejo.org/oci/node:20-bookworm"},
		},
		forgejoClient)
	require.NotNil(t, runner)

	// Must set up cache for our test
	require.NotNil(t, runner.cacheProxy)

	runWorkflow := func(ctx context.Context, cancel context.CancelFunc, yamlContent, eventName, ref, description string) {
		task := &runnerv1.Task{
			WorkflowPayload: []byte(yamlContent),
			Context: &structpb.Struct{
				Fields: map[string]*structpb.Value{
					"token":                       structpb.NewStringValue("some token here"),
					"forgejo_default_actions_url": structpb.NewStringValue("https://data.forgejo.org"),
					"repository":                  structpb.NewStringValue("runner"),
					"event_name":                  structpb.NewStringValue(eventName),
					"ref":                         structpb.NewStringValue(ref),
				},
			},
		}

		reporter := report.NewReporter(ctx, cancel, forgejoClient, task, time.Second)
		err := runner.run(ctx, task, reporter)
		reporter.Close(nil)
		require.NoError(t, err, description)
	}

	t.Run("Cache accessible", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Step 1: Populate shared cache with push workflow
		populateYaml := `
name: Cache Testing Action
on:
  push:
jobs:
  job-cache-check-1:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v4
        with:
          path: cache_path_1
          key: cache-key-1
      - run: |
          mkdir -p cache_path_1
          echo "Hello from push workflow!" > cache_path_1/cache_content_1
`
		runWorkflow(ctx, cancel, populateYaml, "push", "refs/heads/main", "step 1: push cache populate expected to succeed")

		// Step 2: Validate that cache is accessible; mostly a sanity check that the test environment and mock context
		// provides everything needed for the cache setup.
		checkYaml := `
name: Cache Testing Action
on:
  push:
jobs:
  job-cache-check-2:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v4
        with:
          path: cache_path_1
          key: cache-key-1
      - run: |
          [[ -f cache_path_1/cache_content_1 ]] && echo "Step 2: cache file found." || echo "Step 2: cache file missing!"
          [[ -f cache_path_1/cache_content_1 ]] || exit 1
`
		runWorkflow(ctx, cancel, checkYaml, "push", "refs/heads/main", "step 2: push cache check expected to succeed")
	})

	t.Run("PR cache pollution prevented", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		// Step 1: Populate shared cache with push workflow
		populateYaml := `
name: Cache Testing Action
on:
  push:
jobs:
  job-cache-check-1:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v4
        with:
          path: cache_path_1
          key: cache-key-1
      - run: |
          mkdir -p cache_path_1
          echo "Hello from push workflow!" > cache_path_1/cache_content_1
`
		runWorkflow(ctx, cancel, populateYaml, "push", "refs/heads/main", "step 1: push cache populate expected to succeed")

		// Step 2: Check if pull_request can read push cache, should be available as it's a trusted cache.
		checkPRYaml := `
name: Cache Testing Action
on:
  pull_request:
jobs:
  job-cache-check-2:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v4
        with:
          path: cache_path_1
          key: cache-key-1
      - run: |
          [[ -f cache_path_1/cache_content_1 ]] && echo "Step 2: cache file found." || echo "Step 2: cache file missing!"
          [[ -f cache_path_1/cache_content_1 ]] || exit 1
`
		runWorkflow(ctx, cancel, checkPRYaml, "pull_request", "refs/pull/1234/head", "step 2: PR should read push cache")

		// Step 3: Pull request writes to cache; here we need to use a new cache key because we'll get a cache-hit like we
		// did in step #2 if we keep the same key, and then the cache contents won't be updated.
		populatePRYaml := `
name: Cache Testing Action
on:
  pull_request:
jobs:
  job-cache-check-3:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v4
        with:
          path: cache_path_1
          key: cache-key-2
      - run: |
          mkdir -p cache_path_1
          echo "Hello from PR workflow!" > cache_path_1/cache_content_2
`
		runWorkflow(ctx, cancel, populatePRYaml, "pull_request", "refs/pull/1234/head", "step 3: PR cache populate expected to succeed")

		// Step 4: Check if pull_request can read its own cache written by step #3.
		checkPRKey2Yaml := `
name: Cache Testing Action
on:
  pull_request:
jobs:
  job-cache-check-4:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v4
        with:
          path: cache_path_1
          key: cache-key-2
      - run: |
          [[ -f cache_path_1/cache_content_2 ]] && echo "Step 4 cache file found." || echo "Step 4 cache file missing!"
          [[ -f cache_path_1/cache_content_2 ]] || exit 1
`
		runWorkflow(ctx, cancel, checkPRKey2Yaml, "pull_request", "refs/pull/1234/head", "step 4: PR should read its own cache")

		// Step 5: Check that the push workflow cannot access the isolated cache that was written by the pull_request in
		// step #3, ensuring that it's not possible to pollute the cache by predicting cache keys.
		checkKey2Yaml := `
name: Cache Testing Action
on:
  push:
jobs:
  job-cache-check-6:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/cache@v4
        with:
          path: cache_path_1
          key: cache-key-2
      - run: |
          [[ -f cache_path_1/cache_content_2 ]] && echo "Step 5 cache file found, oh no!" || echo "Step 5: cache file missing as expected."
          [[ -f cache_path_1/cache_content_2 ]] && exit 1 || exit 0
`
		runWorkflow(ctx, cancel, checkKey2Yaml, "push", "refs/heads/main", "step 5: push cache should not be polluted by PR")
	})
}
