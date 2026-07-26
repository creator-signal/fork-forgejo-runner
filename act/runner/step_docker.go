package runner

import (
	"context"
	"fmt"
	"strings"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/container"
	"code.forgejo.org/forgejo/runner/v12/act/container/docker"
	"code.forgejo.org/forgejo/runner/v12/act/model"
	"github.com/kballard/go-shellquote"
)

type stepDocker struct {
	Step       *model.Step
	RunContext *RunContext
	env        map[string]string
}

func (sd *stepDocker) pre() common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (sd *stepDocker) main() common.Executor {
	sd.env = map[string]string{}

	return runStepExecutor(sd, stepStageMain, sd.runUsesContainer())
}

func (sd *stepDocker) post() common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (sd *stepDocker) getRunContext() *RunContext {
	return sd.RunContext
}

func (sd *stepDocker) getGithubContext(ctx context.Context) *model.GithubContext {
	return sd.getRunContext().getGithubContext(ctx)
}

func (sd *stepDocker) getStepModel() *model.Step {
	return sd.Step
}

func (sd *stepDocker) getEnv() *map[string]string {
	return &sd.env
}

func (sd *stepDocker) getIfExpression(_ context.Context, _ stepStage) string {
	return sd.Step.If.Value
}

func (sd *stepDocker) runUsesContainer() common.Executor {
	rc := sd.RunContext
	step := sd.Step

	return func(ctx context.Context) error {
		if !rc.JobContainer.SupportsDockerContainerActions() {
			return fmt.Errorf("docker container actions are not supported by the back-end %s", rc.JobContainer.BackendID())
		}

		eval, err := rc.NewExpressionEvaluator(ctx)
		if err != nil {
			return fmt.Errorf("could not create new ExpressionEvaluator: %w", err)
		}

		interpolatedUses, err := eval.Interpolate(ctx, step.Uses)
		if err != nil {
			return fmt.Errorf("could not interpolate uses: %w", err)
		}
		image := strings.TrimPrefix(interpolatedUses, "docker://")

		interpolatedArgs, err := eval.Interpolate(ctx, step.With["args"])
		if err != nil {
			return fmt.Errorf("unable to interpolate args: %w", err)
		}
		cmd, err := shellquote.Split(interpolatedArgs)
		if err != nil {
			return fmt.Errorf("unable to process args: %w", err)
		}

		var entrypoint []string
		if entry, err := eval.Interpolate(ctx, step.With["entrypoint"]); err != nil {
			return fmt.Errorf("unable to interpolate entrypoint: %w", err)
		} else if entry != "" {
			entrypoint = []string{entry}
		}

		ep, err := rc.DockerEndpoint(ctx)
		if err != nil {
			return err
		}

		stepContainer, err := sd.newStepContainer(ctx, ep, image, cmd, entrypoint)
		if err != nil {
			return fmt.Errorf("could not create new step container: %w", err)
		}

		return common.NewPipelineExecutor(
			stepContainer.Pull(rc.Config.ForcePull),
			stepContainer.Remove().IfBool(!rc.Config.ReuseContainers),
			stepContainer.Create(rc.Config.ContainerCapAdd, rc.Config.ContainerCapDrop),
			stepContainer.Start(true),
		).Finally(
			stepContainer.Remove().IfBool(!rc.Config.ReuseContainers),
		).Finally(stepContainer.Close())(ctx)
	}
}

var ContainerNewContainer = docker.NewContainer

func (sd *stepDocker) newStepContainer(ctx context.Context, ep docker.Endpoint, image string, cmd, entrypoint []string) (container.Container, error) {
	rc := sd.RunContext
	step := sd.Step

	rawLogger := common.Logger(ctx).WithField("raw_output", true)
	logWriter := common.NewLineWriter(rc.commandHandler(ctx), func(s string) bool {
		if rc.Config.LogOutput {
			rawLogger.Infof("%s", s)
		} else {
			rawLogger.Debugf("%s", s)
		}
		return true
	})
	envList := make([]string, 0, len(sd.env))
	for k, v := range sd.env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}

	toolCachePath, err := rc.getToolCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not determine path of tool cache: %w", err)
	}

	envList = append(envList, fmt.Sprintf("%s=%s", "RUNNER_TOOL_CACHE", toolCachePath))
	envList = append(envList, fmt.Sprintf("%s=%s", "RUNNER_OS", "Linux"))
	envList = append(envList, fmt.Sprintf("%s=%s", "RUNNER_ARCH", ep.RunnerArch()))
	envList = append(envList, fmt.Sprintf("%s=%s", "RUNNER_TEMP", "/tmp"))

	binds, mounts, validVolumes, err := rc.GetBindsAndMounts(ctx)
	if err != nil {
		return nil, fmt.Errorf("could not get container binds and mounts: %w", err)
	}

	stepContainer := ContainerNewContainer(ep, &container.NewContainerInput{
		Cmd:             cmd,
		Entrypoint:      entrypoint,
		WorkingDir:      rc.JobContainer.ToContainerPath(rc.Config.Workdir),
		Image:           image,
		Name:            createSimpleContainerName(rc.jobContainerName(), "STEP-"+step.ID),
		Env:             envList,
		ToolCache:       toolCachePath,
		Mounts:          mounts,
		NetworkMode:     rc.getNetworkName(ctx),
		NetworkAliases:  []string{sanitizeNetworkAlias(ctx, step.ID)},
		Binds:           binds,
		Stdout:          logWriter,
		Stderr:          logWriter,
		Privileged:      rc.Config.Privileged,
		UsernsMode:      rc.Config.UsernsMode,
		DefaultPlatform: rc.dockerImagePlatform(ctx),
		ValidVolumes:    validVolumes,
	})
	return stepContainer, nil
}
