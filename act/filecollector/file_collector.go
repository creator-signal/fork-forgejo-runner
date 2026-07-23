package filecollector

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"code.forgejo.org/forgejo/runner/v13/act/common/git"
	"code.forgejo.org/forgejo/runner/v13/act/common/gitignore"
	log "github.com/sirupsen/logrus"
)

type Handler interface {
	WriteFile(path string, fi fs.FileInfo, linkName string, f io.Reader) error
}

type TarCollector struct {
	TarWriter *tar.Writer
	UID       int
	GID       int
	DstDir    string
}

func (tc TarCollector) WriteFile(fpath string, fi fs.FileInfo, linkName string, f io.Reader) error {
	// create a new dir/file header
	header, err := tar.FileInfoHeader(fi, linkName)
	if err != nil {
		return err
	}

	// update the name to correctly reflect the desired destination when untaring
	header.Name = path.Join(tc.DstDir, fpath)
	header.Mode = int64(fi.Mode())
	header.ModTime = fi.ModTime()
	header.Uid = tc.UID
	header.Gid = tc.GID

	// write the header
	if err := tc.TarWriter.WriteHeader(header); err != nil {
		return err
	}

	// this is a symlink no reader provided
	if f == nil {
		return nil
	}

	// copy file data into tar writer
	if _, err := io.Copy(tc.TarWriter, f); err != nil {
		return err
	}
	return nil
}

type CopyCollector struct {
	DstDir string
}

func (cc *CopyCollector) WriteFile(fpath string, fi fs.FileInfo, linkName string, f io.Reader) error {
	fdestpath := filepath.Join(cc.DstDir, fpath)
	if err := os.MkdirAll(filepath.Dir(fdestpath), 0o777); err != nil {
		return err
	}
	if linkName != "" {
		return os.Symlink(linkName, fdestpath)
	}
	df, err := os.OpenFile(fdestpath, os.O_CREATE|os.O_WRONLY, fi.Mode())
	if err != nil {
		return err
	}
	defer df.Close()
	if _, err := io.Copy(df, f); err != nil {
		return err
	}
	return nil
}

type FileCollector struct {
	Ignorer   gitignore.Matcher
	SrcPath   string
	SrcPrefix string
	Handler   Handler
}

func (fc *FileCollector) CollectFiles(ctx context.Context, submodulePath []string) filepath.WalkFunc {
	// By only reading Git indices in the root directory, Git repositories in nested directories are ignored. It is
	// unclear whether that's correct because that behaviour was inherited from act.
	rootDirectory := filepath.Join(fc.SrcPath, filepath.Join(submodulePath...))
	index, err := readGitIndex(ctx, rootDirectory)
	if err != nil {
		log.Debugf("An error occurred while reading the Git index of %q: %s", rootDirectory, err)
	}

	return func(file string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if ctx != nil {
			select {
			case <-ctx.Done():
				return errors.New("copying cancelled")
			default:
			}
		}

		pathWithoutPrefix := strings.TrimPrefix(file, fc.SrcPrefix)
		pathComponentsWithoutPrefix := strings.Split(pathWithoutPrefix, string(filepath.Separator))

		// If a Git repository is stored in `/a/b` and file is `/a/b/c/hello.txt`, then we need `c/hello.txt` because
		// that's the path stored in the Git index.
		pathRelativeToRoot := strings.TrimPrefix(file, rootDirectory+string(filepath.Separator))

		// The Git index always operates with forward slashes, even on Windows. Therefore, the backslashes used by
		// Windows have to be converted to forward slashes.
		objectMode, isTracked := index[filepath.ToSlash(pathRelativeToRoot)]
		isSubmodule := objectMode == "160000"
		if isTracked && isSubmodule {
			// Recurse into Git submodules because the submodule has to be interpreted using its own Git index.
			err = filepath.Walk(file, fc.CollectFiles(ctx, strings.Split(pathRelativeToRoot, string(filepath.Separator))))
			if err != nil {
				return err
			}
			return filepath.SkipDir
		}

		// Because this function is used to either collect files in normal directories or in Git repositories, it
		// cannot rely on Git's interpretation of `.gitignore` alone. Therefore, if a file isn't tracked by Git, the
		// externally supplied `gitignore.Matcher` is used to skip *untracked* files.
		if !isTracked && fc.Ignorer != nil && fc.Ignorer.Match(pathComponentsWithoutPrefix, fi.IsDir()) {
			return nil
		}
		targetPath := filepath.ToSlash(pathWithoutPrefix)

		if fi.Mode()&os.ModeSymlink == os.ModeSymlink {
			linkName, err := os.Readlink(file)
			if err != nil {
				return fmt.Errorf("unable to readlink '%s': %w", file, err)
			}
			return fc.Handler.WriteFile(targetPath, fi, linkName, nil)
		} else if !fi.Mode().IsRegular() {
			return nil
		}

		// open file
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		defer f.Close()

		if ctx != nil {
			// make io.Copy cancellable by closing the file
			cpctx, cpfinish := context.WithCancel(ctx)
			defer cpfinish()
			go func() {
				select {
				case <-cpctx.Done():
				case <-ctx.Done():
					f.Close()
				}
			}()
		}

		return fc.Handler.WriteFile(targetPath, fi, "", f)
	}
}

func readGitIndex(ctx context.Context, path string) (map[string]string, error) {
	// 0x1f is the ASCII code for the Unit separator (https://www.lammertbies.nl/comm/info/ascii-characters#us)
	cmd := exec.CommandContext(ctx, "git", "-C", path, "ls-files", "-z", "--format=%(objectmode)%x1f%(path)")

	output, err := git.RunWithOutput(cmd)
	if err != nil {
		return nil, fmt.Errorf("could not read index of Git repository %q: %w", path, err)
	}

	trimmedOutput := strings.TrimSpace(string(output))
	indexLines := strings.Split(trimmedOutput, string(rune(0x00)))
	index := make(map[string]string, len(indexLines))

	for _, line := range indexLines {
		mode, relativePath, found := strings.Cut(line, string(rune(0x1f)))
		if !found {
			continue
		}
		index[relativePath] = mode
	}
	return index, nil
}
