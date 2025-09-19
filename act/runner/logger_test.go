package runner

import (
	"testing"

	"code.forgejo.org/forgejo/runner/v11/act/common"

	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunner_GetOuterStepResult(t *testing.T) {
	nullLogger, hook := test.NewNullLogger()
	ctx := common.WithLogger(t.Context(), nullLogger)

	t.Run("no stepResult", func(t *testing.T) {
		hook.Reset()
		common.Logger(ctx).Info("✅ Success")
		entry := hook.LastEntry()
		require.NotNil(t, entry)
		assert.Nil(t, GetOuterStepResult(entry))
	})

	t.Run("stepResult and no stepID", func(t *testing.T) {
		hook.Reset()
		common.Logger(ctx).WithField("stepResult", "success").Info("✅ Success")
		entry := hook.LastEntry()
		require.NotNil(t, entry)
		assert.Nil(t, GetOuterStepResult(entry))
	})

	stepNumber := 123
	stepID := "step id"
	stepName := "readable name"
	stageName := "Main"
	ctx = withStepLogger(ctx, stepNumber, stepID, stepName, stageName)

	t.Run("stepResult and stepID", func(t *testing.T) {
		hook.Reset()
		common.Logger(ctx).WithField("stepResult", "success").Info("✅ Success")
		entry := hook.LastEntry()
		actualStepIDs, ok := entry.Data["stepID"]
		require.True(t, ok)
		require.Equal(t, []string{stepID}, actualStepIDs)
		require.NotNil(t, entry)
		assert.Equal(t, "success", GetOuterStepResult(entry))
	})

	compositeStepID := "composite step id"
	ctx = WithCompositeStepLogger(ctx, compositeStepID)

	t.Run("stepResult and composite stepID", func(t *testing.T) {
		hook.Reset()
		common.Logger(ctx).WithField("stepResult", "success").Info("✅ Success")
		entry := hook.LastEntry()
		actualStepIDs, ok := entry.Data["stepID"]
		require.True(t, ok)
		require.Equal(t, []string{stepID, compositeStepID}, actualStepIDs)
		require.NotNil(t, entry)
		assert.Nil(t, GetOuterStepResult(entry))
	})
}
