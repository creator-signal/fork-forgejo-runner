// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package client

import (
	"time"

	"code.forgejo.org/forgejo/actions-proto/ping/v1/pingv1connect"
	"code.forgejo.org/forgejo/actions-proto/runner/v1/runnerv1connect"
	gouuid "github.com/google/uuid"
)

// A Client manages communication with the runner.
//
//mockery:generate: true
//mockery:filename: mocks/client.go
//mockery:pkgname: mocks
type Client interface {
	pingv1connect.PingServiceClient
	runnerv1connect.RunnerServiceClient
	Address() string
	Insecure() bool
	FetchInterval() time.Duration
	SetRequestKey(gouuid.UUID) func()
}
