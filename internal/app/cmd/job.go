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
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/ver"
)

type runJobArgs struct {
	wait bool
}

func runJob(ctx context.Context, configFile *string, runJobArgs *runJobArgs) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := config.New(config.FromFile(*configFile))
		if err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}

		initLogging(cfg)
		log.Infoln("Starting job")

		reg, err := config.LoadRegistration(cfg.Runner.File)
		if os.IsNotExist(err) {
			log.Error("registration file not found, please register the runner first")
			return err
		} else if err != nil {
			return fmt.Errorf("failed to load registration file: %w", err)
		}

		lbls := reg.Labels
		if len(cfg.Runner.Labels) > 0 {
			lbls = cfg.Runner.Labels
		}

		ls := labels.Labels{}
		for _, l := range lbls {
			label, err := labels.Parse(l)
			if err != nil {
				log.WithError(err).Warnf("ignored invalid label %q", l)
				continue
			}
			ls = append(ls, label)
		}
		if len(ls) == 0 {
			log.Warn("no labels configured, runner may not be able to pick up jobs")
		}

		if ls.RequireDocker() {
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
			reg.Address,
			cfg.Runner.Insecure,
			reg.UUID,
			reg.Token,
			ver.Version(),
			cfg.Runner.FetchInterval,
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

		runner, _, err := createRunner(ctx, cfg, reg, cli, ls, cacheProxy)
		if err != nil {
			return err
		}

		j := job.NewJob(cfg, cli, runner)
		return j.Run(ctx, runJobArgs.wait)
	}
}
