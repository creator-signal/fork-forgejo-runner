package runner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestConfig_GetToken verifies token priority: ACTIONS_TOKEN > GITEA_TOKEN > GITHUB_TOKEN
func TestConfig_GetToken(t *testing.T) {
	t.Run("returns ACTIONS_TOKEN when all tokens present", func(t *testing.T) {
		c := &Config{
			Secrets: map[string]string{
				"GITHUB_TOKEN":  "github-token",
				"GITEA_TOKEN":   "gitea-token",
				"ACTIONS_TOKEN": "actions-token",
			},
		}
		assert.Equal(t, "actions-token", c.GetToken())
	})

	t.Run("returns GITEA_TOKEN when ACTIONS_TOKEN absent", func(t *testing.T) {
		c := &Config{
			Secrets: map[string]string{
				"GITHUB_TOKEN": "github-token",
				"GITEA_TOKEN":  "gitea-token",
			},
		}
		assert.Equal(t, "gitea-token", c.GetToken())
	})

	t.Run("returns GITHUB_TOKEN when only GITHUB_TOKEN present", func(t *testing.T) {
		c := &Config{
			Secrets: map[string]string{
				"GITHUB_TOKEN": "github-token",
			},
		}
		assert.Equal(t, "github-token", c.GetToken())
	})

	t.Run("returns empty string when no tokens present", func(t *testing.T) {
		c := &Config{
			Secrets: map[string]string{},
		}
		assert.Equal(t, "", c.GetToken())
	})

	t.Run("returns empty string when Secrets is nil", func(t *testing.T) {
		c := &Config{}
		assert.Equal(t, "", c.GetToken())
	})
}

// TestRemoteReusableWorkflow_CloneURL verifies URL formatting for different instances
func TestRemoteReusableWorkflow_CloneURL(t *testing.T) {
	t.Run("adds https prefix when missing", func(t *testing.T) {
		rw := &remoteReusableWorkflow{
			URL:  "code.forgejo.org",
			Org:  "owner",
			Repo: "repo",
		}
		assert.Equal(t, "https://code.forgejo.org/owner/repo", rw.CloneURL())
	})

	t.Run("preserves https prefix", func(t *testing.T) {
		rw := &remoteReusableWorkflow{
			URL:  "https://code.forgejo.org",
			Org:  "owner",
			Repo: "repo",
		}
		assert.Equal(t, "https://code.forgejo.org/owner/repo", rw.CloneURL())
	})

	t.Run("preserves http prefix", func(t *testing.T) {
		rw := &remoteReusableWorkflow{
			URL:  "http://localhost:3000",
			Org:  "owner",
			Repo: "repo",
		}
		assert.Equal(t, "http://localhost:3000/owner/repo", rw.CloneURL())
	})
}

// TestRemoteReusableWorkflow_FilePath verifies workflow file path construction
func TestRemoteReusableWorkflow_FilePath(t *testing.T) {
	tests := []struct {
		name         string
		gitPlatform  string
		filename     string
		expectedPath string
	}{
		{
			name:         "github platform",
			gitPlatform:  "github",
			filename:     "test.yml",
			expectedPath: "./.github/workflows/test.yml",
		},
		{
			name:         "gitea platform",
			gitPlatform:  "gitea",
			filename:     "build.yaml",
			expectedPath: "./.gitea/workflows/build.yaml",
		},
		{
			name:         "forgejo platform",
			gitPlatform:  "forgejo",
			filename:     "deploy.yml",
			expectedPath: "./.forgejo/workflows/deploy.yml",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rw := &remoteReusableWorkflow{
				GitPlatform: tt.gitPlatform,
				Filename:    tt.filename,
			}
			assert.Equal(t, tt.expectedPath, rw.FilePath())
		})
	}
}

// TestNewRemoteReusableWorkflowWithPlat verifies parsing of uses: syntax
func TestNewRemoteReusableWorkflowWithPlat(t *testing.T) {
	tests := []struct {
		name             string
		url              string
		uses             string
		expectedOrg      string
		expectedRepo     string
		expectedPlatform string
		expectedFilename string
		expectedRef      string
		shouldFail       bool
	}{
		{
			name:             "valid github workflow",
			url:              "github.com",
			uses:             "owner/repo/.github/workflows/test.yml@main",
			expectedOrg:      "owner",
			expectedRepo:     "repo",
			expectedPlatform: "github",
			expectedFilename: "test.yml",
			expectedRef:      "main",
			shouldFail:       false,
		},
		{
			name:             "valid gitea workflow",
			url:              "code.forgejo.org",
			uses:             "forgejo/runner/.gitea/workflows/build.yaml@v1.0.0",
			expectedOrg:      "forgejo",
			expectedRepo:     "runner",
			expectedPlatform: "gitea",
			expectedFilename: "build.yaml",
			expectedRef:      "v1.0.0",
			shouldFail:       false,
		},
		{
			name:       "invalid format - missing platform",
			url:        "github.com",
			uses:       "owner/repo/workflows/test.yml@main",
			shouldFail: true,
		},
		{
			name:       "invalid format - no ref",
			url:        "github.com",
			uses:       "owner/repo/.github/workflows/test.yml",
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := newRemoteReusableWorkflowWithPlat(tt.url, tt.uses)

			if tt.shouldFail {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tt.expectedOrg, result.Org)
				assert.Equal(t, tt.expectedRepo, result.Repo)
				assert.Equal(t, tt.expectedPlatform, result.GitPlatform)
				assert.Equal(t, tt.expectedFilename, result.Filename)
				assert.Equal(t, tt.expectedRef, result.Ref)
				assert.Equal(t, tt.url, result.URL)
			}
		})
	}
}
