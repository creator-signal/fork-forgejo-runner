// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"errors"
	"fmt"
	"net/url"
	"time"

	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	gouuid "github.com/google/uuid"
)

type connection struct {
	url           string
	uuid          string
	tokenURL      string
	labels        []string
	fetchInterval time.Duration
}

func (c *connection) isConnectionDefined() bool {
	return c.url != "" ||
		c.uuid != "" ||
		c.tokenURL != "" ||
		len(c.labels) > 1 ||
		c.fetchInterval > 0
}

func connectionFromArguments(conn *connection) func(cfg *config.Config) error {
	return func(cfg *config.Config) error {
		if !conn.isConnectionDefined() {
			return nil
		}
		if len(cfg.Server.Connections) != 0 {
			return fmt.Errorf("server connection conflict between program arguments and config; only one source can provide server connections")
		}

		if conn.url == "" {
			return errors.New("`url` is empty")
		}
		if conn.uuid == "" {
			return errors.New("`uuid` is empty")
		}
		if conn.tokenURL == "" {
			return errors.New("`token-url` is empty")
		}

		parsedURL, err := url.ParseRequestURI(conn.url)
		if err != nil {
			return fmt.Errorf("malformed `url` %q: %w", conn.url, err)
		}
		if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
			return fmt.Errorf("invalid scheme in `url` %q: only http and https supported", conn.url)
		}
		parsedUUID, err := gouuid.Parse(conn.uuid)
		if err != nil {
			return fmt.Errorf("malformed `uuid` %q: %w", conn.uuid, err)
		}
		var parsedLabels labels.Labels
		for _, label := range conn.labels {
			parsedLabel, err := labels.Parse(label)
			if err != nil {
				return fmt.Errorf("malformed `label` %q: %w", label, err)
			}
			parsedLabels = append(parsedLabels, parsedLabel)
		}
		var resolvedToken string
		if conn.tokenURL != "" {
			if resolvedToken, err = config.ResolveSecretURL(conn.tokenURL); err != nil {
				return fmt.Errorf("invalid `token-url`: %w", err)
			}
		}

		if cfg.Server.Connections == nil {
			cfg.Server.Connections = map[string]*config.Connection{}
		}
		cfg.Server.Connections["default"] = &config.Connection{
			URL:           parsedURL,
			UUID:          parsedUUID,
			Token:         resolvedToken,
			Labels:        parsedLabels,
			FetchInterval: conn.fetchInterval,
		}
		return nil
	}
}
