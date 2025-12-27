// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package poll

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"connectrpc.com/connect"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"

	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
)

const PollerID = "PollerID"

//go:generate mockery --name Poller
type Poller interface {
	Poll()
	Shutdown(ctx context.Context) error
}

type poller struct {
	client       client.Client
	runner       run.RunnerInterface
	cfg          *config.Config
	tasksVersion atomic.Int64 // tasksVersion used to store the version of the last task fetched from the Gitea.

	pollingCtx      context.Context
	shutdownPolling context.CancelFunc

	jobsCtx      context.Context
	shutdownJobs context.CancelFunc

	done chan any
}

func New(ctx context.Context, cfg *config.Config, client client.Client, runner run.RunnerInterface) Poller {
	return (&poller{}).init(ctx, cfg, client, runner)
}

func (p *poller) init(ctx context.Context, cfg *config.Config, client client.Client, runner run.RunnerInterface) Poller {
	pollingCtx, shutdownPolling := context.WithCancel(ctx)

	jobsCtx, shutdownJobs := context.WithCancel(ctx)

	done := make(chan any)

	p.client = client
	p.runner = runner
	p.cfg = cfg

	p.pollingCtx = pollingCtx
	p.shutdownPolling = shutdownPolling

	p.jobsCtx = jobsCtx
	p.shutdownJobs = shutdownJobs
	p.done = done

	return p
}

func (p *poller) Poll() {
	limiter := rate.NewLimiter(rate.Every(p.cfg.Runner.FetchInterval), 1)

	capacity := int64(p.cfg.Runner.Capacity)
	inProgressTasks := atomic.Int64{}
	wg := &sync.WaitGroup{}

	log.Infof("[poller] launched")
	for {
		if err := limiter.Wait(p.pollingCtx); err != nil {
			log.Infof("[poller] shutdown begin, %d tasks currently running", inProgressTasks.Load())
			break
		}

		availableCapacity := capacity - inProgressTasks.Load()
		if availableCapacity > 0 {
			log.Tracef("[poller] fetching at most %d tasks", availableCapacity)
			tasks, ok := p.fetchTasks(p.pollingCtx, availableCapacity)
			if !ok {
				continue
			}

			log.Tracef("[poller] successfully fetched %d tasks", len(tasks))
			for _, task := range tasks {
				inProgressTasks.Add(1)
				wg.Go(func() {
					p.runTaskWithRecover(p.jobsCtx, task)
					inProgressTasks.Add(-1)
				})
			}
		}
	}

	wg.Wait()
	log.Trace("[poller] shutdown complete, all tasks complete")

	// signal the poller is finished
	close(p.done)
}

func (p *poller) Shutdown(ctx context.Context) error {
	p.shutdownPolling()

	select {
	case <-p.done:
		log.Trace("all jobs are complete")
		return nil

	case <-ctx.Done():
		log.Info("forcing the jobs to shutdown")
		p.shutdownJobs()
		<-p.done
		log.Info("all jobs have been shutdown")
		return ctx.Err()
	}
}

func (p *poller) runTaskWithRecover(ctx context.Context, task *runnerv1.Task) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic: %v", r)
			log.WithError(err).Error("panic in runTaskWithRecover")
		}
	}()

	if err := p.runner.Run(ctx, task); err != nil {
		log.WithError(err).Error("failed to run task")
	}
}

func (p *poller) fetchTasks(ctx context.Context, availableCapacity int64) ([]*runnerv1.Task, bool) {
	if availableCapacity == 0 {
		return nil, false
	}

	reqCtx, cancel := context.WithTimeout(ctx, p.cfg.Runner.FetchTimeout)
	defer cancel()

	// Load the version value that was in the cache when the request was sent.
	v := p.tasksVersion.Load()
	resp, err := p.client.FetchTask(reqCtx, connect.NewRequest(&runnerv1.FetchTaskRequest{
		TasksVersion: v,
		TaskCapacity: &availableCapacity,
	}))
	if errors.Is(err, context.DeadlineExceeded) {
		log.Trace("deadline exceeded")
		err = nil
	}
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

	if resp.Msg.GetTasksVersion() > v {
		p.tasksVersion.CompareAndSwap(v, resp.Msg.GetTasksVersion())
	}

	if resp.Msg.Task == nil {
		return nil, false
	}

	// got a task, set `tasksVersion` to zero to force query db in next request.
	p.tasksVersion.CompareAndSwap(resp.Msg.GetTasksVersion(), 0)

	taskSlice := []*runnerv1.Task{resp.Msg.GetTask()}
	taskSlice = append(taskSlice, resp.Msg.GetAdditionalTasks()...)

	return taskSlice, true
}
