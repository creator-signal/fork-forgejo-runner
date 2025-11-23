package container

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// Type assert HostEnvironment implements ExecutionsEnvironment
var _ ExecutionsEnvironment = &HostEnvironment{}

func TestCopyDir(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()
	e := &HostEnvironment{
		Path:      filepath.Join(dir, "path"),
		TmpDir:    filepath.Join(dir, "tmp"),
		ToolCache: filepath.Join(dir, "tool_cache"),
		ActPath:   filepath.Join(dir, "act_path"),
		StdOut:    os.Stdout,
		Workdir:   path.Join("testdata", "scratch"),
	}
	_ = os.MkdirAll(e.Path, 0o700)
	_ = os.MkdirAll(e.TmpDir, 0o700)
	_ = os.MkdirAll(e.ToolCache, 0o700)
	_ = os.MkdirAll(e.ActPath, 0o700)
	err := e.CopyDir(e.Workdir, e.Path, true)(ctx)
	assert.NoError(t, err)
}

func TestGetContainerArchive(t *testing.T) {
	dir := t.TempDir()
	ctx := t.Context()
	e := &HostEnvironment{
		Path:      filepath.Join(dir, "path"),
		TmpDir:    filepath.Join(dir, "tmp"),
		ToolCache: filepath.Join(dir, "tool_cache"),
		ActPath:   filepath.Join(dir, "act_path"),
		StdOut:    os.Stdout,
		Workdir:   path.Join("testdata", "scratch"),
	}
	_ = os.MkdirAll(e.Path, 0o700)
	_ = os.MkdirAll(e.TmpDir, 0o700)
	_ = os.MkdirAll(e.ToolCache, 0o700)
	_ = os.MkdirAll(e.ActPath, 0o700)
	expectedContent := []byte("sdde/7sh")
	err := os.WriteFile(filepath.Join(e.Path, "action.yml"), expectedContent, 0o600)
	assert.NoError(t, err)
	archive, err := e.GetContainerArchive(ctx, e.Path)
	assert.NoError(t, err)
	defer archive.Close()
	reader := tar.NewReader(archive)
	h, err := reader.Next()
	assert.NoError(t, err)
	assert.Equal(t, "action.yml", h.Name)
	content, err := io.ReadAll(reader)
	assert.NoError(t, err)
	assert.Equal(t, expectedContent, content)
	_, err = reader.Next()
	assert.ErrorIs(t, err, io.EOF)
}

func TestCancelLongRunningCommand(t *testing.T) {
	dir := t.TempDir()

	var argv []string
	var expectedExit string
	if runtime.GOOS == "windows" {
		path := filepath.Join(dir, "evil.ps1")
		contents := `Start-Job -ScriptBlock { while ($true) { sleep 1 } }
Start-Process -FilePath 'powershell' -ArgumentList '-Command','while ($true) {echo hi}'
while ($true) { sleep 1 }
		`
		powershellPath, err := exec.LookPath("pwsh")
		if err != nil {
			powershellPath, err = exec.LookPath("powershell")
			if err != nil {
				powershellPath = "powershell"
			}
		}
		_ = os.WriteFile(path, []byte(contents), 0o700)
		argv = []string{powershellPath, "-File", path}
		expectedExit = "exit status 1"
	} else {
		path := filepath.Join(dir, "evil.sh")
		contents := `#!/bin/sh
nohup yes &
yes | cat &
mkfifo tmp_fifo; cat <tmp_fifo >tmp_fifo &
sh -c 'while true; do true; done' &
while true; do sleep 1; done
		`
		_ = os.WriteFile(path, []byte(contents), 0o700)
		argv = []string{path}
		expectedExit = "signal: killed"
	}

	e := &HostEnvironment{
		Path:      dir,
		TmpDir:    dir,
		ToolCache: dir,
		ActPath:   dir,
		StdOut:    io.Discard,
		Workdir:   dir,
	}

	ctx, cancel := context.WithCancel(t.Context())

	// Cancel the process after it's had some time to get going
	go func() {
		time.Sleep(500 * time.Millisecond)
		cancel()
	}()

	err := e.Exec(argv, map[string]string{}, "", dir)(ctx)
	assert.Error(t, err, fmt.Errorf("this step has been cancelled: ctx: context canceled, exec: RUN %s", expectedExit))
}
