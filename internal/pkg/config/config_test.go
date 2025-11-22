// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"os"
	"path"
	"path/filepath"
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
		assert.EqualValues(t, 60*time.Second, c.Runner.FetchInterval)

		c.Runner.FetchInterval = 2 * time.Second
		c.Tune("https://codeberg.org")
		assert.EqualValues(t, 30*time.Second, c.Runner.FetchInterval)
	})

	t.Run("Non-public instance tuning", func(t *testing.T) {
		c.Runner.FetchInterval = 60 * time.Second
		c.Tune("https://example.com")
		assert.EqualValues(t, 60*time.Second, c.Runner.FetchInterval)

		c.Runner.FetchInterval = 2 * time.Second
		c.Tune("https://codeberg.com")
		assert.EqualValues(t, 2*time.Second, c.Runner.FetchInterval)
	})
}

func TestDefaultSettings(t *testing.T) {
	config, err := LoadDefault("")
	assert.NoError(t, err)

	assert.EqualValues(t, config.Container.DockerHost, "-")
	assert.EqualValues(t, config.Log.JobLevel, "info")
	assert.EqualValues(t, config.Container.ForceRebuild, false)
	assert.True(t, filepath.IsAbs(config.Host.WorkdirParent))
}

func TestConfigNormalization(t *testing.T) {
	tmp := path.Join(t.TempDir(), "config.yml")
	err := os.WriteFile(tmp, []byte("{ host: { workdir_parent: blah } }"), 0o644)
	require.NoError(t, err)

	config, err := LoadDefault(tmp)
	assert.NoError(t, err)

	assert.NotEqual(t, "blah", config.Host.WorkdirParent)
	assert.True(t, filepath.IsAbs(config.Host.WorkdirParent))
}
