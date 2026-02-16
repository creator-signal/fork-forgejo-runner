# Firecracker Jailer Integration

This document explains the Firecracker Jailer integration for per-VM memory isolation using cgroups.

## Problem Statement

When multiple concurrent VMs run without isolation, they share the runner process's cgroup. If total memory usage exceeds the host's available RAM, the Linux OOM killer may terminate the forgejo-runner service process (the parent) rather than individual VMs. This causes all running jobs to crash simultaneously.

## Solution Overview

The Firecracker Jailer creates a dedicated cgroup for each VM with a configurable memory limit. When a VM exceeds its allocation, the OOM killer targets only that VM's processes, leaving the runner and other VMs unaffected.

## How Jailer Works

### What is Jailer?

Jailer is a companion binary included in official Firecracker releases. It provides:

1. **Cgroup isolation**: Each VM runs in its own cgroup with resource limits
2. **Chroot isolation**: VMs run in a restricted filesystem namespace
3. **User namespace isolation**: VMs can run as unprivileged users (optional)
4. **Device node management**: Automatically creates `/dev/kvm` and `/dev/net/tun` in the chroot

### Process Hierarchy

Without jailer:
```
forgejo-runner (pid 1000)
├── firecracker (pid 1001) - VM 1
├── firecracker (pid 1002) - VM 2
└── firecracker (pid 1003) - VM 3
```
All processes share the runner's cgroup. OOM killer may kill pid 1000.

With jailer:
```
forgejo-runner (pid 1000)
├── jailer (pid 1001)
│   └── firecracker (pid 1002) - VM 1 [cgroup: memory.max=8G]
├── jailer (pid 1003)
│   └── firecracker (pid 1004) - VM 2 [cgroup: memory.max=8G]
└── jailer (pid 1005)
    └── firecracker (pid 1006) - VM 3 [cgroup: memory.max=8G]
```
Each VM is isolated. OOM killer targets the specific VM exceeding limits.

## Directory Structure

When jailer is enabled, each VM gets a unique jail directory:

```
/srv/jailer/                           # ChrootBaseDir (configurable)
└── firecracker/
    └── fc-{name}-{timestamp}/         # Unique VM ID
        └── root/                      # Chroot root for this VM
            ├── vmlinux                # Hard-link to kernel (shared)
            ├── rootfs.ext4            # Per-VM rootfs (moved from vmDir)
            ├── ssh_key                # Per-VM SSH private key
            ├── ssh_key.pub            # Per-VM SSH public key
            ├── config.json            # Firecracker config (chroot-relative paths)
            ├── firecracker            # Hard-link to firecracker binary (created by jailer)
            ├── firecracker.pid        # PID file (created by jailer when daemonized)
            └── dev/                   # Device nodes (created by jailer)
                ├── kvm
                └── net/
                    └── tun
```

### Path Handling

The firecracker config inside the jail uses chroot-relative paths:
- Kernel: `/vmlinux`
- RootFS: `/rootfs.ext4`
- API socket: (not used, config-file mode)

The runner tracks the actual host paths for SSH key access.

## Configuration

### Go Configuration (config.yaml)

```yaml
firecracker:
  # ... existing fields ...

  # Jailer settings
  use_jailer: true                        # Enable jailer (default: false)
  jailer_binary: /usr/local/bin/jailer    # Path to jailer binary
  jailer_uid: 0                           # UID for jailed process (0=root)
  jailer_gid: 0                           # GID for jailed process (0=root)
  chroot_base_dir: /srv/jailer            # Base directory for jail chroots
```

### Ansible Variables

```yaml
# Enable jailer isolation
forgejo_runner_firecracker_use_jailer: true

# Paths (defaults work for standard installations)
forgejo_runner_firecracker_jailer_binary: /usr/local/bin/jailer
forgejo_runner_firecracker_jailer_chroot_base: /srv/jailer

# UID/GID for jailed processes (0=root, can use unprivileged user)
forgejo_runner_firecracker_jailer_uid: 0
forgejo_runner_firecracker_jailer_gid: 0
```

## Memory Limit Calculation

The cgroup memory limit is calculated as:

```
limit = profile.memory_mb * 1.05
```

The 5% headroom accommodates:
- Guest kernel overhead
- Page tables
- Memory fragmentation
- Firecracker VMM overhead

For example, an 8GB VM profile gets an 8.4GB cgroup limit.

## Cgroup Version Detection

The runner automatically detects the cgroup version at runtime:

- **cgroup v2** (Ubuntu 24.04+): Uses `memory.max={limit}M`
- **cgroup v1** (older systems): Uses `memory.limit_in_bytes={limit_bytes}`

Detection checks for `/sys/fs/cgroup/cgroup.controllers` which only exists in cgroup v2.

## Network Handling

The current implementation keeps VMs in the host network namespace:

1. TAP devices are created in the host namespace before jailer starts
2. Jailer receives `--netns /proc/1/ns/net` to use the host network namespace
3. Firecracker binds to the pre-created TAP device

This means network isolation is unchanged from non-jailer mode. Each VM still gets:
- Unique TAP device (fctap{N})
- Unique subnet (172.16.{N}.0/24)
- NAT rules for outbound traffic
- FORWARD rules if output_interface is configured

## Jailer Command

The runner invokes jailer with these arguments:

```bash
jailer \
    --id {vm_id} \
    --exec-file /usr/local/bin/firecracker \
    --uid {uid} --gid {gid} \
    --chroot-base-dir /srv/jailer \
    --cgroup-version 2 \
    --cgroup memory.max={limit}M \
    --netns /proc/1/ns/net \
    --daemonize \
    -- \
    --config-file /config.json
```

Key flags:
- `--id`: Unique identifier for this jail
- `--exec-file`: Firecracker binary to copy into jail
- `--chroot-base-dir`: Where to create jail directories
- `--cgroup`: Resource limits (version-specific syntax)
- `--netns`: Network namespace (host namespace for TAP access)
- `--daemonize`: Return immediately, write PID to file
- `--`: Separator for firecracker arguments

## Lifecycle

### VM Creation (unchanged)
1. Allocate subnet ID
2. Create TAP device and configure networking
3. Copy rootfs from template (reflink if supported)
4. Generate SSH keypair
5. Inject SSH key and network config into rootfs
6. Write firecracker config

### VM Start (jailer mode)
1. Generate unique VM ID (`fc-{name}-{timestamp}`)
2. Create jail directory structure
3. Hard-link kernel into jail (saves space, instant)
4. Move rootfs into jail (already configured)
5. Move SSH key into jail
6. Generate chroot-relative firecracker config
7. Start jailer process
8. Wait for PID file
9. Wait for SSH connectivity

### VM Stop (unchanged)
1. Kill firecracker process
2. Delete network rules and TAP device
3. Release subnet ID

### VM Destroy (with jailer cleanup)
1. Stop VM
2. Remove standard VM directory
3. **Remove jail directory** (`/srv/jailer/firecracker/{vm_id}/`)

## Resource Considerations

### Disk Space
- Kernel is hard-linked (no extra space)
- Rootfs is moved (no extra space)
- Firecracker binary is hard-linked by jailer (no extra space)
- Jail directories are cleaned up after VM termination

### Filesystem Requirements
- Hard-links require same filesystem for source and destination
- Kernel and firecracker binary must be on same filesystem as `/srv/jailer`
- Consider using XFS or Btrfs for reflink support on rootfs copies

## Troubleshooting

### Check if Jailer is Being Used
```bash
# Look for jailer processes
ps aux | grep jailer

# Check cgroup for a running VM
cat /sys/fs/cgroup/system.slice/fc-*/memory.max
```

### Verify Memory Limits
```bash
# Find VM's cgroup
VMID=$(ls /srv/jailer/firecracker/ | head -1)
cat /sys/fs/cgroup/*/fc-${VMID}/memory.max

# Check current memory usage
cat /sys/fs/cgroup/*/fc-${VMID}/memory.current
```

### Debug Jailer Startup
If VMs fail to start with jailer:

1. Check jailer binary is installed and executable:
   ```bash
   /usr/local/bin/jailer --version
   ```

2. Check chroot base directory exists and is writable:
   ```bash
   ls -la /srv/jailer/
   ```

3. Check for partially created jails:
   ```bash
   ls -la /srv/jailer/firecracker/
   ```

4. Look for jailer errors in runner logs (jailer stderr is captured)

### OOM Events
When a VM is killed by OOM, check:
```bash
dmesg | grep -i oom
journalctl -k | grep -i "killed process"
```

The output should show the firecracker process being killed, not the runner.

## Configuration Default

Jailer is enabled by default (`use_jailer: true`). To disable:

```yaml
firecracker:
  use_jailer: false
```

## Security Notes

- Jailer with UID/GID 0 provides memory isolation but not privilege separation
- For stronger isolation, use non-root UID/GID (requires additional setup)
- The chroot provides filesystem isolation but firecracker already uses seccomp
- Network namespace is shared with host for TAP device access
