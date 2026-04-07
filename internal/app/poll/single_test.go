// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package poll

import (
	"testing"
	"time"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	mock_runner "code.forgejo.org/forgejo/runner/v12/internal/app/run/mocks"
	mock_client "code.forgejo.org/forgejo/runner/v12/internal/pkg/client/mocks"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"connectrpc.com/connect"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestSingleTaskPoller_FetchesAvailableTask(t *testing.T) {
	cfg := &config.Config{}

	mockClient := mock_client.NewMockClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchTask", mock.Anything, connect.NewRequest(&runnerv1.FetchTaskRequest{})).
		Return(connect.NewResponse(&runnerv1.FetchTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil)

	mockRunner := mock_runner.NewMockRunner(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	taskPoller := NewSingleTaskPoller(t.Context(), cfg, mockClient, mockRunner, false, nil)
	err := taskPoller.Poll()
	require.NoError(t, err)
}

func TestSingleTaskPoller_ReturnsErrorWhenNoTaskReceived(t *testing.T) {
	cfg := &config.Config{}

	mockClient := mock_client.NewMockClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchTask", mock.Anything, connect.NewRequest(&runnerv1.FetchTaskRequest{})).
		Return(connect.NewResponse(&runnerv1.FetchTaskResponse{Task: nil, TasksVersion: int64(1)}), nil)

	mockRunner := mock_runner.NewMockRunner(t)

	taskPoller := NewSingleTaskPoller(t.Context(), cfg, mockClient, mockRunner, false, nil)
	err := taskPoller.Poll()
	require.ErrorIs(t, err, ErrNoTaskReceived)
}

func TestSingleTaskPoller_WaitsForAvailableTask(t *testing.T) {
	cfg := &config.Config{}

	mockClient := mock_client.NewMockClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchTask", mock.Anything, connect.NewRequest(&runnerv1.FetchTaskRequest{})).
		Return(connect.NewResponse(&runnerv1.FetchTaskResponse{Task: nil, TasksVersion: int64(1)}), nil).Once().
		On("FetchTask", mock.Anything, connect.NewRequest(&runnerv1.FetchTaskRequest{})).
		Return(connect.NewResponse(&runnerv1.FetchTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil).Once()

	mockRunner := mock_runner.NewMockRunner(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	taskPoller := NewSingleTaskPoller(t.Context(), cfg, mockClient, mockRunner, true, nil)
	err := taskPoller.Poll()
	require.NoError(t, err)
}

func TestSingleTaskPoller_FetchesRequestedTask(t *testing.T) {
	cfg := &config.Config{}

	handle := "fc0dbe3b-aca2-4ad7-9b7e-d8d7a15dfc42"

	mockClient := mock_client.NewMockClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchSingleTask", mock.Anything, connect.NewRequest(&runnerv1.FetchSingleTaskRequest{Handle: &handle})).
		Return(connect.NewResponse(&runnerv1.FetchSingleTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil)

	mockRunner := mock_runner.NewMockRunner(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	taskPoller := NewSingleTaskPoller(t.Context(), cfg, mockClient, mockRunner, false, &handle)
	err := taskPoller.Poll()
	require.NoError(t, err)
}

func TestSingleTaskPoller_ReturnsErrorWhenRequestedTaskNotReceived(t *testing.T) {
	cfg := &config.Config{}

	handle := "fc0dbe3b-aca2-4ad7-9b7e-d8d7a15dfc42"

	mockClient := mock_client.NewMockClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchSingleTask", mock.Anything, connect.NewRequest(&runnerv1.FetchSingleTaskRequest{Handle: &handle})).
		Return(connect.NewResponse(&runnerv1.FetchSingleTaskResponse{Task: nil, TasksVersion: int64(1)}), nil)

	mockRunner := mock_runner.NewMockRunner(t)

	taskPoller := NewSingleTaskPoller(t.Context(), cfg, mockClient, mockRunner, false, &handle)
	err := taskPoller.Poll()
	require.ErrorIs(t, err, ErrNoTaskReceived)
}

func TestSingleTaskPoller_WaitsForRequestedTask(t *testing.T) {
	cfg := &config.Config{}

	handle := "fc0dbe3b-aca2-4ad7-9b7e-d8d7a15dfc42"

	mockClient := mock_client.NewMockClient(t)
	mockClient.
		On("Address").Return("https://example.com/forgejo").
		On("SetRequestKey", mock.Anything).Return(func() {}).
		On("FetchInterval").Return(time.Millisecond).
		On("FetchSingleTask", mock.Anything, connect.NewRequest(&runnerv1.FetchSingleTaskRequest{Handle: &handle})).
		Return(connect.NewResponse(&runnerv1.FetchSingleTaskResponse{Task: nil, TasksVersion: int64(1)}), nil).Once().
		On("FetchSingleTask", mock.Anything, connect.NewRequest(&runnerv1.FetchSingleTaskRequest{Handle: &handle})).
		Return(connect.NewResponse(&runnerv1.FetchSingleTaskResponse{Task: &runnerv1.Task{}, TasksVersion: int64(1)}), nil).Once()

	mockRunner := mock_runner.NewMockRunner(t)
	mockRunner.
		On("Run", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {})

	taskPoller := NewSingleTaskPoller(t.Context(), cfg, mockClient, mockRunner, true, &handle)
	err := taskPoller.Poll()
	require.NoError(t, err)
}
