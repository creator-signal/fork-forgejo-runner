package runner

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"path"
	"strconv"
	"strings"
	"time"

	"code.forgejo.org/forgejo/runner/v11/act/common"
	"code.forgejo.org/forgejo/runner/v11/act/container"
	"code.forgejo.org/forgejo/runner/v11/act/exprparser"
	"code.forgejo.org/forgejo/runner/v11/act/model"

	"github.com/sirupsen/logrus"
)

type step interface {
	pre() common.Executor
	main() common.Executor
	post() common.Executor

	getRunContext() *RunContext
	getGithubContext(ctx context.Context) *model.GithubContext
	getStepModel() *model.Step
	getEnv() *map[string]string
	getIfExpression(context context.Context, stage stepStage) string
}

type stepStage int

const (
	stepStagePre stepStage = iota
	stepStageMain
	stepStagePost
)

// Controls how many symlinks are resolved for local and remote Actions
const maxSymlinkDepth = 10

func (s stepStage) String() string {
	switch s {
	case stepStagePre:
		return "Pre"
	case stepStageMain:
		return "Main"
	case stepStagePost:
		return "Post"
	}
	return "Unknown"
}

func processRunnerSummaryCommand(ctx context.Context, fileName string, rc *RunContext) error {
	if common.Dryrun(ctx) {
		return nil
	}
	pathTar, err := rc.JobContainer.GetContainerArchive(ctx, path.Join(rc.JobContainer.GetActPath(), fileName))
	if err != nil {
		return err
	}
	defer pathTar.Close()

	reader := tar.NewReader(pathTar)
	_, err = reader.Next()
	if err != nil && err != io.EOF {
		return err
	}
	summary, err := io.ReadAll(reader)
	if err != nil {
		return err
	}
	if len(summary) == 0 {
		return nil
	}
	common.Logger(ctx).WithFields(logrus.Fields{"command": "summary", "content": string(summary)}).Infof("  \U00002699  Summary - %s", string(summary))
	return nil
}

func processRunnerEnvFileCommand(ctx context.Context, fileName string, rc *RunContext, setter func(context.Context, map[string]string, string)) error {
	env := map[string]string{}
	err := rc.JobContainer.UpdateFromEnv(path.Join(rc.JobContainer.GetActPath(), fileName), &env)(ctx)
	if err != nil {
		return err
	}
	for k, v := range env {
		setter(ctx, map[string]string{"name": k}, v)
	}
	return nil
}

func runStepExecutor(step step, stage stepStage, executor common.Executor) common.Executor {
	return func(ctx context.Context) error {
		logger := common.Logger(ctx)
		rc := step.getRunContext()
		stepModel := step.getStepModel()

		ifExpression := step.getIfExpression(ctx, stage)
		rc.CurrentStep = stepModel.ID

		stepResult := &model.StepResult{
			Outcome:    model.StepStatusSuccess,
			Conclusion: model.StepStatusSuccess,
			Outputs:    make(map[string]string),
		}
		if stage == stepStageMain {
			rc.StepResults[rc.CurrentStep] = stepResult
		}

		err := setupEnv(ctx, step)
		if err != nil {
			return err
		}

		runStep, err := isStepEnabled(ctx, ifExpression, step, stage)
		if err != nil {
			stepResult.Conclusion = model.StepStatusFailure
			stepResult.Outcome = model.StepStatusFailure
			return err
		}

		if !runStep {
			stepResult.Conclusion = model.StepStatusSkipped
			stepResult.Outcome = model.StepStatusSkipped
			logger.WithField("stepResult", stepResult.Outcome).Infof("Skipping step '%s' due to '%s'", stepModel, ifExpression)
			return nil
		}

		stepString := rc.ExprEval.Interpolate(ctx, stepModel.String())
		if strings.Contains(stepString, "::add-mask::") {
			stepString = "add-mask command"
		}
		logger.Infof("\u2B50 Run %s %s", stage, stepString)

		// Prepare and clean Runner File Commands
		actPath := rc.JobContainer.GetActPath()

		set := func(k, v string) {
			for _, prefix := range []string{"FORGEJO", "GITHUB"} {
				(*step.getEnv())[prefix+"_"+k] = v
			}
		}

		outputFileCommand := path.Join("workflow", "outputcmd.txt")
		set("OUTPUT", path.Join(actPath, outputFileCommand))

		stateFileCommand := path.Join("workflow", "statecmd.txt")
		set("STATE", path.Join(actPath, stateFileCommand))

		pathFileCommand := path.Join("workflow", "pathcmd.txt")
		set("PATH", path.Join(actPath, pathFileCommand))

		envFileCommand := path.Join("workflow", "envs.txt")
		set("ENV", path.Join(actPath, envFileCommand))

		summaryFileCommand := path.Join("workflow", "SUMMARY.md")
		set("STEP_SUMMARY", path.Join(actPath, summaryFileCommand))

		_ = rc.JobContainer.Copy(actPath, &container.FileEntry{
			Name: outputFileCommand,
			Mode: 0o666,
		}, &container.FileEntry{
			Name: stateFileCommand,
			Mode: 0o666,
		}, &container.FileEntry{
			Name: pathFileCommand,
			Mode: 0o666,
		}, &container.FileEntry{
			Name: envFileCommand,
			Mode: 0o666,
		}, &container.FileEntry{
			Name: summaryFileCommand,
			Mode: 0o666,
		})(ctx)

		timeoutctx, cancelTimeOut := evaluateTimeout(ctx, "step", rc.ExprEval, stepModel.TimeoutMinutes)
		defer cancelTimeOut()
		err = executor(timeoutctx)

		if err == nil {
			logger.WithField("stepResult", stepResult.Outcome).Infof("  \u2705  Success - %s %s", stage, stepString)
		} else {
			stepResult.Outcome = model.StepStatusFailure

			continueOnError, parseErr := isContinueOnError(ctx, stepModel.RawContinueOnError, step, stage)
			if parseErr != nil {
				stepResult.Conclusion = model.StepStatusFailure
				logger.WithField("raw_output", true).Errorf("%s Failed to evaluate continue-on-error while handling original error: %v", runnerLogPrefix, err)
				return fmt.Errorf("failed to evaluate continue-on-error: %v", parseErr)
			}

			if continueOnError {
				logger.WithField("raw_output", true).Infof("%s Failed to execute step (but continue-on-error is true): %v", runnerLogPrefix, err)
				err = nil
				stepResult.Conclusion = model.StepStatusSuccess
			} else {
				stepResult.Conclusion = model.StepStatusFailure
			}

			logger.WithField("stepResult", stepResult.Outcome).Errorf("  \u274C  Failure - %s %s", stage, stepString)
		}
		// Process Runner File Commands
		ferrors := []error{err}
		ferrors = append(ferrors, processRunnerEnvFileCommand(ctx, envFileCommand, rc, rc.setEnv))
		ferrors = append(ferrors, processRunnerEnvFileCommand(ctx, stateFileCommand, rc, rc.saveState))
		ferrors = append(ferrors, processRunnerEnvFileCommand(ctx, outputFileCommand, rc, rc.setOutput))
		ferrors = append(ferrors, processRunnerSummaryCommand(ctx, summaryFileCommand, rc))
		ferrors = append(ferrors, rc.UpdateExtraPath(ctx, path.Join(actPath, pathFileCommand)))
		return errors.Join(ferrors...)
	}
}

func evaluateTimeout(ctx context.Context, contextType string, exprEval ExpressionEvaluator, timeoutMinutes string) (context.Context, context.CancelFunc) {
	timeout := exprEval.Interpolate(ctx, timeoutMinutes)
	if timeout != "" {
		timeOutMinutes, err := strconv.ParseInt(timeout, 10, 64)
		if err == nil {
			common.Logger(ctx).Debugf("the %s will stop in timeout-minutes %s", contextType, timeout)
			return context.WithTimeout(ctx, time.Duration(timeOutMinutes)*time.Minute)
		}
		common.Logger(ctx).Errorf("timeout-minutes %s cannot be parsed and will be ignored: %w", timeout, err)
	}
	return ctx, func() {}
}

func setupEnv(ctx context.Context, step step) error {
	rc := step.getRunContext()

	mergeEnv(ctx, step)
	// merge step env last, since it should not be overwritten
	mergeIntoMap(step, step.getEnv(), step.getStepModel().GetEnv())

	exprEval := rc.NewExpressionEvaluator(ctx)
	for k, v := range *step.getEnv() {
		if !strings.HasPrefix(k, "INPUT_") {
			(*step.getEnv())[k] = exprEval.Interpolate(ctx, v)
		}
	}
	// after we have an evaluated step context, update the expressions evaluator with a new env context
	// you can use step level env in the with property of a uses construct
	exprEval = rc.NewExpressionEvaluatorWithEnv(ctx, *step.getEnv())
	for k, v := range *step.getEnv() {
		if strings.HasPrefix(k, "INPUT_") {
			(*step.getEnv())[k] = exprEval.Interpolate(ctx, v)
		}
	}

	common.Logger(ctx).Debugf("setupEnv => %v", *step.getEnv())

	return nil
}

func mergeEnv(ctx context.Context, step step) {
	env := step.getEnv()
	rc := step.getRunContext()
	job := rc.Run.Job()

	c := job.Container()
	if c != nil {
		mergeIntoMap(step, env, rc.GetEnv(), c.Env)
	} else {
		mergeIntoMap(step, env, rc.GetEnv())
	}

	rc.withGithubEnv(ctx, step.getGithubContext(ctx), *env)

	if step.getStepModel().Uses != "" {
		// prevent uses action input pollution of unset parameters, skip this for run steps
		// due to design flaw
		for key := range *env {
			if strings.HasPrefix(key, "INPUT_") {
				delete(*env, key)
			}
		}
	}
}

func isStepEnabled(ctx context.Context, expr string, step step, stage stepStage) (bool, error) {
	rc := step.getRunContext()

	var defaultStatusCheck exprparser.DefaultStatusCheck
	if stage == stepStagePost {
		defaultStatusCheck = exprparser.DefaultStatusCheckAlways
	} else {
		defaultStatusCheck = exprparser.DefaultStatusCheckSuccess
	}

	runStep, err := EvalBool(ctx, rc.NewStepExpressionEvaluatorExt(ctx, step, stage == stepStageMain), expr, defaultStatusCheck)
	if err != nil {
		return false, fmt.Errorf("  \u274C  Error in if-expression: \"if: %s\" (%s)", expr, err)
	}

	return runStep, nil
}

func isContinueOnError(ctx context.Context, expr string, step step, _ stepStage) (bool, error) {
	// https://github.com/github/docs/blob/3ae84420bd10997bb5f35f629ebb7160fe776eae/content/actions/reference/workflow-syntax-for-github-actions.md?plain=true#L962
	if len(strings.TrimSpace(expr)) == 0 {
		return false, nil
	}

	rc := step.getRunContext()

	continueOnError, err := EvalBool(ctx, rc.NewStepExpressionEvaluator(ctx, step), expr, exprparser.DefaultStatusCheckNone)
	if err != nil {
		return false, fmt.Errorf("  \u274C  Error in continue-on-error-expression: \"continue-on-error: %s\" (%s)", expr, err)
	}

	return continueOnError, nil
}

func mergeIntoMap(step step, target *map[string]string, maps ...map[string]string) {
	if rc := step.getRunContext(); rc != nil && rc.JobContainer != nil && rc.JobContainer.IsEnvironmentCaseInsensitive() {
		mergeIntoMapCaseInsensitive(*target, maps...)
	} else {
		mergeIntoMapCaseSensitive(*target, maps...)
	}
}

func mergeIntoMapCaseSensitive(target map[string]string, args ...map[string]string) {
	for _, m := range args {
		maps.Copy(target, m)
	}
}

func mergeIntoMapCaseInsensitive(target map[string]string, maps ...map[string]string) {
	foldKeys := make(map[string]string, len(target))
	for k := range target {
		foldKeys[strings.ToLower(k)] = k
	}
	toKey := func(s string) string {
		foldKey := strings.ToLower(s)
		if k, ok := foldKeys[foldKey]; ok {
			return k
		}
		foldKeys[strings.ToLower(foldKey)] = s
		return s
	}
	for _, m := range maps {
		for k, v := range m {
			target[toKey(k)] = v
		}
	}
}

func symlinkJoin(filename, sym, parent string) (string, error) {
	dir := path.Dir(filename)
	dest := path.Join(dir, sym)
	prefix := path.Clean(parent) + "/"
	if strings.HasPrefix(dest, prefix) || prefix == "./" {
		return dest, nil
	}
	return "", fmt.Errorf("symlink tries to access file '%s' outside of '%s'", strings.ReplaceAll(dest, "'", "''"), strings.ReplaceAll(parent, "'", "''"))
}
