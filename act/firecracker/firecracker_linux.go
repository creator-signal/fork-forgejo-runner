//go:build linux

// SPDX-License-Identifier: MIT

package firecracker

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/sys/unix"

	"code.forgejo.org/forgejo/runner/v12/act/common"
)

var warnReflinkOnce sync.Once

// VM represents a running Firecracker VM instance.
type VM struct {
	Name     string
	RootDir  string
	Config   Config
	SubnetID int
	TAPName  string
	GuestIP  string
	HostIP   string
	SSHKey   string
	PID      int

	// Jailer-specific state
	JailDir       string // Path to jail directory (set when using jailer)
	VMID          string // Unique VM ID for jailer
	CgroupVersion int    // Cgroup version (1 or 2) for stats collection

	// System interface for testability
	sys SystemInterface
}

// CgroupStats contains resource usage statistics from cgroups.
type CgroupStats struct {
	MemoryCurrentMB int64   // Current memory usage in MB
	MemoryPeakMB    int64   // Peak memory usage in MB
	CPUUsageSec     float64 // Total CPU time used in seconds
}

// SystemInterface abstracts system operations for testability.
type SystemInterface interface {
	// Network operations
	CreateTAP(name string) error
	DeleteTAP(name string) error
	SetIPAddress(device, address string) error
	SetLinkUp(device string) error
	EnableIPForward() error
	AddNATRule(subnet string) error
	DeleteNATRule(subnet string) error
	AddForwardRule(tapName, outInterface string) error
	DeleteForwardRule(tapName, outInterface string) error

	// File operations
	CopyFile(src, dst string) error
	CreateHardLink(src, dst string) error
	MoveFile(src, dst string) error
	MountLoop(image, mountpoint string) error
	Unmount(mountpoint string) error
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	MkdirAll(path string, perm os.FileMode) error
	RemoveAll(path string) error

	// Process operations
	StartProcess(ctx context.Context, name string, args []string, logFile string) (int, error)
	StartJailedProcess(ctx context.Context, cfg JailerStartConfig) (int, error)
	KillProcess(pid int) error

	// Subnet lock operations
	AllocateSubnetID() (int, error)
	ReleaseSubnetID(id int) error

	// System detection
	DetectCgroupVersion() int
}

// JailerStartConfig contains parameters for starting a jailed Firecracker process.
type JailerStartConfig struct {
	JailerBin      string
	FirecrackerBin string
	VMID           string
	ChrootBaseDir  string
	UID            int
	GID            int
	MemoryLimitMB  int
	CgroupVersion  int
	NetNS          string // Network namespace path (e.g., /proc/1/ns/net for host namespace)
}

// realSystem implements SystemInterface using actual system calls.
type realSystem struct{}

func (s *realSystem) CreateTAP(name string) error {
	cmd := exec.Command("ip", "tuntap", "add", "dev", name, "mode", "tap")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ip tuntap add %s: %w (output: %s)", name, err, output)
	}
	return nil
}

func (s *realSystem) DeleteTAP(name string) error {
	return exec.Command("ip", "link", "del", name).Run()
}

func (s *realSystem) SetIPAddress(device, address string) error {
	return exec.Command("ip", "addr", "add", address, "dev", device).Run()
}

func (s *realSystem) SetLinkUp(device string) error {
	return exec.Command("ip", "link", "set", device, "up").Run()
}

func (s *realSystem) EnableIPForward() error {
	return exec.Command("sysctl", "-w", "net.ipv4.ip_forward=1").Run()
}

func (s *realSystem) AddNATRule(subnet string) error {
	return exec.Command("iptables", "-t", "nat", "-A", "POSTROUTING",
		"-s", subnet, "!", "-d", "172.16.0.0/16", "-j", "MASQUERADE").Run()
}

func (s *realSystem) DeleteNATRule(subnet string) error {
	// Ignore errors - rule might not exist
	_ = exec.Command("iptables", "-t", "nat", "-D", "POSTROUTING",
		"-s", subnet, "!", "-d", "172.16.0.0/16", "-j", "MASQUERADE").Run()
	return nil
}

func (s *realSystem) AddForwardRule(tapName, outInterface string) error {
	// Allow outbound traffic from VM
	cmd1 := exec.Command("iptables", "-I", "FORWARD",
		"-i", tapName, "-o", outInterface, "-j", "ACCEPT")
	if output, err := cmd1.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables FORWARD %s->%s: %w (output: %s)", tapName, outInterface, err, output)
	}
	// Allow return traffic to VM
	cmd2 := exec.Command("iptables", "-I", "FORWARD",
		"-i", outInterface, "-o", tapName,
		"-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT")
	if output, err := cmd2.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables FORWARD %s->%s (return): %w (output: %s)", outInterface, tapName, err, output)
	}
	return nil
}

func (s *realSystem) DeleteForwardRule(tapName, outInterface string) error {
	// Ignore errors - rules might not exist
	_ = exec.Command("iptables", "-D", "FORWARD",
		"-i", tapName, "-o", outInterface, "-j", "ACCEPT").Run()
	_ = exec.Command("iptables", "-D", "FORWARD",
		"-i", outInterface, "-o", tapName,
		"-m", "state", "--state", "RELATED,ESTABLISHED", "-j", "ACCEPT").Run()
	return nil
}

func (s *realSystem) CopyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()

	// Try reflink (FICLONE ioctl) first - instant CoW copy on XFS/Btrfs
	err = unix.IoctlSetInt(int(dstFile.Fd()), unix.FICLONE, int(srcFile.Fd()))
	if err == nil {
		return nil
	}

	// Fall back to regular copy if reflinks not supported
	warnReflinkOnce.Do(func() {
		log.Printf("WARNING: Firecracker rootfs copy falling back to full copy (reflink not supported). " +
			"For better performance and disk usage, use a filesystem with reflink support (XFS or Btrfs).")
	})
	_, err = io.Copy(dstFile, srcFile)
	return err
}

func (s *realSystem) CreateHardLink(src, dst string) error {
	return os.Link(src, dst)
}

func (s *realSystem) MoveFile(src, dst string) error {
	return os.Rename(src, dst)
}

func (s *realSystem) MountLoop(image, mountpoint string) error {
	return exec.Command("mount", "-o", "loop", image, mountpoint).Run()
}

func (s *realSystem) Unmount(mountpoint string) error {
	return exec.Command("umount", mountpoint).Run()
}

func (s *realSystem) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (s *realSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (s *realSystem) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (s *realSystem) RemoveAll(path string) error {
	return os.RemoveAll(path)
}

func (s *realSystem) StartProcess(ctx context.Context, name string, args []string, logFile string) (int, error) {
	f, err := os.Create(logFile)
	if err != nil {
		return 0, err
	}

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = f
	cmd.Stderr = f

	if err := cmd.Start(); err != nil {
		f.Close()
		return 0, err
	}

	// Goroutine to wait on process exit and clean up resources.
	// This prevents zombie processes and ensures the log file is closed.
	go func() {
		_ = cmd.Wait() // Reaps the process, preventing zombie
		f.Close()      // Close log file after process exits
	}()

	return cmd.Process.Pid, nil
}

func (s *realSystem) KillProcess(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return proc.Kill()
}

func (s *realSystem) StartJailedProcess(ctx context.Context, cfg JailerStartConfig) (int, error) {
	// Build jailer command arguments
	args := []string{
		"--id", cfg.VMID,
		"--exec-file", cfg.FirecrackerBin,
		"--uid", fmt.Sprintf("%d", cfg.UID),
		"--gid", fmt.Sprintf("%d", cfg.GID),
		"--chroot-base-dir", cfg.ChrootBaseDir,
	}

	// Add cgroup configuration based on version
	args = append(args, "--cgroup-version", fmt.Sprintf("%d", cfg.CgroupVersion))
	if cfg.CgroupVersion == 2 {
		args = append(args, "--cgroup", fmt.Sprintf("memory.max=%dM", cfg.MemoryLimitMB))
	} else {
		// cgroup v1
		args = append(args, "--cgroup", fmt.Sprintf("memory.limit_in_bytes=%d", cfg.MemoryLimitMB*1024*1024))
	}

	// Keep using host network namespace (TAP devices are created there)
	if cfg.NetNS != "" {
		args = append(args, "--netns", cfg.NetNS)
	}

	// Daemonize so jailer returns immediately
	args = append(args, "--daemonize")

	// Separator for firecracker arguments
	args = append(args, "--")

	// Firecracker arguments (paths relative to chroot root)
	args = append(args, "--config-file", "/config.json")

	cmd := exec.CommandContext(ctx, cfg.JailerBin, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return 0, fmt.Errorf("jailer failed: %w (output: %s)", err, string(output))
	}

	// Read the PID from the jailer's pidfile
	// Jailer creates: {chroot_base}/firecracker/{vm_id}/root/firecracker.pid
	pidFile := filepath.Join(cfg.ChrootBaseDir, "firecracker", cfg.VMID, "root", "firecracker.pid")

	// Wait briefly for pidfile to appear (jailer daemonizes)
	var pid int
	for i := 0; i < 50; i++ { // 5 seconds max
		data, err := os.ReadFile(pidFile)
		if err == nil {
			if _, err := fmt.Sscanf(string(data), "%d", &pid); err == nil && pid > 0 {
				return pid, nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	return 0, fmt.Errorf("jailer started but no PID file found at %s", pidFile)
}

func (s *realSystem) DetectCgroupVersion() int {
	// Check if unified cgroup v2 hierarchy is mounted
	if _, err := os.Stat("/sys/fs/cgroup/cgroup.controllers"); err == nil {
		return 2
	}
	return 1
}

const subnetLockDir = "/tmp/fc-subnet-locks"

func (s *realSystem) AllocateSubnetID() (int, error) {
	if err := os.MkdirAll(subnetLockDir, 0o755); err != nil {
		return 0, err
	}

	for id := 1; id <= 254; id++ {
		lockFile := filepath.Join(subnetLockDir, fmt.Sprintf("subnet-%d", id))
		f, err := os.OpenFile(lockFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = fmt.Fprintf(f, "%d", os.Getpid())
			_ = f.Close()
			return id, nil
		}
	}
	return 0, fmt.Errorf("no available subnet IDs")
}

func (s *realSystem) ReleaseSubnetID(id int) error {
	lockFile := filepath.Join(subnetLockDir, fmt.Sprintf("subnet-%d", id))
	return os.Remove(lockFile)
}

// NewVM creates a new VM instance.
func NewVM(name, rootDir string, config Config) *VM {
	return &VM{
		Name:    name,
		RootDir: rootDir,
		Config:  config,
		sys:     &realSystem{},
	}
}

// NewVMWithSystem creates a VM with a custom SystemInterface (for testing).
func NewVMWithSystem(name, rootDir string, config Config, sys SystemInterface) *VM {
	return &VM{
		Name:    name,
		RootDir: rootDir,
		Config:  config,
		sys:     sys,
	}
}

// Create sets up the VM: allocates network, copies rootfs, injects SSH key.
func (vm *VM) Create(ctx context.Context) error {
	logger := common.Logger(ctx)

	// Allocate subnet
	subnetID, err := vm.sys.AllocateSubnetID()
	if err != nil {
		return fmt.Errorf("allocate subnet: %w", err)
	}
	vm.SubnetID = subnetID
	vm.TAPName = fmt.Sprintf("fctap%d", subnetID)
	vm.GuestIP = fmt.Sprintf("%s.%d.2", vm.Config.NetworkPrefix, subnetID)
	vm.HostIP = fmt.Sprintf("%s.%d.1", vm.Config.NetworkPrefix, subnetID)

	logger.Debugf("Firecracker: allocated subnet %d, guest IP %s, output_interface=%q", subnetID, vm.GuestIP, vm.Config.OutputInterface)

	// Create VM directory
	vmDir := filepath.Join(vm.RootDir, "firecracker")
	if err := vm.sys.MkdirAll(vmDir, 0o755); err != nil {
		return fmt.Errorf("create VM directory: %w", err)
	}

	// Setup network
	if err := vm.setupNetwork(ctx); err != nil {
		return fmt.Errorf("setup network: %w", err)
	}

	// Copy rootfs
	rootfsPath := filepath.Join(vmDir, "rootfs.ext4")
	if err := vm.sys.CopyFile(vm.Config.RootFSTemplate, rootfsPath); err != nil {
		return fmt.Errorf("copy rootfs: %w", err)
	}

	// Generate SSH key
	vm.SSHKey = filepath.Join(vmDir, "ssh_key")
	if err := vm.generateSSHKey(); err != nil {
		return fmt.Errorf("generate SSH key: %w", err)
	}

	// Inject SSH key and network config into rootfs
	if err := vm.configureRootFS(ctx, rootfsPath); err != nil {
		return fmt.Errorf("configure rootfs: %w", err)
	}

	// Write Firecracker config
	configPath := filepath.Join(vmDir, "config.json")
	configJSON := vm.BuildConfig(rootfsPath)
	if err := vm.sys.WriteFile(configPath, configJSON, 0o644); err != nil {
		return fmt.Errorf("write config: %w", err)
	}

	logger.Debugf("Firecracker: VM created in %s", vmDir)
	return nil
}

func (vm *VM) setupNetwork(ctx context.Context) error {
	logger := common.Logger(ctx)

	// Delete any stale TAP device left over from a previous VM that wasn't
	// properly cleaned up (e.g. process killed, host crash). Ignore errors
	// since the device usually won't exist.
	_ = vm.sys.DeleteTAP(vm.TAPName)

	// Create TAP device
	if err := vm.sys.CreateTAP(vm.TAPName); err != nil {
		return fmt.Errorf("create TAP: %w", err)
	}

	// Set IP address
	if err := vm.sys.SetIPAddress(vm.TAPName, vm.HostIP+"/24"); err != nil {
		_ = vm.sys.DeleteTAP(vm.TAPName)
		return fmt.Errorf("set IP: %w", err)
	}

	// Bring up interface
	if err := vm.sys.SetLinkUp(vm.TAPName); err != nil {
		_ = vm.sys.DeleteTAP(vm.TAPName)
		return fmt.Errorf("set link up: %w", err)
	}

	// Enable IP forwarding
	if err := vm.sys.EnableIPForward(); err != nil {
		_ = vm.sys.DeleteTAP(vm.TAPName)
		return fmt.Errorf("enable IP forward: %w", err)
	}

	// Add NAT rule
	subnet := fmt.Sprintf("%s.%d.0/24", vm.Config.NetworkPrefix, vm.SubnetID)
	if err := vm.sys.AddNATRule(subnet); err != nil {
		_ = vm.sys.DeleteTAP(vm.TAPName)
		return fmt.Errorf("add NAT rule: %w", err)
	}

	// Add FORWARD rules if output interface is configured
	if vm.Config.OutputInterface != "" {
		logger.Debugf("Firecracker: adding FORWARD rules for %s -> %s", vm.TAPName, vm.Config.OutputInterface)
		if err := vm.sys.AddForwardRule(vm.TAPName, vm.Config.OutputInterface); err != nil {
			logger.Warnf("Firecracker: failed to add FORWARD rule: %v", err)
			_ = vm.sys.DeleteNATRule(subnet)
			_ = vm.sys.DeleteTAP(vm.TAPName)
			return fmt.Errorf("add FORWARD rule: %w", err)
		}
		logger.Infof("Firecracker: added FORWARD rules for %s -> %s", vm.TAPName, vm.Config.OutputInterface)
	} else {
		logger.Debugf("Firecracker: output_interface not configured, skipping FORWARD rules")
	}

	logger.Debugf("Firecracker: network configured, TAP=%s, host=%s", vm.TAPName, vm.HostIP)
	return nil
}

func (vm *VM) generateSSHKey() error {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}

	// Write private key in OpenSSH format
	privBytes, err := ssh.MarshalPrivateKey(priv, "")
	if err != nil {
		return err
	}
	if err := vm.sys.WriteFile(vm.SSHKey, pem.EncodeToMemory(privBytes), 0o600); err != nil {
		return err
	}

	// Write public key
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return err
	}
	pubBytes := ssh.MarshalAuthorizedKey(sshPub)
	return vm.sys.WriteFile(vm.SSHKey+".pub", pubBytes, 0o644)
}

func (vm *VM) configureRootFS(ctx context.Context, rootfsPath string) error {
	logger := common.Logger(ctx)
	vmDir := filepath.Join(vm.RootDir, "firecracker")
	mountPoint := filepath.Join(vmDir, "mnt")

	if err := vm.sys.MkdirAll(mountPoint, 0o755); err != nil {
		return err
	}

	if err := vm.sys.MountLoop(rootfsPath, mountPoint); err != nil {
		return fmt.Errorf("mount rootfs: %w", err)
	}
	defer func() { _ = vm.sys.Unmount(mountPoint) }()

	// Create .ssh directory and copy public key
	sshDir := filepath.Join(mountPoint, "root", ".ssh")
	if err := vm.sys.MkdirAll(sshDir, 0o700); err != nil {
		return err
	}

	pubKey, err := vm.sys.ReadFile(vm.SSHKey + ".pub")
	if err != nil {
		return err
	}
	if err := vm.sys.WriteFile(filepath.Join(sshDir, "authorized_keys"), pubKey, 0o600); err != nil {
		return err
	}

	// Configure network interface
	netConfig := fmt.Sprintf(`auto eth0
iface eth0 inet static
    address %s
    netmask 255.255.255.0
    gateway %s
`, vm.GuestIP, vm.HostIP)

	netDir := filepath.Join(mountPoint, "etc", "network", "interfaces.d")
	if err := vm.sys.MkdirAll(netDir, 0o755); err != nil {
		return err
	}
	if err := vm.sys.WriteFile(filepath.Join(netDir, "eth0"), []byte(netConfig), 0o644); err != nil {
		return err
	}

	// Configure DNS - remove symlink first (systemd systems have /etc/resolv.conf -> /run/systemd/resolve/stub-resolv.conf)
	resolvPath := filepath.Join(mountPoint, "etc", "resolv.conf")
	_ = vm.sys.RemoveAll(resolvPath) // ignore error if doesn't exist
	resolvConf := "nameserver 8.8.8.8\nnameserver 8.8.4.4\n"
	if err := vm.sys.WriteFile(resolvPath, []byte(resolvConf), 0o644); err != nil {
		return err
	}

	logger.Debugf("Firecracker: rootfs configured with SSH key and network")
	return nil
}

// BuildConfig generates the Firecracker JSON configuration.
// This is a pure function for easy testing.
func (vm *VM) BuildConfig(rootfsPath string) []byte {
	mac := fmt.Sprintf("02:FC:00:00:%02X:02", vm.SubnetID)

	config := map[string]any{
		"boot-source": map[string]any{
			"kernel_image_path": vm.Config.KernelPath,
			"boot_args":         "console=ttyS0 reboot=k panic=1 pci=off init=/sbin/init",
		},
		"drives": []map[string]any{
			{
				"drive_id":       "rootfs",
				"path_on_host":   rootfsPath,
				"is_root_device": true,
				"is_read_only":   false,
			},
		},
		"machine-config": map[string]any{
			"vcpu_count":   vm.Config.VCPUs,
			"mem_size_mib": vm.Config.MemoryMB,
		},
		"network-interfaces": []map[string]any{
			{
				"iface_id":      "eth0",
				"guest_mac":     mac,
				"host_dev_name": vm.TAPName,
			},
		},
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	return data
}

// Start launches the Firecracker process and waits for SSH to become available.
func (vm *VM) Start(ctx context.Context) (*ConnectionInfo, error) {
	if vm.Config.UseJailer {
		return vm.startWithJailer(ctx)
	}
	return vm.startDirect(ctx)
}

// startDirect launches Firecracker directly without jailer.
func (vm *VM) startDirect(ctx context.Context) (*ConnectionInfo, error) {
	logger := common.Logger(ctx)
	vmDir := filepath.Join(vm.RootDir, "firecracker")
	configPath := filepath.Join(vmDir, "config.json")
	logFile := filepath.Join(vmDir, "firecracker.log")
	socketPath := filepath.Join(vmDir, "firecracker.sock")

	// Remove old socket if exists
	os.Remove(socketPath)

	// Start Firecracker
	args := []string{
		"--api-sock", socketPath,
		"--config-file", configPath,
	}

	pid, err := vm.sys.StartProcess(ctx, vm.Config.FirecrackerBin, args, logFile)
	if err != nil {
		return nil, fmt.Errorf("start firecracker: %w", err)
	}
	vm.PID = pid

	logger.Debugf("Firecracker: started with PID %d", pid)

	// Wait for SSH
	if err := vm.waitForSSH(ctx); err != nil {
		_ = vm.sys.KillProcess(pid)
		return nil, fmt.Errorf("wait for SSH: %w", err)
	}

	logger.Infof("Firecracker: VM ready at %s (pid=%d)", vm.GuestIP, vm.PID)

	return &ConnectionInfo{
		Host:   vm.GuestIP,
		HostIP: vm.HostIP,
		Port:   "22",
		Key:    vm.SSHKey,
	}, nil
}

// startWithJailer launches Firecracker inside a jailer for cgroup isolation.
func (vm *VM) startWithJailer(ctx context.Context) (*ConnectionInfo, error) {
	logger := common.Logger(ctx)
	vmDir := filepath.Join(vm.RootDir, "firecracker")

	// Generate unique VM ID for jailer
	vm.VMID = fmt.Sprintf("fc-%s-%d", vm.Name, time.Now().UnixNano())
	vm.JailDir = filepath.Join(vm.Config.ChrootBaseDir, "firecracker", vm.VMID, "root")

	logger.Debugf("Firecracker: setting up jail at %s", vm.JailDir)

	// Create jail directory structure
	if err := vm.sys.MkdirAll(vm.JailDir, 0o755); err != nil {
		return nil, fmt.Errorf("create jail directory: %w", err)
	}

	// Hard-link kernel into jail (read-only, can be shared)
	jailKernel := filepath.Join(vm.JailDir, "vmlinux")
	if err := vm.sys.CreateHardLink(vm.Config.KernelPath, jailKernel); err != nil {
		return nil, fmt.Errorf("link kernel to jail: %w", err)
	}

	// Move rootfs into jail (already prepared with SSH key and network config)
	srcRootfs := filepath.Join(vmDir, "rootfs.ext4")
	jailRootfs := filepath.Join(vm.JailDir, "rootfs.ext4")
	if err := vm.sys.MoveFile(srcRootfs, jailRootfs); err != nil {
		return nil, fmt.Errorf("move rootfs to jail: %w", err)
	}

	// Move SSH key into jail
	jailSSHKey := filepath.Join(vm.JailDir, "ssh_key")
	if err := vm.sys.MoveFile(vm.SSHKey, jailSSHKey); err != nil {
		return nil, fmt.Errorf("move SSH key to jail: %w", err)
	}
	if err := vm.sys.MoveFile(vm.SSHKey+".pub", jailSSHKey+".pub"); err != nil {
		// Public key move is optional, ignore errors
		logger.Debugf("Firecracker: failed to move public key (optional): %v", err)
	}
	vm.SSHKey = jailSSHKey

	// Generate firecracker config with chroot-relative paths
	jailConfig := vm.buildJailConfig()
	jailConfigPath := filepath.Join(vm.JailDir, "config.json")
	if err := vm.sys.WriteFile(jailConfigPath, jailConfig, 0o644); err != nil {
		return nil, fmt.Errorf("write jail config: %w", err)
	}

	// Calculate memory limit with 5% headroom
	memoryLimitMB := int(float64(vm.Config.MemoryMB) * 1.05)

	// Detect cgroup version
	cgroupVersion := vm.sys.DetectCgroupVersion()
	vm.CgroupVersion = cgroupVersion
	logger.Debugf("Firecracker: detected cgroup v%d, memory limit %dMB", cgroupVersion, memoryLimitMB)

	// Start jailer
	jailCfg := JailerStartConfig{
		JailerBin:      vm.Config.JailerBin,
		FirecrackerBin: vm.Config.FirecrackerBin,
		VMID:           vm.VMID,
		ChrootBaseDir:  vm.Config.ChrootBaseDir,
		UID:            vm.Config.JailerUID,
		GID:            vm.Config.JailerGID,
		MemoryLimitMB:  memoryLimitMB,
		CgroupVersion:  cgroupVersion,
		NetNS:          "/proc/1/ns/net", // Use host network namespace
	}

	pid, err := vm.sys.StartJailedProcess(ctx, jailCfg)
	if err != nil {
		return nil, fmt.Errorf("start jailer: %w", err)
	}
	vm.PID = pid

	logger.Debugf("Firecracker: jailer started with PID %d", pid)

	// Wait for SSH
	if err := vm.waitForSSH(ctx); err != nil {
		_ = vm.sys.KillProcess(pid)
		return nil, fmt.Errorf("wait for SSH: %w", err)
	}

	logger.Infof("Firecracker: jailed VM ready at %s (pid=%d, vmid=%s)", vm.GuestIP, vm.PID, vm.VMID)

	return &ConnectionInfo{
		Host:   vm.GuestIP,
		HostIP: vm.HostIP,
		Port:   "22",
		Key:    vm.SSHKey,
	}, nil
}

// buildJailConfig generates config with chroot-relative paths.
func (vm *VM) buildJailConfig() []byte {
	mac := fmt.Sprintf("02:FC:00:00:%02X:02", vm.SubnetID)

	// All paths are relative to chroot root
	config := map[string]any{
		"boot-source": map[string]any{
			"kernel_image_path": "/vmlinux",
			"boot_args":         "console=ttyS0 reboot=k panic=1 pci=off init=/sbin/init",
		},
		"drives": []map[string]any{
			{
				"drive_id":       "rootfs",
				"path_on_host":   "/rootfs.ext4",
				"is_root_device": true,
				"is_read_only":   false,
			},
		},
		"machine-config": map[string]any{
			"vcpu_count":   vm.Config.VCPUs,
			"mem_size_mib": vm.Config.MemoryMB,
		},
		"network-interfaces": []map[string]any{
			{
				"iface_id":      "eth0",
				"guest_mac":     mac,
				"host_dev_name": vm.TAPName,
			},
		},
	}

	data, _ := json.MarshalIndent(config, "", "  ")
	return data
}

func (vm *VM) waitForSSH(ctx context.Context) error {
	deadline := time.Now().Add(vm.Config.SSHTimeout)

	key, err := os.ReadFile(vm.SSHKey)
	if err != nil {
		return err
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return err
	}

	config := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         2 * time.Second,
	}

	addr := net.JoinHostPort(vm.GuestIP, "22")

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		client, err := ssh.Dial("tcp", addr, config)
		if err == nil {
			client.Close()
			return nil
		}

		time.Sleep(500 * time.Millisecond)
	}

	return fmt.Errorf("SSH timeout after %v", vm.Config.SSHTimeout)
}

// ReadCgroupStats reads resource usage from cgroups before VM shutdown.
// Returns nil if stats cannot be read (e.g., non-jailer mode or missing cgroup).
func (vm *VM) ReadCgroupStats() *CgroupStats {
	if vm.VMID == "" || vm.CgroupVersion == 0 {
		return nil // Not using jailer or cgroup version unknown
	}

	stats := &CgroupStats{}

	if vm.CgroupVersion == 2 {
		// cgroup v2: unified hierarchy
		// Jailer creates cgroups at /sys/fs/cgroup/firecracker/{VMID}
		basePath := filepath.Join("/sys/fs/cgroup", "firecracker", vm.VMID)

		// Memory current
		if data, err := os.ReadFile(filepath.Join(basePath, "memory.current")); err == nil {
			var bytes int64
			_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &bytes)
			stats.MemoryCurrentMB = bytes / (1024 * 1024)
		}

		// Memory peak
		if data, err := os.ReadFile(filepath.Join(basePath, "memory.peak")); err == nil {
			var bytes int64
			_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &bytes)
			stats.MemoryPeakMB = bytes / (1024 * 1024)
		}

		// CPU usage from cpu.stat (usage_usec)
		if data, err := os.ReadFile(filepath.Join(basePath, "cpu.stat")); err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				if strings.HasPrefix(line, "usage_usec ") {
					var usec int64
					_, _ = fmt.Sscanf(line, "usage_usec %d", &usec)
					stats.CPUUsageSec = float64(usec) / 1_000_000
					break
				}
			}
		}
	} else {
		// cgroup v1: separate hierarchies
		// Jailer creates cgroups at /sys/fs/cgroup/{controller}/firecracker/{VMID}
		memPath := filepath.Join("/sys/fs/cgroup/memory/firecracker", vm.VMID)
		cpuPath := filepath.Join("/sys/fs/cgroup/cpuacct/firecracker", vm.VMID)

		// Memory current
		if data, err := os.ReadFile(filepath.Join(memPath, "memory.usage_in_bytes")); err == nil {
			var bytes int64
			_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &bytes)
			stats.MemoryCurrentMB = bytes / (1024 * 1024)
		}

		// Memory peak
		if data, err := os.ReadFile(filepath.Join(memPath, "memory.max_usage_in_bytes")); err == nil {
			var bytes int64
			_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &bytes)
			stats.MemoryPeakMB = bytes / (1024 * 1024)
		}

		// CPU usage (in nanoseconds)
		if data, err := os.ReadFile(filepath.Join(cpuPath, "cpuacct.usage")); err == nil {
			var nsec int64
			_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &nsec)
			stats.CPUUsageSec = float64(nsec) / 1_000_000_000
		}
	}

	return stats
}

// LogStats logs resource usage statistics for the VM.
// This should be called while the job logger context is still active.
func (vm *VM) LogStats(ctx context.Context) {
	if stats := vm.ReadCgroupStats(); stats != nil {
		logger := common.Logger(ctx)
		logger.Infof("Firecracker: VM %s usage: requested %dMB/%dvcpu, used %dMB peak (current %dMB), %.1fs CPU",
			vm.Name, vm.Config.MemoryMB, vm.Config.VCPUs,
			stats.MemoryPeakMB, stats.MemoryCurrentMB, stats.CPUUsageSec)
	}
}

// Stop terminates the VM and cleans up resources.
// It continues cleanup even if individual operations fail, logging warnings.
func (vm *VM) Stop(ctx context.Context) error {
	logger := common.Logger(ctx)
	var errs []string

	// Kill Firecracker process
	if vm.PID > 0 {
		if err := vm.sys.KillProcess(vm.PID); err != nil {
			// Process may already be dead, which is fine
			logger.Debugf("Firecracker: failed to kill process %d: %v", vm.PID, err)
		}
	}

	// Clean up network - continue even if individual operations fail
	if vm.TAPName != "" {
		if vm.Config.OutputInterface != "" {
			logger.Debugf("Firecracker: deleting FORWARD rules for %s -> %s", vm.TAPName, vm.Config.OutputInterface)
			_ = vm.sys.DeleteForwardRule(vm.TAPName, vm.Config.OutputInterface)
		}
		subnet := fmt.Sprintf("%s.%d.0/24", vm.Config.NetworkPrefix, vm.SubnetID)
		logger.Debugf("Firecracker: deleting NAT rule for %s", subnet)
		_ = vm.sys.DeleteNATRule(subnet)
		logger.Debugf("Firecracker: deleting TAP %s", vm.TAPName)
		if err := vm.sys.DeleteTAP(vm.TAPName); err != nil {
			logger.Warnf("Firecracker: failed to delete TAP %s: %v", vm.TAPName, err)
			errs = append(errs, fmt.Sprintf("delete TAP: %v", err))
		}
	}

	// Release subnet
	if vm.SubnetID > 0 {
		if err := vm.sys.ReleaseSubnetID(vm.SubnetID); err != nil {
			logger.Warnf("Firecracker: failed to release subnet %d: %v", vm.SubnetID, err)
			errs = append(errs, fmt.Sprintf("release subnet: %v", err))
		}
	}

	logger.Debugf("Firecracker: VM stopped")

	if len(errs) > 0 {
		return fmt.Errorf("cleanup errors: %s", strings.Join(errs, "; "))
	}
	return nil
}

// Destroy stops the VM and removes all files.
// It always attempts to remove the VM directory even if Stop() fails.
func (vm *VM) Destroy(ctx context.Context) error {
	logger := common.Logger(ctx)
	stopErr := vm.Stop(ctx)

	// Remove standard VM directory
	vmDir := filepath.Join(vm.RootDir, "firecracker")
	rmErr := vm.sys.RemoveAll(vmDir)

	// If using jailer, also clean up the jail directory
	// Jail directory is: {chroot_base}/firecracker/{vm_id}/
	if vm.VMID != "" && vm.Config.ChrootBaseDir != "" {
		jailParent := filepath.Join(vm.Config.ChrootBaseDir, "firecracker", vm.VMID)
		if err := vm.sys.RemoveAll(jailParent); err != nil {
			logger.Warnf("Firecracker: failed to remove jail directory %s: %v", jailParent, err)
			if rmErr == nil {
				rmErr = err
			}
		} else {
			logger.Debugf("Firecracker: removed jail directory %s", jailParent)
		}
	}

	if stopErr != nil && rmErr != nil {
		logger.Warnf("Firecracker: both Stop and RemoveAll failed: stop=%v, rm=%v", stopErr, rmErr)
		return fmt.Errorf("stop: %w; remove: %v", stopErr, rmErr)
	}
	if stopErr != nil {
		return stopErr
	}
	return rmErr
}

// CreateVMDirectory creates a directory inside the running VM via SSH.
func (vm *VM) CreateVMDirectory(ctx context.Context, path string) error {
	logger := common.Logger(ctx)

	key, err := os.ReadFile(vm.SSHKey)
	if err != nil {
		return err
	}

	signer, err := ssh.ParsePrivateKey(key)
	if err != nil {
		return err
	}

	config := &ssh.ClientConfig{
		User:            "root",
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(vm.GuestIP, "22")
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return fmt.Errorf("SSH dial: %w", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("SSH session: %w", err)
	}
	defer session.Close()

	// Escape single quotes in path
	escapedPath := strings.ReplaceAll(path, "'", "'\"'\"'")
	cmd := fmt.Sprintf("mkdir -p '%s'", escapedPath)
	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("mkdir %s: %w", path, err)
	}

	logger.Debugf("Firecracker: created directory %s in VM", path)
	return nil
}
