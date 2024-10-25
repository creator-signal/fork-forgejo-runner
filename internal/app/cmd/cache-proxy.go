// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package cmd

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"gitea.com/gitea/act_runner/internal/pkg/config"

	"github.com/nektos/act/pkg/artifactcache"
	"github.com/nektos/act/pkg/artifactproxy"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
)

type cacheProxyArgs struct {
	Dir        string
	selfHost   string
	selfPort   uint16
	targetHost string
}

func runCacheProxy(ctx context.Context, configFile *string, proxyArgs *cacheProxyArgs) func(cmd *cobra.Command, args []string) error {
	return func(cmd *cobra.Command, args []string) error {
		cfg, err := config.LoadDefault(*configFile)
		if err != nil {
			return fmt.Errorf("invalid configuration: %w", err)
		}

		initLogging(cfg)

		// cacheArgs has higher priority
		dir = proxyArgs.Dir
		host = proxyArgs.Host
		port = proxyArgs.Port

		artifactproxy.StartHandler()
		cacheHandler, err := artifactcache.StartHandler(
			dir,
			host,
			port,
			log.StandardLogger().WithField("module", "cache_request"),
		)
		if err != nil {
			return err
		}

		log.Infof("cache server is listening on %v", cacheHandler.ExternalURL())

		c := make(chan os.Signal, 1)
		signal.Notify(c, os.Interrupt)
		<-c

		return nil
	}
}
