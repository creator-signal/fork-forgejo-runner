package container

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/djherbis/buffer"
	"github.com/djherbis/nio/v3"
	"github.com/go-git/go-billy/v5/helper/polyfill"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
	"golang.org/x/term"

	"code.forgejo.org/forgejo/runner/v12/act/common"
	"code.forgejo.org/forgejo/runner/v12/act/filecollector"
	"code.forgejo.org/forgejo/runner/v12/act/lookpath"
)

// FirecrackerVMInterface allows the runner package to pass in a VM instance.
type FirecrackerVMInterface interface {
	Destroy(ctx context.Context) error
	LogStats(ctx context.Context) // Log resource usage stats (memory, CPU) if available
}

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
	// Firecracker fields
	Firecracker         bool
	FirecrackerHost     string                 // SSH host - VM's IP (e.g., "172.16.0.2")
	FirecrackerHostIP   string                 // Host's TAP interface IP - gateway for VM (e.g., "172.16.0.1")
	FirecrackerPort     string                 // SSH port (e.g., "22")
	FirecrackerKey      string                 // Path to SSH private key
	FirecrackerVMPath   string                 // Working directory path inside the VM (e.g., "/workspace")
	FirecrackerVM       FirecrackerVMInterface // VM instance for cleanup
	FirecrackerMemoryMB int                    // Memory acquired from scheduler (for release on cleanup)
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
		if e.GetFirecracker() {
			// For Firecracker: write files locally, then SCP to VM
			tmpDir := filepath.Join(e.Root, ".fc-copy")
			if err := os.MkdirAll(tmpDir, 0o755); err != nil {
				return err
			}
			for _, f := range files {
				localPath := filepath.Join(tmpDir, f.Name)
				if err := os.MkdirAll(filepath.Dir(localPath), 0o777); err != nil {
					return err
				}
				if err := os.WriteFile(localPath, []byte(f.Body), fs.FileMode(f.Mode)); err != nil { //nolint:gosec
					return err
				}
			}
			// Create destination directory in VM
			if err := e.sshMkdir(ctx, destPath); err != nil {
				return fmt.Errorf("mkdir in VM: %w", err)
			}
			// SCP files to VM
			for _, f := range files {
				localPath := filepath.Join(tmpDir, f.Name)
				vmPath := filepath.Join(destPath, f.Name)
				// Ensure parent directory exists in VM
				if err := e.sshMkdir(ctx, filepath.Dir(vmPath)); err != nil {
					return fmt.Errorf("mkdir parent in VM: %w", err)
				}
				if err := e.scpToVM(ctx, localPath, vmPath); err != nil {
					return fmt.Errorf("copy %s to VM: %w", f.Name, err)
				}
			}
			return nil
		}
		// Default: write directly to host filesystem (works for LXC with bind mounts and bare host)
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

		if e.GetFirecracker() {
			// For Firecracker: copy to temp directory first, then SCP to VM
			tmpDir := filepath.Join(e.Root, ".fc-copydir")
			if err := os.RemoveAll(tmpDir); err != nil {
				return err
			}
			if err := os.MkdirAll(tmpDir, 0o755); err != nil {
				return err
			}

			// Use filecollector to copy locally first (respects .gitignore)
			fc := &filecollector.FileCollector{
				Fs:        &filecollector.DefaultFs{},
				Ignorer:   ignorer,
				SrcPath:   srcPath,
				SrcPrefix: srcPrefix,
				Handler: &filecollector.CopyCollector{
					DstDir: tmpDir,
				},
			}
			if err := filepath.Walk(srcPath, fc.CollectFiles(ctx, []string{})); err != nil {
				return err
			}

			// Create destination directory in VM and SCP the collected files
			if err := e.sshMkdir(ctx, destPath); err != nil {
				return fmt.Errorf("mkdir in VM: %w", err)
			}
			// SCP the contents of tmpDir to destPath
			// Use "/." suffix to copy contents, not the directory itself
			return e.scpToVM(ctx, tmpDir+"/.", destPath)
		}

		// Default: copy directly to host filesystem
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
	srcPath = filepath.Clean(srcPath)

	// For Firecracker: SCP files from VM to local temp, then create archive
	if e.GetFirecracker() {
		tmpDir := filepath.Join(e.Root, ".fc-archive")
		if err := os.RemoveAll(tmpDir); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(tmpDir, 0o755); err != nil {
			return nil, err
		}

		localPath := filepath.Join(tmpDir, filepath.Base(srcPath))
		if err := e.scpFromVM(ctx, srcPath, localPath); err != nil {
			return nil, fmt.Errorf("scp from VM: %w", err)
		}
		srcPath = localPath // Continue with local copy
	}

	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	defer tw.Close()

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

func copyPtyOutput(writer io.Writer, master io.Reader, finishLog context.CancelFunc) {
	// LXC had a behaviour which permitted short writes to the PTY to cause discarded data, which is fixed upstream in
	// https://github.com/lxc/lxc/pull/4633.  As of writing, this isn't released for our Debian LXC images.  Until it
	// is, we have a partial workaround to reduce the risk of data loss.
	//
	// Writing to `writer` can be relatively slow; when forgejo-runner is in daemon mode then `writer` is `lineWriter`
	// which will split the contents up line-by-line and call a lineHandler, which will send the output to a logger,
	// which will end up in `Reporter` which acquires a mutex for each line received in order to append the line to its
	// internal buffers.  Experimentally, when using an LXC command and PTY configuration, if a command outputs a large
	// log chunk (~500kB), a straight `io.Copy()` between `master` and `reader` will end up with data being lost in
	// chunks in random places in the log -- sometimes the middle, sometimes the end.
	//
	// Introducing a memory buffer in forgejo-runner helps to address this problem.  We read as fast as possible in a
	// dedicated goroutine into the buffer, attempting to keep the PTY buffer clear and ready for writes from the
	// subcommand.  Concurrently, we drain that buffer into `writer`.
	//
	// There's no limit to the buffer size that could be required to get this right and always guarantee all data.  A 2
	// MB buffer was sufficient to meet the needs of reproduction test cases, but this has been bumped up to 100 MB for
	// anticipated real-world use cases.  `buffer.New(x)` is allocated on-demand, so 100 MB is a maximum buffer size.

	pipeReader, pipeWriter := nio.Pipe(buffer.New(100 * 1024 * 1024))
	var wg sync.WaitGroup
	wg.Go(func() {
		// Error is expected -- "read /dev/ptmx: input/output error" is the typical exit for io.Copy here.
		_, _ = io.Copy(pipeWriter, master)
		pipeWriter.Close()
	})
	wg.Go(func() {
		_, _ = io.Copy(writer, pipeReader)
	})
	wg.Wait()

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

	// For Firecracker, the working directory is inside the VM, so skip the host stat check
	if !e.GetFirecracker() {
		if stat, err := os.Stat(wd); err != nil {
			return fmt.Errorf("failed to stat working directory %s %w", wd, err)
		} else if !stat.IsDir() {
			return fmt.Errorf("working directory %s is not a directory", wd)
		}
	}

	command := make([]string, len(commandparam))
	copy(command, commandparam)

	if e.GetFirecracker() {
		common.Logger(ctx).Debugf("execute in Firecracker VM %v via SSH: %v", e.Name, command)

		// Rewrite ACTIONS_CACHE_URL to use the host's TAP interface IP (gateway)
		// so the VM can reach the cache proxy running on the host.
		// The original URL uses the host's public IP which may not be reachable
		// from inside the VM due to hairpin NAT issues.
		if cacheURL, ok := env["ACTIONS_CACHE_URL"]; ok && e.FirecrackerHostIP != "" {
			if u, err := url.Parse(cacheURL); err == nil {
				// Replace the host while preserving the port
				_, port, _ := net.SplitHostPort(u.Host)
				if port != "" {
					u.Host = net.JoinHostPort(e.FirecrackerHostIP, port)
				} else {
					u.Host = e.FirecrackerHostIP
				}
				env["ACTIONS_CACHE_URL"] = u.String()
				common.Logger(ctx).Debugf("Firecracker: rewrote ACTIONS_CACHE_URL to %s", env["ACTIONS_CACHE_URL"])
			}
		}

		// Set TERM=dumb to match GitHub Actions behavior.
		// This disables interactive progress bars and spinners in tools like mise,
		// npm, cargo, etc.
		if _, ok := env["TERM"]; !ok {
			env["TERM"] = "dumb"
		}

		// Build env command arguments for all environment variables.
		// We use the `env` command instead of bash `export` because GitHub Actions
		// creates INPUT_<param-name> variables that may contain hyphens
		// (e.g., INPUT_KEEP-STATE), which are not valid bash variable names
		// but can be passed via the `env` command.
		var envArgs strings.Builder
		for k, v := range env {
			// Shell-quote both key and value to handle special characters
			escapedK := strings.ReplaceAll(k, "'", "'\"'\"'")
			escapedV := strings.ReplaceAll(v, "'", "'\"'\"'")
			envArgs.WriteString(fmt.Sprintf("'%s=%s' ", escapedK, escapedV))
		}

		// Use VM-local working directory if set, otherwise use Workdir
		// The host path (e.g., /var/lib/forgejo-runner/act/<id>/hostexecutor) doesn't exist inside the VM
		vmWorkdir := e.FirecrackerVMPath
		if vmWorkdir == "" {
			vmWorkdir = e.Workdir // fallback to configured workdir
		}
		// If workdir is specified and is relative, append it to the VM workspace
		if workdir != "" && !filepath.IsAbs(workdir) {
			vmWorkdir = filepath.Join(vmWorkdir, workdir)
		}

		// Build the remote command with working directory and env-wrapped command
		var remoteCmd string
		if envArgs.Len() > 0 {
			remoteCmd = fmt.Sprintf("cd '%s' && env %s%s",
				strings.ReplaceAll(vmWorkdir, "'", "'\"'\"'"),
				envArgs.String(),
				strings.Join(command, " "))
		} else {
			remoteCmd = fmt.Sprintf("cd '%s' && %s",
				strings.ReplaceAll(vmWorkdir, "'", "'\"'\"'"),
				strings.Join(command, " "))
		}

		// Build SSH command
		sshUser := "root"
		if user != "" && user != "root" {
			sshUser = user
		}
		command = []string{
			"/usr/bin/ssh",
			"-i", e.FirecrackerKey,
			"-p", e.FirecrackerPort,
			"-o", "StrictHostKeyChecking=no",
			"-o", "UserKnownHostsFile=/dev/null",
			"-o", "LogLevel=ERROR",
			"-T", // Disable pseudo-terminal allocation for CI
			fmt.Sprintf("%s@%s", sshUser, e.FirecrackerHost),
			remoteCmd,
		}
		// Clear env for SSH command - env is passed in the remote command
		envList = nil
		// SSH runs on the host, so use a valid host directory
		// The working directory inside the VM is handled by 'cd' in remoteCmd
		wd = "/"
	} else if e.GetLXC() {
		if user == "root" {
			command = append([]string{"/usr/bin/sudo"}, command...)
		} else {
			common.Logger(ctx).Debugf("lxc-attach --name %v %v", e.Name, command)
			command = append([]string{"/usr/bin/sudo", "--preserve-env", "--preserve-env=PATH", "/usr/bin/lxc-attach", "--keep-env", "--name", e.Name, "--"}, command...)
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
		go copyPtyOutput(e.StdOut, master, finishLog)
	} else {
		finishLog()
	}

	// Don't immediately return error if the command fails -- closing the pty and ensuring all data is flushed through
	// to the logs needs to occur in the command error case.
	runCmdErr := runCmdInGroup(cmd, cmdline, master != nil)

	if slave != nil {
		_ = slave.Close()
	}
	<-logctx.Done()

	if runCmdErr != nil {
		return fmt.Errorf("RUN %w", runCmdErr)
	}
	return nil
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
		if e.GetLXC() || e.GetFirecracker() {
			// there may be files owned by root: removal
			// is the responsibility of the LXC/Firecracker backend
			return nil
		}
		return os.RemoveAll(e.Root)
	}
}

func (e *HostEnvironment) ToContainerPath(path string) string {
	// For Firecracker, use VM-local paths instead of host paths
	if e.GetFirecracker() && e.FirecrackerVMPath != "" {
		if filepath.Clean(e.Workdir) == filepath.Clean(path) {
			return e.FirecrackerVMPath
		}
		// For paths under Workdir, compute relative path and join with VMPath
		if bp, err := filepath.Rel(e.Workdir, path); err == nil && !strings.HasPrefix(bp, "..") {
			return filepath.Join(e.FirecrackerVMPath, bp)
		}
		return e.FirecrackerVMPath
	}

	// Original logic for non-Firecracker
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

func (e *HostEnvironment) GetFirecracker() bool {
	return e.Firecracker
}

// scpToVM copies a file or directory from the host to the Firecracker VM.
func (e *HostEnvironment) scpToVM(ctx context.Context, src, dst string) error {
	args := []string{
		"-i", e.FirecrackerKey,
		"-P", e.FirecrackerPort,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-r",
		src,
		fmt.Sprintf("root@%s:%s", e.FirecrackerHost, dst),
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/scp", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp to VM failed: %w: %s", err, output)
	}
	return nil
}

// scpFromVM copies a file or directory from the Firecracker VM to the host.
func (e *HostEnvironment) scpFromVM(ctx context.Context, src, dst string) error {
	args := []string{
		"-i", e.FirecrackerKey,
		"-P", e.FirecrackerPort,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		"-r",
		fmt.Sprintf("root@%s:%s", e.FirecrackerHost, src),
		dst,
	}
	cmd := exec.CommandContext(ctx, "/usr/bin/scp", args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("scp from VM failed: %w: %s", err, output)
	}
	return nil
}

// sshMkdir creates a directory in the Firecracker VM.
func (e *HostEnvironment) sshMkdir(ctx context.Context, path string) error {
	args := []string{
		"-i", e.FirecrackerKey,
		"-p", e.FirecrackerPort,
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		fmt.Sprintf("root@%s", e.FirecrackerHost),
		fmt.Sprintf("mkdir -p '%s'", strings.ReplaceAll(path, "'", "'\\''")),
	}
	return exec.CommandContext(ctx, "/usr/bin/ssh", args...).Run()
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
