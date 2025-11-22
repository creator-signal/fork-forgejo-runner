// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/mattn/go-isatty"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"

	"code.forgejo.org/forgejo/runner/v12/internal/app/poll"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/common"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/envcheck"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/ver"
)

func getRunDaemonCommandProcessor(signalContext context.Context, configFile *string) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		return runDaemon(signalContext, configFile)
	}
}

func runDaemon(signalContext context.Context, configFile *string) error {
	// signalContext will be 'done' when we receive a graceful shutdown signal; daemonContext is not a derived context
	// because we want it to 'outlive' the signalContext in order to perform graceful cleanup.
	daemonContext, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx := common.WithDaemonContext(daemonContext, daemonContext)

	cfg, err := initializeConfig(configFile)
	if err != nil {
		return err
	}

	initLogging(cfg)
	log.Infoln("Starting runner daemon")

	reg, err := loadRegistration(cfg)
	if err != nil {
		return err
	}

	cfg.Tune(reg.Address)
	ls := extractLabels(cfg, reg)

	err = configCheck(ctx, cfg, ls)
	if err != nil {
		return err
	}

	cli := createClient(cfg, reg)

	runner, runnerName, err := createRunner(ctx, cfg, reg, cli, ls)
	if err != nil {
		return err
	}

	poller := createPoller(ctx, cfg, cli, runner)

	go poller.Poll()

	<-signalContext.Done()
	log.Infof("runner: %s shutdown initiated, waiting [runner].shutdown_timeout=%s for running jobs to complete before shutting down", runnerName, cfg.Runner.ShutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(daemonContext, cfg.Runner.ShutdownTimeout)
	defer cancel()

	err = poller.Shutdown(shutdownCtx)
	if err != nil {
		log.Warnf("runner: %s cancelled in progress jobs during shutdown", runnerName)
	}
	return nil
}

var initializeConfig = func(configFile *string) (*config.Config, error) {
	cfg, err := config.LoadDefault(*configFile)
	if err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	return cfg, nil
}

// initLogging setup the global logrus logger.
var initLogging = func(cfg *config.Config) {
	isTerm := isatty.IsTerminal(os.Stdout.Fd())
	format := &log.TextFormatter{
		DisableColors: !isTerm,
		FullTimestamp: true,
	}
	log.SetFormatter(format)

	if l := cfg.Log.Level; l != "" {
		level, err := log.ParseLevel(l)
		if err != nil {
			log.WithError(err).
				Errorf("invalid log level: %q", l)
		}

		// debug level
		if level == log.DebugLevel {
			log.SetReportCaller(true)
			format.CallerPrettyfier = func(f *runtime.Frame) (string, string) {
				// get function name
				s := strings.Split(f.Function, ".")
				funcname := "[" + s[len(s)-1] + "]"
				// get file name and line number
				_, filename := path.Split(f.File)
				filename = "[" + filename + ":" + strconv.Itoa(f.Line) + "]"
				return funcname, filename
			}
			log.SetFormatter(format)
		}

		if log.GetLevel() != level {
			log.Infof("log level changed to %v", level)
			log.SetLevel(level)
		}
	}
}

var loadRegistration = func(cfg *config.Config) (*config.Registration, error) {
	reg, err := config.LoadRegistration(cfg.Runner.File)
	if os.IsNotExist(err) {
		log.Error("registration file not found, please register the runner first")
		return nil, err
	} else if err != nil {
		return nil, fmt.Errorf("failed to load registration file: %w", err)
	}
	return reg, nil
}

var extractLabels = func(cfg *config.Config, reg *config.Registration) labels.Labels {
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
	return ls
}

var configCheck = func(ctx context.Context, cfg *config.Config, ls labels.Labels) error {
	if ls.RequireDocker() {
		dockerSocketPath, err := getDockerSocketPath(cfg.Container.DockerHost)
		if err != nil {
			return err
		}
		if err := envcheck.CheckIfDockerRunning(ctx, dockerSocketPath); err != nil {
			return err
		}
		os.Setenv("DOCKER_HOST", dockerSocketPath)
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
	return nil
}

var createClient = func(cfg *config.Config, reg *config.Registration) client.Client {
	return client.New(
		reg.Address,
		cfg.Runner.Insecure,
		reg.UUID,
		reg.Token,
		ver.Version(),
	)
}

var createRunner = func(ctx context.Context, cfg *config.Config, reg *config.Registration, cli client.Client, ls labels.Labels) (run.RunnerInterface, string, error) {
	runner := run.NewRunner(cfg, reg, cli)
	// declare the labels of the runner before fetching tasks
	resp, err := runner.Declare(ctx, ls.Names())
	if err != nil && connect.CodeOf(err) == connect.CodeUnimplemented {
		log.Warn("Because the Forgejo instance is an old version, skipping declaring the labels and version.")
		return runner, "runner", nil
	} else if err != nil {
		log.WithError(err).Error("fail to invoke Declare")
		return nil, "", err
	}

	log.Infof("runner: %s, with version: %s, with labels: %v, declared successfully",
		resp.Msg.GetRunner().GetName(), resp.Msg.GetRunner().GetVersion(), resp.Msg.GetRunner().GetLabels())
	// if declared successfully, override the labels in the.runner file with valid labels in the config file (if specified)
	runner.Update(ctx, ls)
	reg.Labels = ls.ToStrings()
	if err := config.SaveRegistration(cfg.Runner.File, reg); err != nil {
		return nil, "", fmt.Errorf("failed to save runner config: %w", err)
	}
	return runner, resp.Msg.GetRunner().GetName(), nil
}

// func(ctx context.Context, cfg *config.Config, cli client.Client, runner run.RunnerInterface) poll.Poller
var createPoller = poll.New

var commonSocketPaths = []string{
	"/var/run/docker.sock",
	"/run/podman/podman.sock",
	"$HOME/.colima/docker.sock",
	"$XDG_RUNTIME_DIR/docker.sock",
	"$XDG_RUNTIME_DIR/podman/podman.sock",
	`\\.\pipe\docker_engine`,
	"$HOME/.docker/run/docker.sock",
}

func getDockerSocketPath(configDockerHost string) (string, error) {
	// a `-` means don't mount the docker socket to job containers
	if configDockerHost != "automount" && configDockerHost != "-" {
		return configDockerHost, nil
	}

	socket, found := os.LookupEnv("DOCKER_HOST")
	if found {
		return socket, nil
	}

	for _, p := range commonSocketPaths {
		if _, err := os.Lstat(os.ExpandEnv(p)); err == nil {
			if strings.HasPrefix(p, `\\.\`) {
				return "npipe://" + filepath.ToSlash(os.ExpandEnv(p)), nil
			}
			return "unix://" + filepath.ToSlash(os.ExpandEnv(p)), nil
		}
	}

	return "", fmt.Errorf("daemon Docker Engine socket not found and docker_host config was invalid")
}
