// Copyright 2022 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package run

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"sync"

	"code.forgejo.org/forgejo/runner/v12/act/artifactcache"
	"code.forgejo.org/forgejo/runner/v12/act/cacheproxy"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	log "github.com/sirupsen/logrus"
)

var (
	cacheServer    artifactcache.Handler
	cacheProxy     *cacheproxy.Handler
	cacheProxyOnce sync.Once
)

// SetupCache starts the artifact cache and proxy.
// It returns a cacheproxy.Handler that can be used to manage cache runs.
// It uses a singleton pattern to ensure the cache is only started once.
// Concurrent calls are handled by sync.Once, ensuring that both cacheServer
// and cacheProxy singletons are initialized exactly once.
func SetupCache(cfg *config.Config, envs map[string]string) *cacheproxy.Handler {
	cacheProxyOnce.Do(func() {
		var cacheURL string
		var cacheSecret string

		if cfg.Cache.ExternalServer == "" {
			// No external cache server was specified, start internal cache server
			cacheSecret = cfg.Cache.Secret

			if cacheSecret == "" {
				// no cache secret was specified, generate one
				secretBytes := make([]byte, 64)
				_, err := rand.Read(secretBytes)
				if err != nil {
					log.Errorf("Failed to generate random bytes, this should not happen")
				}
				cacheSecret = hex.EncodeToString(secretBytes)
			}

			var err error
			cacheServer, err = artifactcache.StartHandler(
				cfg.Cache.Dir,
				"", // automatically detect
				cfg.Cache.Port,
				cacheSecret,
				log.StandardLogger().WithField("module", "cache_request"),
			)
			if err != nil {
				log.Errorf("Could not start the cache server, cache will be disabled: %v", err)
				return
			}

			cacheURL = cacheServer.ExternalURL()
		} else {
			// An external cache server was specified, use its url
			cacheSecret = cfg.Cache.Secret

			if cacheSecret == "" {
				log.Error("A cache secret must be specified to use an external cache server, cache will be disabled")
				return
			}

			cacheURL = strings.TrimSuffix(cfg.Cache.ExternalServer, "/")
		}

		var err error
		cacheProxy, err = cacheproxy.StartHandler(
			cacheURL,
			cfg.Cache.Host,
			cfg.Cache.ProxyPort,
			cfg.Cache.ActionsCacheURLOverride,
			cacheSecret, // Same secret for all instances. HMAC includes instance ID for isolation.
			log.StandardLogger().WithField("module", "cache_proxy"),
		)
		if err != nil {
			log.Errorf("cannot init cache proxy, cache will be disabled: %v", err)
		}
	})

	if cacheProxy != nil {
		envs["ACTIONS_CACHE_URL"] = cacheProxy.ExternalURL()
	}

	return cacheProxy
}

// CloseCache closes the artifact cache and proxy.
func CloseCache() error {
	if cacheProxy != nil {
		if err := cacheProxy.Close(); err != nil {
			log.Errorf("failed to close cache proxy: %v", err)
		}
	}
	if cacheServer != nil {
		if err := cacheServer.Close(); err != nil {
			log.Errorf("failed to close cache server: %v", err)
		}
	}
	return nil
}
