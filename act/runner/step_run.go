package runner

import (
	"context"
	"fmt"
	"maps"
	"runtime"
	"strings"

	"github.com/kballard/go-shellquote"

	"code.forgejo.org/forgejo/runner/v11/act/common"
	"code.forgejo.org/forgejo/runner/v11/act/container"
	"code.forgejo.org/forgejo/runner/v11/act/lookpath"
	"code.forgejo.org/forgejo/runner/v11/act/model"
)

type stepRun struct {
	Step             *model.Step
	RunContext       *RunContext
	cmd              []string
	cmdline          string
	env              map[string]string
	WorkingDirectory string
}

func (sr *stepRun) pre() common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (sr *stepRun) main() common.Executor {
	sr.env = map[string]string{}
	return runStepExecutor(sr, stepStageMain, common.NewPipelineExecutor(
		sr.setupShellCommandExecutor(),
		func(ctx context.Context) error {
			sr.getRunContext().ApplyExtraPath(ctx, &sr.env)
			if he, ok := sr.getRunContext().JobContainer.(*container.HostEnvironment); ok && he != nil {
				return he.ExecWithCmdLine(sr.cmd, sr.cmdline, sr.env, "", sr.WorkingDirectory)(ctx)
			}
			return sr.getRunContext().JobContainer.Exec(sr.cmd, sr.env, "", sr.WorkingDirectory)(ctx)
		},
	))
}

func (sr *stepRun) post() common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (sr *stepRun) getRunContext() *RunContext {
	return sr.RunContext
}

func (sr *stepRun) getGithubContext(ctx context.Context) *model.GithubContext {
	return sr.getRunContext().getGithubContext(ctx)
}

func (sr *stepRun) getStepModel() *model.Step {
	return sr.Step
}

func (sr *stepRun) getEnv() *map[string]string {
	return &sr.env
}

func (sr *stepRun) getIfExpression(_ context.Context, _ stepStage) string {
	return sr.Step.If.Value
}

func (sr *stepRun) setupShellCommandExecutor() common.Executor {
	return func(ctx context.Context) error {
		scriptName, script, err := sr.setupShellCommand(ctx)
		if err != nil {
			return err
		}

		rc := sr.getRunContext()
		return rc.JobContainer.Copy(rc.JobContainer.GetActPath(), &container.FileEntry{
			Name: scriptName,
			Mode: 0o755,
			Body: script,
		})(ctx)
	}
}

func getScriptName(rc *RunContext, step *model.Step) string {
	scriptName := step.ID
	for rcs := rc; rcs.Parent != nil; rcs = rcs.Parent {
		scriptName = fmt.Sprintf("%s-composite-%s", rcs.Parent.CurrentStep, scriptName)
	}
	return fmt.Sprintf("workflow/%s", scriptName)
}

func shellCommand(shell string) string {
	// Reference: https://github.com/actions/runner/blob/8109c962f09d9acc473d92c595ff43afceddb347/src/Runner.Worker/Handlers/ScriptHandlerHelpers.cs#L9-L17
	switch shell {
	case "", "bash":
		return "bash --noprofile --norc -e -o pipefail {0}"
	case "pwsh":
		return "pwsh -command . '{0}'"
	case "python":
		return "python {0}"
	case "sh":
		return "sh -e {0}"
	case "cmd":
		return "cmd /D /E:ON /V:OFF /S /C \"CALL \"{0}\"\""
	case "powershell":
		return "powershell -command . '{0}'"
	case "node":
		return "node {0}"
	default:
		return shell
	}
}

// TODO: Currently we just ignore top level keys, BUT we should return proper error on them
// BUTx2 I leave this for when we rewrite act to use actionlint for workflow validation
// so we return proper errors before any execution or spawning containers
// it will error anyway with:
// OCI runtime exec failed: exec failed: container_linux.go:380: starting container process caused: exec: "${{": executable file not found in $PATH: unknown
func (sr *stepRun) setupShellCommand(ctx context.Context) (name, script string, err error) {
	logger := common.Logger(ctx)
	shell := sr.interpretShell(ctx)
	sr.setupWorkingDirectory(ctx)

	step := sr.Step

	script = sr.RunContext.NewStepExpressionEvaluator(ctx, sr).Interpolate(ctx, step.Run)

	shellCommand := shellCommand(shell)
	name = getScriptName(sr.RunContext, step)

	// Reference: https://github.com/actions/runner/blob/8109c962f09d9acc473d92c595ff43afceddb347/src/Runner.Worker/Handlers/ScriptHandlerHelpers.cs#L47-L64
	// Reference: https://github.com/actions/runner/blob/8109c962f09d9acc473d92c595ff43afceddb347/src/Runner.Worker/Handlers/ScriptHandlerHelpers.cs#L19-L27
	runPrepend := ""
	runAppend := ""
	switch shell {
	case "bash", "sh":
		name += ".sh"
	case "pwsh", "powershell":
		name += ".ps1"
		runPrepend = "$ErrorActionPreference = 'stop'"
		runAppend = "if ((Test-Path -LiteralPath variable:/LASTEXITCODE)) { exit $LASTEXITCODE }"
	case "cmd":
		name += ".cmd"
		runPrepend = "@echo off"
	case "python":
		name += ".py"
	case "node":
		name += ".js"
	}

	script = fmt.Sprintf("%s\n%s\n%s", runPrepend, script, runAppend)

	if !strings.Contains(script, "::add-mask::") && !sr.RunContext.Config.InsecureSecrets {
		logger.Debugf("Wrote command \n%s\n to '%s'", script, name)
	} else {
		logger.Debugf("Wrote add-mask command to '%s'", name)
	}

	rc := sr.getRunContext()
	scriptPath := fmt.Sprintf("%s/%s", rc.JobContainer.GetActPath(), name)
	sr.cmdline = strings.Replace(shellCommand, `{0}`, scriptPath, 1)
	sr.cmd, err = shellquote.Split(sr.cmdline)

	return name, script, err
}

type localEnv struct {
	env map[string]string
}

func (l *localEnv) Getenv(name string) string {
	if runtime.GOOS == "windows" {
		for k, v := range l.env {
			if strings.EqualFold(name, k) {
				return v
			}
		}
		return ""
	}
	return l.env[name]
}

func (sr *stepRun) interpretShell(ctx context.Context) string {
	rc := sr.RunContext
	shell := sr.Step.RawShell

	if shell == "" {
		shell = rc.Run.Job().Defaults.Run.Shell
	}

	shell = rc.NewExpressionEvaluator(ctx).Interpolate(ctx, shell)

	if shell == "" {
		shell = rc.Run.Workflow.Defaults.Run.Shell
	}

	if shell == "" {
		if _, ok := rc.JobContainer.(*container.HostEnvironment); ok {
			shellWithFallback := []string{"bash", "sh"}
			// Don't use bash on windows by default, if not using a docker container
			if runtime.GOOS == "windows" {
				shellWithFallback = []string{"pwsh", "powershell"}
			}
			shell = shellWithFallback[0]
			lenv := &localEnv{env: map[string]string{}}
			maps.Copy(lenv.env, sr.env)
			sr.getRunContext().ApplyExtraPath(ctx, &lenv.env)
			_, err := lookpath.LookPath2(shellWithFallback[0], lenv)
			if err != nil {
				shell = shellWithFallback[1]
			}
		} else {
			shellFallback := `
if command -v bash >/dev/null; then
	echo -n bash
else
	echo -n sh
fi
`
			stdout, _, err := rc.sh(ctx, shellFallback)
			if err != nil {
				common.Logger(ctx).Error("fail to run %q: %v", shellFallback, err)
			} else {
				shell = stdout
			}
		}
	}

	return shell
}

func (sr *stepRun) setupWorkingDirectory(ctx context.Context) {
	rc := sr.RunContext
	step := sr.Step
	var workingdirectory string

	if step.WorkingDirectory == "" {
		workingdirectory = rc.Run.Job().Defaults.Run.WorkingDirectory
	} else {
		workingdirectory = step.WorkingDirectory
	}

	// jobs can receive context values, so we interpolate
	workingdirectory = rc.NewExpressionEvaluator(ctx).Interpolate(ctx, workingdirectory)

	// but top level keys in workflow file like `defaults` or `env` can't
	if workingdirectory == "" {
		workingdirectory = rc.Run.Workflow.Defaults.Run.WorkingDirectory
	}
	sr.WorkingDirectory = workingdirectory
}
