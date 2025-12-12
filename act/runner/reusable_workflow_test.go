package runner

import (
	"errors"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/model"
	"code.forgejo.org/forgejo/runner/v12/act/runner/mocks"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestConfig_GetToken(t *testing.T) {
	t.Run("returns GITEA_TOKEN when both GITEA_TOKEN and GITHUB_TOKEN present", func(t *testing.T) {
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

func TestFinalizeReusableWorkflow_PrintsBannerSuccess(t *testing.T) {
	mockLogger := mocks.NewFieldLogger(t)

	bannerCalled := false
	mockLogger.On("WithFields",
		mock.MatchedBy(func(fields logrus.Fields) bool {
			result, ok := fields["jobResult"].(string)
			if !ok || result != "success" {
				return false
			}
			outs, ok := fields["jobOutputs"].(map[string]string)
			return ok && outs["foo"] == "bar"
		}),
	).Run(func(args mock.Arguments) {
		bannerCalled = true
	}).Return(&logrus.Entry{Logger: &logrus.Logger{}}).Once()

	ctx := common.WithLogger(t.Context(), mockLogger)
	rc := &RunContext{
		Run: &model.Run{
			JobID: "parent",
			Workflow: &model.Workflow{
				Jobs: map[string]*model.Job{
					"parent": {
						Outputs: map[string]string{"foo": "bar"},
					},
				},
			},
		},
	}

	err := finalizeReusableWorkflow(ctx, rc, nil)
	assert.NoError(t, err)
	assert.True(t, bannerCalled, "final banner should be printed from parent")
}

func TestFinalizeReusableWorkflow_PrintsBannerFailure(t *testing.T) {
	mockLogger := mocks.NewFieldLogger(t)

	bannerCalled := false
	mockLogger.On("WithFields",
		mock.MatchedBy(func(fields logrus.Fields) bool {
			result, ok := fields["jobResult"].(string)
			return ok && result == "failure"
		}),
	).Run(func(args mock.Arguments) {
		bannerCalled = true
	}).Return(&logrus.Entry{Logger: &logrus.Logger{}}).Once()

	ctx := common.WithLogger(t.Context(), mockLogger)
	rc := &RunContext{
		Run: &model.Run{
			JobID: "parent",
			Workflow: &model.Workflow{
				Jobs: map[string]*model.Job{
					"parent": {},
				},
			},
		},
	}

	planErr := errors.New("workflow failed")
	err := finalizeReusableWorkflow(ctx, rc, planErr)
	assert.EqualError(t, err, "workflow failed")
	assert.True(t, bannerCalled, "banner should be printed even on failure")
}
