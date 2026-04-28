package filecollector

import (
	"archive/tar"
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"syscall"
	"testing"

	"github.com/go-git/go-billy/v5/helper/polyfill"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gotest.tools/v3/skip"
)

func TestIgnoredTrackedfile(t *testing.T) {
	tempDir := t.TempDir()

	fs := osfs.New(tempDir)

	require.NoError(t, fs.MkdirAll("mygitrepo/.git", 0o777))
	dotgit, _ := fs.Chroot("mygitrepo/.git")
	worktree, _ := fs.Chroot("mygitrepo")
	repo, _ := git.Init(filesystem.NewStorage(dotgit, cache.NewObjectLRUDefault()), worktree)
	f, _ := worktree.Create(".gitignore")
	_, _ = f.Write([]byte(".*\n"))
	require.NoError(t, f.Close())
	// This file shouldn't be in the tar
	f, _ = worktree.Create(".env")
	_, _ = f.Write([]byte("test=val1\n"))
	require.NoError(t, f.Close())
	w, _ := repo.Worktree()
	// .gitignore is in the tar after adding it to the index
	_, _ = w.Add(".gitignore")

	tmpTar, _ := fs.Create("temp.tar")
	tw := tar.NewWriter(tmpTar)
	ps, _ := gitignore.ReadPatterns(worktree, []string{})
	ignorer := gitignore.NewMatcher(ps)
	fc := &FileCollector{
		Ignorer:   ignorer,
		SrcPath:   filepath.Join(tempDir, "mygitrepo"),
		SrcPrefix: filepath.Join(tempDir, "mygitrepo") + string(filepath.Separator),
		Handler: &TarCollector{
			TarWriter: tw,
		},
	}
	err := filepath.Walk(filepath.Join(tempDir, "mygitrepo"), fc.CollectFiles(t.Context(), []string{}))
	require.NoError(t, err, "successfully collect files")
	require.NoError(t, tw.Close())
	_, _ = tmpTar.Seek(0, io.SeekStart)
	tr := tar.NewReader(tmpTar)
	h, err := tr.Next()
	require.NoError(t, err, "tar must not be empty")
	assert.Equal(t, ".gitignore", h.Name)
	_, err = tr.Next()
	require.ErrorIs(t, err, io.EOF, "tar must only contain one element")
	require.NoError(t, tmpTar.Close())
}

func TestSymlinks(t *testing.T) {
	tempDir := t.TempDir()

	fs := osfs.New(tempDir)
	require.NoError(t, fs.MkdirAll("mygitrepo/.git", 0o777))

	dotgit, _ := fs.Chroot("mygitrepo/.git")
	worktree, _ := fs.Chroot("mygitrepo")
	repo, _ := git.Init(filesystem.NewStorage(dotgit, cache.NewObjectLRUDefault()), worktree)

	// This file shouldn't be in the tar
	f, err := worktree.Create(".env")
	require.NoError(t, err)
	_, err = f.Write([]byte("test=val1\n"))
	require.NoError(t, err)
	require.NoError(t, f.Close())
	err = worktree.Symlink(".env", "test.env")
	require.NoError(t, err)
	w, err := repo.Worktree()
	require.NoError(t, err)

	// .gitignore is in the tar after adding it to the index
	_, err = w.Add(".env")
	require.NoError(t, err)
	_, err = w.Add("test.env")
	require.NoError(t, err)

	tmpTar, _ := fs.Create("temp.tar")
	tw := tar.NewWriter(tmpTar)

	ps, err := gitignore.ReadPatterns(worktree, []string{})
	require.NoError(t, err)

	fc := &FileCollector{
		Ignorer:   gitignore.NewMatcher(ps),
		SrcPath:   filepath.Join(tempDir, "mygitrepo"),
		SrcPrefix: filepath.Join(tempDir, "mygitrepo") + string(filepath.Separator),
		Handler: &TarCollector{
			TarWriter: tw,
		},
	}
	err = filepath.Walk(filepath.Join(tempDir, "mygitrepo"), fc.CollectFiles(t.Context(), []string{}))
	require.NoError(t, err, "successfully collect files")
	require.NoError(t, tw.Close())

	_, err = tmpTar.Seek(0, io.SeekStart)
	require.NoError(t, err)

	tr := tar.NewReader(tmpTar)
	h, err := tr.Next()
	files := map[string]tar.Header{}
	for err == nil {
		files[h.Name] = *h
		h, err = tr.Next()
	}

	assert.Equal(t, ".env", files[".env"].Name)
	assert.Equal(t, "test.env", files["test.env"].Name)
	assert.Equal(t, ".env", files["test.env"].Linkname)
	require.ErrorIs(t, err, io.EOF, "tar must be read cleanly to EOF")
	require.NoError(t, tmpTar.Close())
}

func TestFileCollector_CollectFiles(t *testing.T) {
	t.Run("with ignored gitignore", func(t *testing.T) {
		src := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(src, "one.txt"), []byte("1"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "two.txt"), []byte("2"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, ".gitignore"), []byte("two*\n"), 0o644))

		dst := t.TempDir()

		fc := &FileCollector{
			Ignorer:   nil,
			SrcPath:   src,
			SrcPrefix: src + string(filepath.Separator),
			Handler: &CopyCollector{
				DstDir: dst,
			},
		}

		err := filepath.Walk(src, fc.CollectFiles(t.Context(), []string{}))
		assert.NoError(t, err)

		expectFileContents := func(path, expected string) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, expected, string(data))
		}

		expectFileContents(filepath.Join(dst, "one.txt"), "1")
		expectFileContents(filepath.Join(dst, "two.txt"), "2")
		expectFileContents(filepath.Join(dst, ".gitignore"), "two*\n")
	})

	t.Run("with gitignore", func(t *testing.T) {
		src := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(src, "one.txt"), []byte("1"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "two.txt"), []byte("2"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, ".gitignore"), []byte("two*\n"), 0o644))

		dst := t.TempDir()

		ps, err := gitignore.ReadPatterns(polyfill.New(osfs.New(src)), []string{})
		require.NoError(t, err)

		fc := &FileCollector{
			Ignorer:   gitignore.NewMatcher(ps),
			SrcPath:   src,
			SrcPrefix: src + string(filepath.Separator),
			Handler: &CopyCollector{
				DstDir: dst,
			},
		}

		err = filepath.Walk(src, fc.CollectFiles(t.Context(), []string{}))
		assert.NoError(t, err)

		expectFileContents := func(path, expected string) {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, expected, string(data))
		}

		expectFileContents(filepath.Join(dst, "one.txt"), "1")
		assert.NoFileExists(t, filepath.Join(dst, "two.txt"))
		expectFileContents(filepath.Join(dst, ".gitignore"), "two*\n")
	})

	t.Run("with subdirectories", func(t *testing.T) {
		src := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(src, "a", "b", "c"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(src, "one.txt"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "a", "b", "c", "two.txt"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "a", "b", "c", "three.txt"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "a", "b", "two.txt"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, ".gitignore"), []byte("**/two.txt\n"), 0o644))

		dst := t.TempDir()

		ps, err := gitignore.ReadPatterns(polyfill.New(osfs.New(src)), []string{})
		require.NoError(t, err)

		fc := &FileCollector{
			Ignorer:   gitignore.NewMatcher(ps),
			SrcPath:   src,
			SrcPrefix: src + string(filepath.Separator),
			Handler: &CopyCollector{
				DstDir: dst,
			},
		}

		err = filepath.Walk(src, fc.CollectFiles(t.Context(), []string{}))
		assert.NoError(t, err)

		assert.FileExists(t, filepath.Join(dst, "one.txt"))
		assert.NoFileExists(t, filepath.Join(dst, "a", "b", "c", "two.txt"))
		assert.FileExists(t, filepath.Join(dst, "a", "b", "c", "three.txt"))
		assert.NoFileExists(t, filepath.Join(dst, "a", "b", "two.txt"))
	})

	t.Run("with gitignore in subdirectory", func(t *testing.T) {
		src := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(src, "a", "b", "c"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(src, "one.txt"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "a", "b", "c", "two.txt"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "a", "b", "c", "three.txt"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "a", "b", "two.txt"), nil, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "a", "b", ".gitignore"), []byte("two.txt\n"), 0o644))

		dst := t.TempDir()

		ps, err := gitignore.ReadPatterns(polyfill.New(osfs.New(src)), []string{})
		require.NoError(t, err)

		fc := &FileCollector{
			Ignorer:   gitignore.NewMatcher(ps),
			SrcPath:   src,
			SrcPrefix: src + string(filepath.Separator),
			Handler: &CopyCollector{
				DstDir: dst,
			},
		}

		err = filepath.Walk(src, fc.CollectFiles(t.Context(), []string{}))
		assert.NoError(t, err)

		assert.FileExists(t, filepath.Join(dst, "one.txt"))
		assert.NoFileExists(t, filepath.Join(dst, "a", "b", "c", "two.txt"))
		assert.FileExists(t, filepath.Join(dst, "a", "b", "c", "three.txt"))
		assert.FileExists(t, filepath.Join(dst, "a", "b", ".gitignore"))
		assert.NoFileExists(t, filepath.Join(dst, "a", "b", "two.txt"))
	})

	t.Run("retains UNIX permissions", func(t *testing.T) {
		skip.If(t, runtime.GOOS == "windows")

		src := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(src, "one.txt"), nil, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(src, "two.txt"), nil, 0o755))

		dst := t.TempDir()

		fc := &FileCollector{
			Ignorer:   nil,
			SrcPath:   src,
			SrcPrefix: src + string(filepath.Separator),
			Handler: &CopyCollector{
				DstDir: dst,
			},
		}

		err := filepath.Walk(src, fc.CollectFiles(t.Context(), []string{}))
		assert.NoError(t, err)

		infoOne, err := os.Stat(filepath.Join(dst, "one.txt"))
		assert.NoError(t, err)
		assert.EqualValues(t, os.FileMode(0o700), infoOne.Mode().Perm())

		infoTwo, err := os.Stat(filepath.Join(dst, "two.txt"))
		assert.NoError(t, err)
		assert.EqualValues(t, os.FileMode(0o755), infoTwo.Mode().Perm())
	})

	t.Run("Git repository with submodule matching ignore pattern", func(t *testing.T) {
		submoduleRepo := makeTestRepo(t)
		require.NoError(t, os.WriteFile(filepath.Join(submoduleRepo, "one.txt"), []byte("1\n"), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(submoduleRepo, "a", "b"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(submoduleRepo, "a", "b", "two.txt"), []byte("2\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(submoduleRepo, "a", "b", "three.txt"), []byte("3\n"), 0o644))
		require.NoError(t, gitCmd("-C", submoduleRepo, "add", "--all"))
		require.NoError(t, gitCmd("-C", submoduleRepo, "commit", "-m", "Import"))

		src := makeTestRepo(t)

		require.NoError(t, gitCmd("-C", src, "-c", "protocol.file.allow=always", "submodule", "add", submoduleRepo, "test-submodule-\U0001fae3"))
		require.NoError(t, os.WriteFile(filepath.Join(src, ".gitignore"), []byte("test-submodule-\U0001fae3\n**/two.txt\n"), 0o644))
		require.NoError(t, gitCmd("-C", src, "add", "--all"))
		require.NoError(t, gitCmd("-C", src, "commit", "-m", "Import"))

		dst := t.TempDir()

		ps, err := gitignore.ReadPatterns(polyfill.New(osfs.New(src)), []string{})
		require.NoError(t, err)

		fc := &FileCollector{
			Ignorer:   gitignore.NewMatcher(ps),
			SrcPath:   src,
			SrcPrefix: src + string(filepath.Separator),
			Handler: &CopyCollector{
				DstDir: dst,
			},
		}

		err = filepath.Walk(src, fc.CollectFiles(t.Context(), []string{}))
		assert.NoError(t, err)

		assert.DirExists(t, filepath.Join(dst, ".git"))
		assert.NoFileExists(t, filepath.Join(dst, "test-submodule-\U0001fae3", ".git"))
		assert.NoDirExists(t, filepath.Join(dst, "test-submodule-\U0001fae3", ".git"))
		assert.FileExists(t, filepath.Join(dst, ".gitignore"))
		assert.FileExists(t, filepath.Join(dst, "test-submodule-\U0001fae3", "one.txt"))
		assert.FileExists(t, filepath.Join(dst, "test-submodule-\U0001fae3", "a", "b", "two.txt"))
		assert.FileExists(t, filepath.Join(dst, "test-submodule-\U0001fae3", "a", "b", "three.txt"))
	})

	t.Run("Git repository with funky files and paths", func(t *testing.T) {
		src := makeTestRepo(t)

		// U+1fae3 is the "Face with Peeking Eye" emoji, U+1f600 is "Grinning Face".
		require.NoError(t, os.Mkdir(filepath.Join(src, "å"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(src, "å", "\U0001fae3.txt"), []byte("1"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, "å", "\U0001f600.txt"), []byte("2"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(src, ".gitignore"), []byte("å/\U0001f600.txt\n"), 0o644))
		require.NoError(t, gitCmd("-C", src, "add", "--all"))
		require.NoError(t, gitCmd("-C", src, "commit", "-m", "Import"))

		dst := t.TempDir()

		ps, err := gitignore.ReadPatterns(polyfill.New(osfs.New(src)), []string{})
		require.NoError(t, err)

		fc := &FileCollector{
			Ignorer:   gitignore.NewMatcher(ps),
			SrcPath:   src,
			SrcPrefix: src + string(filepath.Separator),
			Handler: &CopyCollector{
				DstDir: dst,
			},
		}

		err = filepath.Walk(src, fc.CollectFiles(t.Context(), []string{}))
		assert.NoError(t, err)

		assert.DirExists(t, filepath.Join(dst, ".git"))
		assert.FileExists(t, filepath.Join(dst, "å", "\U0001fae3.txt"))
		assert.FileExists(t, filepath.Join(src, "å", "\U0001f600.txt"))
		assert.NoFileExists(t, filepath.Join(dst, "å", "\U0001f600.txt"))
		assert.FileExists(t, filepath.Join(dst, ".gitignore"))
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

func gitCmd(args ...string) error {
	_, err := gitCmdWithStdout(args...)
	return err
}

func gitCmdWithStdout(args ...string) ([]byte, error) {
	var stdoutBuffer bytes.Buffer
	stdout := bufio.NewWriter(&stdoutBuffer)

	cmd := exec.Command("git", args...)
	cmd.Stdout = stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	if exitError, ok := err.(*exec.ExitError); ok {
		if waitStatus, ok := exitError.Sys().(syscall.WaitStatus); ok {
			return nil, fmt.Errorf("Exit error %d", waitStatus.ExitStatus())
		}
		return nil, exitError
	}

	return stdoutBuffer.Bytes(), nil
}
