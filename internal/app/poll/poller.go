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

	"gitea.com/gitea/act_runner/internal/app/run"
	"gitea.com/gitea/act_runner/internal/pkg/client"
	"gitea.com/gitea/act_runner/internal/pkg/config"
)

const PollerID = "PollerID"

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

func New(cfg *config.Config, client client.Client, runner run.RunnerInterface) Poller {
	return (&poller{}).init(cfg, client, runner)
}

func (p *poller) init(cfg *config.Config, client client.Client, runner run.RunnerInterface) Poller {
	pollingCtx, shutdownPolling := context.WithCancel(context.Background())

	jobsCtx, shutdownJobs := context.WithCancel(context.Background())

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
	wg := &sync.WaitGroup{}
	for i := 0; i < p.cfg.Runner.Capacity; i++ {
		wg.Add(1)
		go p.poll(i, wg, limiter)
	}
	wg.Wait()

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
		log.Trace("forcing the jobs to shutdown")
		p.shutdownJobs()
		<-p.done
		log.Trace("all jobs have been shutdown")
		return ctx.Err()
	}
}

func (p *poller) poll(id int, wg *sync.WaitGroup, limiter *rate.Limiter) {
	log.Infof("[poller %d] launched", id)
	defer wg.Done()
	for {
		if err := limiter.Wait(p.pollingCtx); err != nil {
			log.Infof("[poller %d] shutdown", id)
			return
		}
		task, ok := p.fetchTask(p.pollingCtx)
		if !ok {
			continue
		}
		p.runTaskWithRecover(p.jobsCtx, task)
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

func (p *poller) fetchTask(ctx context.Context) (*runnerv1.Task, bool) {
	reqCtx, cancel := context.WithTimeout(ctx, p.cfg.Runner.FetchTimeout)
	defer cancel()

	// Load the version value that was in the cache when the request was sent.
	v := p.tasksVersion.Load()
	resp, err := p.client.FetchTask(reqCtx, connect.NewRequest(&runnerv1.FetchTaskRequest{
		TasksVersion: v,
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

	if resp.Msg.TasksVersion > v {
		p.tasksVersion.CompareAndSwap(v, resp.Msg.TasksVersion)
	}

	if resp.Msg.Task == nil {
		return nil, false
	}

	// got a task, set `tasksVersion` to zero to focre query db in next request.
	p.tasksVersion.CompareAndSwap(resp.Msg.TasksVersion, 0)

	return resp.Msg.Task, true
}
