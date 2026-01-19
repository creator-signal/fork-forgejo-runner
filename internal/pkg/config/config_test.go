// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestLoadDefault(t *testing.T) {
	t.Run("Missing configuration file results in error", func(t *testing.T) {
		config, err := LoadDefault("does-not-exist")

		assert.Nil(t, config)
		assert.ErrorContains(t, err, "open does-not-exist: no such file or directory")
	})

	t.Run("Malformed configuration file results in error", func(t *testing.T) {
		tempDir := t.TempDir()

		configPath := filepath.Join(tempDir, "config.yaml")

		err := os.WriteFile(configPath, []byte("malformed"), 0o644)
		require.NoError(t, err)

		config, err := LoadDefault(configPath)

		assert.Nil(t, config)
		assert.ErrorContains(t, err, fmt.Sprintf(`cannot parse config file "%s"`, configPath))
	})

	t.Run("Without configuration file", func(t *testing.T) {
		config, err := LoadDefault("")
		require.NoError(t, err)

		home, err := os.UserHomeDir()
		require.NoError(t, err)

		assert.Equal(t, 5, reflect.TypeOf(Config{}).NumField())
		assert.Equal(t, 2, reflect.TypeOf(Log{}).NumField())
		assert.Equal(t, 12, reflect.TypeOf(Runner{}).NumField())
		assert.Equal(t, 8, reflect.TypeOf(Cache{}).NumField())
		assert.Equal(t, 10, reflect.TypeOf(Container{}).NumField())
		assert.Equal(t, 1, reflect.TypeOf(Host{}).NumField())

		assert.Equal(t, "info", config.Log.Level)
		assert.Equal(t, "info", config.Log.JobLevel)

		assert.Equal(t, ".runner", config.Runner.File)
		assert.Equal(t, 1, config.Runner.Capacity)
		assert.Empty(t, config.Runner.Envs)
		assert.Empty(t, config.Runner.EnvFile)
		assert.Equal(t, 3*time.Hour, config.Runner.Timeout)
		assert.Zero(t, config.Runner.ShutdownTimeout)
		assert.False(t, config.Runner.Insecure)
		assert.Equal(t, 5*time.Second, config.Runner.FetchTimeout)
		assert.Equal(t, 2*time.Second, config.Runner.FetchInterval)
		assert.Equal(t, 1*time.Second, config.Runner.ReportInterval)
		assert.Empty(t, config.Runner.Labels)
		assert.Equal(t, uint(10), config.Runner.ReportRetry.MaxRetries)
		assert.Equal(t, 100*time.Millisecond, config.Runner.ReportRetry.InitialDelay)
		assert.Zero(t, config.Runner.ReportRetry.MaxDelay)

		assert.True(t, *config.Cache.Enabled)
		assert.Equal(t, filepath.Join(home, ".cache", "actcache"), config.Cache.Dir)
		assert.Empty(t, config.Cache.Host)
		assert.Zero(t, config.Cache.Port)
		assert.Zero(t, config.Cache.ProxyPort)
		assert.Empty(t, config.Cache.ExternalServer)
		assert.Empty(t, config.Cache.ActionsCacheURLOverride)
		assert.Empty(t, config.Cache.Secret)

		assert.Empty(t, config.Container.Network)
		assert.Empty(t, config.Container.NetworkMode)
		assert.False(t, config.Container.EnableIPv6)
		assert.False(t, config.Container.Privileged)
		assert.Empty(t, config.Container.Options)
		assert.Equal(t, "workspace", config.Container.WorkdirParent)
		assert.Empty(t, config.Container.ValidVolumes)
		assert.Equal(t, "-", config.Container.DockerHost)
		assert.False(t, config.Container.ForcePull)
		assert.False(t, config.Container.ForceRebuild)

		assert.Equal(t, filepath.Join(home, ".cache", "act"), config.Host.WorkdirParent)
		assert.True(t, filepath.IsAbs(config.Host.WorkdirParent))
	})

	t.Run("Configuration file takes precedence over defaults", func(t *testing.T) {
		rawConfig := `
log:
  level: warn
  job_level: warn
runner:
  file: runner.txt
  capacity: 37
  envs:
    MY_VARIABLE: value
  env_file: .env
  timeout: 6h
  shutdown_timeout: 30s
  insecure: true
  fetch_timeout: 25s
  fetch_interval: 8s
  report_interval: 5s
  labels:
    - docker:docker://node:24-trixie
  report_retry:
    max_retries: 26
    initial_delay: 600ms
    max_delay: 975s
cache:
  enabled: false
  dir: some/directory
  host: https://example.com/
  port: 8080
  proxy_port: 8081
  external_server: https://external.example.com/
  actions_cache_url_override: https://override.example.com/
  secret: vruvRdu5Rm
container:
  network: host
  network_mode: bridge
  enable_ipv6: true
  privileged: true
  options: "--hostname=runner"
  workdir_parent: "a/workdir/parent"
  valid_volumes:
    - /etc/ssl/certs
  docker_host: tcp://10.10.10.10/
  force_pull: true
  force_rebuild: true
host:
  workdir_parent: some/path
`

		tempDir := t.TempDir()

		configPath := filepath.Join(tempDir, "config.yml")
		err := os.WriteFile(configPath, []byte(rawConfig), 0o644)
		require.NoError(t, err)

		defaultConfig, err := LoadDefault("")
		require.NoError(t, err)

		config, err := LoadDefault(configPath)
		require.NoError(t, err)

		assert.Equal(t, 5, reflect.TypeOf(Config{}).NumField())
		assert.Equal(t, 2, reflect.TypeOf(Log{}).NumField())
		assert.Equal(t, 12, reflect.TypeOf(Runner{}).NumField())
		assert.Equal(t, 8, reflect.TypeOf(Cache{}).NumField())
		assert.Equal(t, 10, reflect.TypeOf(Container{}).NumField())
		assert.Equal(t, 1, reflect.TypeOf(Host{}).NumField())

		// Verify that each value loaded from the configuration file does not match the default configuration.
		// Otherwise, the test would be meaningless.
		assert.NotEqual(t, defaultConfig.Log.Level, config.Log.Level)
		assert.Equal(t, "warn", config.Log.Level)
		assert.NotEqual(t, defaultConfig.Log.JobLevel, config.Log.JobLevel)
		assert.Equal(t, "warn", config.Log.JobLevel)

		assert.NotEqual(t, defaultConfig.Runner.File, config.Runner.File)
		assert.Equal(t, "runner.txt", config.Runner.File)
		assert.NotEqual(t, defaultConfig.Runner.Capacity, config.Runner.Capacity)
		assert.Equal(t, 37, config.Runner.Capacity)
		assert.NotEqual(t, defaultConfig.Runner.Envs, config.Runner.Envs)
		assert.Equal(t, map[string]string{"MY_VARIABLE": "value"}, config.Runner.Envs)
		assert.NotEqual(t, defaultConfig.Runner.EnvFile, config.Runner.EnvFile)
		assert.Equal(t, ".env", config.Runner.EnvFile)
		assert.NotEqual(t, defaultConfig.Runner.Timeout, config.Runner.Timeout)
		assert.Equal(t, 6*time.Hour, config.Runner.Timeout)
		assert.NotEqual(t, defaultConfig.Runner.ShutdownTimeout, config.Runner.ShutdownTimeout)
		assert.Equal(t, 30*time.Second, config.Runner.ShutdownTimeout)
		assert.NotEqual(t, defaultConfig.Runner.Insecure, config.Runner.Insecure)
		assert.True(t, config.Runner.Insecure)
		assert.NotEqual(t, defaultConfig.Runner.FetchTimeout, config.Runner.FetchTimeout)
		assert.Equal(t, 25*time.Second, config.Runner.FetchTimeout)
		assert.NotEqual(t, defaultConfig.Runner.FetchInterval, config.Runner.FetchInterval)
		assert.Equal(t, 8*time.Second, config.Runner.FetchInterval)
		assert.NotEqual(t, defaultConfig.Runner.ReportInterval, config.Runner.ReportInterval)
		assert.Equal(t, 5*time.Second, config.Runner.ReportInterval)
		assert.NotEqual(t, defaultConfig.Runner.Labels, config.Runner.Labels)
		assert.Equal(t, []string{"docker:docker://node:24-trixie"}, config.Runner.Labels)
		assert.NotEqual(t, defaultConfig.Runner.ReportRetry.MaxRetries, config.Runner.ReportRetry.MaxRetries)
		assert.Equal(t, uint(26), config.Runner.ReportRetry.MaxRetries)
		assert.NotEqual(t, defaultConfig.Runner.ReportRetry.InitialDelay, config.Runner.ReportRetry.InitialDelay)
		assert.Equal(t, 600*time.Millisecond, config.Runner.ReportRetry.InitialDelay)
		assert.NotEqual(t, defaultConfig.Runner.ReportRetry.MaxDelay, config.Runner.ReportRetry.MaxDelay)
		assert.Equal(t, 975*time.Second, config.Runner.ReportRetry.MaxDelay)

		assert.NotEqual(t, defaultConfig.Cache.Enabled, config.Cache.Enabled)
		assert.False(t, *config.Cache.Enabled)
		assert.NotEqual(t, defaultConfig.Cache.Dir, config.Cache.Dir)
		assert.Equal(t, "some/directory", config.Cache.Dir)
		assert.NotEqual(t, defaultConfig.Cache.Host, config.Cache.Host)
		assert.Equal(t, "https://example.com/", config.Cache.Host)
		assert.NotEqual(t, defaultConfig.Cache.Port, config.Cache.Port)
		assert.Equal(t, uint16(8080), config.Cache.Port)
		assert.NotEqual(t, defaultConfig.Cache.ProxyPort, config.Cache.ProxyPort)
		assert.Equal(t, uint16(8081), config.Cache.ProxyPort)
		assert.NotEqual(t, defaultConfig.Cache.ExternalServer, config.Cache.ExternalServer)
		assert.Equal(t, "https://external.example.com/", config.Cache.ExternalServer)
		assert.NotEqual(t, defaultConfig.Cache.ActionsCacheURLOverride, config.Cache.ActionsCacheURLOverride)
		assert.Equal(t, "https://override.example.com/", config.Cache.ActionsCacheURLOverride)
		assert.NotEqual(t, defaultConfig.Cache.Secret, config.Cache.Secret)
		assert.Equal(t, "vruvRdu5Rm", config.Cache.Secret)

		assert.NotEqual(t, defaultConfig.Container.Network, config.Container.Network)
		assert.Equal(t, "host", config.Container.Network)
		assert.NotEqual(t, defaultConfig.Container.NetworkMode, config.Container.NetworkMode)
		assert.Equal(t, "bridge", config.Container.NetworkMode)
		assert.NotEqual(t, defaultConfig.Container.EnableIPv6, config.Container.EnableIPv6)
		assert.True(t, config.Container.EnableIPv6)
		assert.NotEqual(t, defaultConfig.Container.Privileged, config.Container.Privileged)
		assert.True(t, config.Container.Privileged)
		assert.NotEqual(t, defaultConfig.Container.Options, config.Container.Options)
		assert.Equal(t, "--hostname=runner", config.Container.Options)
		assert.NotEqual(t, defaultConfig.Container.WorkdirParent, config.Container.WorkdirParent)
		assert.Equal(t, "a/workdir/parent", config.Container.WorkdirParent)
		assert.NotEqual(t, defaultConfig.Container.ValidVolumes, config.Container.ValidVolumes)
		assert.Equal(t, []string{"/etc/ssl/certs"}, config.Container.ValidVolumes)
		assert.NotEqual(t, defaultConfig.Container.DockerHost, config.Container.DockerHost)
		assert.Equal(t, "tcp://10.10.10.10/", config.Container.DockerHost)
		assert.NotEqual(t, defaultConfig.Container.ForcePull, config.Container.ForcePull)
		assert.True(t, config.Container.ForcePull)
		assert.NotEqual(t, defaultConfig.Container.ForceRebuild, config.Container.ForceRebuild)
		assert.True(t, config.Container.ForceRebuild)

		assert.NotEqual(t, defaultConfig.Host.WorkdirParent, config.Host.WorkdirParent)
		assert.True(t, strings.HasSuffix(config.Host.WorkdirParent, "some/path"))
		assert.True(t, filepath.IsAbs(config.Host.WorkdirParent))
	})

	t.Run("Imports optional env file", func(t *testing.T) {
		tempDir := t.TempDir()
		configFile := filepath.Join(tempDir, "config.yaml")
		envFile := filepath.Join(tempDir, ".env")

		rawConfig := fmt.Sprintf(`{ runner: { env_file: "%s" } }`, envFile)
		err := os.WriteFile(configFile, []byte(rawConfig), 0o644)
		require.NoError(t, err)

		err = os.WriteFile(envFile, []byte("SOME_ENV_VAR=some-value"), 0o644)
		require.NoError(t, err)

		config, err := LoadDefault(configFile)
		require.NoError(t, err)

		assert.Equal(t, envFile, config.Runner.EnvFile)
		assert.Equal(t, map[string]string{"SOME_ENV_VAR": "some-value"}, config.Runner.Envs)
	})

	t.Run("Env file merged with envs", func(t *testing.T) {
		tempDir := t.TempDir()
		configFile := filepath.Join(tempDir, "config.yaml")
		envFile := filepath.Join(tempDir, ".env")

		rawConfig := fmt.Sprintf(`
runner:
  envs:
    MY_VARIABLE: value
  env_file: "%s"
`, envFile)
		err := os.WriteFile(configFile, []byte(rawConfig), 0o644)
		require.NoError(t, err)

		err = os.WriteFile(envFile, []byte("SOME_ENV_VAR=some-value"), 0o644)
		require.NoError(t, err)

		config, err := LoadDefault(configFile)
		require.NoError(t, err)

		assert.Equal(t, envFile, config.Runner.EnvFile)
		assert.Equal(t, map[string]string{"MY_VARIABLE": "value", "SOME_ENV_VAR": "some-value"}, config.Runner.Envs)
	})

	t.Run("Ignores missing env file", func(t *testing.T) {
		tempDir := t.TempDir()
		configFile := filepath.Join(tempDir, "config.yaml")
		envFile := filepath.Join(tempDir, ".env")

		rawConfig := fmt.Sprintf(`{ runner: { env_file: "%s" } }`, envFile)
		err := os.WriteFile(configFile, []byte(rawConfig), 0o644)
		require.NoError(t, err)

		config, err := LoadDefault(configFile)
		require.NoError(t, err)

		assert.Equal(t, envFile, config.Runner.EnvFile)
		assert.Empty(t, config.Runner.Envs)
	})

	t.Run("Malformed env file results in error", func(t *testing.T) {
		tempDir := t.TempDir()
		configFile := filepath.Join(tempDir, "config.yaml")
		envFile := filepath.Join(tempDir, ".env")

		rawConfig := fmt.Sprintf(`{ runner: { env_file: "%s" } }`, envFile)
		err := os.WriteFile(configFile, []byte(rawConfig), 0o644)
		require.NoError(t, err)

		err = os.WriteFile(envFile, []byte("very/malformed"), 0o644)
		require.NoError(t, err)

		config, err := LoadDefault(configFile)

		assert.Nil(t, config)
		assert.ErrorContains(t, err, "could not read env file")
	})
}
