// Copyright 2024 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package config

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	gouuid "github.com/google/uuid"
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

func TestNew(t *testing.T) {
	t.Run("Missing configuration file results in error", func(t *testing.T) {
		config, err := New(FromFile("does-not-exist"))

		assert.Nil(t, config)
		assert.ErrorContains(t, err, "open does-not-exist: no such file or directory")
	})

	t.Run("Malformed configuration file results in error", func(t *testing.T) {
		tempDir := t.TempDir()

		configPath := filepath.Join(tempDir, "config.yaml")

		err := os.WriteFile(configPath, []byte("malformed"), 0o644)
		require.NoError(t, err)

		config, err := New(FromFile(configPath))

		assert.Nil(t, config)
		assert.ErrorContains(t, err, fmt.Sprintf(`cannot parse config file "%s"`, configPath))
	})

	t.Run("Without configuration file", func(t *testing.T) {
		config, err := New()
		require.NoError(t, err)

		home, err := os.UserHomeDir()
		require.NoError(t, err)

		assert.Equal(t, 6, reflect.TypeOf(Config{}).NumField())
		assert.Equal(t, 2, reflect.TypeOf(Log{}).NumField())
		assert.Equal(t, 12, reflect.TypeOf(Runner{}).NumField())
		assert.Equal(t, 8, reflect.TypeOf(Cache{}).NumField())
		assert.Equal(t, 9, reflect.TypeOf(Container{}).NumField())
		assert.Equal(t, 1, reflect.TypeOf(Host{}).NumField())
		assert.Equal(t, 1, reflect.TypeOf(Server{}).NumField())

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

		assert.True(t, config.Cache.Enabled)
		assert.Equal(t, filepath.Join(home, ".cache", "actcache"), config.Cache.Dir)
		assert.Empty(t, config.Cache.Host)
		assert.Zero(t, config.Cache.Port)
		assert.Zero(t, config.Cache.ProxyPort)
		assert.Empty(t, config.Cache.ExternalServer)
		assert.Empty(t, config.Cache.ActionsCacheURLOverride)
		assert.Empty(t, config.Cache.Secret)

		assert.Empty(t, config.Container.Network)
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

		assert.Empty(t, config.Server.Connections)
	})

	t.Run("Defaults retained if configuration file is empty", func(t *testing.T) {
		tempDir := t.TempDir()

		configPath := filepath.Join(tempDir, "config.yaml")

		err := os.WriteFile(configPath, []byte(""), 0o644)
		require.NoError(t, err)

		config, err := New(FromFile(configPath))
		require.NoError(t, err)

		home, err := os.UserHomeDir()
		require.NoError(t, err)

		assert.Equal(t, 6, reflect.TypeOf(Config{}).NumField())
		assert.Equal(t, 2, reflect.TypeOf(Log{}).NumField())
		assert.Equal(t, 12, reflect.TypeOf(Runner{}).NumField())
		assert.Equal(t, 8, reflect.TypeOf(Cache{}).NumField())
		assert.Equal(t, 9, reflect.TypeOf(Container{}).NumField())
		assert.Equal(t, 1, reflect.TypeOf(Host{}).NumField())
		assert.Equal(t, 1, reflect.TypeOf(Server{}).NumField())

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

		assert.True(t, config.Cache.Enabled)
		assert.Equal(t, filepath.Join(home, ".cache", "actcache"), config.Cache.Dir)
		assert.Empty(t, config.Cache.Host)
		assert.Zero(t, config.Cache.Port)
		assert.Zero(t, config.Cache.ProxyPort)
		assert.Empty(t, config.Cache.ExternalServer)
		assert.Empty(t, config.Cache.ActionsCacheURLOverride)
		assert.Empty(t, config.Cache.Secret)

		assert.Empty(t, config.Container.Network)
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

		assert.Empty(t, config.Server.Connections)
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
server:
  connections:
    example:
      url: https://example.com/
      uuid: 7f7695df-a064-4c70-a597-56714e851e2c
      token: LxV7RrjXd
      labels:
        - debian:docker://node:24-trixie
`

		tempDir := t.TempDir()

		configPath := filepath.Join(tempDir, "config.yml")
		err := os.WriteFile(configPath, []byte(rawConfig), 0o644)
		require.NoError(t, err)

		defaultConfig, err := New()
		require.NoError(t, err)

		config, err := New(FromFile(configPath))
		require.NoError(t, err)

		assert.Equal(t, 6, reflect.TypeOf(Config{}).NumField())
		assert.Equal(t, 2, reflect.TypeOf(Log{}).NumField())
		assert.Equal(t, 12, reflect.TypeOf(Runner{}).NumField())
		assert.Equal(t, 8, reflect.TypeOf(Cache{}).NumField())
		assert.Equal(t, 9, reflect.TypeOf(Container{}).NumField())
		assert.Equal(t, 1, reflect.TypeOf(Host{}).NumField())
		assert.Equal(t, 1, reflect.TypeOf(Server{}).NumField())

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
		assert.False(t, config.Cache.Enabled)
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

		assert.NotEqual(t, len(defaultConfig.Server.Connections), len(config.Server.Connections))
		assert.Len(t, config.Server.Connections, 1)
		assert.Equal(t, "https://example.com/", config.Server.Connections["example"].URL.String())
		assert.Equal(t, "7f7695df-a064-4c70-a597-56714e851e2c", config.Server.Connections["example"].UUID.String())
		assert.Equal(t, "LxV7RrjXd", config.Server.Connections["example"].Token)
		assert.Len(t, config.Server.Connections["example"].Labels, 1)
		assert.Equal(t, labels.MustParse("debian:docker://node:24-trixie"), config.Server.Connections["example"].Labels[0])
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

		config, err := New(FromFile(configFile))
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

		config, err := New(FromFile(configFile))
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

		config, err := New(FromFile(configFile))
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

		config, err := New(FromFile(configFile))

		assert.Nil(t, config)
		assert.ErrorContains(t, err, "could not read env file")
	})
}

func TestSerializedLogSettings_applyTo(t *testing.T) {
	t.Run("accepts valid settings", func(t *testing.T) {
		expected := Log{
			Level:    "warn",
			JobLevel: "error",
		}

		settings := serializedLogSettings{
			Level:    "warn",
			JobLevel: "error",
		}

		config := Config{}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Log)
	})
	t.Run("rejects invalid level", func(t *testing.T) {
		settings := serializedLogSettings{
			Level:    "invalid",
			JobLevel: "error",
		}

		config := Config{}
		err := settings.applyTo(&config)

		assert.ErrorContains(t, err, "invalid `level` \"invalid\"")
	})
	t.Run("rejects invalid job_level", func(t *testing.T) {
		settings := serializedLogSettings{
			Level:    "error",
			JobLevel: "invalid",
		}

		config := Config{}
		err := settings.applyTo(&config)

		assert.ErrorContains(t, err, "invalid `job_level` \"invalid\"")
	})
}

func TestSerializedRunnerSettings_applyTo(t *testing.T) {
	t.Run("accepts valid settings without env file", func(t *testing.T) {
		expected := Runner{
			File:            ".my_runner",
			Capacity:        762,
			Envs:            map[string]string{"ABC": "def"},
			EnvFile:         "",
			Timeout:         892 * time.Second,
			ShutdownTimeout: 878 * time.Second,
			Insecure:        true,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     Retry{},
		}

		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        762,
			Envs:            map[string]string{"ABC": "def"},
			EnvFile:         "",
			Timeout:         892 * time.Second,
			ShutdownTimeout: 878 * time.Second,
			Insecure:        true,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Runner)
	})

	t.Run("accepts valid settings with env file", func(t *testing.T) {
		tempDir := t.TempDir()
		envPath := filepath.Join(tempDir, "env-file")

		err := os.WriteFile(envPath, []byte("B=99\nC=3"), 0o644)
		require.NoError(t, err)

		expected := Runner{
			File:            ".my_runner",
			Capacity:        762,
			Envs:            map[string]string{"A": "1", "B": "99", "C": "3"},
			EnvFile:         envPath,
			Timeout:         892 * time.Second,
			ShutdownTimeout: 878 * time.Second,
			Insecure:        true,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     Retry{},
		}

		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        762,
			Envs:            map[string]string{"A": "1", "B": "2"},
			EnvFile:         envPath,
			Timeout:         892 * time.Second,
			ShutdownTimeout: 878 * time.Second,
			Insecure:        true,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{}
		err = settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Runner)
	})

	t.Run("ignores missing env file", func(t *testing.T) {
		expected := Runner{
			File:            ".my_runner",
			Capacity:        762,
			Envs:            map[string]string{"A": "1", "B": "2"},
			EnvFile:         "/does/not/exist",
			Timeout:         892 * time.Second,
			ShutdownTimeout: 878 * time.Second,
			Insecure:        true,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     Retry{},
		}

		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        762,
			Envs:            map[string]string{"A": "1", "B": "2"},
			EnvFile:         "/does/not/exist",
			Timeout:         892 * time.Second,
			ShutdownTimeout: 878 * time.Second,
			Insecure:        true,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Runner)
	})

	t.Run("ignores invalid capacity", func(t *testing.T) {
		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        0,
			Envs:            map[string]string{"ABC": "def"},
			EnvFile:         "",
			Timeout:         892 * time.Second,
			ShutdownTimeout: 878 * time.Second,
			Insecure:        false,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{Runner: Runner{Capacity: 1}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, 1, config.Runner.Capacity)
	})

	t.Run("ignores invalid timeout", func(t *testing.T) {
		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        370,
			Envs:            map[string]string{"ABC": "def"},
			EnvFile:         "",
			Timeout:         0 * time.Second,
			ShutdownTimeout: 878 * time.Second,
			Insecure:        false,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{Runner: Runner{Timeout: time.Second}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, time.Second, config.Runner.Timeout)
	})

	t.Run("ignores invalid shutdown_timeout", func(t *testing.T) {
		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        370,
			Envs:            map[string]string{"ABC": "def"},
			EnvFile:         "",
			Timeout:         588 * time.Second,
			ShutdownTimeout: -1 * time.Second,
			Insecure:        false,
			FetchTimeout:    299 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{Runner: Runner{ShutdownTimeout: 0}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, 0*time.Second, config.Runner.ShutdownTimeout)
	})

	t.Run("ignores invalid fetch_timeout", func(t *testing.T) {
		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        370,
			Envs:            map[string]string{"ABC": "def"},
			EnvFile:         "",
			Timeout:         588 * time.Second,
			ShutdownTimeout: 0 * time.Second,
			Insecure:        false,
			FetchTimeout:    -1 * time.Second,
			FetchInterval:   820 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{Runner: Runner{FetchTimeout: 3 * time.Second}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, 3*time.Second, config.Runner.FetchTimeout)
	})

	t.Run("ignores invalid fetch_interval", func(t *testing.T) {
		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        370,
			Envs:            map[string]string{"ABC": "def"},
			EnvFile:         "",
			Timeout:         588 * time.Second,
			ShutdownTimeout: 0 * time.Second,
			Insecure:        false,
			FetchTimeout:    939 * time.Second,
			FetchInterval:   -1 * time.Second,
			ReportInterval:  868 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{Runner: Runner{FetchInterval: 0}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, 0*time.Second, config.Runner.FetchInterval)
	})

	t.Run("ignores invalid report_interval", func(t *testing.T) {
		settings := serializedRunnerSettings{
			File:            ".my_runner",
			Capacity:        370,
			Envs:            map[string]string{"ABC": "def"},
			EnvFile:         "",
			Timeout:         588 * time.Second,
			ShutdownTimeout: 0 * time.Second,
			Insecure:        false,
			FetchTimeout:    939 * time.Second,
			FetchInterval:   477 * time.Second,
			ReportInterval:  -1 * time.Second,
			Labels:          []string{"label-1", "label-2"},
			ReportRetry:     serializedReportRetrySettings{},
		}

		config := Config{Runner: Runner{ReportInterval: 0}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, 0*time.Second, config.Runner.ReportInterval)
	})
}

func TestSerializedReportRetrySettings_applyTo(t *testing.T) {
	t.Run("accepts valid settings", func(t *testing.T) {
		expected := Retry{
			MaxRetries:   13,
			InitialDelay: 200 * time.Millisecond,
			MaxDelay:     30 * time.Second,
		}

		settings := serializedReportRetrySettings{
			MaxRetries:   13,
			InitialDelay: 200 * time.Millisecond,
			MaxDelay:     30 * time.Second,
		}

		config := Config{}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Runner.ReportRetry)
	})
	t.Run("ignores invalid max_retries", func(t *testing.T) {
		settings := serializedReportRetrySettings{
			MaxRetries:   0,
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     0,
		}

		config := Config{Runner: Runner{ReportRetry: Retry{MaxRetries: 10}}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, uint(10), config.Runner.ReportRetry.MaxRetries)
	})
	t.Run("ignores invalid initial_delay", func(t *testing.T) {
		settings := serializedReportRetrySettings{
			MaxRetries:   10,
			InitialDelay: 0,
			MaxDelay:     0,
		}

		config := Config{Runner: Runner{ReportRetry: Retry{InitialDelay: 100 * time.Millisecond}}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, 100*time.Millisecond, config.Runner.ReportRetry.InitialDelay)
	})
	t.Run("ignores invalid max_delay", func(t *testing.T) {
		settings := serializedReportRetrySettings{
			MaxRetries:   10,
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     -1 * time.Second,
		}

		config := Config{Runner: Runner{ReportRetry: Retry{MaxDelay: 0}}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, 0*time.Second, config.Runner.ReportRetry.MaxDelay)
	})
}

func TestSerializedCacheSettings(t *testing.T) {
	t.Run("accepts valid settings", func(t *testing.T) {
		expected := Cache{
			Enabled:                 true,
			Dir:                     "/path/to/cache",
			Host:                    "cache.local",
			Port:                    1234,
			ProxyPort:               5678,
			ExternalServer:          "external.local",
			ActionsCacheURLOverride: "https://example.com/",
			Secret:                  "jaJEV8OHlux",
		}

		booleanTrue := true
		settings := serializedCacheSettings{
			Enabled:                 &booleanTrue,
			Dir:                     "/path/to/cache",
			Host:                    "cache.local",
			Port:                    1234,
			ProxyPort:               5678,
			ExternalServer:          "external.local",
			ActionsCacheURLOverride: "https://example.com/",
			Secret:                  "jaJEV8OHlux",
		}

		config := Config{}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Cache)
	})
}

func TestSerializedContainerSettings(t *testing.T) {
	t.Run("accepts valid settings", func(t *testing.T) {
		expected := Container{
			Network:       "host",
			EnableIPv6:    true,
			Privileged:    true,
			Options:       "--init",
			WorkdirParent: "/path/to/parent",
			ValidVolumes:  []string{"/tmp"},
			DockerHost:    "unix:///run/user/1000/podman/podman.sock",
			ForcePull:     true,
			ForceRebuild:  true,
		}

		settings := serializedContainerSettings{
			Network:       "host",
			NetworkMode:   "",
			EnableIPv6:    true,
			Privileged:    true,
			Options:       "--init",
			WorkdirParent: "/path/to/parent",
			ValidVolumes:  []string{"/tmp"},
			DockerHost:    "unix:///run/user/1000/podman/podman.sock",
			ForcePull:     true,
			ForceRebuild:  true,
		}

		config := Config{}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Container)
	})
	t.Run("translates network_mode bridge to empty network", func(t *testing.T) {
		expected := Container{
			Network:       "",
			EnableIPv6:    true,
			Privileged:    true,
			Options:       "--init",
			WorkdirParent: "/path/to/parent",
			ValidVolumes:  []string{"/tmp"},
			DockerHost:    "unix:///run/user/1000/podman/podman.sock",
			ForcePull:     true,
			ForceRebuild:  true,
		}

		settings := serializedContainerSettings{
			Network:       "",
			NetworkMode:   "bridge",
			EnableIPv6:    true,
			Privileged:    true,
			Options:       "--init",
			WorkdirParent: "/path/to/parent",
			ValidVolumes:  []string{"/tmp"},
			DockerHost:    "unix:///run/user/1000/podman/podman.sock",
			ForcePull:     true,
			ForceRebuild:  true,
		}

		config := Config{}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Container)
	})
	t.Run("skips empty workdir_parent", func(t *testing.T) {
		expected := Container{
			Network:       "host",
			EnableIPv6:    true,
			Privileged:    true,
			Options:       "--init",
			WorkdirParent: "/path/to/parent",
			ValidVolumes:  []string{"/tmp"},
			DockerHost:    "unix:///run/user/1000/podman/podman.sock",
			ForcePull:     true,
			ForceRebuild:  true,
		}

		settings := serializedContainerSettings{
			Network:       "host",
			NetworkMode:   "bridge",
			EnableIPv6:    true,
			Privileged:    true,
			Options:       "--init",
			WorkdirParent: "",
			ValidVolumes:  []string{"/tmp"},
			DockerHost:    "unix:///run/user/1000/podman/podman.sock",
			ForcePull:     true,
			ForceRebuild:  true,
		}

		config := Config{Container: Container{WorkdirParent: "/path/to/parent"}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Container)
	})
	t.Run("skips empty docker_host", func(t *testing.T) {
		expected := Container{
			Network:       "host",
			EnableIPv6:    true,
			Privileged:    true,
			Options:       "--init",
			WorkdirParent: "/path/to/parent",
			ValidVolumes:  []string{"/tmp"},
			DockerHost:    "unix:///run/user/1000/podman/podman.sock",
			ForcePull:     true,
			ForceRebuild:  true,
		}

		settings := serializedContainerSettings{
			Network:       "host",
			NetworkMode:   "bridge",
			EnableIPv6:    true,
			Privileged:    true,
			Options:       "--init",
			WorkdirParent: "/path/to/parent",
			ValidVolumes:  []string{"/tmp"},
			DockerHost:    "",
			ForcePull:     true,
			ForceRebuild:  true,
		}

		config := Config{Container: Container{DockerHost: "unix:///run/user/1000/podman/podman.sock"}}
		err := settings.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Container)
	})
}

func TestSerializedServerSettings_applyTo(t *testing.T) {
	t.Run("accepts valid settings", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		expected := Server{
			Connections: map[string]Connection{
				"example": {
					URL:    serverURL,
					UUID:   gouuid.MustParse("4f79bf07-38f6-44c8-bbc6-d6531134f16a"),
					Token:  "doERMgMiVz",
					Labels: labels.Labels{labels.MustParse("label-1"), labels.MustParse("label-2")},
				},
			},
		}

		serialized := serializedServerSettings{
			Connections: map[string]serializedConnectionSettings{
				"example": {
					URL:    serverURL.String(),
					UUID:   "4f79bf07-38f6-44c8-bbc6-d6531134f16a",
					Token:  "doERMgMiVz",
					Labels: []string{"label-1", "label-2"},
				},
			},
		}

		config := Config{}
		err = serialized.applyTo(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config.Server)
	})
}

func TestSerializedConnectionSettings_applyTo(t *testing.T) {
	t.Run("accepts valid settings", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		expected := Connection{
			URL:    serverURL,
			UUID:   gouuid.MustParse("4f79bf07-38f6-44c8-bbc6-d6531134f16a"),
			Token:  "doERMgMiVz",
			Labels: labels.Labels{labels.MustParse("label-1"), labels.MustParse("label-2")},
		}

		serialized := serializedConnectionSettings{
			URL:    serverURL.String(),
			UUID:   "4f79bf07-38f6-44c8-bbc6-d6531134f16a",
			Token:  "doERMgMiVz",
			Labels: []string{"label-1", "label-2"},
		}

		config := Config{}
		err = serialized.applyTo(&config, "example")
		require.NoError(t, err)

		assert.Len(t, config.Server.Connections, 1)
		assert.Equal(t, expected, config.Server.Connections["example"])
	})

	t.Run("rejects missing url", func(t *testing.T) {
		serialized := serializedConnectionSettings{
			URL:    "",
			UUID:   "26a6db40-e86c-4751-ba2e-adcab9615ba7",
			Token:  "a7h4uCzbB6B",
			Labels: []string{"label-1"},
		}

		err := serialized.applyTo(&Config{}, "example")
		assert.ErrorContains(t, err, "`url` is empty")
	})

	t.Run("rejects url without scheme", func(t *testing.T) {
		serialized := serializedConnectionSettings{
			URL:    "www.example.com",
			UUID:   "26a6db40-e86c-4751-ba2e-adcab9615ba7",
			Token:  "a7h4uCzbB6B",
			Labels: []string{"label-1"},
		}

		err := serialized.applyTo(&Config{}, "example")
		assert.ErrorContains(t, err, "malformed `url` \"www.example.com\"")
	})

	t.Run("only accepts schemes http and https", func(t *testing.T) {
		serialized := serializedConnectionSettings{
			URL:    "file:///some/path",
			UUID:   "26a6db40-e86c-4751-ba2e-adcab9615ba7",
			Token:  "a7h4uCzbB6B",
			Labels: []string{"label-1"},
		}

		err := serialized.applyTo(&Config{}, "example")
		assert.ErrorContains(t, err, "invalid scheme in `url` \"file:///some/path\": only http and https supported")
	})

	t.Run("rejects empty uuid", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		serialized := serializedConnectionSettings{
			URL:    serverURL.String(),
			UUID:   "",
			Token:  "a7h4uCzbB6B",
			Labels: []string{"label-1"},
		}

		err = serialized.applyTo(&Config{}, "example")
		assert.ErrorContains(t, err, "`uuid` is empty")
	})

	t.Run("rejects malformed uuid", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		serialized := serializedConnectionSettings{
			URL:    serverURL.String(),
			UUID:   "very invalid",
			Token:  "a7h4uCzbB6B",
			Labels: []string{"label-1"},
		}

		err = serialized.applyTo(&Config{}, "example")
		assert.ErrorContains(t, err, "malformed `uuid` \"very invalid\"")
	})

	t.Run("rejects empty token", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		serialized := serializedConnectionSettings{
			URL:    serverURL.String(),
			UUID:   "009e3230-0881-4690-8e0e-43ce2c01d2f9",
			Token:  "",
			Labels: []string{"label-1"},
		}

		err = serialized.applyTo(&Config{}, "example")
		assert.ErrorContains(t, err, "`token` is empty")
	})

	t.Run("rejects missing labels", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		serialized := serializedConnectionSettings{
			URL:    serverURL.String(),
			UUID:   "54d7e9a6-0b44-4d81-8948-50c1dd351d19",
			Token:  "Shoi0zUBg6P",
			Labels: nil,
		}

		err = serialized.applyTo(&Config{}, "example")
		assert.ErrorContains(t, err, "at least one `label` is required")
	})

	t.Run("rejects malformed label", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		serialized := serializedConnectionSettings{
			URL:    serverURL.String(),
			UUID:   "54d7e9a6-0b44-4d81-8948-50c1dd351d19",
			Token:  "Shoi0zUBg6P",
			Labels: []string{"label1", " very invalid "},
		}

		err = serialized.applyTo(&Config{}, "example")
		assert.ErrorContains(t, err, "malformed `label` \" very invalid \"")
	})
}

func TestFromFile(t *testing.T) {
	t.Run("ignores empty path", func(t *testing.T) {
		config := Config{}
		err := FromFile("")(&config)
		require.NoError(t, err)

		assert.Equal(t, Config{}, config)
	})

	t.Run("returns error if file missing", func(t *testing.T) {
		config := Config{}
		err := FromFile("/does/not/exist")(&config)

		assert.ErrorContains(t, err, "cannot open config file \"/does/not/exist\"")
	})

	t.Run("returns error if file is invalid", func(t *testing.T) {
		tempDir := t.TempDir()

		path := filepath.Join(tempDir, "config.yml")
		err := os.WriteFile(path, []byte("/mal/formed/"), 0o644)
		require.NoError(t, err)

		config := Config{}
		err = FromFile(path)(&config)

		assert.ErrorContains(t, err, "cannot parse config file")
	})

	t.Run("accepts empty config file", func(t *testing.T) {
		tempDir := t.TempDir()

		path := filepath.Join(tempDir, "config.yml")
		err := os.WriteFile(path, []byte{}, 0o644)
		require.NoError(t, err)

		config := Config{}
		err = FromFile(path)(&config)
		require.NoError(t, err)

		assert.Equal(t, Config{}, config)
	})

	t.Run("accepts partial config file", func(t *testing.T) {
		expected := Config{
			Log: Log{
				Level:    "info",
				JobLevel: "warn",
			},
		}

		tempDir := t.TempDir()

		rawConfig := `
log:
  level: info
  job_level: warn
`

		path := filepath.Join(tempDir, "config.yml")
		err := os.WriteFile(path, []byte(rawConfig), 0o644)
		require.NoError(t, err)

		config := Config{}
		err = FromFile(path)(&config)
		require.NoError(t, err)

		assert.Equal(t, expected, config)
	})
}
