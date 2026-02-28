// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"

	log "github.com/sirupsen/logrus"

	"code.forgejo.org/forgejo/runner/v12/act/cacheproxy"
	"code.forgejo.org/forgejo/runner/v12/internal/app/job"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
)

type runJobArgs struct {
	wait bool
}

var initializeRunJobConfig = func(configFile *string) (*config.Config, error) {
	cfg, err := config.New(
		config.FromFile(*configFile),
		config.FromRegistration,
	)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

func runJob(ctx context.Context, configFile *string, runJobArgs *runJobArgs) error {
	cfg, err := initializeRunJobConfig(configFile)
	if err != nil {
		return err
	} else if len(cfg.Server.Connections) != 1 {
		return fmt.Errorf("one-job is only supported with a single connection, but %d connections are configured", len(cfg.Server.Connections))
	}

	var connName string
	var conn *config.Connection
	for name, c := range cfg.Server.Connections {
		connName = name
		conn = c

		// We always take the first (and only) connection.
		break
	}

	initLogging(cfg)
	log.Infoln("Starting job")

	err = configCheck(ctx, cfg)
	if err != nil {
		return err
	}

	var cacheProxy *cacheproxy.Handler
	if cfg.Cache.Enabled {
		cacheProxy = run.SetupCache(cfg)
		defer func() {
			if cacheProxy != nil {
				err := cacheProxy.Close()
				if err != nil {
					log.WithError(err).Error("failed to close cache")
				}
			}
		}()
	}

	client := createClient(cfg, conn)
	runner, _, _, err := createRunner(ctx, connName, cfg, client, conn.Labels, cacheProxy)
	if err != nil {
		return err
	}

	j := job.NewJob(cfg, client, runner)
	return j.Run(ctx, runJobArgs.wait)
}
