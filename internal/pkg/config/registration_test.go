// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfig_Registration(t *testing.T) {
	reg := Registration{
		Warning: RegistrationWarning,
		ID:      1234,
		UUID:    "UUID",
		Name:    "NAME",
		Token:   "TOKEN",
		Address: "ADDRESS",
		Labels:  []string{"LABEL1", "LABEL2"},
	}

	file := filepath.Join(t.TempDir(), ".runner")

	// when the file does not exist, it is never equal
	equal, err := isEqualRegistration(file, &reg)
	require.NoError(t, err)
	assert.False(t, equal)

	require.NoError(t, SaveRegistration(file, &reg))

	regReloaded, err := LoadRegistration(file)
	require.NoError(t, err)
	assert.Equal(t, reg, *regReloaded)

	equal, err = isEqualRegistration(file, &reg)
	require.NoError(t, err)
	assert.True(t, equal)

	// if the registration is not modified, it is not saved
	time.Sleep(2 * time.Second) // file system precision on modification time is one second
	before, err := os.Stat(file)
	require.NoError(t, err)
	require.NoError(t, SaveRegistration(file, &reg))
	after, err := os.Stat(file)
	require.NoError(t, err)
	assert.Equal(t, before.ModTime(), after.ModTime())

	reg.Labels = []string{"LABEL3"}
	equal, err = isEqualRegistration(file, &reg)
	require.NoError(t, err)
	assert.False(t, equal)

	// if the registration is modified, it is saved
	require.NoError(t, SaveRegistration(file, &reg))
	after, err = os.Stat(file)
	require.NoError(t, err)
	assert.NotEqual(t, before.ModTime(), after.ModTime())
}

func TestFromRegistration(t *testing.T) {
	reg := Registration{
		Warning: RegistrationWarning,
		ID:      1234,
		UUID:    "7f7d3055-711d-4c8e-88ad-a6d9d668801d",
		Name:    "NAME",
		Token:   "TOKEN",
		Address: "https://example.com/",
		Labels:  []string{"ubuntu-latest:docker://code.forgejo.org/oci/node:20-bookworm"},
	}
	file := filepath.Join(t.TempDir(), ".runner")
	require.NoError(t, SaveRegistration(file, &reg))

	t.Run("load no errors", func(t *testing.T) {
		config, err := New(
			func(config *Config) error {
				config.Runner.File = file
				return nil
			},
			FromRegistration)
		require.NoError(t, err)

		require.Len(t, config.Server.Connections, 1)

		c, ok := config.Server.Connections["NAME"]
		require.True(t, ok)
		assert.Equal(t, "https://example.com/", c.URL.String())
		assert.Equal(t, "7f7d3055-711d-4c8e-88ad-a6d9d668801d", c.UUID.String())
		assert.Equal(t, "TOKEN", c.Token)
		require.Len(t, c.Labels, 1)
		assert.Equal(t, c.Labels[0].String(), "ubuntu-latest:docker://code.forgejo.org/oci/node:20-bookworm")
		assert.Equal(t, c.labelPriority, overrideIfPossible)
	})

	t.Run("existing connection error", func(t *testing.T) {
		_, err := New(
			func(config *Config) error {
				config.Runner.File = file
				config.Server.Connections = map[string]*Connection{
					"existing": {},
				}
				return nil
			},
			FromRegistration)
		assert.ErrorContains(t, err, "server connection conflict")
	})

	t.Run("no registration file", func(t *testing.T) {
		config, err := New(
			func(config *Config) error {
				config.Runner.File = "/non-existent-file.runner"
				return nil
			},
			FromRegistration)
		require.NoError(t, err)
		assert.Len(t, config.Server.Connections, 0)
	})
}
