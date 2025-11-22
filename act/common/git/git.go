package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/storer"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/mattn/go-isatty"
	log "github.com/sirupsen/logrus"

	"code.forgejo.org/forgejo/runner/v11/act/common"
)

var (
	codeCommitHTTPRegex = regexp.MustCompile(`^https?://git-codecommit\.(.+)\.amazonaws.com/v1/repos/(.+)$`)
	codeCommitSSHRegex  = regexp.MustCompile(`ssh://git-codecommit\.(.+)\.amazonaws.com/v1/repos/(.+)$`)
	githubHTTPRegex     = regexp.MustCompile(`^https?://.*github.com.*/(.+)/(.+?)(?:.git)?$`)
	githubSSHRegex      = regexp.MustCompile(`github.com[:/](.+)/(.+?)(?:.git)?$`)

	cloneLock common.MutexMap

	ErrShortRef = errors.New("short SHA references are not supported")
	ErrNoRepo   = errors.New("unable to find git repo")
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

// FindGitRevision get the current git revision
func FindGitRevision(ctx context.Context, file string) (shortSha, sha string, err error) {
	logger := common.Logger(ctx)

	gitDir, err := git.PlainOpenWithOptions(
		file,
		&git.PlainOpenOptions{
			DetectDotGit:          true,
			EnableDotGitCommonDir: true,
		},
	)
	if err != nil {
		logger.WithError(err).Error("path", file, "not located inside a git repository")
		return "", "", err
	}

	head, err := gitDir.Reference(plumbing.HEAD, true)
	if err != nil {
		return "", "", err
	}

	if head.Hash().IsZero() {
		return "", "", fmt.Errorf("HEAD sha1 could not be resolved")
	}

	hash := head.Hash().String()

	logger.Debugf("Found revision: %s", hash)
	return hash[:7], strings.TrimSpace(hash), nil
}

// FindGitRef get the current git ref
func FindGitRef(ctx context.Context, file string) (string, error) {
	logger := common.Logger(ctx)

	logger.Debugf("Loading revision from git directory")
	_, ref, err := FindGitRevision(ctx, file)
	if err != nil {
		return "", err
	}

	logger.Debugf("HEAD points to '%s'", ref)

	// Prefer the git library to iterate over the references and find a matching tag or branch.
	refTag := ""
	refBranch := ""
	repo, err := git.PlainOpenWithOptions(
		file,
		&git.PlainOpenOptions{
			DetectDotGit:          true,
			EnableDotGitCommonDir: true,
		},
	)
	if err != nil {
		return "", err
	}

	iter, err := repo.References()
	if err != nil {
		return "", err
	}

	// find the reference that matches the revision's has
	err = iter.ForEach(func(r *plumbing.Reference) error {
		/* tags and branches will have the same hash
		 * when a user checks out a tag, it is not mentioned explicitly
		 * in the go-git package, we must identify the revision
		 * then check if any tag matches that revision,
		 * if so then we checked out a tag
		 * else we look for branches and if matches,
		 * it means we checked out a branch
		 *
		 * If a branches matches first we must continue and check all tags (all references)
		 * in case we match with a tag later in the interation
		 */
		if r.Hash().String() == ref {
			if r.Name().IsTag() {
				refTag = r.Name().String()
			}
			if r.Name().IsBranch() {
				refBranch = r.Name().String()
			}
		}

		// we found what we where looking for
		if refTag != "" && refBranch != "" {
			return storer.ErrStop
		}

		return nil
	})
	if err != nil {
		return "", err
	}

	// order matters here see above comment.
	if refTag != "" {
		return refTag, nil
	}
	if refBranch != "" {
		return refBranch, nil
	}

	return "", fmt.Errorf("failed to identify reference (tag/branch) for the checked-out revision '%s'", ref)
}

// FindGithubRepo get the repo
func FindGithubRepo(ctx context.Context, file, githubInstance, remoteName string) (string, error) {
	if remoteName == "" {
		remoteName = "origin"
	}

	url, err := findGitRemoteURL(ctx, file, remoteName)
	if err != nil {
		return "", err
	}
	_, slug, err := findGitSlug(url, githubInstance)
	return slug, err
}

func findGitRemoteURL(_ context.Context, file, remoteName string) (string, error) {
	repo, err := git.PlainOpenWithOptions(
		file,
		&git.PlainOpenOptions{
			DetectDotGit:          true,
			EnableDotGitCommonDir: true,
		},
	)
	if err != nil {
		return "", err
	}

	remote, err := repo.Remote(remoteName)
	if err != nil {
		return "", err
	}

	if len(remote.Config().URLs) < 1 {
		return "", fmt.Errorf("remote '%s' exists but has no URL", remoteName)
	}

	return remote.Config().URLs[0], nil
}

func findGitSlug(url, githubInstance string) (string, string, error) {
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

func cloneIfRequired(ctx context.Context, refName plumbing.ReferenceName, input CloneInput, logger log.FieldLogger, repoDir string) (*git.Repository, error) {
	// If the remote URL has changed, remove the directory and clone again.
	if r, err := git.PlainOpen(repoDir); err == nil {
		if remote, err := r.Remote("origin"); err == nil {
			if len(remote.Config().URLs) > 0 && remote.Config().URLs[0] != input.URL {
				_ = os.RemoveAll(repoDir)
			}
		}
	}

	r, err := git.PlainOpen(repoDir)
	if err != nil {
		var progressWriter io.Writer
		if isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd()) {
			if entry, ok := logger.(*log.Entry); ok {
				progressWriter = entry.WriterLevel(log.DebugLevel)
			} else if lgr, ok := logger.(*log.Logger); ok {
				progressWriter = lgr.WriterLevel(log.DebugLevel)
			} else {
				log.Errorf("Unable to get writer from logger (type=%T)", logger)
				progressWriter = os.Stdout
			}
		}

		cloneOptions := git.CloneOptions{
			URL:      input.URL,
			Progress: progressWriter,

			InsecureSkipTLS: input.InsecureSkipTLS, // For Gitea
		}
		if input.Token != "" {
			cloneOptions.Auth = &http.BasicAuth{
				Username: "token",
				Password: input.Token,
			}
		}

		logger.Infof("  \u2601\ufe0f  git clone '%s' # ref=%s", input.URL, input.Ref)
		logger.Debugf("  cloning %s to %s", input.URL, repoDir)
		r, err = git.PlainCloneContext(ctx, repoDir, true /* bare */, &cloneOptions)
		if err != nil {
			logger.Errorf("Unable to clone %v %s: %v", input.URL, refName, err)
			return nil, err
		}

		if err = os.Chmod(repoDir, 0o755); err != nil {
			return nil, err
		}
		logger.Debugf("Cloned %s to %s", input.URL, repoDir)
	}

	return r, nil
}

func gitOptions(token string) (fetchOptions git.FetchOptions, pullOptions git.PullOptions) {
	fetchOptions.RefSpecs = []config.RefSpec{"refs/*:refs/*", "HEAD:refs/heads/HEAD"}
	fetchOptions.Force = true
	pullOptions.Force = true

	if token != "" {
		auth := &http.BasicAuth{
			Username: "token",
			Password: token,
		}
		fetchOptions.Auth = auth
		pullOptions.Auth = auth
	}

	return fetchOptions, pullOptions
}

type Worktree interface {
	io.Closer
	WorktreeDir() string // fully qualified path to the work tree for this repo
}

type gitWorktree struct {
	repoDir     string
	worktreeDir string
	closed      bool
}

func (t *gitWorktree) Close() error {
	if !t.closed {
		cmd := exec.Command("git", "-C", t.repoDir, "worktree", "remove", "--force", "--end-of-options", t.worktreeDir)
		output, err := cmd.CombinedOutput()
		worktreeOutput := strings.TrimSpace(string(output))
		if err != nil {
			return fmt.Errorf("git worktree remove error: %s: %v", worktreeOutput, err)
		}

		// prune will remove any record of worktrees that are removed from disk, in the event something didn't cleanup
		// above, but was removed by some other external force from the filesystem.
		cmd = exec.Command("git", "-C", t.repoDir, "worktree", "prune")
		output, err = cmd.CombinedOutput()
		worktreeOutput = strings.TrimSpace(string(output))
		if err != nil {
			return fmt.Errorf("git worktree prune error: %s: %v", worktreeOutput, err)
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

	refName := plumbing.ReferenceName(fmt.Sprintf("refs/heads/%s", input.Ref))
	r, err := cloneIfRequired(ctx, refName, input, logger, repoDir)
	if err != nil {
		return nil, err
	}

	// Optimization: if `input.Ref` is a full sha and it can be found in the repo already, then we can avoid
	// performing a fetch operation because it won't change.
	skipFetch := false
	var hash *plumbing.Hash
	rev := plumbing.Revision(input.Ref)
	hash, err = r.ResolveRevision(rev)
	if err != nil && !errors.Is(err, plumbing.ErrReferenceNotFound) {
		// unexpected error
		logger.Errorf("Unable to resolve %s: %v", input.Ref, err)
		return nil, err
	} else if !hash.IsZero() && hash.String() == input.Ref {
		skipFetch = true
		logger.Infof("  \u2601\ufe0f  git fetch '%s' skipped; ref=%s cached", input.URL, input.Ref)
	}

	if !skipFetch {
		isOfflineMode := input.OfflineMode

		// fetch latest changes
		fetchOptions, _ := gitOptions(input.Token)

		if input.InsecureSkipTLS { // For Gitea
			fetchOptions.InsecureSkipTLS = true
		}

		if !isOfflineMode {
			logger.Infof("  \u2601\ufe0f  git fetch '%s' # ref=%s", input.URL, input.Ref)
			err = r.Fetch(&fetchOptions)
			if err != nil && !errors.Is(err, git.NoErrAlreadyUpToDate) {
				return nil, err
			}
		}
	}

	rev = plumbing.Revision(input.Ref)
	if hash, err = r.ResolveRevision(rev); err != nil {
		logger.Errorf("Unable to resolve %s: %v", input.Ref, err)
		return nil, err
	}

	if hash.String() != input.Ref && len(input.Ref) >= 4 && strings.HasPrefix(hash.String(), input.Ref) {
		return nil, &Error{
			err:    ErrShortRef,
			commit: hash.String(),
		}
	}

	// go-git doesn't support managing multiple worktrees, so shell out to git.
	logger.Debugf("  git worktree create for ref=%s (sha=%s) to %s", input.Ref, hash.String(), worktreeDir)
	cmd := exec.Command("git", "-C", repoDir, "worktree", "add", "-f", "--end-of-options", worktreeDir, hash.String())
	output, err := cmd.CombinedOutput()
	worktreeOutput := strings.TrimSpace(string(output))
	if err != nil {
		return nil, fmt.Errorf("git worktree add error: %s: %v", worktreeOutput, err)
	}

	return &gitWorktree{repoDir: repoDir, worktreeDir: worktreeDir}, nil
}
