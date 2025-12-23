package model

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// NonLocalReusableWorkflow is either a [NonLocalReusableWorkflowReference] or an [ExternalReusableWorkflowReference].
type NonLocalReusableWorkflow interface {
	// Access the shared details that are present on all non-local reusable workflows.
	Reference() *NonLocalReusableWorkflowReference

	// ConvertExternalWithDefaultBaseURL converts this reference from a remote into an external reference with the given
	// base URL. If it is already an external reference, it is returned unchanged.
	ConvertExternalWithDefaultBaseURL(baseURL string) *ExternalReusableWorkflowReference
}

// Parsed version of a `job.<job-id>.uses:` reference, such as `uses:
// org/repo/.forgejo/workflows/reusable-workflow.yml@v1`
type NonLocalReusableWorkflowReference struct {
	Org      string
	Repo     string
	Filename string
	Ref      string

	GitPlatform string
}

// parseReusableWorkflowReference parses a workflow reference like `.forgejo/workflows/reusable-workflow.yml@v1`
func parseReusableWorkflowReference(uses string) *NonLocalReusableWorkflowReference {
	// GitHub docs:
	// https://docs.github.com/en/actions/using-workflows/workflow-syntax-for-github-actions#jobsjob_iduses
	r := regexp.MustCompile(`^([^/]+)/([^/]+)/\.([^/]+)/workflows/([^@]+)@(.*)$`)
	matches := r.FindStringSubmatch(uses)
	if len(matches) != 6 {
		return nil
	}
	return &NonLocalReusableWorkflowReference{
		Org:         matches[1],
		Repo:        matches[2],
		GitPlatform: matches[3],
		Filename:    matches[4],
		Ref:         matches[5],
	}
}

func (r *NonLocalReusableWorkflowReference) FilePath() string {
	return fmt.Sprintf("./.%s/workflows/%s", r.GitPlatform, r.Filename)
}

func (r *NonLocalReusableWorkflowReference) Reference() *NonLocalReusableWorkflowReference {
	return r
}

func (r *NonLocalReusableWorkflowReference) ConvertExternalWithDefaultBaseURL(defaultBaseURL string) *ExternalReusableWorkflowReference {
	return &ExternalReusableWorkflowReference{
		NonLocalReusableWorkflowReference: *r,
		BaseURL:                           defaultBaseURL,
	}
}

// Parsed version of `job.<job-id>.uses:` which is fully qualified with a URL, such as `uses:
// https://example.com/org/repo/.forgejo/workflows/reusable-workflow.yml@v1`.  An external workflow reference has a
// BaseURL that represents the `https://example.com` base.
type ExternalReusableWorkflowReference struct {
	NonLocalReusableWorkflowReference
	BaseURL string
}

func (r *ExternalReusableWorkflowReference) CloneURL() string {
	return fmt.Sprintf("%s/%s/%s", r.BaseURL, r.Org, r.Repo)
}

func (r *ExternalReusableWorkflowReference) Reference() *NonLocalReusableWorkflowReference {
	return &r.NonLocalReusableWorkflowReference
}

func (r *ExternalReusableWorkflowReference) ConvertExternalWithDefaultBaseURL(defaultBaseURL string) *ExternalReusableWorkflowReference {
	return r
}

// Parses a `uses` declaration such as `uses: some-org/some-repo/.forgejo/workflows/called-workflow.yml@v1`.  The
// returned object may be a [NonLocalReusableWorkflowReference] or an [ExternalReusableWorkflowReference] if it was a fully
// qualified URL.
func ParseRemoteReusableWorkflow(uses string) (NonLocalReusableWorkflow, error) {
	url, err := url.Parse(uses)
	if err != nil {
		return nil, fmt.Errorf("'%s' cannot be parsed as a URL: %v", uses, err)
	}
	host := url.Host
	var baseURL *string
	if host == "" {
		baseURL = nil
	} else {
		innerBaseURL := fmt.Sprintf("%s://%s", url.Scheme, url.Host)
		baseURL = &innerBaseURL
	}

	reusableWorkflowReference := parseReusableWorkflowReference(strings.TrimPrefix(url.Path, "/"))
	if reusableWorkflowReference == nil {
		return nil, fmt.Errorf("expected format {owner}/{repo}/.{git_platform}/workflows/{filename}@{ref}. Actual '%s' Input string was not in a correct format", url.Path)
	}
	if baseURL == nil {
		return reusableWorkflowReference, nil
	}

	return &ExternalReusableWorkflowReference{
		NonLocalReusableWorkflowReference: *reusableWorkflowReference,
		BaseURL:                           *baseURL,
	}, nil
}
