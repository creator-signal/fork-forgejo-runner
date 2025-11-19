package runner

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"

	"code.forgejo.org/forgejo/runner/v11/act/common"
	"code.forgejo.org/forgejo/runner/v11/act/common/git"
	"code.forgejo.org/forgejo/runner/v11/act/model"
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

	remoteReusableWorkflow := newRemoteReusableWorkflowWithPlat(rc.Config.GitHubInstance, uses)
	if remoteReusableWorkflow == nil {
		return common.NewErrorExecutor(fmt.Errorf("expected format {owner}/{repo}/.{git_platform}/workflows/{filename}@{ref}. Actual '%s' Input string was not in a correct format", uses))
	}

	// If the repository is private, we need a token to clone it
	token := rc.Config.GetToken()

	makeWorkflowExecutorForWorkTree := func(workflowDir string) common.Executor {
		return newReusableWorkflowExecutor(rc, workflowDir, remoteReusableWorkflow.FilePath())
	}

	return cloneIfRequired(rc, *remoteReusableWorkflow, token, makeWorkflowExecutorForWorkTree)
}

func newRemoteReusableWorkflowExecutor(rc *RunContext) common.Executor {
	uses := rc.Run.Job().Uses

	url, err := url.Parse(uses)
	if err != nil {
		return common.NewErrorExecutor(fmt.Errorf("'%s' cannot be parsed as a URL: %v", uses, err))
	}
	host := url.Host
	if host == "" {
		host = rc.Config.GitHubInstance
	}

	remoteReusableWorkflow := newRemoteReusableWorkflowWithPlat(host, strings.TrimPrefix(url.Path, "/"))
	if remoteReusableWorkflow == nil {
		return common.NewErrorExecutor(fmt.Errorf("expected format {owner}/{repo}/.{git_platform}/workflows/{filename}@{ref}. Actual '%s' Input string was not in a correct format", url.Path))
	}

	// FIXME: if the reusable workflow is from a private repository, we need to provide a token to access the repository.
	token := ""

	makeWorkflowExecutorForWorkTree := func(workflowDir string) common.Executor {
		return newReusableWorkflowExecutor(rc, workflowDir, remoteReusableWorkflow.FilePath())
	}

	return cloneIfRequired(rc, *remoteReusableWorkflow, token, makeWorkflowExecutorForWorkTree)
}

func cloneIfRequired(rc *RunContext, remoteReusableWorkflow remoteReusableWorkflow, token string, makeWorkflowExecutorForWorkTree func(workflowDir string) common.Executor) common.Executor {
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

type remoteReusableWorkflow struct {
	URL      string
	Org      string
	Repo     string
	Filename string
	Ref      string

	GitPlatform string
}

func (r *remoteReusableWorkflow) CloneURL() string {
	// In Gitea, r.URL always has the protocol prefix, we don't need to add extra prefix in this case.
	if strings.HasPrefix(r.URL, "http://") || strings.HasPrefix(r.URL, "https://") {
		return fmt.Sprintf("%s/%s/%s", r.URL, r.Org, r.Repo)
	}
	return fmt.Sprintf("https://%s/%s/%s", r.URL, r.Org, r.Repo)
}

func (r *remoteReusableWorkflow) FilePath() string {
	return fmt.Sprintf("./.%s/workflows/%s", r.GitPlatform, r.Filename)
}

// For Gitea
// newRemoteReusableWorkflowWithPlat create a `remoteReusableWorkflow`
// workflows from `.gitea/workflows` and `.github/workflows` are supported
func newRemoteReusableWorkflowWithPlat(url, uses string) *remoteReusableWorkflow {
	// GitHub docs:
	// https://docs.github.com/en/actions/using-workflows/workflow-syntax-for-github-actions#jobsjob_iduses
	r := regexp.MustCompile(`^([^/]+)/([^/]+)/\.([^/]+)/workflows/([^@]+)@(.*)$`)
	matches := r.FindStringSubmatch(uses)
	if len(matches) != 6 {
		return nil
	}
	return &remoteReusableWorkflow{
		Org:         matches[1],
		Repo:        matches[2],
		GitPlatform: matches[3],
		Filename:    matches[4],
		Ref:         matches[5],
		URL:         url,
	}
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
