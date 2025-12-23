package model

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseRemoteReusableWorkflow(t *testing.T) {
	tests := []struct {
		name             string
		url              string
		uses             string
		expectedBaseURL  string
		expectedOrg      string
		expectedRepo     string
		expectedPlatform string
		expectedFilename string
		expectedRef      string
		shouldFail       bool
	}{
		{
			name:             "valid non-qualified workflow",
			uses:             "owner/repo/.github/workflows/test.yml@main",
			expectedOrg:      "owner",
			expectedRepo:     "repo",
			expectedPlatform: "github",
			expectedFilename: "test.yml",
			expectedRef:      "main",
			shouldFail:       false,
		},
		{
			name:             "valid qualified workflow",
			uses:             "https://example.com/forgejo/runner/.gitea/workflows/build.yaml@v1.0.0",
			expectedBaseURL:  "https://example.com",
			expectedOrg:      "forgejo",
			expectedRepo:     "runner",
			expectedPlatform: "gitea",
			expectedFilename: "build.yaml",
			expectedRef:      "v1.0.0",
			shouldFail:       false,
		},
		{
			name:             "valid qualified non-https",
			uses:             "http://example.com/forgejo/runner/.gitea/workflows/build.yaml@v1.0.0",
			expectedBaseURL:  "http://example.com",
			expectedOrg:      "forgejo",
			expectedRepo:     "runner",
			expectedPlatform: "gitea",
			expectedFilename: "build.yaml",
			expectedRef:      "v1.0.0",
			shouldFail:       false,
		},
		{
			name:       "invalid format - missing platform",
			uses:       "owner/repo/workflows/test.yml@main",
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ParseRemoteReusableWorkflow(tt.uses)
			if tt.shouldFail {
				require.Error(t, err)
				require.Nil(t, result)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, result)
				external, ok := result.(*ExternalReusableWorkflowReference)
				if tt.expectedBaseURL != "" {
					require.True(t, ok)
					assert.Equal(t, tt.expectedBaseURL, external.BaseURL)
				} else {
					assert.False(t, ok)
				}
				assert.Equal(t, tt.expectedOrg, result.Reference().Org)
				assert.Equal(t, tt.expectedRepo, result.Reference().Repo)
				assert.Equal(t, tt.expectedPlatform, result.Reference().GitPlatform)
				assert.Equal(t, tt.expectedFilename, result.Reference().Filename)
				assert.Equal(t, tt.expectedRef, result.Reference().Ref)
			}
		})
	}
}

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
			uses:       "owner/repo/workflows/test.yml@main",
			shouldFail: true,
		},
		{
			name:       "invalid format - no ref",
			uses:       "owner/repo/.github/workflows/test.yml",
			shouldFail: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := parseReusableWorkflowReference(tt.uses)

			if tt.shouldFail {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
				assert.Equal(t, tt.expectedOrg, result.Org)
				assert.Equal(t, tt.expectedRepo, result.Repo)
				assert.Equal(t, tt.expectedPlatform, result.GitPlatform)
				assert.Equal(t, tt.expectedFilename, result.Filename)
				assert.Equal(t, tt.expectedRef, result.Ref)
			}
		})
	}
}

func TestRemoteReusableWorkflow_CloneURL(t *testing.T) {
	rw := &ExternalReusableWorkflowReference{
		NonLocalReusableWorkflowReference: NonLocalReusableWorkflowReference{
			Org:  "owner",
			Repo: "repo",
		},
		BaseURL: "https://code.forgejo.org",
	}
	assert.Equal(t, "https://code.forgejo.org/owner/repo", rw.CloneURL())
}

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
			rw := &NonLocalReusableWorkflowReference{
				GitPlatform: tt.gitPlatform,
				Filename:    tt.filename,
			}
			assert.Equal(t, tt.expectedPath, rw.FilePath())
		})
	}
}
