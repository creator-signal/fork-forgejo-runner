package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

// loadEnvVars loads configuration settings from environment variables
// prefixed with "RUNNER__" and updates the provided Config struct accordingly.
func loadEnvVars(config *Config) {
	// There probably is a better way to do this, but I'm not sure how to do it.
	// This implementation causes env-vars to override config file settings,
	// without writing to the config file.

	if debug, ok := os.LookupEnv("DEBUG"); ok && debug == "true" {
		log.SetLevel(log.DebugLevel)
		log.Debug("Debug logging enabled")
	}

	// Log
	loadEnvStr(config, "RUNNER__log__LEVEL", &config.Log.Level)
	loadEnvStr(config, "RUNNER__log__JOB_LEVEL", &config.Log.JobLevel)

	// Runner
	loadEnvStr(config, "RUNNER__runner__FILE", &config.Runner.File)
	loadEnvInt(config, "RUNNER__runner__CAPACITY", &config.Runner.Capacity)
	loadEnvTable(config, "RUNNER__runner__ENVS", &config.Runner.Envs)
	loadEnvStr(config, "RUNNER__runner__ENV_FILE", &config.Runner.EnvFile)
	loadEnvDuration(config, "RUNNER__runner__SHUTDOWN_TIMEOUT", &config.Runner.ShutdownTimeout)
	loadEnvBool(config, "RUNNER__runner__INSECURE", &config.Runner.Insecure)
	loadEnvDuration(config, "RUNNER__runner__FETCH_TIMEOUT", &config.Runner.FetchTimeout)
	loadEnvDuration(config, "RUNNER__runner__FETCH_INTERVAL", &config.Runner.FetchInterval)
	loadEnvDuration(config, "RUNNER__runner__REPORT_INTERVAL", &config.Runner.ReportInterval)
	loadEnvList(config, "RUNNER__runner__LABELS", &config.Runner.Labels)

	// Cache
	loadEnvBool(config, "RUNNER__cache__ENABLED", config.Cache.Enabled)
	loadEnvStr(config, "RUNNER__cache__DIR", &config.Cache.Dir)
	loadEnvStr(config, "RUNNER__cache__HOST", &config.Cache.Host)
	loadEnvUInt16(config, "RUNNER__cache__PORT", &config.Cache.Port)
	loadEnvStr(config, "RUNNER__cache__EXTERNAL_SERVER", &config.Cache.ExternalServer)

	// Container
	loadEnvStr(config, "RUNNER__container__NETWORK", &config.Container.Network)
	loadEnvStr(config, "RUNNER__container__NETWORK_MODE", &config.Container.NetworkMode)
	loadEnvBool(config, "RUNNER__container__ENABLE_IPV6", &config.Container.EnableIPv6)
	loadEnvBool(config, "RUNNER__container__PRIVILEGED", &config.Container.Privileged)
	loadEnvStr(config, "RUNNER__container__OPTIONS", &config.Container.Options)
	loadEnvStr(config, "RUNNER__container__WORKDIR_PARENT", &config.Container.WorkdirParent)
	loadEnvList(config, "RUNNER__container__VALID_VOLUMES", &config.Container.ValidVolumes)
	loadEnvStr(config, "RUNNER__container__DOCKER_HOST", &config.Container.DockerHost)
	loadEnvBool(config, "RUNNER__container__FORCE_PULL", &config.Container.ForcePull)

	// Host
	loadEnvStr(config, "RUNNER__host__WORKDIR_PARENT", &config.Host.WorkdirParent)
}

// General Coverage Docs for below:
// loadEnvType, where Type is the type of the variable being loaded, loads an environment variable into the provided pointer if the environment variable exists and is not empty.
// The key parameter is the environment variable key to look for.
// The dest parameter is a pointer to the variable to load the environment variable into.
// Config is present but unused, it remains for future use to prevent the need to redo the above functions.

func loadEnvStr(config *Config, key string, dest *string) {
	// Example: RUNNER__LOG__LEVEL = "info"
	if v := os.Getenv(key); v != "" {
		log.Debug("Loading env var: ", key, "=", v)
		*dest = v
	}
}

// loadEnvInt loads an environment variable into the provided int pointer if it exists.
func loadEnvInt(config *Config, key string, dest *int) {
	// Example: RUNNER__RUNNER__CAPACITY = "1"
	if v := os.Getenv(key); v != "" {
		if intValue, err := strconv.Atoi(v); err == nil {
			log.Debug("Loading env var: ", key, "=", v)
			*dest = intValue
		}
	}
}

// loadEnvUInt16 loads an environment variable into the provided uint16 pointer if it exists.
func loadEnvUInt16(config *Config, key string, dest *uint16) {
	// Example: RUNNER__CACHE__PORT = "8080"
	if v := os.Getenv(key); v != "" {
		if uint16Value, err := strconv.ParseUint(v, 10, 16); err == nil {
			log.Debug("Loading env var: ", key, "=", v)
			*dest = uint16(uint16Value)
		}
	}
}

// loadEnvDuration loads an environment variable into the provided time.Duration pointer if it exists.
func loadEnvDuration(config *Config, key string, dest *time.Duration) {
	// Example: RUNNER__RUNNER__SHUTDOWN_TIMEOUT = "3h"
	if v := os.Getenv(key); v != "" {
		if durationValue, err := time.ParseDuration(v); err == nil {
			log.Debug("Loading env var: ", key, "=", v)
			*dest = durationValue
		}
	}
}

// loadEnvBool loads an environment variable into the provided bool pointer if it exists.
func loadEnvBool(config *Config, key string, dest *bool) {
	// Example: RUNNER__RUNNER__INSECURE = "false"
	if v := os.Getenv(key); v != "" {
		if boolValue, err := strconv.ParseBool(v); err == nil {
			log.Debug("Loading env var: ", key, "=", v)
			*dest = boolValue
		}
	}
}

// loadEnvTable loads an environment variable into the provided map[string]string pointer if it exists.
func loadEnvTable(config *Config, key string, dest *map[string]string) {
	// Example: RUNNER__RUNNER__ENVS = "key1=value1, key2=value2, key3=value3"
	if v := os.Getenv(key); v != "" {
		*dest = make(map[string]string)
		for _, pair := range splitAndTrim(v) {
			kv := strings.SplitN(pair, "=", 2)
			if len(kv) == 2 {
				(*dest)[kv[0]] = kv[1]
			}
		}
	}
}

// loadEnvList loads an environment variable into the provided []string pointer if it exists.
func loadEnvList(config *Config, key string, dest *[]string) {
	// Example: RUNNER__RUNNER__LABELS = "label1, label2, label3"
	if v := os.Getenv(key); v != "" {
		log.Debug("Loading env var: ", key, "=", v)
		*dest = splitAndTrim(v)
	}
}

func splitAndTrim(s string) []string {
	lines := strings.Split(s, "\n")
	var result []string
	for _, line := range lines {
		items := strings.Split(line, ",")
		for _, item := range items {
			trimmed := strings.TrimSpace(item)
			if trimmed != "" {
				result = append(result, trimmed)
			}
		}
	}
	return result
}
