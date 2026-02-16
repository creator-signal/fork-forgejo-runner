// SPDX-License-Identifier: MIT

package firecracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"

	"code.forgejo.org/forgejo/runner/v12/act/common"
)

func TestBuildSSHCommand(t *testing.T) {
	tests := []struct {
		name     string
		keyPath  string
		host     string
		port     string
		user     string
		workdir  string
		env      map[string]string
		command  []string
		wantLen  int
		wantHost string
		wantKey  string
	}{
		{
			name:     "basic command",
			keyPath:  "/path/to/key",
			host:     "192.168.1.2",
			port:     "22",
			user:     "root",
			workdir:  "/workspace",
			env:      map[string]string{},
			command:  []string{"echo", "hello"},
			wantLen:  14, // ssh -i key -p port -o x3 -T user@host cmd
			wantHost: "root@192.168.1.2",
			wantKey:  "/path/to/key",
		},
		{
			name:    "with environment variables",
			keyPath: "/key",
			host:    "10.0.0.1",
			port:    "2222",
			user:    "ubuntu",
			workdir: "/home/ubuntu",
			env: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
			command:  []string{"bash", "-c", "echo $FOO"},
			wantLen:  14, // ssh -i key -p port -o x3 -T user@host cmd
			wantHost: "ubuntu@10.0.0.1",
			wantKey:  "/key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildSSHCommand(tt.keyPath, tt.host, tt.port, tt.user, tt.workdir, tt.env, tt.command)

			assert.Equal(t, tt.wantLen, len(result))
			assert.Equal(t, "/usr/bin/ssh", result[0])
			assert.Equal(t, "-i", result[1])
			assert.Equal(t, tt.wantKey, result[2])
			assert.Equal(t, "-p", result[3])
			assert.Equal(t, tt.port, result[4])
			assert.Contains(t, result, tt.wantHost)
		})
	}
}

func TestBuildSSHCommand_EscapesSingleQuotes(t *testing.T) {
	result := BuildSSHCommand(
		"/key", "host", "22", "root", "/work",
		map[string]string{"VAR": "value'with'quotes"},
		[]string{"echo", "test"},
	)

	remoteCmd := result[len(result)-1]
	assert.Contains(t, remoteCmd, "'\"'\"'") // escaped single quotes
}

func TestBuildSSHCommand_IncludesHyphenatedEnvVars(t *testing.T) {
	// GitHub Actions creates INPUT_<param-name> variables that may contain hyphens
	// e.g., INPUT_KEEP-STATE, INPUT_CACHE-BINARY
	// These are passed using the `env` command, not bash exports
	result := BuildSSHCommand(
		"/key", "host", "22", "root", "/work",
		map[string]string{
			"VALID_VAR":          "good",
			"INPUT_KEEP-STATE":   "false",
			"INPUT_CACHE-BINARY": "true",
		},
		[]string{"echo", "test"},
	)

	remoteCmd := result[len(result)-1]

	// All env vars should be present (passed via env command)
	assert.Contains(t, remoteCmd, "env ")
	assert.Contains(t, remoteCmd, "VALID_VAR=good")
	assert.Contains(t, remoteCmd, "INPUT_KEEP-STATE=false")
	assert.Contains(t, remoteCmd, "INPUT_CACHE-BINARY=true")
}

func TestBuildSSHCommand_EmptyEnv(t *testing.T) {
	result := BuildSSHCommand(
		"/key", "host", "22", "root", "/work",
		map[string]string{},
		[]string{"echo", "test"},
	)

	remoteCmd := result[len(result)-1]

	// No env command when env is empty
	assert.NotContains(t, remoteCmd, "env ")
	assert.Contains(t, remoteCmd, "cd '/work' && echo test")
}

func TestBuildConfig(t *testing.T) {
	vm := &VM{
		SubnetID: 42,
		TAPName:  "fctap42",
		Config: Config{
			KernelPath: "/opt/firecracker/vmlinux",
			MemoryMB:   2048,
			VCPUs:      2,
		},
	}

	config := vm.BuildConfig("/path/to/rootfs.ext4")

	assert.Contains(t, string(config), `"kernel_image_path": "/opt/firecracker/vmlinux"`)
	assert.Contains(t, string(config), `"path_on_host": "/path/to/rootfs.ext4"`)
	assert.Contains(t, string(config), `"vcpu_count": 2`)
	assert.Contains(t, string(config), `"mem_size_mib": 2048`)
	assert.Contains(t, string(config), `"host_dev_name": "fctap42"`)
	assert.Contains(t, string(config), `"guest_mac": "02:FC:00:00:2A:02"`) // 0x2A = 42
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	assert.Equal(t, "/opt/firecracker/vmlinux", config.KernelPath)
	assert.Equal(t, "/opt/firecracker/rootfs.ext4", config.RootFSTemplate)
	assert.Equal(t, "/usr/local/bin/firecracker", config.FirecrackerBin)
	assert.Equal(t, 2048, config.MemoryMB)
	assert.Equal(t, 2, config.VCPUs)
	assert.Equal(t, "172.16", config.NetworkPrefix)
}

// MockSystem implements SystemInterface for testing
type MockSystem struct {
	AllocatedSubnetID   int
	CreatedTAPs         []string
	DeletedTAPs         []string
	WrittenFiles        map[string][]byte
	StartedProcesses    []string
	KilledPIDs          []int
	MountedImages       []string
	UnmountedPaths      []string
	AddedForwardRules   []string
	DeletedForwardRules []string
	Err                 error
	AddForwardRuleErr   error

	// Per-operation error fields for granular control
	CreateTAPErr          error
	SetIPAddressErr       error
	SetLinkUpErr          error
	EnableIPForwardErr    error
	AddNATRuleErr         error
	CopyFileErr           error
	CreateHardLinkErr     error
	MoveFileErr           error
	MountLoopErr          error
	WriteFileErr          error
	MkdirAllErr           error
	RemoveAllErr          error
	StartProcessErr       error
	StartJailedProcessErr error
	AllocateSubnetErr     error
	DeleteTAPErr          error
	ReleaseSubnetErr      error

	// Additional tracking
	DeletedNATRules   []string
	CreatedDirs       []string
	RemovedPaths      []string
	ReleasedSubnetIDs []int
	AddedNATRules     []string
	SetIPAddresses    []string
	LinksSetUp        []string
	CreatedHardLinks  []string
	MovedFiles        []string
	JailedProcessCfgs []JailerStartConfig
	DetectedCgroupVer int

	// Call counting for multi-call scenarios
	WriteFileCallCount int
	WriteFileFailAt    int // Fail on Nth call, 0 = use WriteFileErr always
	MkdirAllCallCount  int
	MkdirAllFailAt     int
}

func NewMockSystem() *MockSystem {
	return &MockSystem{
		AllocatedSubnetID: 1,
		WrittenFiles:      make(map[string][]byte),
	}
}

func (m *MockSystem) CreateTAP(name string) error {
	if m.CreateTAPErr != nil {
		return m.CreateTAPErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.CreatedTAPs = append(m.CreatedTAPs, name)
	return nil
}

func (m *MockSystem) DeleteTAP(name string) error {
	if m.DeleteTAPErr != nil {
		return m.DeleteTAPErr
	}
	m.DeletedTAPs = append(m.DeletedTAPs, name)
	return nil
}

func (m *MockSystem) SetIPAddress(device, address string) error {
	if m.SetIPAddressErr != nil {
		return m.SetIPAddressErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.SetIPAddresses = append(m.SetIPAddresses, device+":"+address)
	return nil
}

func (m *MockSystem) SetLinkUp(device string) error {
	if m.SetLinkUpErr != nil {
		return m.SetLinkUpErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.LinksSetUp = append(m.LinksSetUp, device)
	return nil
}

func (m *MockSystem) EnableIPForward() error {
	if m.EnableIPForwardErr != nil {
		return m.EnableIPForwardErr
	}
	return m.Err
}

func (m *MockSystem) AddNATRule(subnet string) error {
	if m.AddNATRuleErr != nil {
		return m.AddNATRuleErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.AddedNATRules = append(m.AddedNATRules, subnet)
	return nil
}

func (m *MockSystem) DeleteNATRule(subnet string) error {
	m.DeletedNATRules = append(m.DeletedNATRules, subnet)
	return nil
}

func (m *MockSystem) AddForwardRule(tapName, outInterface string) error {
	if m.AddForwardRuleErr != nil {
		return m.AddForwardRuleErr
	}
	m.AddedForwardRules = append(m.AddedForwardRules, tapName+":"+outInterface)
	return nil
}

func (m *MockSystem) DeleteForwardRule(tapName, outInterface string) error {
	m.DeletedForwardRules = append(m.DeletedForwardRules, tapName+":"+outInterface)
	return nil
}

func (m *MockSystem) CopyFile(src, dst string) error {
	if m.CopyFileErr != nil {
		return m.CopyFileErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.WrittenFiles[dst] = []byte("copied from " + src)
	return nil
}

func (m *MockSystem) CreateHardLink(src, dst string) error {
	if m.CreateHardLinkErr != nil {
		return m.CreateHardLinkErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.CreatedHardLinks = append(m.CreatedHardLinks, src+"->"+dst)
	return nil
}

func (m *MockSystem) MoveFile(src, dst string) error {
	if m.MoveFileErr != nil {
		return m.MoveFileErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.MovedFiles = append(m.MovedFiles, src+"->"+dst)
	return nil
}

func (m *MockSystem) MountLoop(image, mountpoint string) error {
	if m.MountLoopErr != nil {
		return m.MountLoopErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.MountedImages = append(m.MountedImages, image)
	return nil
}

func (m *MockSystem) Unmount(mountpoint string) error {
	m.UnmountedPaths = append(m.UnmountedPaths, mountpoint)
	return nil
}

func (m *MockSystem) ReadFile(path string) ([]byte, error) {
	if data, ok := m.WrittenFiles[path]; ok {
		return data, nil
	}
	return nil, os.ErrNotExist
}

func (m *MockSystem) WriteFile(path string, data []byte, perm os.FileMode) error {
	m.WriteFileCallCount++
	if m.WriteFileFailAt > 0 && m.WriteFileCallCount == m.WriteFileFailAt {
		return m.WriteFileErr
	}
	if m.WriteFileFailAt == 0 && m.WriteFileErr != nil {
		return m.WriteFileErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.WrittenFiles[path] = data
	return nil
}

func (m *MockSystem) MkdirAll(path string, perm os.FileMode) error {
	m.MkdirAllCallCount++
	if m.MkdirAllFailAt > 0 && m.MkdirAllCallCount == m.MkdirAllFailAt {
		return m.MkdirAllErr
	}
	if m.MkdirAllFailAt == 0 && m.MkdirAllErr != nil {
		return m.MkdirAllErr
	}
	if m.Err != nil {
		return m.Err
	}
	m.CreatedDirs = append(m.CreatedDirs, path)
	return nil
}

func (m *MockSystem) RemoveAll(path string) error {
	if m.RemoveAllErr != nil {
		return m.RemoveAllErr
	}
	m.RemovedPaths = append(m.RemovedPaths, path)
	return nil
}

func (m *MockSystem) StartProcess(ctx context.Context, name string, args []string, logFile string) (int, error) {
	if m.StartProcessErr != nil {
		return 0, m.StartProcessErr
	}
	if m.Err != nil {
		return 0, m.Err
	}
	m.StartedProcesses = append(m.StartedProcesses, name)
	return 12345, nil
}

func (m *MockSystem) KillProcess(pid int) error {
	m.KilledPIDs = append(m.KilledPIDs, pid)
	return nil
}

func (m *MockSystem) AllocateSubnetID() (int, error) {
	if m.AllocateSubnetErr != nil {
		return 0, m.AllocateSubnetErr
	}
	if m.Err != nil {
		return 0, m.Err
	}
	return m.AllocatedSubnetID, nil
}

func (m *MockSystem) ReleaseSubnetID(id int) error {
	if m.ReleaseSubnetErr != nil {
		return m.ReleaseSubnetErr
	}
	m.ReleasedSubnetIDs = append(m.ReleasedSubnetIDs, id)
	return nil
}

func (m *MockSystem) StartJailedProcess(ctx context.Context, cfg JailerStartConfig) (int, error) {
	if m.StartJailedProcessErr != nil {
		return 0, m.StartJailedProcessErr
	}
	if m.Err != nil {
		return 0, m.Err
	}
	m.JailedProcessCfgs = append(m.JailedProcessCfgs, cfg)
	return 12345, nil
}

func (m *MockSystem) DetectCgroupVersion() int {
	if m.DetectedCgroupVer != 0 {
		return m.DetectedCgroupVer
	}
	return 2 // Default to cgroup v2 in tests
}

func TestVM_Stop(t *testing.T) {
	mock := NewMockSystem()
	vm := NewVMWithSystem("test-vm", "/tmp/test", DefaultConfig(), mock)
	vm.PID = 12345
	vm.TAPName = "fctap1"
	vm.SubnetID = 1
	vm.Config.NetworkPrefix = "172.16"

	err := vm.Stop(t.Context())

	assert.NoError(t, err)
	assert.Contains(t, mock.KilledPIDs, 12345)
	assert.Contains(t, mock.DeletedTAPs, "fctap1")
}

func TestVM_Stop_WithOutputInterface(t *testing.T) {
	mock := NewMockSystem()
	config := DefaultConfig()
	config.OutputInterface = "eth0"
	vm := NewVMWithSystem("test-vm", "/tmp/test", config, mock)
	vm.PID = 12345
	vm.TAPName = "fctap1"
	vm.SubnetID = 1
	vm.Config.NetworkPrefix = "172.16"

	err := vm.Stop(t.Context())

	assert.NoError(t, err)
	assert.Contains(t, mock.KilledPIDs, 12345)
	assert.Contains(t, mock.DeletedTAPs, "fctap1")
	assert.Contains(t, mock.DeletedForwardRules, "fctap1:eth0")
}

func TestVM_Stop_WithoutOutputInterface(t *testing.T) {
	mock := NewMockSystem()
	config := DefaultConfig()
	config.OutputInterface = "" // Empty - no FORWARD rules
	vm := NewVMWithSystem("test-vm", "/tmp/test", config, mock)
	vm.PID = 12345
	vm.TAPName = "fctap1"
	vm.SubnetID = 1
	vm.Config.NetworkPrefix = "172.16"

	err := vm.Stop(t.Context())

	assert.NoError(t, err)
	assert.Contains(t, mock.KilledPIDs, 12345)
	assert.Contains(t, mock.DeletedTAPs, "fctap1")
	assert.Empty(t, mock.DeletedForwardRules) // No FORWARD rules deleted
}

// createTestContext returns a context with logger for testing
func createTestContext(t *testing.T) context.Context {
	t.Helper()
	logger := logrus.New()
	logger.SetLevel(logrus.DebugLevel)
	logger.SetOutput(io.Discard)
	return common.WithLogger(t.Context(), logrus.NewEntry(logger))
}

// createTestVM creates a VM with network fields populated for testing
func createTestVM(t *testing.T, mock *MockSystem, subnetID int) *VM {
	t.Helper()
	vm := NewVMWithSystem("test-vm", t.TempDir(), DefaultConfig(), mock)
	vm.SubnetID = subnetID
	vm.TAPName = fmt.Sprintf("fctap%d", subnetID)
	vm.GuestIP = fmt.Sprintf("172.16.%d.2", subnetID)
	vm.HostIP = fmt.Sprintf("172.16.%d.1", subnetID)
	return vm
}

func TestVM_setupNetwork(t *testing.T) {
	tests := []struct {
		name            string
		outputInterface string
		setupMock       func(*MockSystem)
		wantErr         string
		verifyCleanup   func(*testing.T, *MockSystem)
	}{
		{
			name:            "success_without_output_interface",
			outputInterface: "",
			setupMock:       func(m *MockSystem) {},
			wantErr:         "",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
				assert.Contains(t, m.SetIPAddresses, "fctap1:172.16.1.1/24")
				assert.Contains(t, m.LinksSetUp, "fctap1")
				assert.Contains(t, m.AddedNATRules, "172.16.1.0/24")
				assert.Empty(t, m.AddedForwardRules)
			},
		},
		{
			name:            "success_with_output_interface",
			outputInterface: "eth0",
			setupMock:       func(m *MockSystem) {},
			wantErr:         "",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
				assert.Contains(t, m.AddedForwardRules, "fctap1:eth0")
			},
		},
		{
			name:            "stale_tap_cleaned_before_create",
			outputInterface: "",
			setupMock:       func(m *MockSystem) {},
			wantErr:         "",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				// Stale TAP delete happens before creation
				assert.Equal(t, []string{"fctap1"}, m.DeletedTAPs)
				assert.Contains(t, m.CreatedTAPs, "fctap1")
			},
		},
		{
			name:            "create_tap_fails",
			outputInterface: "",
			setupMock: func(m *MockSystem) {
				m.CreateTAPErr = errors.New("tap creation failed")
			},
			wantErr: "create TAP",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Empty(t, m.CreatedTAPs)
				// Stale TAP cleanup is always attempted before creation
				assert.Contains(t, m.DeletedTAPs, "fctap1")
			},
		},
		{
			name:            "set_ip_fails",
			outputInterface: "",
			setupMock: func(m *MockSystem) {
				m.SetIPAddressErr = errors.New("set ip failed")
			},
			wantErr: "set IP",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
				assert.Contains(t, m.DeletedTAPs, "fctap1")
			},
		},
		{
			name:            "set_link_up_fails",
			outputInterface: "",
			setupMock: func(m *MockSystem) {
				m.SetLinkUpErr = errors.New("link up failed")
			},
			wantErr: "set link up",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
				assert.Contains(t, m.DeletedTAPs, "fctap1")
			},
		},
		{
			name:            "enable_ip_forward_fails",
			outputInterface: "",
			setupMock: func(m *MockSystem) {
				m.EnableIPForwardErr = errors.New("ip forward failed")
			},
			wantErr: "enable IP forward",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
				assert.Contains(t, m.DeletedTAPs, "fctap1")
			},
		},
		{
			name:            "add_nat_rule_fails",
			outputInterface: "",
			setupMock: func(m *MockSystem) {
				m.AddNATRuleErr = errors.New("nat rule failed")
			},
			wantErr: "add NAT rule",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
				assert.Contains(t, m.DeletedTAPs, "fctap1")
			},
		},
		{
			name:            "add_forward_rule_fails",
			outputInterface: "eth0",
			setupMock: func(m *MockSystem) {
				m.AddForwardRuleErr = errors.New("forward rule failed")
			},
			wantErr: "add FORWARD rule",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
				assert.Contains(t, m.DeletedNATRules, "172.16.1.0/24")
				assert.Contains(t, m.DeletedTAPs, "fctap1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			tt.setupMock(mock)

			config := DefaultConfig()
			config.OutputInterface = tt.outputInterface
			vm := NewVMWithSystem("test-vm", t.TempDir(), config, mock)
			vm.SubnetID = 1
			vm.TAPName = "fctap1"
			vm.GuestIP = "172.16.1.2"
			vm.HostIP = "172.16.1.1"

			ctx := createTestContext(t)
			err := vm.setupNetwork(ctx)

			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}

			tt.verifyCleanup(t, mock)
		})
	}
}

func TestVM_generateSSHKey(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*MockSystem)
		wantErr   bool
	}{
		{
			name:      "success",
			setupMock: func(m *MockSystem) {},
			wantErr:   false,
		},
		{
			name: "write_private_key_fails",
			setupMock: func(m *MockSystem) {
				m.WriteFileErr = errors.New("write failed")
				m.WriteFileFailAt = 1
			},
			wantErr: true,
		},
		{
			name: "write_public_key_fails",
			setupMock: func(m *MockSystem) {
				m.WriteFileErr = errors.New("write failed")
				m.WriteFileFailAt = 2
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			tt.setupMock(mock)

			vm := NewVMWithSystem("test-vm", t.TempDir(), DefaultConfig(), mock)
			vm.SSHKey = filepath.Join(t.TempDir(), "ssh_key")

			err := vm.generateSSHKey()

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Contains(t, mock.WrittenFiles, vm.SSHKey)
				assert.Contains(t, mock.WrittenFiles, vm.SSHKey+".pub")
				// Verify private key format
				privKey := mock.WrittenFiles[vm.SSHKey]
				assert.Contains(t, string(privKey), "OPENSSH PRIVATE KEY")
				// Verify public key format
				pubKey := mock.WrittenFiles[vm.SSHKey+".pub"]
				assert.Contains(t, string(pubKey), "ssh-ed25519")
			}
		})
	}
}

func TestVM_configureRootFS(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*MockSystem, string)
		wantErr       string
		verifyUnmount func(*testing.T, *MockSystem)
	}{
		{
			name: "success",
			setupMock: func(m *MockSystem, keyPath string) {
				// Write a fake public key to the mock's in-memory store
				m.WrittenFiles[keyPath+".pub"] = []byte("ssh-ed25519 AAAA... test@test")
			},
			wantErr: "",
			verifyUnmount: func(t *testing.T, m *MockSystem) {
				assert.Len(t, m.UnmountedPaths, 1)
				assert.Len(t, m.MountedImages, 1)
			},
		},
		{
			name: "mkdir_mount_point_fails",
			setupMock: func(m *MockSystem, keyPath string) {
				m.MkdirAllErr = errors.New("mkdir failed")
				m.MkdirAllFailAt = 1
			},
			wantErr: "mkdir failed",
			verifyUnmount: func(t *testing.T, m *MockSystem) {
				assert.Empty(t, m.UnmountedPaths)
				assert.Empty(t, m.MountedImages)
			},
		},
		{
			name: "mount_fails",
			setupMock: func(m *MockSystem, keyPath string) {
				m.MountLoopErr = errors.New("mount failed")
			},
			wantErr: "mount rootfs",
			verifyUnmount: func(t *testing.T, m *MockSystem) {
				assert.Empty(t, m.UnmountedPaths)
			},
		},
		{
			name: "mkdir_ssh_fails",
			setupMock: func(m *MockSystem, keyPath string) {
				m.MkdirAllErr = errors.New("mkdir ssh failed")
				m.MkdirAllFailAt = 2
			},
			wantErr: "mkdir ssh failed",
			verifyUnmount: func(t *testing.T, m *MockSystem) {
				assert.Len(t, m.UnmountedPaths, 1)
			},
		},
		{
			name: "pubkey_not_found",
			setupMock: func(m *MockSystem, keyPath string) {
				// Don't create the public key file
			},
			wantErr: "file does not exist",
			verifyUnmount: func(t *testing.T, m *MockSystem) {
				assert.Len(t, m.UnmountedPaths, 1)
			},
		},
		{
			name: "write_authorized_keys_fails",
			setupMock: func(m *MockSystem, keyPath string) {
				m.WrittenFiles[keyPath+".pub"] = []byte("ssh-ed25519 AAAA...")
				m.WriteFileErr = errors.New("write auth keys failed")
				m.WriteFileFailAt = 1
			},
			wantErr: "write auth keys failed",
			verifyUnmount: func(t *testing.T, m *MockSystem) {
				assert.Len(t, m.UnmountedPaths, 1)
			},
		},
		{
			name: "write_network_config_fails",
			setupMock: func(m *MockSystem, keyPath string) {
				m.WrittenFiles[keyPath+".pub"] = []byte("ssh-ed25519 AAAA...")
				m.WriteFileErr = errors.New("write net config failed")
				m.WriteFileFailAt = 2
			},
			wantErr: "write net config failed",
			verifyUnmount: func(t *testing.T, m *MockSystem) {
				assert.Len(t, m.UnmountedPaths, 1)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			tmpDir := t.TempDir()
			keyPath := filepath.Join(tmpDir, "ssh_key")

			tt.setupMock(mock, keyPath)

			vm := NewVMWithSystem("test-vm", tmpDir, DefaultConfig(), mock)
			vm.SSHKey = keyPath
			vm.GuestIP = "172.16.1.2"
			vm.HostIP = "172.16.1.1"

			ctx := createTestContext(t)
			rootfsPath := filepath.Join(tmpDir, "rootfs.ext4")
			err := vm.configureRootFS(ctx, rootfsPath)

			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
				// Verify configs were written
				assert.NotEmpty(t, mock.WrittenFiles)
			}

			tt.verifyUnmount(t, mock)
		})
	}
}

func TestVM_Create(t *testing.T) {
	tests := []struct {
		name          string
		setupMock     func(*MockSystem, string)
		wantErr       string
		verifyCleanup func(*testing.T, *MockSystem)
	}{
		{
			name: "success",
			setupMock: func(m *MockSystem, tmpDir string) {
				// Create will generate SSH key which writes to real filesystem for pubkey read
			},
			wantErr: "",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
				assert.NotEmpty(t, m.WrittenFiles)
			},
		},
		{
			name: "allocate_subnet_fails",
			setupMock: func(m *MockSystem, tmpDir string) {
				m.AllocateSubnetErr = errors.New("no subnets available")
			},
			wantErr: "allocate subnet",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Empty(t, m.CreatedTAPs)
				assert.Empty(t, m.CreatedDirs)
			},
		},
		{
			name: "mkdir_fails",
			setupMock: func(m *MockSystem, tmpDir string) {
				m.MkdirAllErr = errors.New("mkdir failed")
				m.MkdirAllFailAt = 1
			},
			wantErr: "create VM directory",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				// Subnet was allocated but should ideally be released
				// Note: current implementation doesn't release on mkdir failure
			},
		},
		{
			name: "setup_network_fails",
			setupMock: func(m *MockSystem, tmpDir string) {
				m.CreateTAPErr = errors.New("tap failed")
			},
			wantErr: "setup network",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.NotEmpty(t, m.CreatedDirs)
				assert.Empty(t, m.CreatedTAPs)
			},
		},
		{
			name: "copy_rootfs_fails",
			setupMock: func(m *MockSystem, tmpDir string) {
				m.CopyFileErr = errors.New("copy failed")
			},
			wantErr: "copy rootfs",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
			},
		},
		{
			name: "generate_ssh_key_write_fails",
			setupMock: func(m *MockSystem, tmpDir string) {
				m.WriteFileErr = errors.New("write key failed")
				m.WriteFileFailAt = 1
			},
			wantErr: "generate SSH key",
			verifyCleanup: func(t *testing.T, m *MockSystem) {
				assert.Contains(t, m.CreatedTAPs, "fctap1")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			mock.AllocatedSubnetID = 1
			tmpDir := t.TempDir()

			tt.setupMock(mock, tmpDir)

			vm := NewVMWithSystem("test-vm", tmpDir, DefaultConfig(), mock)

			ctx := createTestContext(t)
			err := vm.Create(ctx)

			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, 1, vm.SubnetID)
				assert.Equal(t, "fctap1", vm.TAPName)
				assert.Equal(t, "172.16.1.2", vm.GuestIP)
				assert.Equal(t, "172.16.1.1", vm.HostIP)
			}

			tt.verifyCleanup(t, mock)
		})
	}
}

func TestVM_Start(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*MockSystem, string)
		wantErr   string
	}{
		{
			name: "start_process_fails",
			setupMock: func(m *MockSystem, tmpDir string) {
				m.StartProcessErr = errors.New("process start failed")
			},
			wantErr: "start firecracker",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			tmpDir := t.TempDir()

			tt.setupMock(mock, tmpDir)

			vm := createTestVM(t, mock, 1)
			vm.RootDir = tmpDir

			// Create the firecracker directory and config for Start
			vmDir := filepath.Join(tmpDir, "firecracker")
			_ = os.MkdirAll(vmDir, 0o755)
			_ = os.WriteFile(filepath.Join(vmDir, "config.json"), []byte("{}"), 0o644)
			_ = os.WriteFile(filepath.Join(vmDir, "ssh_key"), []byte("fake key"), 0o600)
			vm.SSHKey = filepath.Join(vmDir, "ssh_key")

			ctx := createTestContext(t)
			connInfo, err := vm.Start(ctx)

			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				assert.Nil(t, connInfo)
				assert.Equal(t, 0, vm.PID)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, connInfo)
			}
		})
	}
}

func TestVM_Destroy(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*MockSystem)
		wantErr   string
	}{
		{
			name:      "success",
			setupMock: func(m *MockSystem) {},
			wantErr:   "",
		},
		{
			name: "stop_fails_remove_succeeds",
			setupMock: func(m *MockSystem) {
				m.DeleteTAPErr = errors.New("delete tap failed")
			},
			wantErr: "delete TAP",
		},
		{
			name: "stop_succeeds_remove_fails",
			setupMock: func(m *MockSystem) {
				m.RemoveAllErr = errors.New("remove failed")
			},
			wantErr: "remove failed",
		},
		{
			name: "both_fail",
			setupMock: func(m *MockSystem) {
				m.DeleteTAPErr = errors.New("delete tap failed")
				m.RemoveAllErr = errors.New("remove failed")
			},
			wantErr: "stop:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			tt.setupMock(mock)

			vm := createTestVM(t, mock, 1)
			vm.PID = 12345

			ctx := createTestContext(t)
			err := vm.Destroy(ctx)

			if tt.wantErr != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
			} else {
				assert.NoError(t, err)
			}

			// Verify both Stop and RemoveAll were attempted
			assert.Contains(t, mock.KilledPIDs, 12345)
			// RemovedPaths is only populated if RemoveAllErr is nil
			if mock.RemoveAllErr == nil {
				assert.NotEmpty(t, mock.RemovedPaths)
			}
		})
	}
}

// =============================================================================
// JAILER TESTS
// =============================================================================

func TestBuildJailConfig(t *testing.T) {
	vm := &VM{
		SubnetID: 42,
		TAPName:  "fctap42",
		Config: Config{
			KernelPath: "/opt/firecracker/vmlinux", // Host path (should NOT appear)
			MemoryMB:   2048,
			VCPUs:      2,
		},
	}

	config := vm.buildJailConfig()
	configStr := string(config)

	// Verify chroot-relative paths are used
	assert.Contains(t, configStr, `"kernel_image_path": "/vmlinux"`)
	assert.Contains(t, configStr, `"path_on_host": "/rootfs.ext4"`)

	// Verify host path is NOT in config
	assert.NotContains(t, configStr, "/opt/firecracker")

	// Verify other config values
	assert.Contains(t, configStr, `"vcpu_count": 2`)
	assert.Contains(t, configStr, `"mem_size_mib": 2048`)
	assert.Contains(t, configStr, `"host_dev_name": "fctap42"`)
	assert.Contains(t, configStr, `"guest_mac": "02:FC:00:00:2A:02"`)
}

func TestVM_Start_RoutesToCorrectMethod(t *testing.T) {
	tests := []struct {
		name      string
		useJailer bool
		wantErr   string
	}{
		{
			name:      "without_jailer_uses_direct_start",
			useJailer: false,
			wantErr:   "start firecracker", // StartProcess fails
		},
		{
			name:      "with_jailer_uses_jailed_start",
			useJailer: true,
			wantErr:   "start jailer", // StartJailedProcess fails
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			mock.StartProcessErr = errors.New("process start failed")
			mock.StartJailedProcessErr = errors.New("jailed process start failed")

			tmpDir := t.TempDir()
			config := DefaultConfig()
			config.UseJailer = tt.useJailer
			config.ChrootBaseDir = filepath.Join(tmpDir, "jailer")

			vm := NewVMWithSystem("test-vm", tmpDir, config, mock)
			vm.SubnetID = 1
			vm.TAPName = "fctap1"
			vm.GuestIP = "172.16.1.2"
			vm.HostIP = "172.16.1.1"

			// Create required files
			vmDir := filepath.Join(tmpDir, "firecracker")
			_ = os.MkdirAll(vmDir, 0o755)
			_ = os.WriteFile(filepath.Join(vmDir, "rootfs.ext4"), []byte("fake"), 0o644)
			_ = os.WriteFile(filepath.Join(vmDir, "ssh_key"), []byte("fake key"), 0o600)
			_ = os.WriteFile(filepath.Join(vmDir, "ssh_key.pub"), []byte("fake pub"), 0o644)
			vm.SSHKey = filepath.Join(vmDir, "ssh_key")

			ctx := createTestContext(t)
			_, err := vm.Start(ctx)

			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestVM_StartWithJailer_Success(t *testing.T) {
	mock := NewMockSystem()
	mock.DetectedCgroupVer = 2

	tmpDir := t.TempDir()
	config := DefaultConfig()
	config.UseJailer = true
	config.JailerBin = "/usr/local/bin/jailer"
	config.JailerUID = 0
	config.JailerGID = 0
	config.ChrootBaseDir = filepath.Join(tmpDir, "jailer")

	vm := NewVMWithSystem("test-vm", tmpDir, config, mock)
	vm.SubnetID = 1
	vm.TAPName = "fctap1"
	vm.GuestIP = "172.16.1.2"
	vm.HostIP = "172.16.1.1"

	// Create required files
	vmDir := filepath.Join(tmpDir, "firecracker")
	_ = os.MkdirAll(vmDir, 0o755)
	_ = os.WriteFile(filepath.Join(vmDir, "rootfs.ext4"), []byte("fake rootfs"), 0o644)
	_ = os.WriteFile(filepath.Join(vmDir, "ssh_key"), []byte("fake key"), 0o600)
	_ = os.WriteFile(filepath.Join(vmDir, "ssh_key.pub"), []byte("fake pub"), 0o644)
	vm.SSHKey = filepath.Join(vmDir, "ssh_key")

	ctx := createTestContext(t)
	// Note: Start will fail at waitForSSH since there's no real VM,
	// but we can verify the setup steps happened correctly
	_, err := vm.Start(ctx)

	// Will fail waiting for SSH, but that's expected
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "SSH")

	// Verify jailer setup was attempted
	assert.NotEmpty(t, vm.VMID)
	assert.Contains(t, vm.VMID, "fc-test-vm-")
	assert.NotEmpty(t, vm.JailDir)

	// Verify jail directory was created
	assert.Contains(t, mock.CreatedDirs, vm.JailDir)

	// Verify kernel was hard-linked
	assert.Len(t, mock.CreatedHardLinks, 1)
	assert.Contains(t, mock.CreatedHardLinks[0], "vmlinux")

	// Verify rootfs was moved
	assert.Len(t, mock.MovedFiles, 3) // rootfs, ssh_key, and ssh_key.pub

	// Verify config was written to jail
	var configWritten bool
	for path := range mock.WrittenFiles {
		if filepath.Base(path) == "config.json" {
			configWritten = true
			break
		}
	}
	assert.True(t, configWritten)

	// Verify jailer was started with correct config
	assert.Len(t, mock.JailedProcessCfgs, 1)
	cfg := mock.JailedProcessCfgs[0]
	assert.Equal(t, "/usr/local/bin/jailer", cfg.JailerBin)
	assert.Equal(t, vm.VMID, cfg.VMID)
	assert.Equal(t, 0, cfg.UID)
	assert.Equal(t, 0, cfg.GID)
	assert.Equal(t, 2, cfg.CgroupVersion)
	assert.Equal(t, "/proc/1/ns/net", cfg.NetNS)

	// Verify memory limit has 5% headroom
	expectedLimit := int(float64(config.MemoryMB) * 1.05)
	assert.Equal(t, expectedLimit, cfg.MemoryLimitMB)
}

func TestVM_StartWithJailer_Errors(t *testing.T) {
	tests := []struct {
		name      string
		setupMock func(*MockSystem)
		wantErr   string
	}{
		{
			name: "create_jail_dir_fails",
			setupMock: func(m *MockSystem) {
				m.MkdirAllErr = errors.New("mkdir failed")
				m.MkdirAllFailAt = 1
			},
			wantErr: "create jail directory",
		},
		{
			name: "hardlink_kernel_fails",
			setupMock: func(m *MockSystem) {
				m.CreateHardLinkErr = errors.New("link failed")
			},
			wantErr: "link kernel to jail",
		},
		{
			name: "move_rootfs_fails",
			setupMock: func(m *MockSystem) {
				m.MoveFileErr = errors.New("move failed")
			},
			wantErr: "move rootfs to jail",
		},
		{
			name: "start_jailer_fails",
			setupMock: func(m *MockSystem) {
				m.StartJailedProcessErr = errors.New("jailer failed to start")
			},
			wantErr: "start jailer",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			mock.DetectedCgroupVer = 2
			tt.setupMock(mock)

			tmpDir := t.TempDir()
			config := DefaultConfig()
			config.UseJailer = true
			config.ChrootBaseDir = filepath.Join(tmpDir, "jailer")

			vm := NewVMWithSystem("test-vm", tmpDir, config, mock)
			vm.SubnetID = 1
			vm.TAPName = "fctap1"
			vm.GuestIP = "172.16.1.2"
			vm.HostIP = "172.16.1.1"

			// Create required files
			vmDir := filepath.Join(tmpDir, "firecracker")
			_ = os.MkdirAll(vmDir, 0o755)
			_ = os.WriteFile(filepath.Join(vmDir, "rootfs.ext4"), []byte("fake"), 0o644)
			_ = os.WriteFile(filepath.Join(vmDir, "ssh_key"), []byte("fake key"), 0o600)
			_ = os.WriteFile(filepath.Join(vmDir, "ssh_key.pub"), []byte("fake pub"), 0o644)
			vm.SSHKey = filepath.Join(vmDir, "ssh_key")

			ctx := createTestContext(t)
			_, err := vm.Start(ctx)

			assert.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestVM_Destroy_CleansUpJailDir(t *testing.T) {
	mock := NewMockSystem()

	tmpDir := t.TempDir()
	config := DefaultConfig()
	config.UseJailer = true
	config.ChrootBaseDir = filepath.Join(tmpDir, "jailer")

	vm := NewVMWithSystem("test-vm", tmpDir, config, mock)
	vm.SubnetID = 1
	vm.TAPName = "fctap1"
	vm.PID = 12345
	vm.VMID = "fc-test-vm-12345"
	vm.JailDir = filepath.Join(config.ChrootBaseDir, "firecracker", vm.VMID, "root")

	ctx := createTestContext(t)
	err := vm.Destroy(ctx)

	assert.NoError(t, err)

	// Verify process was killed
	assert.Contains(t, mock.KilledPIDs, 12345)

	// Verify both vmDir and jailDir were removed
	assert.Len(t, mock.RemovedPaths, 2)

	// One should be the VM directory
	vmDir := filepath.Join(tmpDir, "firecracker")
	assert.Contains(t, mock.RemovedPaths, vmDir)

	// One should be the jail parent directory
	jailParent := filepath.Join(config.ChrootBaseDir, "firecracker", vm.VMID)
	assert.Contains(t, mock.RemovedPaths, jailParent)
}

func TestVM_Destroy_WithoutJailer_NoJailCleanup(t *testing.T) {
	mock := NewMockSystem()

	tmpDir := t.TempDir()
	config := DefaultConfig()
	config.UseJailer = false // Not using jailer

	vm := NewVMWithSystem("test-vm", tmpDir, config, mock)
	vm.SubnetID = 1
	vm.TAPName = "fctap1"
	vm.PID = 12345
	vm.VMID = "" // No VMID when not using jailer

	ctx := createTestContext(t)
	err := vm.Destroy(ctx)

	assert.NoError(t, err)

	// Only VM directory should be removed (no jail cleanup)
	assert.Len(t, mock.RemovedPaths, 1)
	vmDir := filepath.Join(tmpDir, "firecracker")
	assert.Contains(t, mock.RemovedPaths, vmDir)
}

func TestDetectCgroupVersion_MockBehavior(t *testing.T) {
	tests := []struct {
		name        string
		mockVersion int
		want        int
	}{
		{
			name:        "cgroup_v2",
			mockVersion: 2,
			want:        2,
		},
		{
			name:        "cgroup_v1",
			mockVersion: 1,
			want:        1,
		},
		{
			name:        "default_to_v2",
			mockVersion: 0, // Not set
			want:        2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := NewMockSystem()
			mock.DetectedCgroupVer = tt.mockVersion

			got := mock.DetectCgroupVersion()
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestJailerStartConfig_MemoryLimitCalculation(t *testing.T) {
	// The memory limit should be 1.05x the configured memory
	tests := []struct {
		memoryMB      int
		expectedLimit int
	}{
		{memoryMB: 1024, expectedLimit: 1075},   // 1024 * 1.05 = 1075.2
		{memoryMB: 2048, expectedLimit: 2150},   // 2048 * 1.05 = 2150.4
		{memoryMB: 4096, expectedLimit: 4300},   // 4096 * 1.05 = 4300.8
		{memoryMB: 8192, expectedLimit: 8601},   // 8192 * 1.05 = 8601.6
		{memoryMB: 16384, expectedLimit: 17203}, // 16384 * 1.05 = 17203.2
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dMB", tt.memoryMB), func(t *testing.T) {
			mock := NewMockSystem()
			mock.DetectedCgroupVer = 2

			tmpDir := t.TempDir()
			config := DefaultConfig()
			config.UseJailer = true
			config.MemoryMB = tt.memoryMB
			config.ChrootBaseDir = filepath.Join(tmpDir, "jailer")

			vm := NewVMWithSystem("test-vm", tmpDir, config, mock)
			vm.SubnetID = 1
			vm.TAPName = "fctap1"
			vm.GuestIP = "172.16.1.2"
			vm.HostIP = "172.16.1.1"

			// Create required files
			vmDir := filepath.Join(tmpDir, "firecracker")
			_ = os.MkdirAll(vmDir, 0o755)
			_ = os.WriteFile(filepath.Join(vmDir, "rootfs.ext4"), []byte("fake"), 0o644)
			_ = os.WriteFile(filepath.Join(vmDir, "ssh_key"), []byte("fake key"), 0o600)
			_ = os.WriteFile(filepath.Join(vmDir, "ssh_key.pub"), []byte("fake pub"), 0o644)
			vm.SSHKey = filepath.Join(vmDir, "ssh_key")

			ctx := createTestContext(t)
			_, _ = vm.Start(ctx) // Will fail at SSH, but that's OK

			// Verify memory limit
			if len(mock.JailedProcessCfgs) > 0 {
				cfg := mock.JailedProcessCfgs[0]
				assert.Equal(t, tt.expectedLimit, cfg.MemoryLimitMB)
			}
		})
	}
}
