package runner

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"

	"code.forgejo.org/forgejo/runner/v11/act/common"
	"code.forgejo.org/forgejo/runner/v11/act/common/git"
	"code.forgejo.org/forgejo/runner/v11/act/model"
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

	workflowDir := fmt.Sprintf("%s/%s", rc.ActionCacheDir(), safeFilename(uses))

	// If the repository is private, we need a token to clone it
	token := rc.Config.GetToken()

	return common.NewPipelineExecutor(
		newMutexExecutor(cloneIfRequired(rc, *remoteReusableWorkflow, workflowDir, token)),
		newReusableWorkflowExecutor(rc, workflowDir, remoteReusableWorkflow.FilePath()),
	)
}

func newRemoteReusableWorkflowExecutor(rc *RunContext) common.Executor {
	uses := rc.Run.Job().Uses

	url, err := url.Parse(uses)
	if err != nil {
		return common.NewErrorExecutor(fmt.Errorf("'%s' cannot be parsed as a URL: %v", uses, err))
	}

	remoteReusableWorkflow := newRemoteReusableWorkflowWithPlat(url.Host, strings.TrimPrefix(url.Path, "/"))
	if remoteReusableWorkflow == nil {
		return common.NewErrorExecutor(fmt.Errorf("expected format {owner}/{repo}/.{git_platform}/workflows/{filename}@{ref}. Actual '%s' Input string was not in a correct format", url.Path))
	}

	// Host fallback: if 'uses' has no host specified, use the instance from configuration (rc.Config.GitHubInstance)
	if remoteReusableWorkflow.URL == "" {
		remoteReusableWorkflow.URL = rc.Config.GitHubInstance
	}

	// uses with safe filename makes the target directory look something like this {owner}-{repo}-.github-workflows-{filename}@{ref}
	// instead we will just use {owner}-{repo}@{ref} as our target directory. This should also improve performance when we are using
	// multiple reusable workflows from the same repository and ref since for each workflow we won't have to clone it again
	filename := fmt.Sprintf("%s/%s@%s", remoteReusableWorkflow.Org, remoteReusableWorkflow.Repo, remoteReusableWorkflow.Ref)
	workflowDir := fmt.Sprintf("%s/%s", rc.ActionCacheDir(), safeFilename(filename))

	// Provide token only if the reusable workflow is from the same instance.
	// This allows cloning private repositories from the current Forgejo instance,
	// while avoiding token leakage to external instances.
	token := ""
	if isSameInstance(remoteReusableWorkflow.URL, rc.Config.GitHubInstance) {
		token = rc.Config.GetToken()
	}

	if rc.Config.ActionCache != nil {
		return newActionCacheReusableWorkflowExecutor(rc, filename, remoteReusableWorkflow)
	}

	return common.NewPipelineExecutor(
		newMutexExecutor(cloneIfRequired(rc, *remoteReusableWorkflow, workflowDir, token)),
		newReusableWorkflowExecutor(rc, workflowDir, remoteReusableWorkflow.FilePath()),
	)
}

func newActionCacheReusableWorkflowExecutor(rc *RunContext, filename string, remoteReusableWorkflow *remoteReusableWorkflow) common.Executor {
	return func(ctx context.Context) error {
		ghctx := rc.getGithubContext(ctx)
		remoteReusableWorkflow.URL = ghctx.ServerURL
		sha, err := rc.Config.ActionCache.Fetch(ctx, filename, remoteReusableWorkflow.CloneURL(), remoteReusableWorkflow.Ref, ghctx.Token)
		if err != nil {
			return err
		}
		archive, err := rc.Config.ActionCache.GetTarArchive(ctx, filename, sha, fmt.Sprintf(".github/workflows/%s", remoteReusableWorkflow.Filename))
		if err != nil {
			return err
		}
		defer archive.Close()
		treader := tar.NewReader(archive)
		if _, err = treader.Next(); err != nil {
			return err
		}
		planner, err := model.NewSingleWorkflowPlanner(remoteReusableWorkflow.Filename, treader)
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

		return runner.NewPlanExecutor(plan)(ctx)
	}
}

var executorLock sync.Mutex

func newMutexExecutor(executor common.Executor) common.Executor {
	return func(ctx context.Context) error {
		executorLock.Lock()
		defer executorLock.Unlock()

		return executor(ctx)
	}
}

func cloneIfRequired(rc *RunContext, remoteReusableWorkflow remoteReusableWorkflow, targetDirectory, token string) common.Executor {
	return common.NewConditionalExecutor(
		func(ctx context.Context) bool {
			_, err := os.Stat(targetDirectory)
			notExists := errors.Is(err, fs.ErrNotExist)
			return notExists
		},
		func(ctx context.Context) error {
			// Do not change the remoteReusableWorkflow.URL, because:
			// 	1. Gitea doesn't support specifying GithubContext.ServerURL by the GITHUB_SERVER_URL env
			//	2. Gitea has already full URL with rc.Config.GitHubInstance when calling newRemoteReusableWorkflowWithPlat
			// remoteReusableWorkflow.URL = rc.getGithubContext(ctx).ServerURL
			return git.NewGitCloneExecutor(git.NewGitCloneExecutorInput{
				URL:         remoteReusableWorkflow.CloneURL(),
				Ref:         remoteReusableWorkflow.Ref,
				Dir:         targetDirectory,
				Token:       token,
				OfflineMode: rc.Config.ActionOfflineMode,
			})(ctx)
		},
		nil,
	)
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

		return runner.NewPlanExecutor(plan)(ctx)
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
	base := strings.TrimSuffix(r.URL, "/")
	// In Gitea, r.URL always has the protocol prefix, we don't need to add extra prefix in this case.
	if strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://") {
		return fmt.Sprintf("%s/%s/%s", base, r.Org, r.Repo)
	}
	return fmt.Sprintf("https://%s/%s/%s", base, r.Org, r.Repo)
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

// isSameInstance checks if two URLs/hosts refer to the same instance.
// Normalizes protocol prefixes, trailing slashes, and case.
func isSameInstance(urlOrHost, instance string) bool {
	normalize := func(s string) string {
		s = strings.TrimSpace(s)
		s = strings.TrimSuffix(s, "/")
		if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
			if u, err := url.Parse(s); err == nil && u.Host != "" {
				return strings.ToLower(u.Host)
			}
		}
		return strings.ToLower(s)
	}
	return normalize(urlOrHost) == normalize(instance)
}
