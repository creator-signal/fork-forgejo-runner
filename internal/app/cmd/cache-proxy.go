// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"gitea.com/gitea/act_runner/internal/pkg/config"

	"github.com/nektos/act/pkg/cacheproxy"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type cacheProxyArgs struct {
	repoName   string
	targetHost string
	selfHost   string
	selfPort   uint16
}

func runCacheProxy(_ context.Context, configFile *string, proxyArgs *cacheProxyArgs) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault(*configFile)
		if err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}

		initLogging(cfg)

		reponame := proxyArgs.repoName
		target := proxyArgs.targetHost
		host := proxyArgs.selfHost
		port := proxyArgs.selfPort
		cacheSecret := "deadbeef"

		cacheHandler, err := cacheproxy.StartHandler(
			reponame,
			target,
			host,
			port,
			cacheSecret,
			log.StandardLogger().WithField("module", "cache_request"),
		)
		if err != nil {
			return err
		}

		log.Infof("cache proxy is listening on %v", cacheHandler.ExternalURL())

		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c

		return nil
	}
}
