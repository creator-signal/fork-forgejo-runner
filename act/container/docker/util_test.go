// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package docker

import (
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParsePlatform(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		output        ocispec.Platform
		expectedError string
	}{
		{
			name:   "valid platform",
			input:  "linux/amd64",
			output: ocispec.Platform{Architecture: "amd64", OS: "linux"},
		},
		{
			name:          "OS only",
			input:         "windows",
			expectedError: `malformed container platform: "windows"`,
		},
		{
			name:          "Missing architecture",
			input:         "windows/",
			expectedError: `malformed container platform: "windows/"`,
		},
		{
			name:          "Missing OS",
			input:         "/aarch64",
			expectedError: `malformed container platform: "/aarch64"`,
		},
		{
			name:   "No OS or architecture validation",
			input:  "something/made up",
			output: ocispec.Platform{Architecture: "made up", OS: "something"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plat, err := parsePlatform(tc.input)

			if tc.expectedError != "" {
				require.ErrorContains(t, err, tc.expectedError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tc.output.Architecture, plat.Architecture)
				assert.Equal(t, tc.output.OS, plat.OS)
			}
		})
	}
}
