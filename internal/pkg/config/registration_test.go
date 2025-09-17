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
		Warning: registrationWarning,
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
