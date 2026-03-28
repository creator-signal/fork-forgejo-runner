// Copyright 2026 The Forgejo Authors. All rights reserved.
// SPDX-License-Identifier: GPL-3.0-or-later

package cmd

import (
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"code.forgejo.org/forgejo/runner/v12/internal/pkg/config"
	"code.forgejo.org/forgejo/runner/v12/internal/pkg/labels"
	gouuid "github.com/google/uuid"
	"github.com/powerman/fileuri"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgs_connectionFromArguments(t *testing.T) {
	t.Run("does nothing if no connection defined", func(t *testing.T) {
		conn := connection{
			url:           "",
			uuid:          "",
			tokenURL:      "",
			labels:        []string{},
			fetchInterval: 0,
		}

		cfg := config.Config{}
		err := connectionFromArguments(&conn)(&cfg)
		require.NoError(t, err)

		assert.Len(t, cfg.Server.Connections, 0)
	})

	t.Run("accepts valid settings", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		tokenURL, err := prepareTokenFile(t, "doERMgMiVz")
		require.NoError(t, err)

		expected := config.Connection{
			URL:           serverURL,
			UUID:          gouuid.MustParse("4f79bf07-38f6-44c8-bbc6-d6531134f16a"),
			Token:         "doERMgMiVz",
			Labels:        labels.Labels{labels.MustParse("label-1"), labels.MustParse("label-2")},
			FetchInterval: time.Second,
		}

		conn := connection{
			url:           serverURL.String(),
			uuid:          "4f79bf07-38f6-44c8-bbc6-d6531134f16a",
			tokenURL:      tokenURL.String(),
			labels:        []string{"label-1", "label-2"},
			fetchInterval: time.Second,
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		require.NoError(t, err)

		assert.Len(t, cfg.Server.Connections, 1)
		assert.Equal(t, &expected, cfg.Server.Connections["default"])
	})

	t.Run("returns error if connections are already present", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		tokenURL, err := prepareTokenFile(t, "doERMgMiVz")
		require.NoError(t, err)

		conn := connection{
			url:           serverURL.String(),
			uuid:          "4f79bf07-38f6-44c8-bbc6-d6531134f16a",
			tokenURL:      tokenURL.String(),
			labels:        []string{"label-1", "label-2"},
			fetchInterval: time.Second,
		}

		cfg := config.Config{
			Server: config.Server{Connections: map[string]*config.Connection{"example": {}}},
		}
		err = connectionFromArguments(&conn)(&cfg)
		require.ErrorContains(t, err, "server connection conflict between program arguments and config")
	})

	t.Run("rejects missing url", func(t *testing.T) {
		tokenURL, err := prepareTokenFile(t, "a7h4uCzbB6B")
		require.NoError(t, err)

		conn := connection{
			url:      "",
			uuid:     "26a6db40-e86c-4751-ba2e-adcab9615ba7",
			tokenURL: tokenURL.String(),
			labels:   []string{"label-1"},
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		assert.ErrorContains(t, err, "`url` is empty")
	})

	t.Run("rejects url without scheme", func(t *testing.T) {
		tokenURL, err := prepareTokenFile(t, "a7h4uCzbB6B")
		require.NoError(t, err)

		conn := connection{
			url:      "www.example.com",
			uuid:     "26a6db40-e86c-4751-ba2e-adcab9615ba7",
			tokenURL: tokenURL.String(),
			labels:   []string{"label-1"},
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		assert.ErrorContains(t, err, "malformed `url` \"www.example.com\"")
	})

	t.Run("only accepts schemes http and https", func(t *testing.T) {
		tokenURL, err := prepareTokenFile(t, "a7h4uCzbB6B")
		require.NoError(t, err)

		conn := connection{
			url:      "file:///some/path",
			uuid:     "26a6db40-e86c-4751-ba2e-adcab9615ba7",
			tokenURL: tokenURL.String(),
			labels:   []string{"label-1"},
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		assert.ErrorContains(t, err, "invalid scheme in `url` \"file:///some/path\": only http and https supported")
	})

	t.Run("rejects empty uuid", func(t *testing.T) {
		tokenURL, err := prepareTokenFile(t, "a7h4uCzbB6B")
		require.NoError(t, err)

		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		conn := connection{
			url:      serverURL.String(),
			uuid:     "",
			tokenURL: tokenURL.String(),
			labels:   []string{"label-1"},
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		assert.ErrorContains(t, err, "`uuid` is empty")
	})

	t.Run("rejects malformed uuid", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		tokenURL, err := prepareTokenFile(t, "a7h4uCzbB6B")
		require.NoError(t, err)

		conn := connection{
			url:      serverURL.String(),
			uuid:     "very invalid",
			tokenURL: tokenURL.String(),
			labels:   []string{"label-1"},
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		assert.ErrorContains(t, err, "malformed `uuid` \"very invalid\"")
	})

	t.Run("token-url cannot be empty", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		conn := connection{
			url:      serverURL.String(),
			uuid:     "009e3230-0881-4690-8e0e-43ce2c01d2f9",
			tokenURL: "",
			labels:   []string{"label-1"},
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		assert.ErrorContains(t, err, "`token-url` is empty")
	})

	t.Run("resolves token_url", func(t *testing.T) {
		tokenURL, err := prepareTokenFile(t, "8tBZOQlSaH")
		require.NoError(t, err)

		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		conn := connection{
			url:      serverURL.String(),
			uuid:     "009e3230-0881-4690-8e0e-43ce2c01d2f9",
			tokenURL: tokenURL.String(),
			labels:   []string{"label-1"},
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		require.NoError(t, err)

		assert.Equal(t, "8tBZOQlSaH", cfg.Server.Connections["default"].Token)
	})

	t.Run("rejects malformed label", func(t *testing.T) {
		serverURL, err := url.Parse("https://example.com/")
		require.NoError(t, err)

		tokenURL, err := prepareTokenFile(t, "Shoi0zUBg6P")
		require.NoError(t, err)

		conn := connection{
			url:      serverURL.String(),
			uuid:     "54d7e9a6-0b44-4d81-8948-50c1dd351d19",
			tokenURL: tokenURL.String(),
			labels:   []string{"label1", " very invalid "},
		}

		cfg := config.Config{}
		err = connectionFromArguments(&conn)(&cfg)
		assert.ErrorContains(t, err, "malformed `label` \" very invalid \"")
	})
}

func prepareTokenFile(t *testing.T, token string) (*url.URL, error) {
	t.Helper()

	tempDir := t.TempDir()
	tokenPath := filepath.Join(tempDir, "token.txt")

	if err := os.WriteFile(tokenPath, []byte(token), 0o644); err != nil {
		return nil, err
	}

	return fileuri.FromFilePath(tokenPath)
}
