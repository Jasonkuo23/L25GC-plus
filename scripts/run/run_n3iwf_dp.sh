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
control_socket="${N3IWF_DP_CONTROL_SOCKET:-/run/l25gc/n3iwf-dp.sock}"
cp_tap="${N3IWF_CP_TAP_INTERFACE:-n3iwf-cp}"
nwu_ipv4="$N3IWF_TEST_NWU_IPV4"
nwu_prefix="$NWU_PREFIX_LENGTH"

if [[ ! -x "$binary" ]]; then
    echo "N3IWF-DP binary not found: $binary" >&2
    echo "Build it with: cd NFs/onvm-upf && ./env/bin/meson compile -C build l25gc_n3iwf_dp" >&2
    exit 1
fi

if ! pgrep -u root onvm_mgr >/dev/null; then
    echo "ONVM manager is not running" >&2
    exit 1
fi

# The persistent TAP is the only kernel boundary on NWu. Refuse to reuse an
# arbitrary interface with the configured name.
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
sudo ip address replace "$nwu_ipv4/$nwu_prefix" dev "$cp_tap"
sudo ip link set dev "$cp_tap" up

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

exec sudo "$binary" \
    -l "$core_id" -n 3 --proc-type=secondary -- \
    -n "$instance_id" -r "$service_id" -- \
    "${nf_args[@]}"
