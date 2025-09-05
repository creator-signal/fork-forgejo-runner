// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"os"
	"strings"

	"connectrpc.com/connect"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"code.forgejo.org/forgejo/runner/v11/internal/app/job"
	"code.forgejo.org/forgejo/runner/v11/internal/app/run"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/envcheck"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/labels"
	"code.forgejo.org/forgejo/runner/v11/internal/pkg/ver"
)

func runJob(ctx context.Context, configFile *string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault(*configFile)
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
			// empty cfg.Container.DockerHost means act_runner need to find an available docker host automatically
			// and assign the path to cfg.Container.DockerHost
			if cfg.Container.DockerHost == "" {
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
		)

		runner := run.NewRunner(cfg, reg, cli)
		// declare the labels of the runner before fetching tasks
		resp, err := runner.Declare(ctx, ls.Names())
		if err != nil && connect.CodeOf(err) == connect.CodeUnimplemented {
			log.Warn("Because the Forgejo instance is an old version, skipping declaring the labels and version.")
		} else if err != nil {
			log.WithError(err).Error("fail to invoke Declare")
			return err
		} else {
			log.Infof("runner: %s, with version: %s, with labels: %v, declared successfully",
				resp.Msg.GetRunner().GetName(), resp.Msg.GetRunner().GetVersion(), resp.Msg.GetRunner().GetLabels())
			// if declared successfully, override the labels in the.runner file with valid labels in the config file (if specified)
			runner.Update(ctx, ls)
			reg.Labels = ls.ToStrings()
			if err := config.SaveRegistration(cfg.Runner.File, reg); err != nil {
				return fmt.Errorf("failed to save runner config: %w", err)
			}
		}

		j := job.NewJob(cfg, cli, runner)
		return j.Run(ctx)
	}
}
