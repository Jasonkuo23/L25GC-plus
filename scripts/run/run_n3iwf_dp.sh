#!/bin/bash
set -euo pipefail

workspace_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
binary="$workspace_dir/NFs/onvm-upf/build/5gc/l25gc_n3iwf_dp"
topology="$workspace_dir/config/n3iwf_dp_topology.env"

if [[ ! -r "$topology" ]]; then
    echo "N3IWF runtime settings not found: $topology" >&2
    exit 1
fi

# shellcheck source=../../config/n3iwf_dp_topology.env
source "$topology"

service_id="$N3IWF_DP_SERVICE_ID"
instance_id="$N3IWF_DP_INSTANCE_ID"
upf_service_id="$UPF_U_SERVICE_ID"
core_id="$N3IWF_DP_CORE_ID"
access_port="$ONVM_ACCESS_PORT"
access_interface="$ONVM_ACCESS_INTERFACE"
control_socket="${N3IWF_DP_CONTROL_SOCKET:-/run/l25gc/n3iwf-dp.sock}"
cp_tap="${N3IWF_CP_TAP_INTERFACE:-n3iwf-cp}"
nwu_ipv4="$N3IWF_TEST_NWU_IPV4"
nwu_prefix="$NWU_PREFIX_LENGTH"

if ! "$workspace_dir/scripts/check_n3iwf_topology.sh"; then
    echo "N3IWF topology validation failed" >&2
    exit 1
fi

if [[ ! -r "/sys/class/net/$access_interface/address" ]]; then
    echo "Access interface not found: $access_interface" >&2
    exit 1
fi
access_mac=$(<"/sys/class/net/$access_interface/address")
if [[ ! $access_mac =~ ^([[:xdigit:]]{2}:){5}[[:xdigit:]]{2}$ ||
      $access_mac == "00:00:00:00:00:00" ]]; then
    echo "Invalid access-interface MAC on $access_interface: $access_mac" >&2
    exit 1
fi
if ip -o -4 address show dev "$access_interface" | awk '{print $4}' |
    cut -d/ -f1 | grep -Fxq "$nwu_ipv4"; then
    echo "Duplicate NWu address $nwu_ipv4 exists on $access_interface" >&2
    echo "Keep the mlx5 interface UP, but remove its address:" >&2
    echo "  sudo ip address del $nwu_ipv4/$nwu_prefix dev $access_interface" >&2
    exit 1
fi

if [[ ! -c /dev/net/tun ]]; then
    sudo modprobe tun
fi
if [[ ! -c /dev/net/tun ]]; then
    echo "Linux TUN/TAP device is unavailable: /dev/net/tun" >&2
    exit 1
fi
if ip link show dev "$cp_tap" >/dev/null 2>&1; then
    if ! ip tuntap show | awk -F: -v name="$cp_tap" \
        '$1 == name { found = 1 } END { exit !found }'; then
        echo "Refusing to use non-TAP interface: $cp_tap" >&2
        exit 1
    fi
else
    sudo ip tuntap add dev "$cp_tap" mode tap
fi
sudo ip link set dev "$cp_tap" down
sudo ip link set dev "$cp_tap" address "$access_mac"
sudo ip address replace "$nwu_ipv4/$nwu_prefix" dev "$cp_tap"
sudo ip link set dev "$cp_tap" up
echo "N3IWF control TAP $cp_tap uses access MAC $access_mac"

if [[ ! -x "$binary" ]]; then
    echo "N3IWF-DP binary not found: $binary" >&2
    echo "Build it with: cd NFs/onvm-upf && ./env/bin/meson compile -C build l25gc_n3iwf_dp" >&2
    exit 1
fi

manager_pid=$(pgrep -u root -o onvm_mgr || true)
if [[ -z "$manager_pid" ]]; then
    echo "ONVM manager is not running" >&2
    exit 1
fi

# DPDK virtual devices belong to the primary process and cannot be added by an
# already-running secondary.  Fail here with an actionable message instead of
# allowing n3iwf-dp to register and later report that no compatible cryptodev
# exists.
if [[ "${N3IWF_DP_SOFTWARE_IPSEC:-0}" == "1" ]]; then
    manager_cmdline=$(sudo cat "/proc/$manager_pid/cmdline" | tr '\0' ' ')
    if [[ $manager_cmdline != *"--vdev"*"crypto_aesni_mb"* ]]; then
        echo "ONVM manager was started without the AESNI-MB cryptodev" >&2
        echo "Stop all ONVM NFs and the manager, then start the manager with:" >&2
        echo "  N3IWF_DP_SOFTWARE_IPSEC=1 ./scripts/run/run_onvm_mgr.sh" >&2
        echo "The manager is the DPDK primary process and must create the vdev." >&2
        exit 1
    fi
fi

sudo mkdir -p "$(dirname "$control_socket")"
if sudo test -e "$control_socket"; then
    if ! sudo test -S "$control_socket"; then
        echo "Refusing to replace non-socket control path: $control_socket" >&2
        exit 1
    fi
    sudo rm -f "$control_socket"
fi

nf_args=(-c "$control_socket" -s "$upf_service_id" -a "$access_port"
    -p "$cp_tap" -i "$nwu_ipv4")
if [[ "${N3IWF_DP_KERNEL_SIGNALLING_ESP:-0}" == "1" ]]; then
    echo "WARNING: kernel XFRM owns signalling ESP during the migration phase" >&2
    nf_args+=(-k)
fi
if [[ "${N3IWF_DP_CLEAR_TEST:-0}" == "1" ]]; then
    echo "WARNING: starting N3IWF-DP in unencrypted clear-GRE test mode" >&2
    nf_args+=(-t)
fi
if [[ "${N3IWF_DP_SOFTWARE_IPSEC:-0}" == "1" ]]; then
    if [[ "${N3IWF_DP_CLEAR_TEST:-0}" == "1" ]]; then
        echo "Clear-GRE and software-IPsec modes are mutually exclusive" >&2
        exit 1
    fi
    echo "Starting N3IWF-DP with DPDK software IPsec"
    nf_args+=(-e)
fi

exec sudo "$binary" \
    -l "$core_id" -n 3 --proc-type=secondary -- \
    -n "$instance_id" -r "$service_id" -- \
    "${nf_args[@]}"
