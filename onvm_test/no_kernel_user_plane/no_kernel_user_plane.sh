#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
suite_version=no-kernel-user-plane-v1

usage() {
    cat <<'EOF'
Usage:
  no_kernel_user_plane.sh init <result-dir> <proof-plan> <n3iwf-config> <topology-env> <upf-u-config>
  no_kernel_user_plane.sh snapshot <result-dir> <before|after>
  no_kernel_user_plane.sh capture-local <result-dir>
  no_kernel_user_plane.sh collect-remote <result-dir> <ue-ssh-host> <dn-ssh-host>
  no_kernel_user_plane.sh analyze <result-dir>
  no_kernel_user_plane.sh gate <result-dir>
EOF
}

die() { echo "no-kernel-user-plane: $*" >&2; exit 1; }

plan_value() {
    local plan=$1 key=$2
    awk -F '\t' -v key="$key" '$1 == key { print $2; found=1; exit } END { exit !found }' "$plan"
}

require_result() {
    local result_dir=$1
    [[ -f "$result_dir/metadata.tsv" && -f "$result_dir/config/proof-plan.tsv" ]] ||
        die "not an initialized result directory: $result_dir"
    grep -q $'^suite_version\tno-kernel-user-plane-v1$' "$result_dir/metadata.tsv" ||
        die "unsupported proof version"
}

validate_plan() {
    local plan=$1 key value protocol port
    [[ -r "$plan" ]] || die "cannot read proof plan: $plan"
    for key in proof_version topology_id host_access_interface tap_interface xfrm_interface \
        ue_access_interface dn_interface ue_outer_ipv4 n3iwf_outer_ipv4 ue_pdu_ipv4 dn_ipv4 \
        logical_n3iwf_ipv4 logical_upf_ipv4 ue_to_n3iwf_spi n3iwf_to_ue_spi \
        traffic_protocol traffic_port capture_seconds; do
        value=$(plan_value "$plan" "$key") || die "proof plan is missing $key"
        [[ -n "$value" && "$value" != *CHANGE_ME* ]] || die "proof plan has no value for $key"
    done
    [[ $(plan_value "$plan" proof_version) == no-kernel-user-plane-plan-v1 ]] ||
        die "unsupported proof-plan version"
    protocol=$(plan_value "$plan" traffic_protocol)
    [[ "$protocol" == tcp || "$protocol" == udp || "$protocol" == icmp ]] ||
        die "traffic_protocol must be tcp, udp, or icmp"
    port=$(plan_value "$plan" traffic_port)
    [[ "$port" =~ ^[0-9]+$ && "$port" -le 65535 ]] || die "invalid traffic_port"
    [[ "$protocol" == icmp || "$port" -gt 0 ]] || die "TCP/UDP traffic_port must be non-zero"
    value=$(plan_value "$plan" capture_seconds)
    [[ "$value" =~ ^[0-9]+$ && "$value" -ge 10 ]] || die "capture_seconds must be at least 10"
}

capture_repo_state() {
    local result_dir=$1 component path label
    git -C "$repo_root" rev-parse HEAD >"$result_dir/revisions/root.head"
    git -C "$repo_root" status --short --branch >"$result_dir/revisions/root.status"
    git -C "$repo_root" diff --binary >"$result_dir/revisions/root.diff"
    git -C "$repo_root" diff --cached --binary >"$result_dir/revisions/root.cached.diff"
    git -C "$repo_root" submodule status --recursive >"$result_dir/revisions/submodules.status"
    for component in NFs/n3iwf NFs/onvm-upf; do
        path="$repo_root/$component"
        label=${component//\//_}
        git -C "$path" rev-parse HEAD >"$result_dir/revisions/$label.head"
        git -C "$path" status --short --branch >"$result_dir/revisions/$label.status"
        git -C "$path" diff --binary >"$result_dir/revisions/$label.diff"
        git -C "$path" diff --cached --binary >"$result_dir/revisions/$label.cached.diff"
        git -C "$path" ls-files --others --exclude-standard >"$result_dir/revisions/$label.untracked"
        : >"$result_dir/revisions/$label.untracked.sha256"
        while IFS= read -r untracked; do
            [[ -n "$untracked" ]] && sha256sum "$path/$untracked" \
                >>"$result_dir/revisions/$label.untracked.sha256"
        done <"$result_dir/revisions/$label.untracked"
    done
}

init_run() {
    [[ $# -eq 5 ]] || die "init requires result dir, plan, N3IWF config, topology, and UPF-U config"
    local result_dir=$1 plan=$2 n3iwf_config=$3 topology=$4 upf_config=$5 backend timestamp
    local expected actual key
    validate_plan "$plan"
    for file in "$n3iwf_config" "$topology" "$upf_config"; do
        [[ -r "$file" ]] || die "cannot read input: $file"
    done
    backend=$(awk '$1 == "userPlaneBackend:" { value=$2; gsub(/["\047]/, "", value); print value; exit }' \
        "$n3iwf_config")
    [[ "$backend" == onvm ]] || die "N3IWF config must select userPlaneBackend: onvm"
    for key in N3IWF_N3_IPV4 UPF_N3_IPV4 ONVM_ACCESS_INTERFACE N3IWF_CP_TAP_INTERFACE; do
        case "$key" in
            N3IWF_N3_IPV4) expected=$(plan_value "$plan" logical_n3iwf_ipv4) ;;
            UPF_N3_IPV4) expected=$(plan_value "$plan" logical_upf_ipv4) ;;
            ONVM_ACCESS_INTERFACE) expected=$(plan_value "$plan" host_access_interface) ;;
            N3IWF_CP_TAP_INTERFACE) expected=$(plan_value "$plan" tap_interface) ;;
        esac
        actual=$(awk -F= -v key="$key" '$1 == key { print $2; found=1; exit } END { exit !found }' \
            "$topology") || die "topology is missing $key"
        [[ "$actual" == "$expected" ]] ||
            die "plan/topology mismatch for $key: $expected != $actual"
    done
    expected=$(plan_value "$plan" logical_n3iwf_ipv4)
    grep -Eq "peer_n3_ip:[[:space:]]*[\"']?$expected([\"']?[[:space:]]*(#.*)?)?$" "$upf_config" ||
        die "UPF-U config does not select plan logical N3IWF peer $expected"
    grep -Eq 'enabled:[[:space:]]*(true|1)([[:space:]]*(#.*)?)?$' "$upf_config" ||
        die "UPF-U N3IWF logical routing is not enabled"
    [[ ! -e "$result_dir" ]] || die "result path already exists: $result_dir"
    mkdir -p "$result_dir"/{captures,config,raw,revisions,snapshots}
    timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    {
        printf 'suite_version\t%s\n' "$suite_version"
        printf 'created_utc\t%s\n' "$timestamp"
        printf 'host\t%s\n' "$(hostname)"
        printf 'plan_sha256\t%s\n' "$(sha256sum "$plan" | awk '{print $1}')"
        printf 'analyzer_sha256\t%s\n' "$(sha256sum "$script_dir/proof.py" | awk '{print $1}')"
    } >"$result_dir/metadata.tsv"
    cp "$plan" "$result_dir/config/proof-plan.tsv"
    cp "$n3iwf_config" "$result_dir/config/n3iwfcfg.yaml"
    cp "$topology" "$result_dir/config/n3iwf_dp_topology.env"
    cp "$upf_config" "$result_dir/config/upf_u.yaml"
    capture_repo_state "$result_dir"
    echo "initialized proof run: $result_dir"
}

snapshot_one() {
    local out=$1 label=$2
    shift 2
    set +e
    "$@" >"$out/$label.txt" 2>&1
    local rc=$?
    set -e
    printf '%s\t%s\t' "$label" "$rc" >>"$out/status.tsv"
    printf '%q ' "$@" >>"$out/status.tsv"
    printf '\n' >>"$out/status.tsv"
}

snapshot_run() {
    [[ $# -eq 2 ]] || die "snapshot requires result directory and before|after"
    local result_dir=$1 name=$2 plan access tap xfrm out
    require_result "$result_dir"
    [[ "$name" == before || "$name" == after ]] || die "snapshot name must be before or after"
    out="$result_dir/snapshots/$name"
    [[ ! -e "$out" ]] || die "snapshot already exists: $out"
    mkdir "$out"
    : >"$out/status.tsv"
    plan="$result_dir/config/proof-plan.tsv"
    access=$(plan_value "$plan" host_access_interface)
    tap=$(plan_value "$plan" tap_interface)
    xfrm=$(plan_value "$plan" xfrm_interface)
    snapshot_one "$out" ip-address ip -d -s address show
    snapshot_one "$out" ip-route ip route show table all
    snapshot_one "$out" access-link ip -s link show dev "$access"
    snapshot_one "$out" tap-link ip -s link show dev "$tap"
    snapshot_one "$out" xfrm-link ip -d -s link show dev "$xfrm"
    snapshot_one "$out" xfrm-state ip -s xfrm state
    snapshot_one "$out" xfrm-policy ip -s xfrm policy
    snapshot_one "$out" n3iwf-dp-stats "$repo_root/bin/n3iwf-dpctl" -operation stats
    snapshot_one "$out" processes pgrep -a -f 'onvm_mgr|l25gc_n3iwf_dp|l25gc_upf_u|/n3iwf'
    echo "snapshot saved: $out"
}

capture_local() {
    [[ $# -eq 1 ]] || die "capture-local requires result directory"
    local result_dir=$1 plan access tap xfrm seconds pids=() labels=() index rc failed=0
    require_result "$result_dir"
    [[ ${EUID:-$(id -u)} -eq 0 ]] || die "capture-local must run as root"
    command -v tcpdump >/dev/null || die "tcpdump is required"
    plan="$result_dir/config/proof-plan.tsv"
    access=$(plan_value "$plan" host_access_interface)
    tap=$(plan_value "$plan" tap_interface)
    xfrm=$(plan_value "$plan" xfrm_interface)
    seconds=$(plan_value "$plan" capture_seconds)
    for interface in "$access" "$tap" "$xfrm"; do
        ip link show dev "$interface" >/dev/null || die "interface not found: $interface"
    done
    [[ ! -e "$result_dir/captures/tap.pcap" ]] || die "local captures already exist"
    timeout --signal=INT "$seconds" tcpdump -U -nn -s 128 -B 131072 -i "$access" -w \
        "$result_dir/captures/host-access.pcap" 'arp or ip' \
        >"$result_dir/raw/host-access-tcpdump.log" 2>&1 &
    pids+=("$!"); labels+=(host-access)
    timeout --signal=INT "$seconds" tcpdump -U -nn -s 128 -B 131072 -i "$tap" -w \
        "$result_dir/captures/tap.pcap" 'arp or ip' \
        >"$result_dir/raw/tap-tcpdump.log" 2>&1 &
    pids+=("$!"); labels+=(tap)
    timeout --signal=INT "$seconds" tcpdump -U -nn -s 128 -B 131072 -i "$xfrm" -w \
        "$result_dir/captures/xfrm.pcap" 'ip' \
        >"$result_dir/raw/xfrm-tcpdump.log" 2>&1 &
    pids+=("$!"); labels+=(xfrm)
    timeout --signal=INT "$seconds" tcpdump -U -nn -s 128 -B 131072 -i any -w \
        "$result_dir/captures/kernel-n3.pcap" 'udp port 2152' \
        >"$result_dir/raw/kernel-n3-tcpdump.log" 2>&1 &
    pids+=("$!"); labels+=(kernel-n3)
    trap 'for capture_pid in "${pids[@]}"; do kill -INT "$capture_pid" 2>/dev/null || true; done' INT TERM EXIT
    echo "local captures active for $seconds seconds; start the remote UE/DN captures and marker traffic now"
    for index in "${!pids[@]}"; do
        set +e
        wait "${pids[$index]}"
        rc=$?
        set -e
        if [[ $rc -ne 0 && $rc -ne 124 ]]; then
            echo "${labels[$index]} capture failed with status $rc" >&2
            failed=1
        fi
    done
    trap - INT TERM EXIT
    [[ $failed -eq 0 ]] || return 1
    echo "local capture interval complete"
}

collect_remote() {
    [[ $# -eq 3 ]] || die "collect-remote requires result directory, UE SSH host, and DN SSH host"
    local result_dir=$1 ue_host=$2 dn_host=$3
    require_result "$result_dir"
    command -v ssh >/dev/null || die "ssh is required"
    command -v tcpdump >/dev/null || die "tcpdump is required to validate collected pcaps"
    collect_role() {
        local role=$1 host=$2 stage existing=0 missing=0 file
        shift 2
        for file in "$@"; do
            [[ -e "$result_dir/captures/$file" || -e "$result_dir/raw/$file" ]] &&
                existing=$((existing + 1)) || missing=$((missing + 1))
        done
        if [[ $missing -eq 0 ]]; then
            echo "$role evidence already retained; not overwriting"
            return
        fi
        [[ $existing -eq 0 ]] || die "$role evidence is only partially retained; inspect the result directory"
        stage=$(mktemp -d "$result_dir/.collect-$role.XXXXXX")
        if ! ssh -- "$host" tar -C /tmp -cf - "$@" | tar -C "$stage" -xf -; then
            rm -r "$stage"
            die "cannot collect $role evidence from $host; verify SSH login and file permissions"
        fi
        for file in "$@"; do
            [[ -s "$stage/$file" ]] || { rm -r "$stage"; die "$host:/tmp/$file is missing or empty"; }
        done
        for file in "$stage"/*.pcap; do
            if ! tcpdump -nn -r "$file" >/dev/null 2>&1; then
                rm -r "$stage"
                die "$host:/tmp/$(basename "$file") is truncated or invalid; wait for tcpdump to exit"
            fi
        done
        if [[ "$role" == ue ]]; then
            mv "$stage/ue-access.pcap" "$result_dir/captures/ue-access.pcap"
            mv "$stage/traffic-ue.log" "$result_dir/raw/traffic-ue.log"
            mv "$stage/ue-access-tcpdump.log" "$result_dir/raw/ue-access-tcpdump.log"
        else
            mv "$stage/dn.pcap" "$result_dir/captures/dn.pcap"
            mv "$stage/traffic-dn.log" "$result_dir/raw/traffic-dn.log"
            mv "$stage/dn-tcpdump.log" "$result_dir/raw/dn-tcpdump.log"
        fi
        rmdir "$stage"
        echo "collected $role evidence from $host"
    }
    collect_role ue "$ue_host" ue-access.pcap traffic-ue.log ue-access-tcpdump.log
    collect_role dn "$dn_host" dn.pcap traffic-dn.log dn-tcpdump.log
    {
        printf 'role\tssh_target\n'
        printf 'ue\t%s\n' "$ue_host"
        printf 'dn\t%s\n' "$dn_host"
    } >"$result_dir/raw/remote-hosts.tsv"
}

analyze_run() {
    [[ $# -eq 1 ]] || die "analyze requires result directory"
    local result_dir=$1 recorded actual analysis_dir retained timestamp
    require_result "$result_dir"
    recorded=$(awk -F '\t' '$1 == "plan_sha256" { print $2 }' "$result_dir/metadata.tsv")
    actual=$(sha256sum "$result_dir/config/proof-plan.tsv" | awk '{print $1}')
    [[ -n "$recorded" && "$recorded" == "$actual" ]] || die "retained proof plan was modified"
    recorded=$(awk -F '\t' '$1 == "analyzer_sha256" { print $2 }' "$result_dir/metadata.tsv")
    actual=$(sha256sum "$script_dir/proof.py" | awk '{print $1}')
    [[ -n "$recorded" ]] || die "initial analyzer hash is missing"
    analysis_dir="$result_dir/analysis"
    retained="$analysis_dir/proof.$actual.py"
    mkdir -p "$analysis_dir"
    if [[ ! -e "$retained" ]]; then
        cp "$script_dir/proof.py" "$retained"
    fi
    [[ $(sha256sum "$retained" | awk '{print $1}') == "$actual" ]] ||
        die "retained analyzer has unexpected content: $retained"
    if [[ ! -f "$analysis_dir/history.tsv" ]]; then
        printf 'analyzed_utc\tanalyzer_sha256\tinitial_analyzer_sha256\n' \
            >"$analysis_dir/history.tsv"
    fi
    if ! awk -F '\t' -v hash="$actual" 'NR > 1 && $2 == hash { found=1 } END { exit !found }' \
        "$analysis_dir/history.tsv"; then
        timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)
        printf '%s\t%s\t%s\n' "$timestamp" "$actual" "$recorded" \
            >>"$analysis_dir/history.tsv"
    fi
    python3 "$retained" "$result_dir"
}

gate_run() {
    [[ $# -eq 1 ]] || die "gate requires result directory"
    analyze_run "$1"
    [[ -f "$1/gate.status" && $(<"$1/gate.status") == PASS ]] || die "proof has not passed analysis"
    echo "no-kernel-user-plane acceptance gate passed: $1"
}

[[ $# -gt 0 ]] || { usage; exit 2; }
action=$1
shift
case "$action" in
    init) init_run "$@" ;;
    snapshot) snapshot_run "$@" ;;
    capture-local) capture_local "$@" ;;
    collect-remote) collect_remote "$@" ;;
    analyze) analyze_run "$@" ;;
    gate) gate_run "$@" ;;
    *) usage; exit 2 ;;
esac
