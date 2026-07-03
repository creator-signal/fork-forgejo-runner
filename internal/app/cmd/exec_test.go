// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/testutils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunExec(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	testutils.RequireTestFeatures(t, testutils.TestFeatureDocker)

	testCases := []struct {
		name          string
		image         string
		event         string
		expectedError string
	}{
		{
			name:  "forgejo-context",
			image: "node:24-trixie",
			event: "push",
		},
	}

	for _, testCase := range testCases {
		args := &executeArgs{
			event:                 testCase.event,
			containerDaemonSocket: os.Getenv("DOCKER_HOST"),
			image:                 testCase.image,
			workflowsPath:         filepath.Join("testdata", "exec", testCase.name, fmt.Sprintf("%s.yml", testCase.event)),
		}

		err := runExec(t.Context(), args)
		if testCase.expectedError == "" {
			require.NoError(t, err)
		} else {
			assert.ErrorContains(t, err, testCase.expectedError)
		}
	}
}
