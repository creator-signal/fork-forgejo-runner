// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"code.forgejo.org/forgejo/runner/v12/act/cacheproxy"
	"code.forgejo.org/forgejo/runner/v12/internal/app/job"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/envcheck"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/ver"
)

type runJobArgs struct {
	wait bool
}

func runJob(ctx context.Context, configFile *string, runJobArgs *runJobArgs) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := config.New(
			config.FromFile(*configFile),
			config.FromRegistration,
		)
		if err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		} else if len(cfg.Server.Connections) != 1 {
			return fmt.Errorf("one-job is only supported with a single connection, but %d connections are configured", len(cfg.Server.Connections))
		}

		var connName string
		var conn *config.Connection
		for name, c := range cfg.Server.Connections {
			connName = name
			conn = c
		}

		initLogging(cfg)
		log.Infoln("Starting job")

		requireDocker := false
		for _, conn := range cfg.Server.Connections {
			if conn.Labels.RequireDocker() {
				requireDocker = true
				break
			}
		}
		if requireDocker {
			dockerSocketPath, err := getDockerSocketPath(cfg.Container.DockerHost)
			if err != nil {
				return err
			}
			if err := envcheck.CheckIfDockerRunning(ctx, dockerSocketPath); err != nil {
				return err
			}
			// if dockerSocketPath passes the check, override DOCKER_HOST with dockerSocketPath
			os.Setenv("DOCKER_HOST", dockerSocketPath)
			// cfg.Container.DockerHost set to "automount" means act_runner need to find an available docker host automatically
			// and assign the path to cfg.Container.DockerHost
			if cfg.Container.DockerHost == "automount" {
				cfg.Container.DockerHost = dockerSocketPath
			}
			// check the scheme, if the scheme is not npipe or unix
			// set cfg.Container.DockerHost to "-" because it can't be mounted to the job container
			if protoIndex := strings.Index(cfg.Container.DockerHost, "://"); protoIndex != -1 {
				scheme := cfg.Container.DockerHost[:protoIndex]
				if !strings.EqualFold(scheme, "npipe") && !strings.EqualFold(scheme, "unix") {
					cfg.Container.DockerHost = "-"
				}
			}
		}

		cli := client.New(
			conn.URL.String(),
			cfg.Runner.Insecure,
			conn.UUID.String(),
			conn.Token,
			ver.Version(),
			conn.FetchInterval,
		)

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

		runner, _, _, err := createRunner(ctx, connName, cfg, cli, conn.Labels, cacheProxy)
		if err != nil {
			return err
		}

		j := job.NewJob(cfg, cli, runner)
		return j.Run(ctx, runJobArgs.wait)
	}
}
