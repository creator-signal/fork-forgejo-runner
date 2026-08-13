// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build !WITHOUT_DOCKER && (linux || darwin || windows || freebsd || openbsd)

package docker

import (
	actcontainer "code.forgejo.org/forgejo/runner/v13/act/container"
)

// ExecutionEnvironment creates containers against one daemon Endpoint. It is
// the reusable Docker piece the docker and host back-ends share, so container
// creation is the same regardless of which back-end selected the daemon.
type ExecutionEnvironment struct {
	ep Endpoint
}

// NewExecutionEnvironment binds an ExecutionEnvironment to a daemon Endpoint.
func NewExecutionEnvironment(ep Endpoint) *ExecutionEnvironment {
	return &ExecutionEnvironment{ep: ep}
}

// Endpoint returns the daemon these containers run against.
func (x *ExecutionEnvironment) Endpoint() Endpoint {
	return x.ep
}

// NewContainer creates a container against this environment's daemon. The job
// container, service containers, and step containers are all made the same way;
// they differ only in the input, not in how the environment builds them.
func (x *ExecutionEnvironment) NewContainer(input *actcontainer.NewContainerInput) actcontainer.ExecutionsEnvironment {
	return NewContainer(x.ep, input)
}
