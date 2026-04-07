// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package poll

import (
	"context"
	"errors"

	runnerv1 "code.forgejo.org/forgejo/actions-proto/runner/v1"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"connectrpc.com/connect"
	gouuid "github.com/google/uuid"
	log "github.com/sirupsen/logrus"
	"golang.org/x/time/rate"
)

// ErrNoTaskReceived signals that no task was received from the server.
var ErrNoTaskReceived = errors.New("no task received")

//mockery:generate: true
//mockery:filename: mocks/single.go
//mockery:pkgname: mocks
type SingleTaskPoller interface {
	Poll() error
	Shutdown(ctx context.Context) error
}

type singleTaskPoller struct {
	client client.Client
	runner run.RunnerInterface
	cfg    *config.Config

	pollingCtx    context.Context
	cancelPolling context.CancelFunc

	taskCtx     context.Context
	cancelTasks context.CancelFunc

	done chan any

	wait   bool
	handle *string
}

func NewSingleTaskPoller(ctx context.Context, cfg *config.Config, client client.Client, runner run.RunnerInterface, wait bool, handle *string) SingleTaskPoller {
	pollingCtx, cancelPolling := context.WithCancel(ctx)
	taskCtx, cancelTasks := context.WithCancel(ctx)

	return &singleTaskPoller{
		client:        client,
		runner:        runner,
		cfg:           cfg,
		pollingCtx:    pollingCtx,
		cancelPolling: cancelPolling,
		taskCtx:       taskCtx,
		cancelTasks:   cancelTasks,
		done:          make(chan any),
		wait:          wait,
		handle:        handle,
	}
}

func (s *singleTaskPoller) Poll() error {
	rateLimiter := rate.NewLimiter(rate.Every(s.client.FetchInterval()), 1)

	log.Info("single task poller launched")

	// When the FetchTask() API is invoked to create a task, unpreventable environmental errors may occur; for example,
	// network disconnects and timeouts. It's possible that these errors occur after the server-side has assigned a task
	// to the runner during the API call, in which case the error would cause that task to be lost between the two
	// systems -- the server will think it's assigned to the runner, and the runner never received it.
	//
	// The solution implemented here is idempotency in the FetchTask() API call, which means that the "same" FetchTask()
	// API call is expected to return the same values. Specifically, the runner creates a unique identifier `requestKey`
	// which is transmitted to the server along with each FetchTask() invocation which defines the sameness of the call,
	// and the runner retains the `requestKey` value until the API call receives a successful response. If the server
	// implements idempotency, it can use this key to identify repeated invocations of FetchTask() and when the same
	// request is received, the same response is provided.
	//
	// Runner's responsibility is to send the same request key consistently if any error occurred, and, change it to a
	// new key when a successful call is received.
	requestKey := gouuid.New()

	for {
		if err := rateLimiter.Wait(s.pollingCtx); err != nil {
			log.Infof("single task poller is shutting down")
			close(s.done)
			return nil
		}

		log.Tracef("single task poller asking client %s for a task", s.client.Address())

		// fetchSingleTask() has been introduced with Forgejo 15. That means fetchTask() can be removed and replaced
		// with fetchSingleTask() once Forgejo 14 and earlier have reached their end of life.
		var task *runnerv1.Task
		var reuseRequestKey bool
		if s.handle != nil {
			task, reuseRequestKey = s.fetchSingleTask(s.pollingCtx, s.client, requestKey)
		} else {
			task, reuseRequestKey = s.fetchTask(s.pollingCtx, s.client, requestKey)
		}

		if !reuseRequestKey {
			requestKey = gouuid.New()
		}
		if task == nil && s.wait {
			log.Infof("single task poller received no task from %s, trying again", s.client.Address())
			continue
		}

		var err error
		if task != nil {
			log.Infof("single task poller successfully fetched one task from %s", s.client.Address())
			s.runner.Run(s.taskCtx, task)
		} else {
			log.Debugf("single task poller received no task from %s", s.client.Address())
			err = ErrNoTaskReceived
		}

		log.Info("single task poller is shutting down")
		s.cancelPolling()

		// Signal that the poller is done.
		close(s.done)

		return err
	}
}

func (s *singleTaskPoller) Shutdown(ctx context.Context) error {
	s.cancelPolling()

	select {
	case <-s.done:
		log.Trace("all tasks have completed")
		return nil

	case <-ctx.Done():
		log.Info("forcing the the running task to stop")
		s.cancelTasks()
		<-s.done
		log.Info("all tasks have been shut down")
		return ctx.Err()
	}
}

func (s *singleTaskPoller) fetchTask(ctx context.Context, client client.Client, requestKey gouuid.UUID) (task *runnerv1.Task, reuseRequestKey bool) {
	cleanupRequestKey := client.SetRequestKey(requestKey)
	defer cleanupRequestKey()

	reqCtx, cancel := context.WithTimeout(ctx, s.cfg.Runner.FetchTimeout)
	defer cancel()

	resp, err := client.FetchTask(reqCtx, connect.NewRequest(&runnerv1.FetchTaskRequest{TasksVersion: 0}))
	if errors.Is(err, context.DeadlineExceeded) {
		log.Error("failed to fetch task: deadline exceeded; increase fetch_timeout if this error is persistent")
		return nil, true
	} else if err != nil {
		if errors.Is(err, context.Canceled) {
			log.WithError(err).Debugf("shutdown, task fetching cancelled")
		} else {
			log.WithError(err).Error("failed to fetch task")
		}
		return nil, true
	}

	if resp == nil || resp.Msg == nil {
		return nil, true
	}

	// We've made a successful request without an error. Regardless of whether we've received tasks or not, we now
	// signal the calling loop that the request key should not be reused.
	return resp.Msg.GetTask(), false
}

func (s *singleTaskPoller) fetchSingleTask(ctx context.Context, client client.Client, requestKey gouuid.UUID) (task *runnerv1.Task, reuseRequestKey bool) {
	cleanupRequestKey := client.SetRequestKey(requestKey)
	defer cleanupRequestKey()

	reqCtx, cancel := context.WithTimeout(ctx, s.cfg.Runner.FetchTimeout)
	defer cancel()

	resp, err := client.FetchSingleTask(reqCtx, connect.NewRequest(&runnerv1.FetchSingleTaskRequest{
		TasksVersion: 0,
		Handle:       s.handle,
	}))
	if errors.Is(err, context.DeadlineExceeded) {
		log.Trace("failed to fetch task: deadline exceeded")
		return nil, true
	} else if err != nil {
		if errors.Is(err, context.Canceled) {
			log.WithError(err).Debugf("shutdown, task fetching cancelled")
		} else {
			log.WithError(err).Error("failed to fetch task")
		}
		return nil, true
	}

	if resp == nil || resp.Msg == nil {
		return nil, true
	}

	// We've made a successful request without an error. Regardless of whether we've received tasks or not, we now
	// signal the calling loop that the request key should not be reused.
	return resp.Msg.GetTask(), false
}
