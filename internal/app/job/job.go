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

	runnerv1 "code.gitea.io/actions-proto-go/runner/v1"
	"gitea.com/gitea/act_runner/internal/app/run"
	"gitea.com/gitea/act_runner/internal/pkg/client"
	"gitea.com/gitea/act_runner/internal/pkg/config"
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
	task, ok := j.fetchTask(ctx)
	if !ok {
		return fmt.Errorf("could not fetch task")
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

func (j *Job) fetchTask(ctx context.Context) (*runnerv1.Task, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, j.cfg.Runner.FetchTimeout)
	defer cancel()

	// Load the version value that was in the cache when the request was sent.
	v := j.tasksVersion.Load()
	resp, err := j.client.FetchTask(reqCtx, connect.NewRequest(&runnerv1.FetchTaskRequest{
		TasksVersion: v,
	}))
	if err != nil {
		if errors.Is(err, context.Canceled) {
			log.WithError(err).Debugf("shutdown, fetch task canceled")
		} else {
			log.WithError(err).Error("failed to fetch task")
		}
		return nil, false
	}

	if resp == nil || resp.Msg == nil {
		return nil, false
	}

	if resp.Msg.TasksVersion > v {
		j.tasksVersion.CompareAndSwap(v, resp.Msg.TasksVersion)
	}

	if resp.Msg.Task == nil {
		return nil, false
	}

	j.tasksVersion.CompareAndSwap(resp.Msg.TasksVersion, 0)

	return resp.Msg.Task, true
}
