#!/bin/bash
# SPDX-License-Identifier: MIT

export DEBIAN_FRONTEND=noninteractive

LXC_SELF_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
LXC_BIN=/usr/local/bin
LXC_CONTAINER_CONFIG_ALL="unprivileged lxc libvirt docker k8s"
LXC_CONTAINER_CONFIG_DEFAULT="lxc libvirt docker"
LXC_IPV6_PREFIX_DEFAULT="fd15"
LXC_DOCKER_PREFIX_DEFAULT="172.17"
LXC_IPV6_DOCKER_PREFIX_DEFAULT="fd00:d0ca"
LXC_APT_TOO_OLD='1 week ago'
: ${LXC_TRANSACTION_TIMEOUT:=600}
LXC_TRANSACTION_LOCK_FILE=/tmp/lxc-helper.lock

: ${LXC_SUDO:=}
: ${LXC_CONTAINER_RELEASE:=bookworm}
: ${LXC_CONTAINER_CONFIG:=$LXC_CONTAINER_CONFIG_DEFAULT}
: ${LXC_HOME:=/home}
: ${LXC_VERBOSE:=false}

source /etc/os-release

function lxc_release() {
    echo $VERSION_CODENAME
}

function lxc_template_release() {
    echo lxc-helpers-$LXC_CONTAINER_RELEASE
}

function lxc_directory() {
    local name="$1"

    echo /var/lib/lxc/$name
}

function lxc_root() {
    local name="$1"

    echo $(lxc_directory $name)/rootfs
}

function lxc_config() {
    local name="$1"

    echo $(lxc_directory $name)/config
}

function lxc_container_run() {
    local name="$1"
    shift

    $LXC_SUDO lxc-attach --clear-env --name $name -- "$@"
}

function lxc_transaction_lock() {
    exec 7>$LXC_TRANSACTION_LOCK_FILE
    flock --timeout $LXC_TRANSACTION_TIMEOUT 7
}

function lxc_transaction_unlock() {
    exec 7>&-
}

function lxc_transaction_draft_name() {
    echo "lxc-helper-draft"
}

function lxc_transaction_begin() {
    local name=$1 # not actually used but it helps when reading in the caller
    local draft=$(lxc_transaction_draft_name)

    lxc_transaction_lock
    lxc_container_destroy $draft
}

function lxc_transaction_commit() {
    local name=$1
    local draft=$(lxc_transaction_draft_name)

    # do not use lxc-copy because it is not atomic if lxc-copy is
    # interrupted it may leave the $name container half populated
    $LXC_SUDO sed -i -e "s/$draft/$name/g" \
        $(lxc_config $draft) \
        $(lxc_root $draft)/etc/hosts \
        $(lxc_root $draft)/etc/hostname
    $LXC_SUDO rm -f $(lxc_root $draft)/var/lib/dhcp/dhclient.*
    $LXC_SUDO mv $(lxc_directory $draft) $(lxc_directory $name)
    lxc_transaction_unlock
}

function lxc_container_run_script_as() {
    local name="$1"
    local user="$2"
    local script="$3"

    $LXC_SUDO chmod +x $(lxc_root $name)$script
    $LXC_SUDO lxc-attach --name $name -- sudo --user $user $script
}

function lxc_container_run_script() {
    local name="$1"
    local script="$2"

    $LXC_SUDO chmod +x $(lxc_root $name)$script
    lxc_container_run $name $script
}

function lxc_container_inside() {
    local name="$1"
    shift

    lxc_container_run $name $LXC_BIN/lxc-helpers.sh "$@"
}

function lxc_container_user_install() {
    local name="$1"
    local user_id="$2"
    local user="$3"

    if test "$user" = root; then
        return
    fi

    local root=$(lxc_root $name)

    if ! $LXC_SUDO grep --quiet "^$user " $root/etc/sudoers; then
        $LXC_SUDO tee $root/usr/local/bin/lxc-helpers-create-user.sh >/dev/null <<EOF
#!/bin/bash
set -ex

mkdir -p $LXC_HOME
useradd --base-dir $LXC_HOME --create-home --shell /bin/bash --uid $user_id $user
for group in docker kvm libvirt ; do
    if grep --quiet \$group /etc/group ; then adduser $user \$group ; fi
done
echo "$user ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers
sudo --user $user ssh-keygen -b 2048 -N '' -f $LXC_HOME/$user/.ssh/id_rsa
EOF
        lxc_container_run_script $name /usr/local/bin/lxc-helpers-create-user.sh
    fi
}

function lxc_maybe_sudo() {
    if test $(id -u) != 0; then
        LXC_SUDO=sudo
    fi
}

function lxc_prepare_environment() {
    lxc_maybe_sudo
    if ! $(which lxc-create >/dev/null); then
        $LXC_SUDO apt-get install -y -qq make git libvirt0 libpam-cgfs bridge-utils uidmap dnsmasq-base dnsmasq dnsmasq-utils qemu-user-static
    fi
}

function lxc_container_config_nesting() {
    echo 'security.nesting = true'
}

function lxc_container_config_cap() {
    echo 'lxc.cap.drop ='
}

function lxc_container_config_net() {
    cat <<EOF
#
# /dev/net
#
lxc.cgroup2.devices.allow = c 10:200 rwm
lxc.mount.entry = /dev/net dev/net none bind,create=dir 0 0
EOF
}

function lxc_container_config_kvm() {
    cat <<EOF
#
# /dev/kvm
#
lxc.cgroup2.devices.allow = c 10:232 rwm
lxc.mount.entry = /dev/kvm dev/kvm none bind,create=file 0 0
EOF
}

function lxc_container_config_loop() {
    cat <<EOF
#
# /dev/loop
#
lxc.cgroup2.devices.allow = c 10:237 rwm
lxc.cgroup2.devices.allow = b 7:* rwm
lxc.mount.entry = /dev/loop-control dev/loop-control none bind,create=file 0 0
EOF
}

function lxc_container_config_mapper() {
    cat <<EOF
#
# /dev/mapper
#
lxc.cgroup2.devices.allow = c 10:236 rwm
lxc.mount.entry = /dev/mapper dev/mapper none bind,create=dir 0 0
EOF
}

function lxc_container_config_fuse() {
    cat <<EOF
#
# /dev/fuse
#
lxc.cgroup2.devices.allow = b 10:229 rwm
lxc.mount.entry = /dev/fuse dev/fuse none bind,create=file 0 0
EOF
}

function lxc_container_config_kmsg() {
    cat <<EOF
#
# kmsg
#
lxc.cgroup2.devices.allow = c 1:11 rwm
lxc.mount.entry = /dev/kmsg dev/kmsg none bind,create=file 0 0
EOF
}

function lxc_container_config_proc() {
    cat <<EOF
#
# /proc
#
#
# Only because k8s tries to write /proc/sys/vm/overcommit_memory
# is there a way to only allow that? Would it be enough for k8s?
#
lxc.mount.auto = proc:rw
EOF
}

function lxc_container_config() {
    for config in "$@"; do
        case $config in
        unprivileged) ;;
        lxc)
            echo nesting
            echo cap
            ;;
        docker)
            echo net
            ;;
        libvirt)
            echo cap
            echo kvm
            echo loop
            echo mapper
            echo fuse
            ;;
        k8s)
            echo cap
            echo loop
            echo mapper
            echo fuse
            echo kmsg
            echo proc
            ;;
        *)
            echo "$config unknown ($LXC_CONTAINER_CONFIG_ALL)"
            return 1
            ;;
        esac
    done | sort -u | while read config; do
        echo "#"
        echo "# include $config config snippet"
        echo "#"
        lxc_container_config_$config
    done
}

function lxc_container_configure() {
    local name="$1"

    lxc_container_config $LXC_CONTAINER_CONFIG | $LXC_SUDO tee -a $(lxc_config $name)
}

function lxc_container_install_lxc_helpers() {
    local name="$1"

    $LXC_SUDO cp -a $LXC_SELF_DIR/lxc-helpers*.sh $(lxc_root $name)/$LXC_BIN
    #
    # Wait for the network to come up
    #
    local wait_networking=$(lxc_root $name)/usr/local/bin/lxc-helpers-wait-networking.sh
    $LXC_SUDO tee $wait_networking >/dev/null <<'EOF'
#!/bin/sh -e
for d in $(seq 60); do
  getent hosts wikipedia.org > /dev/null && break
  sleep 1
done
getent hosts wikipedia.org > /dev/null || getent hosts wikipedia.org
EOF
    $LXC_SUDO chmod +x $wait_networking
}

function lxc_container_create() {
    local name="$1"

    lxc_prepare_environment
    lxc_build_template $(lxc_template_release) "$name"
}

function lxc_container_mount() {
    local name="$1"
    local dir="$2"

    local config=$(lxc_config $name)

    if ! $LXC_SUDO grep --quiet "lxc.mount.entry = $dir" $config; then
        local relative_dir=${dir##/}
        $LXC_SUDO tee -a $config >/dev/null <<<"lxc.mount.entry = $dir $relative_dir none bind,create=dir 0 0"
    fi
}

function lxc_container_start() {
    local name="$1"

    if lxc_running $name; then
        return
    fi

    local logs
    if $LXC_VERBOSE; then
        logs="--logfile=/dev/tty"
    fi

    $LXC_SUDO lxc-start $logs $name
    $LXC_SUDO lxc-wait --name $name --state RUNNING
    lxc_container_run $name /usr/local/bin/lxc-helpers-wait-networking.sh
}

function lxc_container_stop() {
    local name="$1"

    $LXC_SUDO lxc-ls -1 --running --filter="^$name" | while read container; do
        $LXC_SUDO lxc-stop --kill --name="$container"
    done
}

function lxc_container_destroy() {
    local name="$1"

    if lxc_exists "$name"; then
        lxc_container_stop $name
        $LXC_SUDO lxc-destroy --force --name="$name"
    fi
}

function lxc_exists() {
    local name="$1"

    test "$($LXC_SUDO lxc-ls --filter=^$name\$)"
}

function lxc_exists_and_apt_not_old() {
    local name="$1"

    if lxc_exists $name; then
        if lxc_apt_is_old $name; then
            $LXC_SUDO lxc-destroy --force --name="$name"
            return 1
        else
            return 0
        fi
    else
        return 1
    fi
}

function lxc_running() {
    local name="$1"

    test "$($LXC_SUDO lxc-ls --running --filter=^$name\$)"
}

function lxc_build_template_release() {
    local name="$(lxc_template_release)"

    lxc_transaction_begin $name

    if lxc_exists_and_apt_not_old $name; then
        lxc_transaction_unlock
        return
    fi

    local draft=$(lxc_transaction_draft_name)
    $LXC_SUDO lxc-create --name $draft --template debian -- --release=$LXC_CONTAINER_RELEASE
    echo 'lxc.apparmor.profile = unconfined' | $LXC_SUDO tee -a $(lxc_config $draft)
    lxc_container_install_lxc_helpers $draft
    lxc_container_start $draft
    lxc_container_run $draft apt-get update -qq
    lxc_apt_install $draft sudo git python3
    lxc_container_stop $draft
    lxc_transaction_commit $name
}

function lxc_build_template() {
    local name="$1"
    local newname="$2"

    if test "$name" = "$(lxc_template_release)"; then
        lxc_build_template_release
    fi

    lxc_transaction_begin $name
    if lxc_exists_and_apt_not_old $newname; then
        lxc_transaction_unlock
        return
    fi

    local draft=$(lxc_transaction_draft_name)
    if ! $LXC_SUDO lxc-copy --name=$name --newname=$draft; then
        echo lxc-copy --name=$name --newname=$draft failed
        return 1
    fi
    lxc_transaction_commit $newname
    lxc_container_configure $newname
}

function lxc_apt_age() {
    local name="$1"
    $LXC_SUDO stat --format %Y $(lxc_root $name)/var/cache/apt/pkgcache.bin
}

function lxc_apt_is_old() {
    local name="$1"

    local age=$(lxc_apt_age $name)
    local too_old=$(date --date "$LXC_APT_TOO_OLD" +%s)

    test $age -lt $too_old
}

function lxc_apt_install() {
    local name="$1"
    shift

    lxc_container_inside $name lxc_apt_install_inside "$@"
}

function lxc_apt_install_inside() {
    apt-get install -y -qq "$@"
}

function lxc_install_lxc() {
    local name="$1"
    local prefix="$2"
    local prefixv6="$3"

    lxc_container_inside $name lxc_install_lxc_inside $prefix $prefixv6
}

function lxc_install_lxc_inside() {
    local prefix="$1"
    local prefixv6="${2:-$LXC_IPV6_PREFIX_DEFAULT}"

    local packages="make git libvirt0 libpam-cgfs bridge-utils uidmap dnsmasq-base dnsmasq dnsmasq-utils qemu-user-static lxc-templates debootstrap"
    if test "$(lxc_release)" != bullseye; then
        packages="$packages distro-info"
    fi

    lxc_apt_install_inside $packages

    if ! grep --quiet LXC_ADDR=.$prefix.1. /etc/default/lxc-net; then
        systemctl disable --now dnsmasq
        apt-get install -y -qq lxc
        systemctl stop lxc-net
        sed -i -e '/ConditionVirtualization/d' /usr/lib/systemd/system/lxc-net.service
        systemctl daemon-reload
        cat >>/etc/default/lxc-net <<EOF
LXC_ADDR="$prefix.1"
LXC_NETMASK="255.255.255.0"
LXC_NETWORK="$prefix.0/24"
LXC_DHCP_RANGE="$prefix.2,$prefix.254"
LXC_DHCP_MAX="253"
LXC_IPV6_ADDR="$prefixv6::216:3eff:fe00:1"
LXC_IPV6_MASK="64"
LXC_IPV6_NETWORK="$prefixv6::/64"
LXC_IPV6_NAT="true"
EOF
        systemctl start lxc-net
    fi
}

function lxc_install_docker() {
    local name="$1"

    lxc_container_inside $name lxc_install_docker_inside
}

function lxc_install_docker_inside() {
    mkdir /etc/docker
    cat >/etc/docker/daemon.json <<EOF
{
  "insecure-registries": [ "10.0.0.0/8", "192.168.0.0/16" ],
  "ipv6": true,
  "fixed-cidr-v6": "$LXC_IPV6_DOCKER_PREFIX_DEFAULT:1::/64",
  "default-address-pools": [
    {"base": "$LXC_DOCKER_PREFIX_DEFAULT.0.0/16", "size": 24},
    {"base": "$LXC_IPV6_DOCKER_PREFIX_DEFAULT:2::/104", "size": 112}
  ]
}
EOF
    lxc_apt_install_inside docker.io docker-compose
}
