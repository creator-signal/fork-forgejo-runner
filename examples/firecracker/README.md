# Firecracker Executor

Run Forgejo Actions jobs in [Firecracker](https://firecracker-microvm.github.io/) microVMs for strong isolation, fast boot times, and reproducible environments.

## Prerequisites

- **Linux host with KVM support** - `/dev/kvm` must be accessible
- **Firecracker binary** - [Install from releases](https://github.com/firecracker-microvm/firecracker/releases)
- **Linux kernel** - A vmlinux kernel image for the VMs
- **Root filesystem** - An ext4 image with SSH server and basic tools
- **Network tools** - `ip` and `iptables` commands available
- **Root/sudo access** - Required for TAP device and network configuration

## Preparing VM Images

### Kernel

Download a pre-built kernel from the [Firecracker quickstart](https://github.com/firecracker-microvm/firecracker/blob/main/docs/getting-started.md) or build your own. The kernel must support virtio devices.

```bash
# Example: Download quickstart kernel
curl -fsSL -o /opt/firecracker/vmlinux \
  https://s3.amazonaws.com/spec.ccfc.min/img/quickstart_guide/x86_64/kernels/vmlinux.bin
```

### Root Filesystem

The rootfs image must include:
- SSH server (openssh-server) listening on port 22
- Root user accessible via SSH key authentication
- Basic tools: bash, coreutils, git (for checkout actions)

You can create a rootfs using debootstrap, cloud images, or similar tools:

```bash
# Example: Create a minimal Debian rootfs
truncate -s 2G /opt/firecracker/rootfs.ext4
mkfs.ext4 /opt/firecracker/rootfs.ext4
mkdir -p /mnt/rootfs
mount /opt/firecracker/rootfs.ext4 /mnt/rootfs
debootstrap --include=openssh-server,git stable /mnt/rootfs

# Configure SSH
mkdir -p /mnt/rootfs/root/.ssh
cat ~/.ssh/id_rsa.pub >> /mnt/rootfs/root/.ssh/authorized_keys
chmod 600 /mnt/rootfs/root/.ssh/authorized_keys

# Enable root login
sed -i 's/#PermitRootLogin.*/PermitRootLogin yes/' /mnt/rootfs/etc/ssh/sshd_config

umount /mnt/rootfs
```

## Network Setup

Firecracker VMs use TAP devices for networking. The runner creates TAP devices automatically, but you must configure IP forwarding and NAT rules.

### Enable IP Forwarding

```bash
# Temporary
echo 1 > /proc/sys/net/ipv4/ip_forward

# Persistent (add to /etc/sysctl.conf)
net.ipv4.ip_forward = 1
```

### Configure iptables NAT

Replace `eth0` with your host's outbound network interface:

```bash
# Enable NAT for VM traffic
iptables -t nat -A POSTROUTING -o eth0 -j MASQUERADE

# Allow forwarding for established connections
iptables -A FORWARD -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT

# Allow forwarding from TAP devices (VMs)
iptables -A FORWARD -i tap+ -j ACCEPT
```

To make these rules persistent, use `iptables-save` or your distribution's method.

### Automatic FORWARD Rules

Set `output_interface` in your config to have the runner automatically add FORWARD rules for each VM:

```yaml
firecracker:
  output_interface: "eth0"  # or "eno1", etc.
```

When empty (default), you must manage FORWARD rules manually as shown above.

## Configuration

Add the following to your `config.yaml`:

```yaml
firecracker:
  # Path to the Linux kernel for VMs
  kernel_path: /opt/firecracker/vmlinux

  # Path to the rootfs image template (copied for each VM)
  rootfs_template: /opt/firecracker/rootfs.ext4

  # Path to the Firecracker binary
  binary: /usr/local/bin/firecracker

  # IP prefix for VM networking (VMs get IPs like 172.16.X.2)
  network_prefix: "172.16"

  # Default timeout waiting for SSH (range: 5s to 5m)
  ssh_timeout: 60s

  # Network interface for outbound traffic (empty = manual FORWARD rules)
  output_interface: "eth0"

  # Named profiles define VM resources
  # Profile names MUST match your label names
  profiles:
    small:
      memory_mb: 1024    # Required (max: 65536)
      vcpus: 1           # Required (max: 256)
    medium:
      memory_mb: 2048
      vcpus: 2
    large:
      memory_mb: 8192
      vcpus: 4
      ssh_timeout: 120s  # Override default for this profile

runner:
  labels:
    - "small:firecracker://ubuntu:22.04"
    - "medium:firecracker://ubuntu:22.04"
    - "large:firecracker://ubuntu:22.04"
```

### Configuration Reference

| Option | Description | Default |
|--------|-------------|---------|
| `kernel_path` | Path to vmlinux kernel | (required) |
| `rootfs_template` | Path to rootfs ext4 image | (required) |
| `binary` | Path to firecracker binary | (required) |
| `network_prefix` | IP prefix for VMs (e.g., "172.16") | (required) |
| `ssh_timeout` | Default SSH connection timeout | 60s |
| `output_interface` | Interface for NAT (empty = manual) | "" |
| `profiles` | Map of profile name to resources | (required) |
| `use_jailer` | Enable cgroup isolation per VM (see below) | true |
| `jailer_binary` | Path to jailer binary | /usr/local/bin/jailer |
| `chroot_base_dir` | Base directory for jailer chroots | /srv/jailer |

### Jailer (Cgroup Isolation)

Jailer runs each VM in its own cgroup with memory limits enforced by the kernel. When a VM exceeds its memory allocation, the OOM killer terminates only that VM's processes—not the runner or other VMs.

Without jailer, all VMs share the runner's cgroup. If total memory exceeds available RAM, the OOM killer may terminate the runner process itself, crashing all running jobs.

Jailer is enabled by default. To disable:

```yaml
firecracker:
  use_jailer: false
```

See [JAILER.md](../../docs/JAILER.md) for detailed documentation.

### Profile Options

| Option | Description | Limits |
|--------|-------------|--------|
| `memory_mb` | VM memory in megabytes | 1 - 65536 |
| `vcpus` | Virtual CPU count | 1 - 256 |
| `ssh_timeout` | Override base ssh_timeout | 5s - 5m |

## Runner Registration

Register the runner with Firecracker labels:

```bash
forgejo-runner register \
  --instance https://your-forgejo.example.com \
  --token YOUR_REGISTRATION_TOKEN \
  --labels "small:firecracker://ubuntu:22.04,medium:firecracker://ubuntu:22.04,large:firecracker://ubuntu:22.04"
```

## Workflow Usage

Workflows select VM resources using `runs-on` with your label names:

```yaml
name: Build and Test
on: [push]

jobs:
  lint:
    runs-on: small    # 1GB RAM, 1 vCPU
    steps:
      - uses: actions/checkout@v4
      - run: npm run lint

  test:
    runs-on: medium   # 2GB RAM, 2 vCPUs
    steps:
      - uses: actions/checkout@v4
      - run: npm test

  build:
    runs-on: large    # 8GB RAM, 4 vCPUs
    steps:
      - uses: actions/checkout@v4
      - run: npm run build
```

## Memory Scheduling

By default, the runner accepts jobs without tracking total memory commitment. This can lead to over-commitment where multiple VMs exhaust host memory, causing the OOM killer to terminate processes.

Memory scheduling prevents this by tracking committed memory and queuing jobs when capacity is reached.

### Configuration

```yaml
firecracker:
  memory_scheduling:
    enabled: true
    max_commit_mb: 0      # 0 = auto-detect (80% of total RAM)
    reserve_mb: 2048      # Keep 2GB free for host OS
    acquire_timeout: 5m   # Max time to wait for memory
```

### Configuration Options

| Option | Description | Default |
|--------|-------------|---------|
| `enabled` | Enable memory-aware scheduling | false |
| `max_commit_mb` | Maximum memory to commit to VMs (MB) | 0 (auto) |
| `reserve_mb` | Memory to keep free for host OS | 2048 |
| `acquire_timeout` | Max wait time for memory availability | 5m |

### Auto-Detection

When `max_commit_mb` is 0, the scheduler auto-detects 80% of total system memory as the limit. On a 64GB host, this allows ~51GB to be committed to VMs.

### Memory Overcommit

You can allow memory overcommit by setting `max_commit_mb` higher than physical memory. For example, on a 64GB host:

```yaml
firecracker:
  memory_scheduling:
    enabled: true
    max_commit_mb: 80000  # Allow ~125% overcommit (80GB on 64GB host)
```

**Note:** When overcommitting memory, ensure adequate swap space is configured on the host. Without swap, the OOM killer will terminate VMs when physical memory is exhausted.

### How It Works

1. When a job starts, the scheduler checks if `profile.memory_mb` can be committed
2. If sufficient capacity exists, memory is reserved and the VM starts
3. If at capacity, the job waits (up to `acquire_timeout`) for other VMs to finish
4. When a VM terminates, its memory is released for other jobs

### Debug Logging

Enable debug logging to see scheduler decisions:

```yaml
log:
  level: debug
```

Example output:
```
level=debug msg="acquiring memory: requested=8192MB, committed=16384MB/51200MB, waiting=2" module=memory_scheduler
level=debug msg="acquired memory: 8192MB, new committed=24576MB/51200MB" module=memory_scheduler
```

## Systemd Service

Example systemd unit for running the Firecracker-enabled runner as a service:

```ini
# /etc/systemd/system/forgejo-runner.service
[Unit]
Description=Forgejo Runner (Firecracker)
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/forgejo-runner
ExecStart=/usr/local/bin/forgejo-runner daemon
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
systemctl daemon-reload
systemctl enable --now forgejo-runner
systemctl status forgejo-runner
journalctl -u forgejo-runner -f
```

## Troubleshooting

### "no firecracker profile found for label"

The job's `runs-on` label doesn't match any profile name. Ensure:
- Profile names in `firecracker.profiles` match your label names exactly
- Labels are registered with `name:firecracker://...` format

### SSH Connection Timeout

If VMs fail to become accessible via SSH:
- Increase `ssh_timeout` in the profile or base config
- Verify the rootfs has SSH server installed and enabled
- Check that root SSH login is permitted
- Verify the SSH key is correctly installed in the rootfs

### KVM Permission Denied

```
failed to open /dev/kvm: Permission denied
```

Add the runner user to the `kvm` group:

```bash
usermod -aG kvm youruser
```

Or run the runner as root.

### VM Won't Boot

- Verify `kernel_path` points to a valid vmlinux kernel
- Verify `rootfs_template` points to a valid ext4 image
- Check that Firecracker binary is executable
- Review Firecracker logs for specific errors

### Network Unreachable from VM

VMs cannot reach external networks:
- Verify IP forwarding is enabled: `cat /proc/sys/net/ipv4/ip_forward` should return `1`
- Check iptables FORWARD rules allow traffic from tap devices
- Verify NAT/MASQUERADE rule is configured for your outbound interface
- If using `output_interface`, ensure the interface name is correct

### Debug Logging

Enable debug logging in `config.yaml`:

```yaml
log:
  level: debug
  job_level: debug
```
