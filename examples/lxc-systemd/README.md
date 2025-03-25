forgejo-runner-service.sh installs a [Forgejo runner](https://forgejo.org/docs/next/admin/runner-installation/) within an [LXC container](https://linuxcontainers.org/lxc/) and runs it from a systemd service.

## Quickstart

- Install: `sudo wget -O /usr/local/bin/forgejo-runner-service.sh https://code.forgejo.org/forgejo/runner/raw/branch/main/examples/lxc-systemd/forgejo-runner-service.sh && sudo chmod +x /usr/local/bin/forgejo-runner-service.sh`
- Obtain a runner registration token ($TOKEN)
- Choose a serial number that is not already in use in `/etc/forgejo-runner`
- Create a runner `INPUTS_SERIAL=30 INPUTS_TOKEN=$TOKEN INPUTS_FORGEJO=https://code.forgejo.org forgejo-runner-service.sh`
- Start `systemctl enable --now forgejo-runner@$INPUTS_SERIAL`
- Monitor with:
  - `systemctl status forgejo-runner@$INPUTS_SERIAL`
  - `tail --follow=name /var/log/forgejo-runner/$INPUTS_SERIAL.log`

## Installation or upgrade

### Installation

- `sudo wget -O /usr/local/bin/forgejo-runner-service.sh https://code.forgejo.org/forgejo/runner/raw/branch/main/examples/lxc-systemd/forgejo-runner-service.sh && sudo chmod +x /usr/local/bin/forgejo-runner-service.sh`

### Upgrade

> **Warning** runners will not be upgraded immediately, the upgrade will happen when they restart (at `$INPUTS_LIFETIME` intervals).

The following will be upgraded:

- `forgejo-runner-service.sh` will replace itself with the script found at the provided URL (e.g. `https://code.forgejo.org/forgejo/runner/src/tag/v6.3.1/examples/lxc-systemd/forgejo-runner-service.sh`)
- `lxc-helpers*.sh` will be replaced with the version pinned in `forgejo-runner-service.sh`
- `forgejo-runner-X.Y.Z` will default to the version hardcoded in `forgejo-runner-service.sh`

Example:

- `forgejo-runner-service.sh upgrade https://code.forgejo.org/forgejo/runner/src/tag/v6.3.1/examples/lxc-systemd/forgejo-runner-service.sh`

## Description

- Each runner is assigned a unique serial number (`$INPUTS_SERIAL`)
- The configuration is in `/etc/forgejo-runner/$INPUTS_SERIAL`
- The environment variables are in `/etc/forgejo-runner/$INPUTS_SERIAL/env`
- The cache is in `/var/lib/forgejo-runner/runner-$INPUTS_SERIAL`
- The systemd service unit is `forgejo-runner@$INPUTS_SERIAL`
- The logs of the runner daemon are in `/var/log/forgejo-runner/$INPUTS_SERIAL.log`

## How it works

- Creating a runner (for instance with `INPUTS_SERIAL=30 INPUTS_TOKEN=$TOKEN INPUTS_FORGEJO=https://code.forgejo.org forgejo-runner-service.sh`) will:
  - use `$INPUTS_TOKEN` to register on `$INPUTS_FORGEJO` and save the result in the `/etc/forgejo-runner/$INPUTS_SERIAL/.runner` file
  - generate a default configuration file in the `/etc/forgejo-runner/$INPUTS_SERIAL/config.yml` file which can then be manually edited
- Each runner is launched in a dedicated LXC container named `runner-$INPUTS_SERIAL-lxc` with the following bind mounts:
  - `/etc/forgejo-runner/$INPUTS_SERIAL`
  - `/var/lib/forgejo-runner/runner-$INPUTS_SERIAL/.cache/actcache`
- `systemctl start forgejo-runner@$INPUTS_SERIAL` will do the following when it starts and every `$INPUTS_LIFETIME` interval after that:
  - attempt to gracefully stop (SIGTERM) the runner, waiting for all jobs to complete
  - forcibly kill the runner if it does not stop within 6h
  - shutdown the LXC container and delete it (the volumes bind mounted are preserved)
  - create a brand new LXC container (with the specified `$INPUTS_LXC_CONFIG`)
  - install and run a Forgejo runner daemon in the LXC container using `/etc/forgejo-runner/$INPUTS_SERIAL/config.yml`
  - redirect the output of the runner to `/var/log/forgejo-runner/$INPUTS_SERIAL.log`
- `systemctl stop forgejo-runner@$INPUTS_SERIAL` will stop the runner but keep the LXC container running

## Creation

The creation of a new runner is driven by the following environment variables:

- `INPUTS_SERIAL`: unique number in the range `[10-100]` (check `/etc/forgejo-runner`)
- `INPUTS_TOKEN`: a runner registration token obtained from the web UI
- `INPUTS_FORGEJO`: the Forgejo instance from which `INPUTS_TOKEN` was obtained (e.g. https://code.forgejo.org)
- `INPUTS_RUNNER_VERSION`: the version of the Forgejo runner as found in https://code.forgejo.org/forgejo/runner/releases (e.g. 6.3.0)
- `INPUTS_LXC_CONFIG`: the value of the `--config` argument of [lxc-helpers](https://code.forgejo.org/forgejo/lxc-helpers/#usage) used when creating the LXC container for the runner (e.g. `docker`)
- `INPUTS_LIFETIME`: the LXC container is re-created when its lifetime expires (e.g. 7d)

## Hacking

- An existing LXC configuration will not be modified. If `lxc-ls` exists, it is assumed that LXC is configured and ready to be used.
- Migrating an existing runner:
  ```sh
  serial=10
  mkdir /etc/forgejo-runner/$serial
  cp .runner config.yml /etc/forgejo-runner/$serial
  INPUTS_SERIAL=$serial INPUTS_FORGEJO=https://code.forgejo.org forgejo-runner-service.sh
  systemctl status forgejo-runner@$serial
  ```
- Set debug by adding `VERBOSE=true` in `/etc/forgejo-runner/$INPUTS_SERIAL/env`

### Use a specific version of the Forgejo runner

The goal is that a LXC container uses a version of the Forgejo runner
that is different from the default. It needs to be installed and pinned.

- Install: `INPUTS_RUNNER_VERSION=6.3.0 forgejo-runner-service.sh install_runner`
- Pin the version in `/etc/forgejo-runner/N/env` (e.g. `INPUTS_RUNNER_VERSION=6.3.0`)
