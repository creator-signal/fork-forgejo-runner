// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"code.forgejo.org/forgejo/runner/v12/act/cacheproxy"
	"code.forgejo.org/forgejo/runner/v12/internal/app/poll"
	"code.forgejo.org/forgejo/runner/v12/internal/app/run"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/client"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/common"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/envcheck"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/ver"
	"connectrpc.com/connect"
	"github.com/mattn/go-isatty"
	log "github.com/sirupsen/logrus"
)

type daemonArgs struct {
	connection
}

func runDaemon(signalContext context.Context, configFile *string, args *daemonArgs) error {
	// signalContext will be 'done' when we receive a graceful shutdown signal; daemonContext is not a derived context
	// because we want it to 'outlive' the signalContext in order to perform graceful cleanup.
	daemonContext, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx := common.WithDaemonContext(daemonContext, daemonContext)

	cfg, err := initializeConfig(configFile, args)
	if err != nil {
		return err
	}

	initLogging(cfg)
	log.Infoln("Starting runner daemon")

	err = configCheck(ctx, cfg)
	if err != nil {
		return err
	}

	if len(cfg.Server.Connections) == 0 {
		return errors.New("runner: 0 server connections configured, terminating")
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

	clients := make([]client.Client, 0, len(cfg.Server.Connections))
	runners := make([]run.RunnerInterface, 0, len(cfg.Server.Connections))
	for name, conn := range cfg.Server.Connections {
		cli := createClient(cfg, conn)
		clients = append(clients, cli)

		runner, _, ephemeral, err := createRunner(ctx, name, cfg, cli, conn.Labels, cacheProxy)
		if err != nil {
			return err
		}
		if ephemeral {
			return fmt.Errorf("connection %q requires an ephemeral runner, which is not supported in daemon-mode", name)
		}
		runners = append(runners, runner)
	}

	poller := createPoller(ctx, cfg, clients, runners)

	pollTask(signalContext, poller)

	log.Infof("runner: shutdown initiated, waiting [runner].shutdown_timeout=%s for running jobs to complete before shutting down", cfg.Runner.ShutdownTimeout)

	shutdownCtx, cancel := context.WithTimeout(daemonContext, cfg.Runner.ShutdownTimeout)
	defer cancel()

	err = poller.Shutdown(shutdownCtx)
	if err != nil {
		log.Warnf("runner: cancelled in progress jobs during shutdown")
	}
	return nil
}

func pollTask(ctx context.Context, poller poll.Poller) {
	go poller.Poll()
	<-ctx.Done()
	log.Info("runner: received shutdown signal")
}

var initializeConfig = func(configFile *string, args *daemonArgs) (*config.Config, error) {
	cfg, err := config.New(
		config.FromFile(*configFile),
		config.FromRegistration,
		connectionFromArguments(&args.connection),
	)
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

var configCheck = func(ctx context.Context, cfg *config.Config) error {
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

var createClient = func(cfg *config.Config, conn *config.Connection) client.Client {
	return client.New(
		conn.URL.String(),
		cfg.Runner.Insecure,
		conn.UUID.String(),
		conn.Token,
		ver.Version(),
		conn.FetchInterval,
	)
}

var createRunner = func(ctx context.Context, name string, cfg *config.Config, cli client.Client, ls labels.Labels, cacheProxy *cacheproxy.Handler) (run.RunnerInterface, string, bool, error) {
	runner := run.NewRunner(cfg, name, ls, cli, cacheProxy)
	// declare the labels of the runner before fetching tasks
	resp, err := runner.Declare(ctx, ls.Names())
	if err != nil && connect.CodeOf(err) == connect.CodeUnimplemented {
		log.Warn("Because the Forgejo instance is an old version, skipping declaring the labels and version.")
		return runner, "runner", false, nil
	} else if err != nil {
		log.WithError(err).Error("fail to invoke Declare")
		return nil, "", false, err
	}

	log.Infof("runner: %s, with version: %s, with labels: %v, ephemeral: %v, declared successfully",
		resp.Msg.GetRunner().GetName(), resp.Msg.GetRunner().GetVersion(), resp.Msg.GetRunner().GetLabels(), resp.Msg.GetRunner().GetEphemeral())
	return runner, resp.Msg.GetRunner().GetName(), resp.Msg.GetRunner().GetEphemeral(), nil
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
