// Copyright 2025 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"github.com/stretchr/testify/assert"
	"go.yaml.in/yaml/v3"
)

func TestCommandConnectTokenComplainsAboutMissingFlags(t *testing.T) {
	configFile := "irrelevant.yml"
	ctx := context.Background()
	cmd := createConnectTokenCmd(&configFile)

	output, _, _, err := executeCommand(ctx, t, cmd)

	assert.ErrorContains(t, err, `required flag(s) "instance", "name", "token", "uuid" not set`)
	assert.Contains(t, output, "Usage:")
}

func TestCommandConnectTokenComplainsAboutInvalidArguments(t *testing.T) {
	testCases := []struct {
		name          string
		arguments     []string
		expectedError string
	}{
		{
			name: "invalid-uuid",
			arguments: []string{
				"--instance", "https://example.com/",
				"--name", "my-runner",
				"--token", "0271372f38bf27d3a5e34f1825004d8628b29a12",
				"--uuid", "invalid",
				"--label", "debian:docker://node:24-trixie",
				"--label", "docker:docker://node:22-bookworm",
			},
			expectedError: "invalid uuid:",
		},
		{
			name: "invalid-token",
			arguments: []string{
				"--instance", "https://example.com/",
				"--name", "my-runner",
				"--token", "invalid",
				"--uuid", "170e0ceb-6449-49e6-afaf-3a52d1152670",
				"--label", "debian:docker://node:24-trixie",
				"--label", "docker:docker://node:22-bookworm",
			},
			expectedError: "invalid token: the secret must be exactly 40 characters long, not 7",
		},
		{
			name: "empty-name",
			arguments: []string{
				"--instance", "https://example.com/",
				"--name", "",
				"--token", "0271372f38bf27d3a5e34f1825004d8628b29a12",
				"--uuid", "170e0ceb-6449-49e6-afaf-3a52d1152670",
				"--label", "debian:docker://node:24-trixie",
				"--label", "docker:docker://node:22-bookworm",
			},
			expectedError: "required name is empty",
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configFile, _, err := prepareConfig(t.TempDir())
			assert.NoError(t, err)

			ctx := context.Background()
			cmd := createConnectTokenCmd(&configFile)

			_, _, _, err = executeCommand(ctx, t, cmd, testCase.arguments...)

			assert.ErrorContains(t, err, testCase.expectedError)
		})
	}
}

func TestCommandConnectTokenWritesValidRunnerFile(t *testing.T) {
	testCases := []struct {
		name      string
		arguments []string
		expected  config.Registration
	}{
		{
			name: "config-with-labels",
			arguments: []string{
				"--instance", "https://example.com/",
				"--name", "my-runner",
				"--token", "0271372f38bf27d3a5e34f1825004d8628b29a12",
				"--uuid", "e8082384-3634-4b27-9458-18e1b5d3abc7",
				"--label", "debian:docker://node:24-trixie",
				"--label", "docker:docker://node:22-bookworm",
			},
			expected: config.Registration{
				Warning: config.RegistrationWarning,
				ID:      0,
				UUID:    "e8082384-3634-4b27-9458-18e1b5d3abc7",
				Name:    "my-runner",
				Token:   "0271372f38bf27d3a5e34f1825004d8628b29a12",
				Address: "https://example.com/",
				Labels: []string{
					"debian:docker://node:24-trixie",
					"docker:docker://node:22-bookworm",
				},
			},
		},
		{
			name: "config-without-labels",
			arguments: []string{
				"--instance", "https://example.com/forgejo",
				"--name", "another-runner",
				"--token", "239cd8b44fed4b31d9ca465d818b701c56d16f5b",
				"--uuid", "193f53fa-ca78-489c-9eba-4f5df05f3241",
			},
			expected: config.Registration{
				Warning: config.RegistrationWarning,
				ID:      0,
				UUID:    "193f53fa-ca78-489c-9eba-4f5df05f3241",
				Name:    "another-runner",
				Token:   "239cd8b44fed4b31d9ca465d818b701c56d16f5b",
				Address: "https://example.com/forgejo",
				Labels:  []string{},
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			configFile, cfg, err := prepareConfig(t.TempDir())
			assert.NoError(t, err)

			ctx := context.Background()
			cmd := createConnectTokenCmd(&configFile)

			_, _, _, err = executeCommand(ctx, t, cmd, testCase.arguments...)

			assert.NoError(t, err)

			var writtenRegistration config.Registration

			runnerFileContents, err := os.ReadFile(cfg.Runner.File)
			assert.NoError(t, err)
			assert.NoError(t, json.Unmarshal(runnerFileContents, &writtenRegistration))

			assert.Equal(t, testCase.expected, writtenRegistration)
		})
	}
}

func prepareConfig(tempDir string) (string, *config.Config, error) {
	configFile := filepath.Join(tempDir, "config.yml")
	runnerFile := filepath.Join(tempDir, ".runner")

	cfg, err := config.New()
	if err != nil {
		return "", &config.Config{}, err
	}

	cfg.Runner.File = runnerFile
	yamlData, err := yaml.Marshal(cfg)
	if err != nil {
		return "", &config.Config{}, err
	}

	if err := os.WriteFile(configFile, yamlData, 0o644); err != nil {
		return "", &config.Config{}, err
	}

	return configFile, cfg, nil
}
