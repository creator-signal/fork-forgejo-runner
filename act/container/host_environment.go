package container

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/helper/polyfill"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	log "github.com/sirupsen/logrus"
	"golang.org/x/term"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/filecollector"
	"code.forgejo.org/forgejo/runner/v12/act/lookpath"
)

type HostEnvironment struct {
	Name      string
	Path      string
	TmpDir    string
	ToolCache string
	Workdir   string
	ActPath   string
	Root      string
	StdOut    io.Writer
	LXC       bool
	LXCPID    string
}

func (e *HostEnvironment) Create(_, _ []string) common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (e *HostEnvironment) ConnectToNetwork(name string) common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (e *HostEnvironment) Close() common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (e *HostEnvironment) Copy(destPath string, files ...*FileEntry) common.Executor {
	return func(ctx context.Context) error {
		for _, f := range files {
			if err := os.MkdirAll(filepath.Dir(filepath.Join(destPath, f.Name)), 0o777); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(destPath, f.Name), []byte(f.Body), fs.FileMode(f.Mode)); err != nil { //nolint:gosec
				return err
			}
		}
		return nil
	}
}

func (e *HostEnvironment) CopyTarStream(ctx context.Context, destPath string, tarStream io.Reader) error {
	if err := os.RemoveAll(destPath); err != nil {
		return err
	}
	tr := tar.NewReader(tarStream)
	cp := &filecollector.CopyCollector{
		DstDir: destPath,
	}
	for {
		ti, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		} else if err != nil {
			return err
		}
		if ti.FileInfo().IsDir() {
			continue
		}
		if ctx.Err() != nil {
			return fmt.Errorf("CopyTarStream has been cancelled")
		}
		if err := cp.WriteFile(ti.Name, ti.FileInfo(), ti.Linkname, tr); err != nil {
			return err
		}
	}
}

func (e *HostEnvironment) CopyDir(destPath, srcPath string, useGitIgnore bool) common.Executor {
	return func(ctx context.Context) error {
		logger := common.Logger(ctx)
		srcPrefix := filepath.Dir(srcPath)
		if !strings.HasSuffix(srcPrefix, string(filepath.Separator)) {
			srcPrefix += string(filepath.Separator)
		}
		logger.Debugf("Stripping prefix:%s src:%s", srcPrefix, srcPath)
		var ignorer gitignore.Matcher
		if useGitIgnore {
			ps, err := gitignore.ReadPatterns(polyfill.New(osfs.New(srcPath)), nil)
			if err != nil {
				logger.Debugf("Error loading .gitignore: %v", err)
			}

			ignorer = gitignore.NewMatcher(ps)
		}
		fc := &filecollector.FileCollector{
			Fs:        &filecollector.DefaultFs{},
			Ignorer:   ignorer,
			SrcPath:   srcPath,
			SrcPrefix: srcPrefix,
			Handler: &filecollector.CopyCollector{
				DstDir: destPath,
			},
		}
		return filepath.Walk(srcPath, fc.CollectFiles(ctx, []string{}))
	}
}

func (e *HostEnvironment) GetContainerArchive(ctx context.Context, srcPath string) (io.ReadCloser, error) {
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	defer tw.Close()
	srcPath = filepath.Clean(srcPath)
	fi, err := os.Lstat(srcPath)
	if err != nil {
		return nil, err
	}
	tc := &filecollector.TarCollector{
		TarWriter: tw,
	}
	if fi.IsDir() {
		srcPrefix := srcPath
		if !strings.HasSuffix(srcPrefix, string(filepath.Separator)) {
			srcPrefix += string(filepath.Separator)
		}
		fc := &filecollector.FileCollector{
			Fs:        &filecollector.DefaultFs{},
			SrcPath:   srcPath,
			SrcPrefix: srcPrefix,
			Handler:   tc,
		}
		err = filepath.Walk(srcPath, fc.CollectFiles(ctx, []string{}))
		if err != nil {
			return nil, err
		}
	} else {
		var f io.ReadCloser
		var linkname string
		if fi.Mode()&fs.ModeSymlink != 0 {
			linkname, err = os.Readlink(srcPath)
			if err != nil {
				return nil, err
			}
		} else {
			f, err = os.Open(srcPath)
			if err != nil {
				return nil, err
			}
			defer f.Close()
		}
		err := tc.WriteFile(fi.Name(), fi, linkname, f)
		if err != nil {
			return nil, err
		}
	}
	return io.NopCloser(buf), nil
}

func (e *HostEnvironment) Pull(_ bool) common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func (e *HostEnvironment) Start(_ bool) common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

type localEnv struct {
	env map[string]string
}

func (l *localEnv) Getenv(name string) string {
	if runtime.GOOS == "windows" {
		for k, v := range l.env {
			if strings.EqualFold(name, k) {
				return v
			}
		}
		return ""
	}
	return l.env[name]
}

func lookupPathHost(cmd string, env map[string]string, writer io.Writer) (string, error) {
	f, err := lookpath.LookPath2(cmd, &localEnv{env: env})
	if err != nil {
		err := "Cannot find: " + fmt.Sprint(cmd) + " in PATH"
		if _, _err := writer.Write([]byte(err + "\n")); _err != nil {
			return "", fmt.Errorf("%v: %w", err, _err)
		}
		return "", errors.New(err)
	}
	return f, nil
}

func setupPty(cmd *exec.Cmd) (*os.File, *os.File, error) {
	master, slave, err := openPty()
	if err != nil {
		return nil, nil, err
	}
	if term.IsTerminal(int(slave.Fd())) {
		_, err := term.MakeRaw(int(slave.Fd()))
		if err != nil {
			master.Close()
			slave.Close()
			return nil, nil, err
		}
	}
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave
	return master, slave, nil
}

func copyPtyOutput(writer io.Writer, master io.Reader, finishLog context.CancelFunc, logger log.FieldLogger) {
	_, err := io.Copy(writer, master)
	if err != nil {
		var pathErr *fs.PathError
		// Typically io.Copy ends with an error reading /dev/ptmx, as a pty doesn't EOF like a normal file.
		// This error specifically can be suppressed.
		if !errors.As(err, &pathErr) || pathErr.Op != "read" || pathErr.Path != "/dev/ptmx" {
			logger.Errorf("unexpected error handling command output: %w", err)
		}
	}
	finishLog()
}

func (e *HostEnvironment) UpdateFromImageEnv(_ *map[string]string) common.Executor {
	return func(ctx context.Context) error {
		return nil
	}
}

func getEnvListFromMap(env map[string]string) []string {
	envList := make([]string, 0)
	for k, v := range env {
		envList = append(envList, fmt.Sprintf("%s=%s", k, v))
	}
	return envList
}

func (e *HostEnvironment) exec(ctx context.Context, commandparam []string, cmdline string, env map[string]string, user, workdir string) error {
	envList := getEnvListFromMap(env)
	var wd string
	if workdir != "" {
		if filepath.IsAbs(workdir) {
			wd = workdir
		} else {
			wd = filepath.Join(e.Path, workdir)
		}
	} else {
		wd = e.Path
	}

	if stat, err := os.Stat(wd); err != nil {
		return fmt.Errorf("failed to stat working directory %s %w", wd, err)
	} else if !stat.IsDir() {
		return fmt.Errorf("working directory %s is not a directory", wd)
	}

	command := make([]string, len(commandparam))
	copy(command, commandparam)

	if e.GetLXC() {
		if user == "root" {
			command = append([]string{"/usr/bin/sudo"}, command...)
		} else {
			common.Logger(ctx).Debugf("execute in LXC container %v: %v", e.Name, command)

			command = append([]string{
				"/usr/bin/sudo", "--preserve-env", "--preserve-env=PATH",
				"/usr/bin/nsenter",
				"--target", e.LXCPID,
				"--all",                      // enter all the same namespaces as the target process
				fmt.Sprintf("--wdns=%s", wd), // set the working directory inside the namespace
				"--",
				// We used to use lxc-attach, which would cause processes to be in the .lxc cgroup; to mirror that with
				// nsenter we start a shell and add our own PID ($$) to the .lxc cgroup.  `--join-cgroup` is an option
				// of nsenter but it joins the same group as the init process, which is /init.scope, and not the lxc
				// cgroup.
				"/usr/bin/bash",
				"-c",
				`echo $$ > /sys/fs/cgroup/.lxc/cgroup.procs 2>/dev/null || true; exec $@`,
				"--",
			}, command...)
		}
	}

	f, err := lookupPathHost(command[0], env, e.StdOut)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, f)
	cmd.Path = f
	cmd.Args = command
	cmd.Stdin = nil
	cmd.Stdout = e.StdOut
	cmd.Env = envList
	cmd.Stderr = e.StdOut
	cmd.Dir = wd

	var master *os.File
	var slave *os.File
	defer func() {
		if master != nil {
			master.Close()
		}
		if slave != nil {
			slave.Close()
		}
	}()
	if true /* allocate Terminal */ {
		var err error
		master, slave, err = setupPty(cmd)
		if err != nil {
			common.Logger(ctx).Debugf("Failed to setup Pty %v\n", err.Error())
		}
	}

	logctx, finishLog := context.WithCancel(context.Background())
	if master != nil {
		go copyPtyOutput(e.StdOut, master, finishLog, common.Logger(ctx))
	} else {
		finishLog()
	}

	if err := runCmdInGroup(cmd, cmdline, master != nil); err != nil {
		return fmt.Errorf("RUN %w", err)
	}

	if slave != nil {
		_ = slave.Close()
	}

	<-logctx.Done()

	return err
}

func (e *HostEnvironment) Exec(command []string /*cmdline string, */, env map[string]string, user, workdir string) common.Executor {
	return e.ExecWithCmdLine(command, "", env, user, workdir)
}

func (e *HostEnvironment) ExecWithCmdLine(command []string, cmdline string, env map[string]string, user, workdir string) common.Executor {
	return func(ctx context.Context) error {
		if err := e.exec(ctx, command, cmdline, env, user, workdir); err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("this step has been cancelled: ctx: %w, exec: %w", ctx.Err(), err)
			default:
				return err
			}
		}
		return nil
	}
}

func (e *HostEnvironment) UpdateFromEnv(srcPath string, env *map[string]string) common.Executor {
	return parseEnvFile(e, srcPath, env)
}

func (e *HostEnvironment) Remove() common.Executor {
	return func(ctx context.Context) error {
		if e.GetLXC() {
			// there may be files owned by root: removal
			// is the responsibility of the LXC backend
			return nil
		}
		return os.RemoveAll(e.Root)
	}
}

func (e *HostEnvironment) ToContainerPath(path string) string {
	if bp, err := filepath.Rel(e.Workdir, path); err != nil {
		return filepath.Join(e.Path, bp)
	} else if filepath.Clean(e.Workdir) == filepath.Clean(path) {
		return e.Path
	}
	return path
}

func (e *HostEnvironment) GetLXC() bool {
	return e.LXC
}

func (e *HostEnvironment) GetName() string {
	return e.Name
}

func (e *HostEnvironment) GetRoot() string {
	return e.Root
}

func (e *HostEnvironment) GetActPath() string {
	actPath := e.ActPath
	if runtime.GOOS == "windows" {
		actPath = strings.ReplaceAll(actPath, "\\", "/")
	}
	return actPath
}

func (*HostEnvironment) GetPathVariableName() string {
	switch runtime.GOOS {
	case "plan9":
		return "path"
	case "windows":
		return "Path" // Actually we need a case insensitive map
	}
	return "PATH"
}

func (e *HostEnvironment) DefaultPathVariable() string {
	v, _ := os.LookupEnv(e.GetPathVariableName())
	return v
}

func (*HostEnvironment) JoinPathVariable(paths ...string) string {
	return strings.Join(paths, string(filepath.ListSeparator))
}

// Reference for Arch values for runner.arch
// https://docs.github.com/en/actions/learn-github-actions/contexts#runner-context
func goArchToActionArch(arch string) string {
	archMapper := map[string]string{
		"amd64":   "X64",
		"x86_64":  "X64",
		"386":     "X86",
		"aarch64": "ARM64",
	}
	if arch, ok := archMapper[arch]; ok {
		return arch
	}
	return arch
}

func goOsToActionOs(os string) string {
	osMapper := map[string]string{
		"linux":   "Linux",
		"windows": "Windows",
		"darwin":  "macOS",
	}
	if os, ok := osMapper[os]; ok {
		return os
	}
	return os
}

func (e *HostEnvironment) GetRunnerContext(_ context.Context) map[string]any {
	return map[string]any{
		"os":         goOsToActionOs(runtime.GOOS),
		"arch":       goArchToActionArch(runtime.GOARCH),
		"temp":       e.TmpDir,
		"tool_cache": e.ToolCache,
	}
}

func (e *HostEnvironment) IsHealthy(ctx context.Context) (time.Duration, error) {
	return 0, nil
}

func (e *HostEnvironment) ReplaceLogWriter(stdout, _ io.Writer) (io.Writer, io.Writer) {
	org := e.StdOut
	e.StdOut = stdout
	return org, org
}

func (*HostEnvironment) IsEnvironmentCaseInsensitive() bool {
	return runtime.GOOS == "windows"
}
