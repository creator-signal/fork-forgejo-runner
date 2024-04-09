// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadSaveRegistration(t *testing.T) {
	originalLabels := []string{"Label1", "Label2"}
	original := Registration{
		ID:      4242,
		UUID:    "UUID",
		Name:    "Name",
		Token:   "Token",
		Address: "Address",
		Labels:  originalLabels,
	}
	runnerFile := t.TempDir() + "/.runner"

	assert.NoError(t, SaveRegistration(runnerFile, &original))

	reg, err := LoadRegistration(runnerFile, nil)
	assert.NoError(t, err)
	assert.EqualValues(t, originalLabels, reg.Labels)

	expectedLabels := []string{"Override1"}
	reg, err = LoadRegistration(runnerFile, expectedLabels)
	assert.NoError(t, err)
	assert.EqualValues(t, expectedLabels, reg.Labels)
}
