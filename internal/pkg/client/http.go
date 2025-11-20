// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package client

import (
	"context"
	"crypto/tls"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"code.forgejo.org/forgejo/actions-proto/ping/v1/pingv1connect"
	"code.forgejo.org/forgejo/actions-proto/runner/v1/runnerv1connect"
	"connectrpc.com/connect"
	log "github.com/sirupsen/logrus"
)

func getHTTPClient(endpoint string, insecure bool) *http.Client {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
	}

	if strings.HasPrefix(endpoint, "https://") && insecure {
		transport.TLSClientConfig = &tls.Config{
			InsecureSkipVerify: true,
		}
	}

	// Typically all accesses to the Forgejo server will occur in a context with a timeout, like when running a step or
	// an action, but occasionally some actions like closing a `Reporter` will occur in `context.Background` to ensure
	// that transmission of the final logs of an action can progress even when the job is timed out.  In those cases,
	// it's important to have an HTTP-level timeout (which `http.DefaultClient` does not have) so that the processes
	// don't get frozen in the event of irregular network activity.  Overriddable with FORGEJO_CLIENT_TIMEOUT.
	timeout := 60 * time.Second
	env := os.Getenv("FORGEJO_CLIENT_TIMEOUT")
	if env != "" {
		timeoutSeconds, err := strconv.ParseInt(env, 10, 64)
		if err != nil {
			log.Errorf("Failed to parse FORGEJO_CLIENT_TIMEOUT: %v", err)
		} else {
			timeout = time.Duration(timeoutSeconds) * time.Second
		}
	}

	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
	}
}

// New returns a new runner client.
func New(endpoint string, insecure bool, uuid, token, version string, opts ...connect.ClientOption) *HTTPClient {
	baseURL := strings.TrimRight(endpoint, "/") + "/api/actions"

	opts = append(opts, connect.WithInterceptors(connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if uuid != "" {
				req.Header().Set(UUIDHeader, uuid)
			}
			if token != "" {
				req.Header().Set(TokenHeader, token)
			}
			return next(ctx, req)
		}
	})))

	return &HTTPClient{
		PingServiceClient: pingv1connect.NewPingServiceClient(
			getHTTPClient(endpoint, insecure),
			baseURL,
			opts...,
		),
		RunnerServiceClient: runnerv1connect.NewRunnerServiceClient(
			getHTTPClient(endpoint, insecure),
			baseURL,
			opts...,
		),
		endpoint: endpoint,
		insecure: insecure,
	}
}

func (c *HTTPClient) Address() string {
	return c.endpoint
}

func (c *HTTPClient) Insecure() bool {
	return c.insecure
}

var _ Client = (*HTTPClient)(nil)

// An HTTPClient manages communication with the runner API.
type HTTPClient struct {
	pingv1connect.PingServiceClient
	runnerv1connect.RunnerServiceClient
	endpoint string
	insecure bool
}
