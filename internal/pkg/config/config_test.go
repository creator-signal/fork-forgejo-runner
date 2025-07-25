// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigTune(t *testing.T) {
	c := &Config{
		Runner: Runner{},
	}

	t.Run("Public instance tuning", func(t *testing.T) {
		c.Runner.FetchInterval = 60 * time.Second
		c.Tune("https://codeberg.org")
		assert.Equal(t, 60*time.Second, c.Runner.FetchInterval)

		c.Runner.FetchInterval = 2 * time.Second
		c.Tune("https://codeberg.org")
		assert.Equal(t, 30*time.Second, c.Runner.FetchInterval)
	})

	t.Run("Non-public instance tuning", func(t *testing.T) {
		c.Runner.FetchInterval = 60 * time.Second
		c.Tune("https://example.com")
		assert.Equal(t, 60*time.Second, c.Runner.FetchInterval)

		c.Runner.FetchInterval = 2 * time.Second
		c.Tune("https://codeberg.com")
		assert.Equal(t, 2*time.Second, c.Runner.FetchInterval)
	})
}

func TestDefaultSettings(t *testing.T) {
	config, err := LoadDefault("")
	require.NoError(t, err)

	assert.Equal(t, "-", config.Container.DockerHost)
	assert.Equal(t, "info", config.Log.JobLevel)
	assert.False(t, config.Container.ForceRebuild)
}
