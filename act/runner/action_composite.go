package runner

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/model"
)

func evaluateCompositeInputAndEnv(ctx context.Context, parent *RunContext, step actionStep) (map[string]string, error) {
	env := make(map[string]string)
	stepEnv := *step.getEnv()
	for k, v := range stepEnv {
		// do not set current inputs into composite action
		// the required inputs are added in the second loop
		if !strings.HasPrefix(k, "INPUT_") {
			env[k] = v
		}
	}

	ee, err := parent.NewStepExpressionEvaluator(ctx, step)
	if err != nil {
		return map[string]string{}, fmt.Errorf("could not create new StepExpressionEvaluator: %w", err)
	}

	for inputID, input := range step.getActionModel().Inputs {
		envKey := regexp.MustCompile("[^A-Z0-9-]").ReplaceAllString(strings.ToUpper(inputID), "_")
		envKey = fmt.Sprintf("INPUT_%s", strings.ToUpper(envKey))

		// lookup if key is defined in the step but the already
		// evaluated value from the environment
		_, defined := step.getStepModel().With[inputID]
		if value, ok := stepEnv[envKey]; defined && ok {
			env[envKey] = value
		} else {
			// defaults could contain expressions
			var err error
			if env[envKey], err = ee.Interpolate(ctx, input.Default); err != nil {
				return make(map[string]string), fmt.Errorf("unable to interpolate default value: %w", err)
			}
		}
	}
	gh := step.getGithubContext(ctx)
	env["GITHUB_ACTION_REPOSITORY"] = gh.ActionRepository
	env["GITHUB_ACTION_REF"] = gh.ActionRef

	return env, nil
}

func newCompositeRunContext(ctx context.Context, parent *RunContext, step actionStep, actionPath string) (*RunContext, error) {
	env, err := evaluateCompositeInputAndEnv(ctx, parent, step)
	if err != nil {
		return nil, err
	}

	// run with the global config but without secrets
	configCopy := *parent.Config
	configCopy.Secrets = nil

	// create a run context for the composite action to run in
	compositerc := &RunContext{
		Name:    parent.Name,
		JobName: parent.JobName,
		Run: &model.Run{
			JobID: parent.Run.JobID,
			Workflow: &model.Workflow{
				Name: parent.Run.Workflow.Name,
				Jobs: map[string]*model.Job{
					parent.Run.JobID: {},
				},
			},
		},
		Config:       &configCopy,
		StepResults:  map[string]*model.StepResult{},
		JobContainer: parent.JobContainer,
		ActionPath:   actionPath,
		Env:          env,
		GlobalEnv:    parent.GlobalEnv,
		Masks:        parent.Masks,
		ExtraPath:    parent.ExtraPath,
		Parent:       parent,
		EventJSON:    parent.EventJSON,
	}
	if compositerc.ExprEval, err = compositerc.NewExpressionEvaluator(ctx); err != nil {
		return nil, fmt.Errorf("could not create a new NewExpressionEvaluator: %w", err)
	}

	return compositerc, nil
}

func execAsComposite(step actionStep) common.Executor {
	rc := step.getRunContext()
	action := step.getActionModel()

	return func(ctx context.Context) error {
		compositeRC, err := step.getCompositeRunContext(ctx)
		if err != nil {
			return fmt.Errorf("unable to generate composite run context: %w", err)
		}

		steps := step.getCompositeSteps()

		if steps == nil || steps.main == nil {
			return fmt.Errorf("missing steps in composite action")
		}

		ctx = WithCompositeLogger(ctx, &compositeRC.Masks)

		// It is expected that outputs, even in case of an execution error, are propagated.
		err = steps.main(ctx)

		// Map outputs from composite RunContext to job RunContext
		eval, err2 := compositeRC.NewExpressionEvaluator(ctx)
		if err2 != nil && err == nil {
			return fmt.Errorf("could not create new ExpressionEvaluator: %w", err)
		}
		for outputName, output := range action.Outputs {
			interpolatedOutput, err2 := eval.Interpolate(ctx, output.Value)
			if err2 != nil && err == nil {
				err = fmt.Errorf("unable to interpolate output %q: %w", outputName, err2)
			}
			rc.setOutput(ctx, map[string]string{"name": outputName}, interpolatedOutput)
		}

		rc.Masks = append(rc.Masks, compositeRC.Masks...)
		rc.ExtraPath = compositeRC.ExtraPath
		// compositeRC.Env is dirty, contains INPUT_ and merged step env, only rely on compositeRC.GlobalEnv
		mergeIntoMap := mergeIntoMapCaseSensitive
		if rc.JobContainer.IsEnvironmentCaseInsensitive() {
			mergeIntoMap = mergeIntoMapCaseInsensitive
		}
		if rc.GlobalEnv == nil {
			rc.GlobalEnv = map[string]string{}
		}
		mergeIntoMap(rc.GlobalEnv, compositeRC.GlobalEnv)
		mergeIntoMap(rc.Env, compositeRC.GlobalEnv)

		return err
	}
}

type compositeSteps struct {
	pre  common.Executor
	main common.Executor
	post common.Executor
}

// Executor returns a pipeline executor for all the steps in the job
func (rc *RunContext) compositeExecutor(action *model.Action) *compositeSteps {
	steps := make([]common.Executor, 0)
	preSteps := make([]common.Executor, 0)
	var postExecutor common.Executor

	sf := &stepFactoryImpl{}

	for i, stepModel := range action.Runs.Steps {
		if stepModel.Number != i {
			return &compositeSteps{
				pre:  func(ctx context.Context) error { return nil },
				main: common.NewErrorExecutor(fmt.Errorf("internal error: invalid Step: Number expected %v, was actually %v", i, stepModel.Number)),
				post: func(ctx context.Context) error { return nil },
			}
		}

		step, err := sf.newStep(&stepModel, rc)
		if err != nil {
			return &compositeSteps{
				main: common.NewErrorExecutor(err),
			}
		}

		stepID := step.getStepModel().ID
		stepPre := rc.newCompositeCommandExecutor(step.pre())
		preSteps = append(preSteps, newCompositeStepLogExecutor(stepPre, stepID))

		steps = append(steps, func(ctx context.Context) error {
			ctx = WithCompositeStepLogger(ctx, stepID)
			logger := common.Logger(ctx)
			err := rc.newCompositeCommandExecutor(step.main())(ctx)

			if err != nil {
				logger.Errorf("%v", err)
				common.SetJobError(ctx, err)
			} else if ctx.Err() != nil {
				logger.Errorf("%v", ctx.Err())
				common.SetJobError(ctx, ctx.Err())
			}
			return nil
		})

		// run the post executor in reverse order
		if postExecutor != nil {
			stepPost := rc.newCompositeCommandExecutor(step.post())
			postExecutor = newCompositeStepLogExecutor(stepPost.Finally(postExecutor), stepID)
		} else {
			stepPost := rc.newCompositeCommandExecutor(step.post())
			postExecutor = newCompositeStepLogExecutor(stepPost, stepID)
		}
	}

	steps = append(steps, common.JobError)
	return &compositeSteps{
		pre: func(ctx context.Context) error {
			return common.NewPipelineExecutor(preSteps...)(common.WithJobErrorContainer(ctx))
		},
		main: func(ctx context.Context) error {
			return common.NewPipelineExecutor(steps...)(common.WithJobErrorContainer(ctx))
		},
		post: postExecutor,
	}
}

func (rc *RunContext) newCompositeCommandExecutor(executor common.Executor) common.Executor {
	return func(ctx context.Context) error {
		ctx = WithCompositeLogger(ctx, &rc.Masks)

		// We need to inject a composite RunContext related command
		// handler into the current running job container
		// We need this, to support scoping commands to the composite action
		// executing.
		rawLogger := common.Logger(ctx).WithField("raw_output", true)
		logWriter := common.NewLineWriter(rc.commandHandler(ctx), func(s string) bool {
			if rc.Config.LogOutput {
				rawLogger.Infof("%s", s)
			} else {
				rawLogger.Debugf("%s", s)
			}
			return true
		})

		oldout, olderr := rc.JobContainer.ReplaceLogWriter(logWriter, logWriter)
		defer rc.JobContainer.ReplaceLogWriter(oldout, olderr)

		return executor(ctx)
	}
}

func newCompositeStepLogExecutor(runStep common.Executor, stepID string) common.Executor {
	return func(ctx context.Context) error {
		ctx = WithCompositeStepLogger(ctx, stepID)
		logger := common.Logger(ctx)
		err := runStep(ctx)
		if err != nil {
			logger.Errorf("%v", err)
			common.SetJobError(ctx, err)
		} else if ctx.Err() != nil {
			logger.Errorf("%v", ctx.Err())
			common.SetJobError(ctx, ctx.Err())
		}
		return nil
	}
}
