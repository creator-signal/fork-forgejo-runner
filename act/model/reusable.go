package model

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Parsed version of a remote `job.<job-id>.uses:` reference, such as `uses:
// org/repo/.forgejo/workflows/reusable-workflow.yml`.
type RemoteReusableWorkflow struct {
	Org      string
	Repo     string
	Filename string
	Ref      string

	GitPlatform string
}

// newRemoteReusableWorkflowWithPlat create a `remoteReusableWorkflow`
// workflows from `.gitea/workflows` and `.github/workflows` are supported
func NewRemoteReusableWorkflowWithPlat(uses string) *RemoteReusableWorkflow {
	// GitHub docs:
	// https://docs.github.com/en/actions/using-workflows/workflow-syntax-for-github-actions#jobsjob_iduses
	r := regexp.MustCompile(`^([^/]+)/([^/]+)/\.([^/]+)/workflows/([^@]+)@(.*)$`)
	matches := r.FindStringSubmatch(uses)
	if len(matches) != 6 {
		return nil
	}
	return &RemoteReusableWorkflow{
		Org:         matches[1],
		Repo:        matches[2],
		GitPlatform: matches[3],
		Filename:    matches[4],
		Ref:         matches[5],
	}
}

func (r *RemoteReusableWorkflow) FilePath() string {
	return fmt.Sprintf("./.%s/workflows/%s", r.GitPlatform, r.Filename)
}

type RemoteReusableWorkflowWithHost struct {
	RemoteReusableWorkflow
	Host *string
}

func (r *RemoteReusableWorkflowWithHost) CloneURL() string {
	if r.Host == nil {
		return ""
	}
	return fmt.Sprintf("https://%s/%s/%s", *r.Host, r.Org, r.Repo)
}

// Parses a `uses` declaration for a "remote" (not this repo) reusable workflow.  Typically something like
// "some-org/some-repo/.forgejo/workflows/called-workflow.yml@v1".  Can also be domain-qualified, in which case the
// `BaseURL` field will be populated -- otherwise it should be assumed to be an org/repo on the same Forgejo instance as
// the `uses: ...` was declared.
func ParseRemoteReusableWorkflow(uses string) (*RemoteReusableWorkflowWithHost, error) {
	url, err := url.Parse(uses)
	if err != nil {
		return nil, fmt.Errorf("'%s' cannot be parsed as a URL: %v", uses, err)
	}
	host := &url.Host
	if *host == "" {
		host = nil
	}

	remoteReusableWorkflow := NewRemoteReusableWorkflowWithPlat(strings.TrimPrefix(url.Path, "/"))
	if remoteReusableWorkflow == nil {
		return nil, fmt.Errorf("expected format {owner}/{repo}/.{git_platform}/workflows/{filename}@{ref}. Actual '%s' Input string was not in a correct format", url.Path)
	}
	return &RemoteReusableWorkflowWithHost{
		RemoteReusableWorkflow: *remoteReusableWorkflow,
		Host:                   host,
	}, nil
}
