#!/bin/bash
set -euo pipefail

workspace_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
binary="$workspace_dir/NFs/onvm-upf/build/5gc/l25gc_n3iwf_dp"
topology="$workspace_dir/config/n3iwf_dp_topology.env"

if [[ ! -r "$topology" ]]; then
    echo "N3IWF topology contract not found: $topology" >&2
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

if ! "$workspace_dir/scripts/check_n3iwf_topology.sh"; then
    echo "N3IWF topology validation failed" >&2
    exit 1
fi

if [[ ! -x "$binary" ]]; then
    echo "N3IWF-DP binary not found: $binary" >&2
    echo "Build it with: cd NFs/onvm-upf && ./env/bin/meson compile -C build l25gc_n3iwf_dp" >&2
    exit 1
fi

if ! pgrep -u root onvm_mgr >/dev/null; then
    echo "ONVM manager is not running" >&2
    exit 1
fi

sudo mkdir -p "$(dirname "$control_socket")"
if sudo test -e "$control_socket"; then
    if ! sudo test -S "$control_socket"; then
        echo "Refusing to replace non-socket control path: $control_socket" >&2
        exit 1
    fi
    sudo rm -f "$control_socket"
fi

nf_args=(-c "$control_socket" -s "$upf_service_id" -a "$access_port")
if [[ "${N3IWF_DP_CLEAR_TEST:-0}" == "1" ]]; then
    echo "WARNING: starting N3IWF-DP in unencrypted clear-GRE test mode" >&2
    nf_args+=(-t)
fi

exec sudo "$binary" \
    -l "$core_id" -n 3 --proc-type=secondary -- \
    -n "$instance_id" -r "$service_id" -- \
    "${nf_args[@]}"
