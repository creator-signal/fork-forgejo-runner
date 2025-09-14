package runner

import (
	"context"
	"fmt"
	"time"

	"code.forgejo.org/forgejo/runner/v11/act/common"
	"code.forgejo.org/forgejo/runner/v11/act/container"
	"code.forgejo.org/forgejo/runner/v11/act/model"
	"github.com/sirupsen/logrus"
)

type jobInfo interface {
	matrix() map[string]any
	steps() []*model.Step
	startContainer() common.Executor
	stopContainer() common.Executor
	closeContainer() common.Executor
	interpolateOutputs() common.Executor
	result(result string)
}

const cleanupTimeout = 30 * time.Minute

func newJobExecutor(info jobInfo, sf stepFactory, rc *RunContext) common.Executor {
	steps := make([]common.Executor, 0)
	preSteps := make([]common.Executor, 0)
	var postExecutor common.Executor

	steps = append(steps, func(ctx context.Context) error {
		logger := common.Logger(ctx)
		if len(info.matrix()) > 0 {
			logger.Infof("\U0001F9EA  Matrix: %v", info.matrix())
		}
		return nil
	})

	infoSteps := info.steps()

	if len(infoSteps) == 0 {
		return common.NewDebugExecutor("No steps found")
	}

	preSteps = append(preSteps, func(ctx context.Context) error {
		// Have to be skipped for some Tests
		if rc.Run == nil {
			return nil
		}
		rc.ExprEval = rc.NewExpressionEvaluator(ctx)
		// evaluate environment variables since they can contain
		// GitHub's special environment variables.
		for k, v := range rc.GetEnv() {
			rc.Env[k] = rc.ExprEval.Interpolate(ctx, v)
		}
		return nil
	})

	for i, stepModel := range infoSteps {
		if stepModel == nil {
			return common.NewErrorExecutor(fmt.Errorf("invalid Step %v: missing run or uses key", i))
		} else if stepModel.Number != i {
			return common.NewErrorExecutor(fmt.Errorf("internal error: invalid Step: Number expected %v, was actually %v", i, stepModel.Number))
		}

		step, err := sf.newStep(stepModel, rc)
		if err != nil {
			return common.NewErrorExecutor(err)
		}

		preExec := step.pre()
		preSteps = append(preSteps, useStepLogger(rc, stepModel, stepStagePre, func(ctx context.Context) error {
			logger := common.Logger(ctx)
			preErr := preExec(ctx)
			if preErr != nil {
				logger.Errorf("%v", preErr)
				common.SetJobError(ctx, preErr)
			} else if ctx.Err() != nil {
				logger.Errorf("%v", ctx.Err())
				common.SetJobError(ctx, ctx.Err())
			}
			return preErr
		}))

		stepExec := step.main()
		steps = append(steps, useStepLogger(rc, stepModel, stepStageMain, func(ctx context.Context) error {
			logger := common.Logger(ctx)
			err := stepExec(ctx)
			if err != nil {
				logger.Errorf("%v", err)
				common.SetJobError(ctx, err)
			} else if ctx.Err() != nil {
				logger.Errorf("%v", ctx.Err())
				common.SetJobError(ctx, ctx.Err())
			}
			return nil
		}))

		postExec := useStepLogger(rc, stepModel, stepStagePost, step.post())
		if postExecutor != nil {
			// run the post executor in reverse order
			postExecutor = postExec.Finally(postExecutor)
		} else {
			postExecutor = postExec
		}
	}

	setJobResults := func(ctx context.Context) error {
		jobError := common.JobError(ctx)

		// Fresh context to ensure job result output works even if prev. context was a cancelled job
		ctx, cancel := context.WithTimeout(common.WithLogger(context.Background(), common.Logger(ctx)), time.Minute)
		defer cancel()
		setJobResult(ctx, info, rc, jobError == nil)

		return nil
	}

	cleanupJob := func(_ context.Context) error {
		var err error

		// Separate timeout for cleanup tasks; logger is cleared so that cleanup logs go to runner, not job
		ctx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		logger := common.Logger(ctx)
		logger.Debugf("Cleaning up container for job %s", rc.jobContainerName())
		if err = info.stopContainer()(ctx); err != nil {
			logger.Errorf("Error while stop job container %s: %v", rc.jobContainerName(), err)
		}

		if !rc.IsHostEnv(ctx) && rc.getNetworkCreated(ctx) {
			networkName := rc.getNetworkName(ctx)
			logger.Debugf("Cleaning up network %s for job %s", networkName, rc.jobContainerName())
			if err := container.NewDockerNetworkRemoveExecutor(networkName)(ctx); err != nil {
				logger.Errorf("Error while cleaning network %s: %v", networkName, err)
			}
		}

		return err
	}

	pipeline := make([]common.Executor, 0)
	pipeline = append(pipeline, preSteps...)
	pipeline = append(pipeline, steps...)

	return common.NewPipelineExecutor(info.startContainer(), common.NewPipelineExecutor(pipeline...).
		Finally(func(ctx context.Context) error { //nolint:contextcheck
			var cancel context.CancelFunc
			if ctx.Err() == context.Canceled {
				// in case of an aborted run, we still should execute the
				// post steps to allow cleanup.
				ctx, cancel = context.WithTimeout(common.WithLogger(context.Background(), common.Logger(ctx)), cleanupTimeout)
				defer cancel()
			}
			return postExecutor(ctx)
		}).
		Finally(info.interpolateOutputs()).
		Finally(setJobResults).
		Finally(cleanupJob).
		Finally(info.closeContainer()))
}

func setJobResult(ctx context.Context, info jobInfo, rc *RunContext, success bool) {
	logger := common.Logger(ctx)

	// As we're reading the matrix build's status (`rc.Run.Job().Result`), it's possible for it change in another
	// goroutine running `setJobResult` and invoking `.result(...)`. Prevent concurrent execution of `setJobResult`...
	rc.Run.Job().ResultMutex.Lock()
	defer rc.Run.Job().ResultMutex.Unlock()

	jobResult := "success"
	// we have only one result for a whole matrix build, so we need
	// to keep an existing result state if we run a matrix
	if len(info.matrix()) > 0 && rc.Run.Job().Result != "" {
		jobResult = rc.Run.Job().Result
	}

	if !success {
		jobResult = "failure"
	}

	info.result(jobResult)
	if rc.caller != nil {
		// set reusable workflow job result
		rc.caller.runContext.result(jobResult)
	}

	jobResultMessage := "succeeded"
	if jobResult != "success" {
		jobResultMessage = "failed"
	}

	jobOutputs := rc.Run.Job().Outputs
	if rc.caller != nil {
		// Rewrite the job's outputs into the workflow_call outputs...
		jobOutputs = make(map[string]string)
		ee := rc.NewExpressionEvaluator(ctx)
		for k, v := range rc.Run.Workflow.WorkflowCallConfig().Outputs {
			jobOutputs[k] = ee.Interpolate(ctx, ee.Interpolate(ctx, v.Value))
		}
		// When running as a daemon and receiving jobs from Forgejo, the next job (and any of it's `needs` outputs) will
		// be provided by Forgejo based upon the data sent to the logger below.  However, when running `forgejo-runner
		// exec` with a reusable workflow, the next job will only be able to read outputs if those outputs are stored on
		// the workflow -- that's what is accomplished here:
		rc.caller.runContext.Run.Job().Outputs = jobOutputs
	}

	logger.
		WithFields(logrus.Fields{
			"jobResult":  jobResult,
			"jobOutputs": jobOutputs,
		}).
		Infof("\U0001F3C1  Job %s", jobResultMessage)
}

func useStepLogger(rc *RunContext, stepModel *model.Step, stage stepStage, executor common.Executor) common.Executor {
	return func(ctx context.Context) error {
		ctx = withStepLogger(ctx, stepModel.Number, stepModel.ID, rc.ExprEval.Interpolate(ctx, stepModel.String()), stage.String())

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
