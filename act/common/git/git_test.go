package git

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFindGitSlug(t *testing.T) {
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

		provider, slug, err := findGitSlug(tt.url, instance)

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

func TestFindGitRemoteURL(t *testing.T) {
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

	u, err := findGitRemoteURL(t.Context(), basedir, "origin")
	assert.NoError(err)
	assert.Equal(remoteURL, u)

	remoteURL = "git@github.com/AwesomeOwner/MyAwesomeRepo.git"
	err = gitCmd("-C", basedir, "remote", "add", "upstream", remoteURL)
	assert.NoError(err)
	u, err = findGitRemoteURL(t.Context(), basedir, "upstream")
	assert.NoError(err)
	assert.Equal(remoteURL, u)
}

func TestGitFindRef(t *testing.T) {
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
			ref, err := FindGitRef(t.Context(), dir)
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
				wt.Close()
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
		defer wt1.Close()

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
		defer wt2.Close()

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
		defer wt1.Close()

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
		defer wt2.Close()

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
		defer wt1.Close()

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
		defer wt2.Close()

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
		defer wt1.Close()

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
		defer wt2.Close()

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
		defer wt1.Close()

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
		defer wt2.Close()

		// The clone should be updated to the new tag ref
		clonedSHA = getTestRepoHead(t, wt2.WorktreeDir())
		assert.Equal(t, commit3, clonedSHA)
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

func getTestRepoHead(t *testing.T, repoPath string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", repoPath, "rev-parse", "HEAD")
	output, err := cmd.Output()
	require.NoError(t, err)
	clonedSHA := strings.TrimSpace(string(output))
	return clonedSHA
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
	tempDir := t.TempDir()
	ctx := t.Context()

	t.Run("clone", func(t *testing.T) {
		repo, err := cloneIfRequired(ctx, "refs/heads/main", CloneInput{
			URL: "https://github.com/actions/checkout",
		}, common.Logger(ctx), tempDir)
		assert.NoError(t, err)
		assert.NotNil(t, repo)
	})

	t.Run("clone different remote", func(t *testing.T) {
		repo, err := cloneIfRequired(ctx, "refs/heads/main", CloneInput{
			URL: "https://github.com/actions/setup-go",
		}, common.Logger(ctx), tempDir)
		require.NoError(t, err)
		require.NotNil(t, repo)

		remote, err := repo.Remote("origin")
		require.NoError(t, err)
		require.Len(t, remote.Config().URLs, 1)
		assert.Equal(t, "https://github.com/actions/setup-go", remote.Config().URLs[0])
	})
}

func TestFindGitRevision(t *testing.T) {
	t.Run("on created repo", func(t *testing.T) {
		remoteDir := makeTestRepo(t)

		fullSHA := makeTestCommit(t, remoteDir, "initial commit")

		short, sha, err := FindGitRevision(t.Context(), remoteDir)
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
		defer wt.Close()

		short, sha, err := FindGitRevision(t.Context(), wt.WorktreeDir())
		require.NoError(t, err)
		assert.Equal(t, fullSHA, sha)
		assert.Equal(t, fullSHA[:7], short)
		runtime.GC() // release file locks held by go-git on Windows
	})
}

func TestFindGitRefOnClone(t *testing.T) {
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
	defer wt.Close()

	ref, err := FindGitRef(t.Context(), wt.WorktreeDir())
	require.NoError(t, err)
	assert.Equal(t, "refs/tags/tag-1", ref)
	runtime.GC() // release file locks held by go-git on Windows
}
