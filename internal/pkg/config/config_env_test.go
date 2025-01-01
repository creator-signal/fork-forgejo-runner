package config

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadEnvStr(t *testing.T) {
	key := "RUNNER__test__STRING"
	value := "info"
	_ = os.Setenv(key, value)
	defer os.Unsetenv(key)

	var dest string
	loadEnvStr(nil, key, &dest)

	assert.Equal(t, value, dest, "The loaded string value should match the expected value")
}

func TestLoadEnvInt(t *testing.T) {
	key := "RUNNER__test__INT"
	_ = os.Setenv(key, "42")
	defer os.Unsetenv(key)

	var dest int
	loadEnvInt(nil, key, &dest)

	expected := 42
	assert.Equal(t, expected, dest, "The loaded int value should match the expected value")
}

func TestLoadEnvUInt16(t *testing.T) {
	key := "RUNNER__test__UINT16"
	_ = os.Setenv(key, "8080")
	defer os.Unsetenv(key)

	var dest uint16
	loadEnvUInt16(nil, key, &dest)

	expected := uint16(8080)
	assert.Equal(t, expected, dest, "The loaded uint16 value should match the expected value")
}

func TestLoadEnvDuration(t *testing.T) {
	key := "RUNNER__test__DURATION"
	value := "1h"
	_ = os.Setenv(key, value)
	defer os.Unsetenv(key)

	var dest time.Duration
	loadEnvDuration(nil, key, &dest)

	expected, err := time.ParseDuration(value)
	require.NoError(t, err, "Parsing the duration value should not produce an error")
	assert.Equal(t, expected, dest, "The loaded duration value should match the expected value")
}

func TestLoadEnvBool(t *testing.T) {
	key := "RUNNER__test__BOOL"
	_ = os.Setenv(key, "true")
	defer os.Unsetenv(key)

	var dest bool
	loadEnvBool(nil, key, &dest)

	assert.True(t, dest, "The loaded bool value should be true")
}

func TestLoadEnvTable(t *testing.T) {
	key := "RUNNER__test__TABLE"
	_ = os.Setenv(key, "key1=value1, key2=value2, key3=value3")
	defer os.Unsetenv(key)

	dest := make(map[string]string)
	loadEnvTable(nil, key, &dest)

	expected := map[string]string{
		"key1": "value1",
		"key2": "value2",
		"key3": "value3",
	}

	assert.Equal(t, expected, dest, "The loaded map value should match the expected value")
}

func TestLoadEnvList(t *testing.T) {
	key := "RUNNER__test__LIST"
	_ = os.Setenv(key, "label1, label2, label3")
	defer os.Unsetenv(key)

	var dest []string
	loadEnvList(nil, key, &dest)

	expected := []string{"label1", "label2", "label3"}
	assert.Equal(t, expected, dest, "The loaded list value should match the expected value")
}

// Edge case: Load a table as a list, could happen.
func TestLoadEnvTableAsList(t *testing.T) {
	key := "RUNNER__test__TABLE_AS_LIST"
	_ = os.Setenv(key, "key1=value1, key2=value2, key3=value3")
	defer os.Unsetenv(key)

	var dest []string
	loadEnvList(nil, key, &dest)

	expected := []string{"key1=value1", "key2=value2", "key3=value3"}
	assert.Equal(t, expected, dest, "The loaded list value should match the expected value")
}
