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
	clients []client.Client
	runners []run.RunnerInterface
	cfg     *config.Config

	pollingCtx      context.Context
	shutdownPolling context.CancelFunc

	jobsCtx      context.Context
	shutdownJobs context.CancelFunc

	done chan any
}

func New(ctx context.Context, cfg *config.Config, clients []client.Client, runners []run.RunnerInterface) Poller {
	if len(clients) != len(runners) {
		panic("len(clients) must equal len(runners)")
	}
	return (&poller{}).init(ctx, cfg, clients, runners)
}

func (p *poller) init(ctx context.Context, cfg *config.Config, clients []client.Client, runners []run.RunnerInterface) Poller {
	pollingCtx, shutdownPolling := context.WithCancel(ctx)

	jobsCtx, shutdownJobs := context.WithCancel(ctx)

	done := make(chan any)

	p.clients = clients
	p.runners = runners
	p.cfg = cfg

	p.pollingCtx = pollingCtx
	p.shutdownPolling = shutdownPolling

	p.jobsCtx = jobsCtx
	p.shutdownJobs = shutdownJobs
	p.done = done

	return p
}

func (p *poller) Poll() {
	limiters := make([]*rate.Limiter, len(p.clients))
	for i := range p.clients {
		limiters[i] = rate.NewLimiter(rate.Every(p.clients[i].FetchInterval()), 1)
	}
	taskVersions := make([]atomic.Int64, len(p.clients))

	capacity := int64(p.cfg.Runner.Capacity)
	inProgressTasks := atomic.Int64{}
	wg := &sync.WaitGroup{}

	// When we start a FetchTask, we'll be requesting (capacity - inProgressTasks) tasks from a remote and may receive
	// up to that number.  We can't perform multiple fetches simulanteously or else we could be overprovisioned for
	// capacity.  fetchMutex is held during each fetch.  It's not a sync.Mutex because those aren't supported by
	// synctest; a buffered channel of size 1 is used as a replacement.
	fetchMutex := make(chan any, 1)

	log.Infof("[poller] launched")
	for i := range p.clients {
		wg.Go(func() {
			p.pollForClient(limiters[i], p.clients[i], p.runners[i], capacity, fetchMutex, &taskVersions[i], &inProgressTasks, wg)
		})
	}

	wg.Wait()
	log.Trace("[poller] shutdown complete, all tasks complete")

	// signal the poller is finished
	close(p.done)
}

func (p *poller) pollForClient(limiter *rate.Limiter, client client.Client, runner run.RunnerInterface, capacity int64, fetchMutex chan any, taskVersions, inProgressTasks *atomic.Int64, wg *sync.WaitGroup) {
	for {
		if err := limiter.Wait(p.pollingCtx); err != nil {
			log.Infof("[poller] shutdown begin, %d tasks currently running", inProgressTasks.Load())
			return
		}

		fetchMutex <- struct{}{} // lock mutex
		availableCapacity := capacity - inProgressTasks.Load()
		if availableCapacity > 0 {
			log.Tracef("[poller] fetching at most %d tasks from client %s", availableCapacity, client.Address())
			tasks, ok := p.fetchTasks(p.pollingCtx, client, taskVersions, availableCapacity)
			inProgressTasks.Add(int64(len(tasks)))
			<-fetchMutex // unlock mutex by draining channel
			if !ok {
				continue
			}

			log.Tracef("[poller] successfully fetched %d tasks from client %s", len(tasks), client.Address())
			for _, task := range tasks {
				wg.Go(func() {
					p.runTaskWithRecover(p.jobsCtx, runner, task)
					inProgressTasks.Add(-1)
				})
			}
		} else {
			<-fetchMutex // unlock mutex by draining channel
		}
	}
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

func (p *poller) runTaskWithRecover(ctx context.Context, runner run.RunnerInterface, task *runnerv1.Task) {
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic: %v", r)
			log.WithError(err).Error("panic in runTaskWithRecover")
		}
	}()

	if err := runner.Run(ctx, task); err != nil {
		log.WithError(err).Error("failed to run task")
	}
}

func (p *poller) fetchTasks(ctx context.Context, client client.Client, tasksVersion *atomic.Int64, availableCapacity int64) ([]*runnerv1.Task, bool) {
	if availableCapacity == 0 {
		return nil, false
	}

	reqCtx, cancel := context.WithTimeout(ctx, p.cfg.Runner.FetchTimeout)
	defer cancel()

	v := tasksVersion.Load()
	resp, err := client.FetchTask(reqCtx, connect.NewRequest(&runnerv1.FetchTaskRequest{
		TasksVersion: tasksVersion.Load(),
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
		tasksVersion.CompareAndSwap(v, resp.Msg.GetTasksVersion())
	}

	taskSlice := []*runnerv1.Task{}
	// Normally we'd expect to get a Task, and maybe AdditionalTasks.  But we're resilient here to a bug in Forgejo
	// 14.0.0 & 14.0.1 where we might, rarely, get AdditionalTasks without a Task.
	if resp.Msg.Task != nil {
		taskSlice = append(taskSlice, resp.Msg.Task)
	}
	taskSlice = append(taskSlice, resp.Msg.GetAdditionalTasks()...)

	if len(taskSlice) == 0 {
		return nil, false
	} else if resp.Msg.Task == nil {
		log.Warn("FetchTask received tasks in AdditionalTasks field but not Task field; this is unexpected but runner will run them")
	}

	// got a task, set `tasksVersion` to zero to force query db in next request.
	tasksVersion.CompareAndSwap(resp.Msg.GetTasksVersion(), 0)
	return taskSlice, true
}
