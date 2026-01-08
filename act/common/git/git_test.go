package git

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindSlug(t *testing.T) {
	assert := assert.New(t)

	slugTests := []struct {
		url      string // input
		provider string // expected result
		slug     string // expected result
	}{
		{"https://git-codecommit.us-east-1.amazonaws.com/v1/repos/my-repo-name", "CodeCommit", "my-repo-name"},
		{"ssh://git-codecommit.us-west-2.amazonaws.com/v1/repos/my-repo", "CodeCommit", "my-repo"},
		{"git@github.com:nektos/act.git", "GitHub", "nektos/act"},
		{"git@github.com:nektos/act", "GitHub", "nektos/act"},
		{"https://github.com/nektos/act.git", "GitHub", "nektos/act"},
		{"http://github.com/nektos/act.git", "GitHub", "nektos/act"},
		{"https://github.com/nektos/act", "GitHub", "nektos/act"},
		{"http://github.com/nektos/act", "GitHub", "nektos/act"},
		{"git+ssh://git@github.com/owner/repo.git", "GitHub", "owner/repo"},
		{"http://myotherrepo.com/act.git", "", "http://myotherrepo.com/act.git"},
		{"ssh://git@example.com/forgejo/act.git", "GitHubEnterprise", "forgejo/act"},
		{"ssh://git@example.com:2222/forgejo/act.git", "GitHubEnterprise", "forgejo/act"},
		{"ssh://git@example.com:forgejo/act.git", "GitHubEnterprise", "forgejo/act"},
		{"ssh://git@example.com:/forgejo/act.git", "GitHubEnterprise", "forgejo/act"},
	}

	for _, tt := range slugTests {
		instance := "example.com"
		if tt.provider == "GitHub" {
			instance = "github.com"
		}

		provider, slug, err := findSlug(tt.url, instance)

		assert.NoError(err)
		assert.Equal(tt.provider, provider)
		assert.Equal(tt.slug, slug)
	}
}

func cleanGitHooks(dir string) error {
	hooksDir := filepath.Join(dir, ".git", "hooks")
	files, err := os.ReadDir(hooksDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		relName := filepath.Join(hooksDir, f.Name())
		if err := os.Remove(relName); err != nil {
			return err
		}
	}
	return nil
}

func TestGetRemoteURL(t *testing.T) {
	assert := assert.New(t)

	basedir := t.TempDir()
	gitConfig()
	err := gitCmd("init", basedir)
	assert.NoError(err)
	err = cleanGitHooks(basedir)
	assert.NoError(err)

	remoteURL := "https://git-codecommit.us-east-1.amazonaws.com/v1/repos/my-repo-name"
	err = gitCmd("-C", basedir, "remote", "add", "origin", remoteURL)
	assert.NoError(err)

	u, err := getRemoteURL(t.Context(), basedir, "origin")
	assert.NoError(err)
	assert.Equal(remoteURL, u)

	remoteURL = "git@github.com/AwesomeOwner/MyAwesomeRepo.git"
	err = gitCmd("-C", basedir, "remote", "add", "upstream", remoteURL)
	assert.NoError(err)
	u, err = getRemoteURL(t.Context(), basedir, "upstream")
	assert.NoError(err)
	assert.Equal(remoteURL, u)
}

func TestDescribeHead(t *testing.T) {
	basedir := t.TempDir()
	gitConfig()

	for name, tt := range map[string]struct {
		Prepare func(t *testing.T, dir string)
		Assert  func(t *testing.T, ref string, err error)
	}{
		"new_repo": {
			Prepare: func(t *testing.T, dir string) {},
			Assert: func(t *testing.T, ref string, err error) {
				require.Error(t, err)
			},
		},
		"new_repo_with_commit": {
			Prepare: func(t *testing.T, dir string) {
				require.NoError(t, gitCmd("-C", dir, "commit", "--allow-empty", "-m", "msg"))
			},
			Assert: func(t *testing.T, ref string, err error) {
				require.NoError(t, err)
				require.Equal(t, "refs/heads/master", ref)
			},
		},
		"current_head_is_tag": {
			Prepare: func(t *testing.T, dir string) {
				require.NoError(t, gitCmd("-C", dir, "commit", "--allow-empty", "-m", "commit msg"))
				require.NoError(t, gitCmd("-C", dir, "tag", "v1.2.3"))
				require.NoError(t, gitCmd("-C", dir, "checkout", "v1.2.3"))
			},
			Assert: func(t *testing.T, ref string, err error) {
				require.NoError(t, err)
				require.Equal(t, "refs/tags/v1.2.3", ref)
			},
		},
		"current_head_is_same_as_tag": {
			Prepare: func(t *testing.T, dir string) {
				require.NoError(t, gitCmd("-C", dir, "commit", "--allow-empty", "-m", "1.4.2 release"))
				require.NoError(t, gitCmd("-C", dir, "tag", "v1.4.2"))
			},
			Assert: func(t *testing.T, ref string, err error) {
				require.NoError(t, err)
				require.Equal(t, "refs/tags/v1.4.2", ref)
			},
		},
		"current_head_is_not_tag": {
			Prepare: func(t *testing.T, dir string) {
				require.NoError(t, gitCmd("-C", dir, "commit", "--allow-empty", "-m", "msg"))
				require.NoError(t, gitCmd("-C", dir, "tag", "v1.4.2"))
				require.NoError(t, gitCmd("-C", dir, "commit", "--allow-empty", "-m", "msg2"))
			},
			Assert: func(t *testing.T, ref string, err error) {
				require.NoError(t, err)
				require.Equal(t, "refs/heads/master", ref)
			},
		},
		"current_head_is_another_branch": {
			Prepare: func(t *testing.T, dir string) {
				require.NoError(t, gitCmd("-C", dir, "checkout", "-b", "mybranch"))
				require.NoError(t, gitCmd("-C", dir, "commit", "--allow-empty", "-m", "msg"))
			},
			Assert: func(t *testing.T, ref string, err error) {
				require.NoError(t, err)
				require.Equal(t, "refs/heads/mybranch", ref)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := filepath.Join(basedir, name)
			require.NoError(t, os.MkdirAll(dir, 0o755))
			require.NoError(t, gitCmd("-C", dir, "init", "--initial-branch=master"))
			require.NoError(t, gitCmd("-C", dir, "config", "user.name", "user@example.com"))
			require.NoError(t, gitCmd("-C", dir, "config", "user.email", "user@example.com"))
			require.NoError(t, cleanGitHooks(dir))
			tt.Prepare(t, dir)
			ref, err := DescribeHead(t.Context(), dir)
			tt.Assert(t, ref, err)
		})
	}
}

func TestClone(t *testing.T) {
	for name, tt := range map[string]struct {
		Err      error
		URL, Ref string
	}{
		"tag": {
			Err: nil,
			URL: "https://github.com/actions/checkout",
			Ref: "v2",
		},
		"branch": {
			Err: nil,
			URL: "https://github.com/anchore/scan-action",
			Ref: "act-fails",
		},
		"sha": {
			Err: nil,
			URL: "https://github.com/actions/checkout",
			Ref: "5a4ac9002d0be2fb38bd78e4b4dbde5606d7042f", // v2
		},
		"short-sha": {
			Err: &Error{ErrShortRef, "5a4ac9002d0be2fb38bd78e4b4dbde5606d7042f"},
			URL: "https://github.com/actions/checkout",
			Ref: "5a4ac90", // v2
		},
	} {
		t.Run(name, func(t *testing.T) {
			if testing.Short() {
				t.Skip("skipping integration test")
			}

			wt, err := Clone(t.Context(), CloneInput{
				CacheDir: t.TempDir(),
				URL:      tt.URL,
				Ref:      tt.Ref,
			})
			if tt.Err != nil {
				assert.Error(t, err)
				assert.Equal(t, tt.Err, err)
			} else {
				require.NoError(t, err)
				wt.Close(t.Context())
			}
		})
	}

	t.Run("Skips Fetch on Present Full SHA", func(t *testing.T) {
		cacheDir := t.TempDir()

		// Create a local repo that will act as the remote to be cloned.
		remoteDir := makeTestRepo(t)

		// Create a commit and get its full SHA
		fullSHA := makeTestCommit(t, remoteDir, "initial commit")

		// Clone the repo by fullSHA
		wt1, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      fullSHA,
		})
		require.NoError(t, err)
		defer wt1.Close(t.Context())

		// Verify that the head in cloneDir is correct.
		clonedSHA := getTestRepoHead(t, wt1.WorktreeDir())
		assert.Equal(t, fullSHA, clonedSHA)

		// Create a new commit in the "remote".
		newCommitSHA := makeTestCommit(t, remoteDir, "second commit")

		// Run the clone again, still targeting the first SHA.
		wt2, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      fullSHA,
		})
		require.NoError(t, err)
		defer wt2.Close(t.Context())

		// The clone should still have the original fullSHA as its HEAD...
		clonedSHA2 := getTestRepoHead(t, wt2.WorktreeDir())
		assert.Equal(t, fullSHA, clonedSHA2)

		// And we can be sure that the clone operation didn't do a fetch if the second commit, `newCommitSHA`, isn't present:
		cmd := exec.Command("git", "-C", wt2.WorktreeDir(), "log", newCommitSHA)
		output, err := cmd.CombinedOutput()
		require.Error(t, err)
		errorOutput := strings.TrimSpace(string(output))
		assert.Contains(t, errorOutput, "bad object") // eg. "fatal: bad object f543870e42c0a04594770e8ecca8d259a35fa627"
	})

	t.Run("Refetches Tag Fast-Forward", func(t *testing.T) {
		cacheDir := t.TempDir()

		// Create a local repo that will act as the remote to be cloned.
		remoteDir := makeTestRepo(t)

		// Create a tag
		fullSHA := makeTestCommit(t, remoteDir, "initial commit")
		makeTestTag(t, remoteDir, fullSHA, "tag-1")

		// Clone the repo by tag
		wt1, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "tag-1",
		})
		require.NoError(t, err)
		defer wt1.Close(t.Context())

		// Verify that the head in cloneDir is correct.
		clonedSHA := getTestRepoHead(t, wt1.WorktreeDir())
		assert.Equal(t, fullSHA, clonedSHA)

		// Create a new commit in the "remote", and move the tag
		newCommitSHA := makeTestCommit(t, remoteDir, "second commit")
		makeTestTag(t, remoteDir, newCommitSHA, "tag-1")

		// Run the clone again
		wt2, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "tag-1",
		})
		require.NoError(t, err)
		defer wt2.Close(t.Context())

		// The clone should be updated to the new tag ref
		clonedSHA = getTestRepoHead(t, wt2.WorktreeDir())
		assert.Equal(t, newCommitSHA, clonedSHA)
	})

	t.Run("Refetches Tag Force-Push", func(t *testing.T) {
		cacheDir := t.TempDir()

		// Create a local repo that will act as the remote to be cloned.
		remoteDir := makeTestRepo(t)

		// Create a couple commits and then a tag; the history will be used to create a force-push situation.
		commit1 := makeTestCommit(t, remoteDir, "commit 1")
		commit2 := makeTestCommit(t, remoteDir, "commit 2")
		makeTestTag(t, remoteDir, commit2, "tag-2")

		// Clone the repo by tag
		wt1, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "tag-2",
		})
		require.NoError(t, err)
		defer wt1.Close(t.Context())

		// Verify that the head in cloneDir is correct.
		clonedSHA := getTestRepoHead(t, wt1.WorktreeDir())
		assert.Equal(t, commit2, clonedSHA)

		// Do a `git reset` to revert the remoteDir back to the initial commit, then add a new commit, then move the tag
		// to it.
		require.NoError(t, gitCmd("-C", remoteDir, "reset", "--hard", commit1))
		commit3 := makeTestCommit(t, remoteDir, "commit 3")
		require.NotEqual(t, commit1, commit3) // seems like dumb assertions but just safety checks for the test
		require.NotEqual(t, commit2, commit3)
		makeTestTag(t, remoteDir, commit3, "tag-2")

		// Run the clone again
		wt2, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "tag-2",
		})
		require.NoError(t, err)
		defer wt2.Close(t.Context())

		// The clone should be updated to the new tag ref
		clonedSHA = getTestRepoHead(t, wt2.WorktreeDir())
		assert.Equal(t, commit3, clonedSHA)
	})

	t.Run("Refetches Branch Fast-Forward", func(t *testing.T) {
		cacheDir := t.TempDir()

		// Create a local repo that will act as the remote to be cloned.
		remoteDir := makeTestRepo(t)

		// Create a commit on main
		fullSHA := makeTestCommit(t, remoteDir, "initial commit")

		// Clone the repo by branch, main
		wt1, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "main",
		})
		require.NoError(t, err)
		defer wt1.Close(t.Context())

		// Verify that the head in cloneDir is correct
		clonedSHA := getTestRepoHead(t, wt1.WorktreeDir())
		assert.Equal(t, fullSHA, clonedSHA)

		// Create a new commit in the "remote", moving the branch forward
		newCommitSHA := makeTestCommit(t, remoteDir, "second commit")

		// Run the clone again
		wt2, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "main",
		})
		require.NoError(t, err)
		defer wt2.Close(t.Context())

		// The clone should be updated to the new branch ref
		clonedSHA = getTestRepoHead(t, wt2.WorktreeDir())
		assert.Equal(t, newCommitSHA, clonedSHA)
	})

	t.Run("Refetches Branch Force-Push", func(t *testing.T) {
		cacheDir := t.TempDir()

		// Create a local repo that will act as the remote to be cloned.
		remoteDir := makeTestRepo(t)

		// Create a couple commits on the main branch; the history will be used to create a force-push situation.
		commit1 := makeTestCommit(t, remoteDir, "commit 1")
		commit2 := makeTestCommit(t, remoteDir, "commit 2")

		// Clone the repo by branch, main
		wt1, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "main",
		})
		require.NoError(t, err)
		defer wt1.Close(t.Context())

		// Verify that the head in cloneDir is correct.
		clonedSHA := getTestRepoHead(t, wt1.WorktreeDir())
		assert.Equal(t, commit2, clonedSHA)

		// Do a `git reset` to revert the remoteDir back to the initial commit, then add a new commit, moving `main` in
		// a non-fast-forward way.
		require.NoError(t, gitCmd("-C", remoteDir, "reset", "--hard", commit1))
		commit3 := makeTestCommit(t, remoteDir, "commit 3")
		require.NotEqual(t, commit1, commit3) // seems like dumb assertions but just safety checks for the test
		require.NotEqual(t, commit2, commit3)

		// Run the clone again
		wt2, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "main",
		})
		require.NoError(t, err)
		defer wt2.Close(t.Context())

		// The clone should be updated to the new tag ref
		clonedSHA = getTestRepoHead(t, wt2.WorktreeDir())
		assert.Equal(t, commit3, clonedSHA)
	})

	t.Run("Performs update to resolve newly added branch", func(t *testing.T) {
		cacheDir := t.TempDir()

		// Create a local repo that will act as the remote to be cloned.
		remoteDir := makeTestRepo(t)

		// Create some commits
		commit1 := makeTestCommit(t, remoteDir, "commit 1")
		commit2 := makeTestCommit(t, remoteDir, "commit 2")

		// Clone the repo
		wt1, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "main",
		})
		require.NoError(t, err)
		defer wt1.Close(t.Context())

		// Verify that the head in cloneDir is correct.
		clonedSHA := getTestRepoHead(t, wt1.WorktreeDir())
		assert.Equal(t, commit2, clonedSHA)

		// Create a branch that points to commit1
		makeTestBranch(t, remoteDir, commit1, "test-branch")

		// Clone test-branch
		wt2, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "test-branch",
		})
		require.NoError(t, err)
		defer wt2.Close(t.Context())

		// The clone should point to commit1 as we should now be on `test-branch`
		clonedSHA = getTestRepoHead(t, wt2.WorktreeDir())
		assert.Equal(t, commit1, clonedSHA)
	})

	t.Run("Does not create spurious refs", func(t *testing.T) {
		cacheDir := t.TempDir()

		// Create a local repo that will act as the remote to be cloned.
		remoteDir := makeTestRepo(t)

		// Create a tag
		fullSHA := makeTestCommit(t, remoteDir, "initial commit")
		makeTestTag(t, remoteDir, fullSHA, "tag-1")

		// Clone the repo by tag
		wt1, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "tag-1",
		})
		require.NoError(t, err)
		defer wt1.Close(t.Context())

		// Verify that the head in cloneDir is correct.
		clonedSHA := getTestRepoHead(t, wt1.WorktreeDir())
		assert.Equal(t, fullSHA, clonedSHA)

		// Verify no spurious branches were created during cloning due to an invalid refspec.
		branches := getTestRepoBranches(t, wt1.WorktreeDir())
		assert.Equal(t, []string{"* (no branch)", "+ main"}, branches)

		// Verify no spurious tags were created during cloning due to an invalid refspec.
		tags := getTestRepoTags(t, wt1.WorktreeDir())
		assert.Equal(t, []string{"tag-1"}, tags)
	})

	t.Run("Removes non-empty clone target directory", func(t *testing.T) {
		cacheDir := t.TempDir()

		// Create a local repo that will act as the remote to be cloned.
		remoteDir := makeTestRepo(t)

		// `git clone` is happy to clone into empty directories. Add a file to the directory that the repository will be
		// cloned to prevent it from succeeding.
		remoteDirHash := common.Sha256(remoteDir)
		repoDir := filepath.Join(cacheDir, remoteDirHash[:2], remoteDirHash[2:])
		err := os.MkdirAll(repoDir, 0o755)
		require.NoError(t, err)

		obstacleFilePath := filepath.Join(repoDir, ".test.txt")
		err = os.WriteFile(obstacleFilePath, []byte("Lorem ipsum"), 0o644)
		require.NoError(t, err)

		// Create some commits
		_ = makeTestCommit(t, remoteDir, "commit 1")
		commit2 := makeTestCommit(t, remoteDir, "commit 2")

		// Clone the repo
		wt1, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      "main",
		})
		require.NoError(t, err)
		defer wt1.Close(t.Context())

		// Verify that the repository was cloned successfully.
		clonedSHA := getTestRepoHead(t, wt1.WorktreeDir())
		assert.Equal(t, commit2, clonedSHA)

		// Ensure that the obstacle file was removed.
		_, err = os.Stat(obstacleFilePath)
		assert.Error(t, err)
	})
}

func makeTestRepo(t *testing.T) string {
	t.Helper()
	repoPath := t.TempDir()
	require.NoError(t, gitCmd("-C", repoPath, "init", "--initial-branch=main"))
	require.NoError(t, gitCmd("-C", repoPath, "config", "user.name", "test"))
	require.NoError(t, gitCmd("-C", repoPath, "config", "user.email", "test@test.com"))
	return repoPath
}

func makeTestCommit(t *testing.T, repoPath, comment string) string {
	t.Helper()
	require.NoError(t, gitCmd("-C", repoPath, "commit", "--allow-empty", "-m", comment))
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	output, err := cmd.Output()
	require.NoError(t, err)
	fullSHA := strings.TrimSpace(string(output))
	return fullSHA
}

func makeTestTag(t *testing.T, repoPath, commitSHA, tag string) {
	t.Helper()
	require.NoError(t, gitCmd("-C", repoPath, "tag", "--force", tag, commitSHA))
}

func makeTestBranch(t *testing.T, repoPath, commitSHA, branchName string) {
	t.Helper()
	require.NoError(t, gitCmd("-C", repoPath, "branch", branchName, commitSHA))
}

func objectExists(t *testing.T, repoPath, object string) bool {
	t.Helper()

	cmd := exec.Command("git", "-C", repoPath, "cat-file", "-e", object)
	_, err := cmd.Output()

	return err == nil
}

func getTestRepoHead(t *testing.T, repoPath string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	output, err := cmd.Output()
	require.NoError(t, err)
	clonedSHA := strings.TrimSpace(string(output))
	return clonedSHA
}

func getTestRepoBranches(t *testing.T, repoPath string) []string {
	t.Helper()

	cmd := exec.Command("git", "-C", repoPath, "branch")
	output, err := cmd.Output()
	require.NoError(t, err)

	var branches []string
	for line := range strings.Lines(string(output)) {
		branches = append(branches, strings.TrimSpace(line))
	}

	return branches
}

func getTestRepoTags(t *testing.T, repoPath string) []string {
	t.Helper()

	cmd := exec.Command("git", "-C", repoPath, "tag")
	output, err := cmd.Output()
	require.NoError(t, err)

	var tags []string
	for line := range strings.Lines(string(output)) {
		tags = append(tags, strings.TrimSpace(line))
	}

	return tags
}

func gitConfig() {
	if os.Getenv("GITHUB_ACTIONS") == "true" {
		var err error
		if err = gitCmd("config", "--global", "user.email", "test@test.com"); err != nil {
			log.Error(err)
		}
		if err = gitCmd("config", "--global", "user.name", "Unit Test"); err != nil {
			log.Error(err)
		}
	}
}

func gitCmd(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if exitError, ok := err.(*exec.ExitError); ok {
		if waitStatus, ok := exitError.Sys().(syscall.WaitStatus); ok {
			return fmt.Errorf("Exit error %d", waitStatus.ExitStatus())
		}
		return exitError
	}
	return nil
}

func TestCloneIfRequired(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	tempDir := t.TempDir()
	ctx := t.Context()

	t.Run("clone", func(t *testing.T) {
		err := cloneIfRequired(t.Context(), CloneInput{
			URL: "https://github.com/actions/checkout",
		}, common.Logger(ctx), tempDir)
		assert.NoError(t, err)
	})

	t.Run("clone different remote", func(t *testing.T) {
		err := cloneIfRequired(t.Context(), CloneInput{
			URL: "https://github.com/actions/setup-go",
		}, common.Logger(ctx), tempDir)
		require.NoError(t, err)

		remoteURL, err := getRemoteURL(t.Context(), tempDir, "origin")
		require.NoError(t, err)
		assert.Equal(t, "https://github.com/actions/setup-go", remoteURL)
	})
}

func TestClone_UsesTokenForHTTPAuth_NoInteractivePrompt(t *testing.T) {
	// Make sure git will not try to interactively prompt (and hang the test).
	t.Setenv("GIT_TERMINAL_PROMPT", "0")

	token := "test-token-value"

	var sawTokenAuth atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")

		// Expect: Authorization: Basic base64("<user>:<token>")
		if strings.HasPrefix(auth, "Basic ") {
			raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, "Basic "))
			if err == nil && strings.HasSuffix(string(raw), ":"+token) {
				sawTokenAuth.Store(true)
				// We don't serve a real git backend; just fail after proving auth was sent.
				w.WriteHeader(http.StatusNotFound)
				return
			}
		}

		// Challenge for Basic auth.
		w.Header().Set("WWW-Authenticate", `Basic realm="forgejo-runner-test"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	// Any path is fine; useHttpPath=true makes git include the path in credential lookup.
	url := srv.URL + "/org/repo"

	_, err := Clone(t.Context(), CloneInput{
		CacheDir: t.TempDir(),
		URL:      url,
		Ref:      "main",
		Token:    token,
	})
	require.Error(t, err)

	// The whole point: token must be used (git should send Authorization), and it must not try to prompt.
	assert.True(t, sawTokenAuth.Load(), "expected git to send Authorization header when Token is provided")

	// These are the typical symptoms of the bug when credentials are not picked up.
	assert.NotContains(t, err.Error(), "could not read Username")
	assert.NotContains(t, err.Error(), "terminal prompts disabled")
}

func TestResolveHead(t *testing.T) {
	t.Run("on created repo", func(t *testing.T) {
		remoteDir := makeTestRepo(t)

		fullSHA := makeTestCommit(t, remoteDir, "initial commit")

		short, sha, err := ResolveHead(t.Context(), remoteDir)
		require.NoError(t, err)
		assert.Equal(t, fullSHA, sha)
		assert.Equal(t, fullSHA[:7], short)
	})

	t.Run("on cloned repo", func(t *testing.T) {
		cacheDir := t.TempDir()
		remoteDir := makeTestRepo(t)

		fullSHA := makeTestCommit(t, remoteDir, "initial commit")

		wt, err := Clone(t.Context(), CloneInput{
			CacheDir: cacheDir,
			URL:      remoteDir,
			Ref:      fullSHA,
		})
		require.NoError(t, err)
		defer wt.Close(t.Context())

		short, sha, err := ResolveHead(t.Context(), wt.WorktreeDir())
		require.NoError(t, err)
		assert.Equal(t, fullSHA, sha)
		assert.Equal(t, fullSHA[:7], short)
	})
}

func TestDescribeHeadOnClone(t *testing.T) {
	cacheDir := t.TempDir()
	remoteDir := makeTestRepo(t)

	fullSHA := makeTestCommit(t, remoteDir, "initial commit")
	makeTestTag(t, remoteDir, fullSHA, "tag-1")

	wt, err := Clone(t.Context(), CloneInput{
		CacheDir: cacheDir,
		URL:      remoteDir,
		Ref:      fullSHA,
	})
	require.NoError(t, err)
	defer wt.Close(t.Context())

	ref, err := DescribeHead(t.Context(), wt.WorktreeDir())
	require.NoError(t, err)
	assert.Equal(t, "refs/tags/tag-1", ref)
}

func TestResolveRevision(t *testing.T) {
	repoPath := makeTestRepo(t)

	t.Run("fails-without-commits", func(t *testing.T) {
		commitID, err := ResolveRevision(t.Context(), repoPath, "HEAD")
		assert.Empty(t, commitID)
		assert.ErrorContains(t, err, "could not determine the commit ID of HEAD: fatal: ambiguous argument 'HEAD':")
	})

	t.Run("resolves-head", func(t *testing.T) {
		fullSHA := makeTestCommit(t, repoPath, "initial commit")
		commitID, err := ResolveRevision(t.Context(), repoPath, "HEAD")
		require.NoError(t, err)
		assert.Equal(t, fullSHA, commitID)
	})

	t.Run("resolves-tag", func(t *testing.T) {
		fullSHA := makeTestCommit(t, repoPath, "initial commit")
		makeTestTag(t, repoPath, fullSHA, "tag-1")

		commitID, err := ResolveRevision(t.Context(), repoPath, "tag-1")
		require.NoError(t, err)
		assert.Equal(t, fullSHA, commitID)
	})

	t.Run("resolves-branch", func(t *testing.T) {
		fullSHA := makeTestCommit(t, repoPath, "initial commit")
		makeTestBranch(t, repoPath, fullSHA, "branch-1")

		commitID, err := ResolveRevision(t.Context(), repoPath, "branch-1")
		require.NoError(t, err)
		assert.Equal(t, fullSHA, commitID)
	})

	t.Run("resolves-commit-id-to-itself", func(t *testing.T) {
		fullSHA := makeTestCommit(t, repoPath, "initial commit")

		commitID, err := ResolveRevision(t.Context(), repoPath, fullSHA)
		require.NoError(t, err)
		assert.Equal(t, fullSHA, commitID)
	})
}

func TestFetch(t *testing.T) {
	cloneDir := t.TempDir()
	remoteDir := makeTestRepo(t)

	commitOne := makeTestCommit(t, remoteDir, "Create first commit")

	err := gitCmd("clone", "--bare", remoteDir, cloneDir)
	require.NoError(t, err)

	t.Run("Remote is mandatory", func(t *testing.T) {
		input := FetchInput{
			repoPath: cloneDir,
			remote:   "",
			refspec:  "",
		}
		err := Fetch(t.Context(), input)
		assert.ErrorContains(t, err, "mandatory argument remote is empty")
	})

	t.Run("Refspec is optional", func(t *testing.T) {
		assert.True(t, objectExists(t, cloneDir, commitOne))

		commitTwo := makeTestCommit(t, remoteDir, "Create second commit")

		assert.False(t, objectExists(t, cloneDir, commitTwo))

		input := FetchInput{
			repoPath: cloneDir,
			remote:   "origin",
			refspec:  "",
		}
		err := Fetch(t.Context(), input)
		require.NoError(t, err)

		assert.True(t, objectExists(t, cloneDir, commitTwo))
	})

	t.Run("Fetch with refspec", func(t *testing.T) {
		headAfterClone := getTestRepoHead(t, cloneDir)
		assert.Equal(t, commitOne, headAfterClone)

		// Add another commit that can be fast-forwarded.
		commitTwo := makeTestCommit(t, remoteDir, "Create second commit")

		// Without having fetched the latest changes, HEAD should still point to the first commit.
		headBeforeFetch := getTestRepoHead(t, cloneDir)
		assert.Equal(t, commitOne, headBeforeFetch)

		input := FetchInput{
			repoPath: cloneDir,
			remote:   "origin",
			refspec:  "+refs/*:refs/*",
		}
		err := Fetch(t.Context(), input)
		require.NoError(t, err)

		// Ensure that HEAD was moved forward.
		headAfterFetch := getTestRepoHead(t, cloneDir)
		assert.Equal(t, commitTwo, headAfterFetch)
	})
}
