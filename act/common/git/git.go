package git

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	log "github.com/sirupsen/logrus"

	"code.forgejo.org/forgejo/runner/v12/act/common"
)

var (
	codeCommitHTTPRegex = regexp.MustCompile(`^https?://git-codecommit\.(.+)\.amazonaws.com/v1/repos/(.+)$`)
	codeCommitSSHRegex  = regexp.MustCompile(`ssh://git-codecommit\.(.+)\.amazonaws.com/v1/repos/(.+)$`)
	githubHTTPRegex     = regexp.MustCompile(`^https?://.*github.com.*/(.+)/(.+?)(?:.git)?$`)
	githubSSHRegex      = regexp.MustCompile(`github.com[:/](.+)/(.+?)(?:.git)?$`)

	cloneLock common.MutexMap

	ErrShortRef = errors.New("short SHA references are not supported")
)

type Error struct {
	err    error
	commit string
}

func (e *Error) Error() string {
	return e.err.Error()
}

func (e *Error) Unwrap() error {
	return e.err
}

func (e *Error) Commit() string {
	return e.commit
}

// ResolveHead determines the commit ID of the current HEAD.
func ResolveHead(ctx context.Context, repoPath string) (shortSha, sha string, err error) {
	commitID, err := ResolveRevision(ctx, repoPath, "HEAD")
	if err != nil {
		return "", "", err
	}
	return commitID[:7], commitID, nil
}

// ResolveRevision determines the commit ID of the given revision (commit ID, tag, branch, HEAD, ...).
//
// *Warning*: ResolveRevision is not suitable to test whether a commit is actually present.
func ResolveRevision(ctx context.Context, repoPath, rev string) (string, error) {
	logger := common.Logger(ctx)

	options := gitOptions{
		workingDirectory: repoPath,
	}
	output, err := git(ctx, &options, "rev-parse", rev)
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr := strings.TrimSpace(string(exitError.Stderr))
			return "", fmt.Errorf("could not determine the commit ID of %s: %s: %w", rev, stderr, err)
		}
		return "", fmt.Errorf("could not determine the commit ID of %s: %w", rev, err)
	}

	logger.Debugf("Found revision: %s", output)

	return output, nil
}

// objectExists tests whether the given object exists in the repository. Returns true if it does, false otherwise.
func objectExists(ctx context.Context, repoPath, object string) (bool, error) {
	logger := common.Logger(ctx)

	options := gitOptions{
		workingDirectory: repoPath,
	}
	_, err := git(ctx, &options, "cat-file", "-e", object)
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			if exitError.ExitCode() == 1 {
				return false, nil
			}
			stderr := strings.TrimSpace(string(exitError.Stderr))
			return false, fmt.Errorf("could not determine whether %s exists: %s: %w", object, stderr, err)
		}
		return false, fmt.Errorf("could not determine whether %s exists %s", object, err)
	}

	logger.Debugf("Object exists: %s", object)

	return true, nil
}

// DescribeHead resolves the symbolic name (tag or branch) of HEAD.
func DescribeHead(ctx context.Context, repoPath string) (string, error) {
	logger := common.Logger(ctx)

	options := gitOptions{
		workingDirectory: repoPath,
	}

	output, err := git(ctx, &options, "describe", "--all", "HEAD")
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr := strings.TrimSpace(string(exitError.Stderr))
			return "", fmt.Errorf("could not determine the symbolic name (tag or branch) of HEAD: %s: %w", stderr, err)
		}
		return "", fmt.Errorf("could not determine the symbolic name (tag or branch) of HEAD: %w", err)
	}

	logger.Debugf("HEAD points to '%s'", output)

	return fmt.Sprintf("refs/%s", output), nil
}

// GetRepositoryName determines the name of a repository (for example, `actions/checkout`) by extracting it from the
// URL of the remote with the given remoteName.
func GetRepositoryName(ctx context.Context, repoPath, githubInstance, remoteName string) (string, error) {
	if remoteName == "" {
		remoteName = "origin"
	}

	url, err := getRemoteURL(ctx, repoPath, remoteName)
	if err != nil {
		return "", err
	}
	_, slug, err := findSlug(url, githubInstance)
	return slug, err
}

func getRemoteURL(ctx context.Context, repoPath, remoteName string) (string, error) {
	options := gitOptions{
		workingDirectory: repoPath,
	}

	output, err := git(ctx, &options, "remote", "get-url", remoteName)
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr := strings.TrimSpace(string(exitError.Stderr))
			return "", fmt.Errorf("could not determine the URL of remote '%s': %s: %w", remoteName, stderr, err)
		}
		return "", fmt.Errorf("could not determine the URL of remote '%s': %w", remoteName, err)
	}
	if output == "" {
		return "", fmt.Errorf("remote %s has no URL", remoteName)
	}

	return output, nil
}

func findSlug(url, githubInstance string) (string, string, error) {
	if matches := codeCommitHTTPRegex.FindStringSubmatch(url); matches != nil {
		return "CodeCommit", matches[2], nil
	} else if matches := codeCommitSSHRegex.FindStringSubmatch(url); matches != nil {
		return "CodeCommit", matches[2], nil
	} else if matches := githubHTTPRegex.FindStringSubmatch(url); matches != nil {
		return "GitHub", fmt.Sprintf("%s/%s", matches[1], matches[2]), nil
	} else if matches := githubSSHRegex.FindStringSubmatch(url); matches != nil {
		return "GitHub", fmt.Sprintf("%s/%s", matches[1], matches[2]), nil
	} else if githubInstance != "github.com" {
		gheHTTPRegex := regexp.MustCompile(fmt.Sprintf(`^https?://%s/(.+)/(.+?)(?:.git)?$`, githubInstance))
		// Examples:
		// - `code.forgejo.org/forgejo/act`
		// - `code.forgejo.org:22/forgejo/act`
		// - `code.forgejo.org:forgejo/act`
		// - `code.forgejo.org:/forgejo/act`
		gheSSHRegex := regexp.MustCompile(fmt.Sprintf(`%s(?::\d+/|:|/|:/)([^/].+)/(.+?)(?:.git)?$`, githubInstance))
		if matches := gheHTTPRegex.FindStringSubmatch(url); matches != nil {
			return "GitHubEnterprise", fmt.Sprintf("%s/%s", matches[1], matches[2]), nil
		} else if matches := gheSSHRegex.FindStringSubmatch(url); matches != nil {
			return "GitHubEnterprise", fmt.Sprintf("%s/%s", matches[1], matches[2]), nil
		}
	}
	return "", url, nil
}

// CloneInput is a parameter struct for the method `Clone` to simplify the multiple parameters required.
type CloneInput struct {
	CacheDir        string // parent-location for all git caches that the runner maintains
	URL             string // url of the remote to clone
	Ref             string // reference from the remote; eg. tag, branch, or sha
	Token           string // authentication token
	OfflineMode     bool   // when true, no remote operations will occur
	InsecureSkipTLS bool   // when true, TLS verification will be skipped on remote operations
}

func cloneIfRequired(ctx context.Context, input CloneInput, logger log.FieldLogger, repoDir string) error {
	// Check whether cloning can be skipped or whether we have to remove an existing clone. We're not attempting to
	// figure out whether cloning can succeed. That's why errors are ignored.
	if url, err := getRemoteURL(ctx, repoDir, "origin"); err == nil {
		if url != input.URL {
			// There is either no remote URL (something went wrong) or the remote URL has changed. The best course of
			// action is removing the directory before creating a fresh clone.
			_ = os.RemoveAll(repoDir)
		} else {
			// The remote exists and has not changed, therefore we do not have to clone the repository.
			return nil
		}
	}

	// The clone directory might not be empty, for example, due to a failed cloning attempt. Because the target
	// directory's name is derived from `input.URL`, that is an unrecoverable situation and would require human
	// intervention. Try to delete it. If it does not work, `git clone` will fail with an appropriate error. Then, it is
	// up to a human to fix the problem.
	if _, err := os.Stat(repoDir); err == nil {
		_ = os.RemoveAll(repoDir)
	}

	logger.Infof("  \u2601\ufe0f  git clone '%s' # ref=%s", input.URL, input.Ref)
	logger.Debugf("  cloning %s to %s", input.URL, repoDir)

	options := gitOptions{
		token:                     input.Token,
		ignoreInvalidCertificates: input.InsecureSkipTLS,
		workingDirectory:          "",
		remoteURL:                 input.URL,
	}
	_, err := git(ctx, &options, "clone", "--bare", input.URL, repoDir)
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr := strings.TrimSpace(string(exitError.Stderr))
			return fmt.Errorf("unable to clone '%s' to '%s': %s: %w", input.URL, repoDir, stderr, err)
		}
		return fmt.Errorf("unable to clone '%s' to '%s': %w", input.URL, repoDir, err)
	}

	logger.Debugf("Cloned %s to %s", input.URL, repoDir)

	return nil
}

type Worktree interface {
	Close(ctx context.Context) error
	WorktreeDir() string // fully qualified path to the work tree for this repo
}

type gitWorktree struct {
	repoDir     string
	worktreeDir string
	closed      bool
}

func (t *gitWorktree) Close(ctx context.Context) error {
	if !t.closed {
		options := gitOptions{
			workingDirectory: t.repoDir,
		}
		_, err := git(ctx, &options, "worktree", "remove", "--force", "--end-of-options", t.worktreeDir)
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				stderr := strings.TrimSpace(string(exitError.Stderr))
				return fmt.Errorf("git worktree remove error: %s: %w", stderr, err)
			}
			return fmt.Errorf("git worktree remove error: %w", err)
		}

		// prune will remove any record of worktrees that are removed from disk, in the event something didn't clean up
		// above, but was removed by some other external force from the filesystem.
		_, err = git(ctx, &options, "worktree", "prune")
		if err != nil {
			var exitError *exec.ExitError
			if errors.As(err, &exitError) {
				stderr := strings.TrimSpace(string(exitError.Stderr))
				return fmt.Errorf("git worktree prune error: %s: %w", stderr, err)
			}
			return fmt.Errorf("git worktree prune error: %w", err)
		}

		t.closed = true
	}
	return nil
}

func (t *gitWorktree) WorktreeDir() string {
	return t.worktreeDir
}

// Clones a git repo.  The repo contents are stored opaquely in the provided `CacheDir`, and may be reused by future
// clone operations.  The returned value contains a path to the working tree that can be used to interact with the
// requested ref; it must be closed to indicate operations against it are complete.
func Clone(ctx context.Context, input CloneInput) (Worktree, error) {
	if input.CacheDir == "" {
		return nil, errors.New("missing CacheDir to Clone()")
	} else if !filepath.IsAbs(input.CacheDir) {
		// `git -C repoDir ...` will change the working directory -- to ensure consistency between all operations and
		// irrelevant of any change in $PWD, require an absolute CacheDir.
		return nil, errors.New("relative CacheDir is not supported")
	}

	// worktreeDir's format is /[0-9a-f]{2}/[0-9a-f]{62}.  Originally this was a hash of a step's `uses:` text, and the
	// intent was to avoid a flat & wide filesystem by having one byte as a parent directory.  Then it got encoded into
	// the Forgejo end-to-end tests as the expected directory path format for FORGEJO_ACTION_PATH and that format was
	// retained here because it's a fine idea and for test compatibility.
	worktreeDir := filepath.Join(input.CacheDir, common.MustRandName(1), common.MustRandName(31))

	// To make the input URL into a safe path name, to use a fixed length to minimize long pathname issues, and to
	// follow the same principal above of avoiding a flat & wide filesystem, hash the input URL and format it into a
	// directory for the bare repo:
	inputURLHash := common.Sha256(input.URL)
	repoDir := filepath.Join(input.CacheDir, inputURLHash[:2], inputURLHash[2:])

	logger := common.Logger(ctx)

	defer cloneLock.Lock(repoDir)()

	err := cloneIfRequired(ctx, input, logger, repoDir)
	if err != nil {
		return nil, err
	}

	// Optimization: if `input.Ref` is a full sha and it can be found in the repo already, then we can avoid
	// performing a fetch operation because it won't change.
	skipFetch := false
	hash, err := ResolveRevision(ctx, repoDir, input.Ref)
	if err == nil && hash != "" && hash == input.Ref {
		exists, err := objectExists(ctx, repoDir, hash)
		skipFetch = err == nil && exists
		logger.Infof("  \u2601\ufe0f  git fetch '%s' skipped; ref=%s cached", input.URL, input.Ref)
	}

	if !skipFetch {
		isOfflineMode := input.OfflineMode

		if !isOfflineMode {
			logger.Infof("  \u2601\ufe0f  git fetch '%s' # ref=%s", input.URL, input.Ref)

			// Force (indicated by +) update of all branches (even the one that is currently checked out) and tags
			// during `git fetch`. That does only work because the cloned repository is bare.
			fetchInput := FetchInput{
				token:                     input.Token,
				ignoreInvalidCertificates: input.InsecureSkipTLS,
				repoPath:                  repoDir,
				remote:                    "origin",
				refspec:                   "+refs/*:refs/*",
				remoteURL:                 input.URL,
			}
			err = Fetch(ctx, fetchInput)
			if err != nil {
				return nil, err
			}
		}
	}

	if hash, err = ResolveRevision(ctx, repoDir, input.Ref); err != nil {
		logger.Errorf("Unable to resolve %s: %v", input.Ref, err)
		return nil, err
	}

	if hash != input.Ref && len(input.Ref) >= 4 && strings.HasPrefix(hash, input.Ref) {
		return nil, &Error{
			err:    ErrShortRef,
			commit: hash,
		}
	}

	logger.Debugf("  git worktree create for ref=%s (sha=%s) to %s", input.Ref, hash, worktreeDir)

	options := gitOptions{
		workingDirectory: repoDir,
	}
	_, err = git(ctx, &options, "worktree", "add", "-f", "--end-of-options", worktreeDir, hash)
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr := strings.TrimSpace(string(exitError.Stderr))
			return nil, fmt.Errorf("git worktree add error: %s: %w", stderr, err)
		}
		return nil, fmt.Errorf("git worktree add error: %w", err)
	}

	return &gitWorktree{repoDir: repoDir, worktreeDir: worktreeDir}, nil
}

type FetchInput struct {
	token                     string
	ignoreInvalidCertificates bool
	repoPath                  string
	remote                    string
	refspec                   string
	remoteURL                 string
}

func Fetch(ctx context.Context, input FetchInput) error {
	if input.remote == "" {
		return errors.New("mandatory argument remote is empty")
	}

	args := []string{"fetch", input.remote}
	if input.refspec != "" {
		args = append(args, input.refspec)
	}

	options := gitOptions{
		token:                     input.token,
		ignoreInvalidCertificates: input.ignoreInvalidCertificates,
		workingDirectory:          input.repoPath,
		remoteURL:                 input.remoteURL,
	}
	_, err := git(ctx, &options, args...)
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			stderr := strings.TrimSpace(string(exitError.Stderr))
			return fmt.Errorf("could not fetch remote '%s': %s: %w", input.remote, stderr, err)
		}
		return fmt.Errorf("could not fetch remote '%s': %w", input.remote, err)
	}
	return nil
}

type gitOptions struct {
	token                     string
	ignoreInvalidCertificates bool
	workingDirectory          string
	remoteURL                 string
}

func git(ctx context.Context, options *gitOptions, args ...string) (string, error) {
	var gitArguments []string

	if options.token != "" {
		if options.remoteURL == "" {
			return "", errors.New("failed to use git token: missing remote URL")
		}
		remoteURL, err := url.Parse(options.remoteURL)
		if err != nil || remoteURL.Scheme == "" || remoteURL.Host == "" {
			return "", fmt.Errorf("failed to parse remote URL %q to use git token: %w", options.remoteURL, err)
		}

		const envVarName = "GIT_AUTH_HEADER"
		scopedExtraHeader := fmt.Sprintf("http.%s://%s/.extraHeader", remoteURL.Scheme, remoteURL.Host)
		gitArguments = append(gitArguments, "--config-env", fmt.Sprintf("%s=%s", scopedExtraHeader, envVarName))
	}
	if options.ignoreInvalidCertificates {
		gitArguments = append(gitArguments, "-c", "http.sslVerify=false")
	}
	if runtime.GOOS == "windows" {
		gitArguments = append(gitArguments, "-c", "core.longpaths=true")
	}
	if options.workingDirectory != "" {
		gitArguments = append(gitArguments, "-C", options.workingDirectory)
	}

	gitArguments = append(gitArguments, args...)

	logger := common.Logger(ctx)
	logger.Debugf("  git %s", strings.Join(gitArguments, " "))

	cmd := exec.CommandContext(ctx, "git", gitArguments...)

	if options.token != "" {
		auth := "x-access-token:" + options.token
		authHeader := "Authorization: Basic " + base64.StdEncoding.EncodeToString([]byte(auth))
		cmd.Env = append(os.Environ(), "GIT_AUTH_HEADER="+authHeader)
	}

	output, err := cmd.Output()
	trimmedOutput := strings.TrimSpace(string(output))

	return trimmedOutput, err
}
