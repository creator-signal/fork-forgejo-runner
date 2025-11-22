package runner

import (
	"context"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/model"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	yaml "go.yaml.in/yaml/v3"
)

func TestStep_MergeIntoMap(t *testing.T) {
	table := []struct {
		name     string
		target   map[string]string
		maps     []map[string]string
		expected map[string]string
	}{
		{
			name:     "testEmptyMap",
			target:   map[string]string{},
			maps:     []map[string]string{},
			expected: map[string]string{},
		},
		{
			name:   "testMergeIntoEmptyMap",
			target: map[string]string{},
			maps: []map[string]string{
				{
					"key1": "value1",
					"key2": "value2",
				}, {
					"key2": "overridden",
					"key3": "value3",
				},
			},
			expected: map[string]string{
				"key1": "value1",
				"key2": "overridden",
				"key3": "value3",
			},
		},
		{
			name: "testMergeIntoExistingMap",
			target: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			maps: []map[string]string{
				{
					"key1": "overridden",
				},
			},
			expected: map[string]string{
				"key1": "overridden",
				"key2": "value2",
			},
		},
	}

	for _, tt := range table {
		t.Run(tt.name, func(t *testing.T) {
			mergeIntoMapCaseSensitive(tt.target, tt.maps...)
			assert.Equal(t, tt.expected, tt.target)
			mergeIntoMapCaseInsensitive(tt.target, tt.maps...)
			assert.Equal(t, tt.expected, tt.target)
		})
	}
}

type stepMock struct {
	mock.Mock
	step
}

func (sm *stepMock) pre() common.Executor {
	args := sm.Called()
	return args.Get(0).(func(context.Context) error)
}

func (sm *stepMock) main() common.Executor {
	args := sm.Called()
	return args.Get(0).(func(context.Context) error)
}

func (sm *stepMock) post() common.Executor {
	args := sm.Called()
	return args.Get(0).(func(context.Context) error)
}

func (sm *stepMock) getRunContext() *RunContext {
	args := sm.Called()
	return args.Get(0).(*RunContext)
}

func (sm *stepMock) getGithubContext(ctx context.Context) *model.GithubContext {
	args := sm.Called()
	return args.Get(0).(*RunContext).getGithubContext(ctx)
}

func (sm *stepMock) getStepModel() *model.Step {
	args := sm.Called()
	return args.Get(0).(*model.Step)
}

func (sm *stepMock) getEnv() *map[string]string {
	args := sm.Called()
	return args.Get(0).(*map[string]string)
}

func TestStep_IsStepEnabled(t *testing.T) {
	createTestStep := func(t *testing.T, input string) step {
		var step *model.Step
		err := yaml.Unmarshal([]byte(input), &step)
		assert.NoError(t, err)

		return &stepRun{
			RunContext: &RunContext{
				Config: &Config{
					Workdir: ".",
					Platforms: map[string]string{
						"ubuntu-latest": "ubuntu-latest",
					},
				},
				StepResults: map[string]*model.StepResult{},
				Env:         map[string]string{},
				Run: &model.Run{
					JobID: "job1",
					Workflow: &model.Workflow{
						Name: "workflow1",
						Jobs: map[string]*model.Job{
							"job1": createJob(t, `runs-on: ubuntu-latest`, ""),
						},
					},
				},
			},
			Step: step,
		}
	}

	log.SetLevel(log.DebugLevel)
	assertObject := assert.New(t)

	// success()
	step := createTestStep(t, "if: success()")
	assertObject.True(isStepEnabled(t.Context(), step.getIfExpression(t.Context(), stepStageMain), step, stepStageMain))

	step = createTestStep(t, "if: success()")
	step.getRunContext().StepResults["a"] = &model.StepResult{
		Conclusion: model.StepStatusSuccess,
	}
	assertObject.True(isStepEnabled(t.Context(), step.getStepModel().If.Value, step, stepStageMain))

	step = createTestStep(t, "if: success()")
	step.getRunContext().StepResults["a"] = &model.StepResult{
		Conclusion: model.StepStatusFailure,
	}
	assertObject.False(isStepEnabled(t.Context(), step.getStepModel().If.Value, step, stepStageMain))

	// failure()
	step = createTestStep(t, "if: failure()")
	assertObject.False(isStepEnabled(t.Context(), step.getStepModel().If.Value, step, stepStageMain))

	step = createTestStep(t, "if: failure()")
	step.getRunContext().StepResults["a"] = &model.StepResult{
		Conclusion: model.StepStatusSuccess,
	}
	assertObject.False(isStepEnabled(t.Context(), step.getStepModel().If.Value, step, stepStageMain))

	step = createTestStep(t, "if: failure()")
	step.getRunContext().StepResults["a"] = &model.StepResult{
		Conclusion: model.StepStatusFailure,
	}
	assertObject.True(isStepEnabled(t.Context(), step.getStepModel().If.Value, step, stepStageMain))

	// always()
	step = createTestStep(t, "if: always()")
	assertObject.True(isStepEnabled(t.Context(), step.getStepModel().If.Value, step, stepStageMain))

	step = createTestStep(t, "if: always()")
	step.getRunContext().StepResults["a"] = &model.StepResult{
		Conclusion: model.StepStatusSuccess,
	}
	assertObject.True(isStepEnabled(t.Context(), step.getStepModel().If.Value, step, stepStageMain))

	step = createTestStep(t, "if: always()")
	step.getRunContext().StepResults["a"] = &model.StepResult{
		Conclusion: model.StepStatusFailure,
	}
	assertObject.True(isStepEnabled(t.Context(), step.getStepModel().If.Value, step, stepStageMain))
}

func TestStep_IsContinueOnError(t *testing.T) {
	createTestStep := func(t *testing.T, input string) step {
		var step *model.Step
		err := yaml.Unmarshal([]byte(input), &step)
		assert.NoError(t, err)

		return &stepRun{
			RunContext: &RunContext{
				Config: &Config{
					Workdir: ".",
					Platforms: map[string]string{
						"ubuntu-latest": "ubuntu-latest",
					},
				},
				StepResults: map[string]*model.StepResult{},
				Env:         map[string]string{},
				Run: &model.Run{
					JobID: "job1",
					Workflow: &model.Workflow{
						Name: "workflow1",
						Jobs: map[string]*model.Job{
							"job1": createJob(t, `runs-on: ubuntu-latest`, ""),
						},
					},
				},
			},
			Step: step,
		}
	}

	log.SetLevel(log.DebugLevel)
	assertObject := assert.New(t)

	// absent
	step := createTestStep(t, "name: test")
	continueOnError, err := isContinueOnError(t.Context(), step.getStepModel().RawContinueOnError, step, stepStageMain)
	assertObject.False(continueOnError)
	assertObject.Nil(err)

	// explicit true
	step = createTestStep(t, "continue-on-error: true")
	continueOnError, err = isContinueOnError(t.Context(), step.getStepModel().RawContinueOnError, step, stepStageMain)
	assertObject.True(continueOnError)
	assertObject.Nil(err)

	// explicit false
	step = createTestStep(t, "continue-on-error: false")
	continueOnError, err = isContinueOnError(t.Context(), step.getStepModel().RawContinueOnError, step, stepStageMain)
	assertObject.False(continueOnError)
	assertObject.Nil(err)

	// expression true
	step = createTestStep(t, "continue-on-error: ${{ 'test' == 'test' }}")
	continueOnError, err = isContinueOnError(t.Context(), step.getStepModel().RawContinueOnError, step, stepStageMain)
	assertObject.True(continueOnError)
	assertObject.Nil(err)

	// expression false
	step = createTestStep(t, "continue-on-error: ${{ 'test' != 'test' }}")
	continueOnError, err = isContinueOnError(t.Context(), step.getStepModel().RawContinueOnError, step, stepStageMain)
	assertObject.False(continueOnError)
	assertObject.Nil(err)

	// expression parse error
	step = createTestStep(t, "continue-on-error: ${{ 'test' != test }}")
	continueOnError, err = isContinueOnError(t.Context(), step.getStepModel().RawContinueOnError, step, stepStageMain)
	assertObject.False(continueOnError)
	assertObject.NotNil(err)
}
