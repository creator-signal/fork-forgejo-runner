// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package job

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"connectrpc.com/connect"
	log "github.com/sirupsen/logrus"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
)

type Job struct {
	client       client.Client
	runner       run.RunnerInterface
	cfg          *config.Config
	tasksVersion atomic.Int64
}

func NewJob(cfg *config.Config, client client.Client, runner run.RunnerInterface) *Job {
	j := &Job{}

	j.client = client
	j.runner = runner
	j.cfg = cfg

	return j
}

func (j *Job) Run(ctx context.Context) error {
	task, err := j.fetchTask(ctx)
	if err != nil {
		return fmt.Errorf("could not fetch task: %w", err)
	}
	return j.runTaskWithRecover(ctx, task)
}

func (j *Job) runTaskWithRecover(ctx context.Context, task *runnerv1.Task) error {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic: %v", r)
			log.WithError(err).Error("panic in runTaskWithRecover")
		}
	}()

	if err := j.runner.Run(ctx, task); err != nil {
		log.WithError(err).Error("failed to run task")
		return err
	}
	return nil
}

func (j *Job) fetchTask(ctx context.Context) (*runnerv1.Task, error) {
	reqCtx, cancel := context.WithTimeout(ctx, j.cfg.Runner.FetchTimeout)
	defer cancel()

	// Load the version value that was in the cache when the request was sent.
	v := j.tasksVersion.Load()
	resp, err := j.client.FetchTask(reqCtx, connect.NewRequest(&runnerv1.FetchTaskRequest{
		TasksVersion: v,
	}))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("fetch task canceled: %w", err)
		} else {
			return nil, fmt.Errorf("failed to fetch task: %w", err)
		}
	}

	if resp == nil || resp.Msg == nil {
		return nil, fmt.Errorf("task fetch response or message nil")
	}

	if resp.Msg.GetTasksVersion() > v {
		j.tasksVersion.CompareAndSwap(v, resp.Msg.GetTasksVersion())
	}

	if resp.Msg.Task == nil {
		return nil, fmt.Errorf("fetched task nil")
	}

	j.tasksVersion.CompareAndSwap(resp.Msg.GetTasksVersion(), 0)

	return resp.Msg.GetTask(), nil
}
