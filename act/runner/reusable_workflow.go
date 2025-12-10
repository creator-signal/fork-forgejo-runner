package runner

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"strings"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/common/git"
	"code.forgejo.org/forgejo/runner/v12/act/model"
	"github.com/sirupsen/logrus"
)

func newLocalReusableWorkflowExecutor(rc *RunContext) common.Executor {
	if !rc.Config.NoSkipCheckout {
		fullPath := rc.Run.Job().Uses

		fileName := path.Base(fullPath)
		workflowDir := strings.TrimSuffix(fullPath, path.Join("/", fileName))
		workflowDir = strings.TrimPrefix(workflowDir, "./")

		return common.NewPipelineExecutor(
			newReusableWorkflowExecutor(rc, workflowDir, fileName),
		)
	}

	// ./.gitea/workflows/wf.yml -> .gitea/workflows/wf.yml
	trimmedUses := strings.TrimPrefix(rc.Run.Job().Uses, "./")
	// uses string format is {owner}/{repo}/.{git_platform}/workflows/{filename}@{ref}
	uses := fmt.Sprintf("%s/%s@%s", rc.Config.PresetGitHubContext.Repository, trimmedUses, rc.Config.PresetGitHubContext.Sha)

	reusable, err := model.ParseRemoteReusableWorkflow(uses)
	if err != nil {
		return common.NewErrorExecutor(err)
	}
	if reusable.Host == nil {
		// Non-URL qualified remote reusable workflow; default Host for cloning to the currently configured forgejo
		reusable.Host = getHost(rc.Config.GitHubInstance)
	}

	// If the repository is private, we need a token to clone it
	token := rc.Config.GetToken()

	makeWorkflowExecutorForWorkTree := func(workflowDir string) common.Executor {
		return newReusableWorkflowExecutor(rc, workflowDir, reusable.FilePath())
	}

	return cloneIfRequired(rc, reusable, token, makeWorkflowExecutorForWorkTree)
}

func newRemoteReusableWorkflowExecutor(rc *RunContext) common.Executor {
	uses := rc.Run.Job().Uses

	reusable, err := model.ParseRemoteReusableWorkflow(uses)
	if err != nil {
		return common.NewErrorExecutor(err)
	}
	if reusable.Host == nil {
		// Non-URL qualified remote reusable workflow; default Host for cloning to the currently configured forgejo
		reusable.Host = getHost(rc.Config.GitHubInstance)
	}

	// FIXME: if the reusable workflow is from a private repository, we need to provide a token to access the repository.
	token := ""

	makeWorkflowExecutorForWorkTree := func(workflowDir string) common.Executor {
		return newReusableWorkflowExecutor(rc, workflowDir, reusable.FilePath())
	}

	return cloneIfRequired(rc, reusable, token, makeWorkflowExecutorForWorkTree)
}

func getHost(gitHubInstance string) *string {
	if gitHubInstance != "" {
		if u, err := url.Parse(gitHubInstance); err == nil && u.Host != "" {
			return &u.Host
		}
		return &gitHubInstance
	}
	return nil
}

func cloneIfRequired(rc *RunContext, remoteReusableWorkflow *model.RemoteReusableWorkflowWithHost, token string, makeWorkflowExecutorForWorkTree func(workflowDir string) common.Executor) common.Executor {
	return func(ctx context.Context) error {
		// Do not change the remoteReusableWorkflow.URL, because:
		// 	1. Gitea doesn't support specifying GithubContext.ServerURL by the GITHUB_SERVER_URL env
		//	2. Gitea has already full URL with rc.Config.GitHubInstance when calling newRemoteReusableWorkflowWithPlat
		// remoteReusableWorkflow.URL = rc.getGithubContext(ctx).ServerURL
		worktree, err := git.Clone(ctx, git.CloneInput{
			CacheDir:    rc.ActionCacheDir(),
			URL:         remoteReusableWorkflow.CloneURL(),
			Ref:         remoteReusableWorkflow.Ref,
			Token:       token,
			OfflineMode: rc.Config.ActionOfflineMode,
		})
		if err != nil {
			return err
		}
		defer worktree.Close()

		workflowExecutor := makeWorkflowExecutorForWorkTree(worktree.WorktreeDir())
		return workflowExecutor(ctx)
	}
}

func newReusableWorkflowExecutor(rc *RunContext, directory, workflow string) common.Executor {
	return func(ctx context.Context) error {
		planner, err := model.NewWorkflowPlanner(path.Join(directory, workflow), true, false)
		if err != nil {
			return err
		}

		plan, err := planner.PlanEvent("workflow_call")
		if err != nil {
			return err
		}

		runner, err := NewReusableWorkflowRunner(rc)
		if err != nil {
			return err
		}

		planErr := runner.NewPlanExecutor(plan)(ctx)

		// Finalize from parent context: one job-level banner
		return finalizeReusableWorkflow(ctx, rc, planErr)
	}
}

func NewReusableWorkflowRunner(rc *RunContext) (Runner, error) {
	runner := &runnerImpl{
		config:    rc.Config,
		eventJSON: rc.EventJSON,
		caller: &caller{
			runContext: rc,
		},
	}

	return runner.configure()
}

// finalizeReusableWorkflow prints the final job banner from the parent job context.
//
// The Forgejo reporter waits for this banner (log entry with "jobResult"
// field and without stage="Main") before marking the job as complete and revoking
// tokens. Printing this banner from the child reusable workflow would cause
// premature token revocation, breaking subsequent steps in the parent workflow.
func finalizeReusableWorkflow(ctx context.Context, rc *RunContext, planErr error) error {
	jobResult := "success"
	jobResultMessage := "succeeded"
	if planErr != nil {
		jobResult = "failure"
		jobResultMessage = "failed"
	}

	// Outputs should already be present in the parent context:
	// - copied by child's setJobResult branch (rc.caller != nil)
	jobOutputs := rc.Run.Job().Outputs

	common.Logger(ctx).WithFields(logrus.Fields{
		"jobResult":  jobResult,
		"jobOutputs": jobOutputs,
	}).Infof("\U0001F3C1  Job %s", jobResultMessage)

	return planErr
}
