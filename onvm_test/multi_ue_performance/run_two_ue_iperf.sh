#!/usr/bin/env bash
set -euo pipefail

usage() {
    cat <<'EOF'
Usage: run_two_ue_iperf.sh MODE TARGET RESULT_DIR [DURATION] [OMIT]

  MODE       uplink, downlink, or bidir
  TARGET     ue1, ue2, or both
  RESULT_DIR existing directory for this trial
  DURATION   measured seconds after the omitted warm-up (default: 60)
  OMIT       warm-up seconds omitted by iperf3 (default: 5)

Optional environment:
  DN_IP=192.168.3.2  UE1_IP=10.60.0.1  UE2_IP=10.60.0.2
  UE1_NS=n3iwue1     UE2_NS=n3iwue2
  UE1_CPU=<cpu>      UE2_CPU=<cpu>
EOF
}

if (( $# < 3 || $# > 5 )); then
    usage >&2
    exit 2
fi

mode=$1
target=$2
result_dir=$3
duration=${4:-60}
omit=${5:-5}

dn_ip=${DN_IP:-192.168.3.2}
ue1_ip=${UE1_IP:-10.60.0.1}
ue2_ip=${UE2_IP:-10.60.0.2}
ue1_ns=${UE1_NS:-n3iwue1}
ue2_ns=${UE2_NS:-n3iwue2}
ue1_cpu=${UE1_CPU:-}
ue2_cpu=${UE2_CPU:-}

case "$mode" in
    uplink) mode_args=() ;;
    downlink) mode_args=(-R) ;;
    bidir) mode_args=(--bidir) ;;
    *) usage >&2; exit 2 ;;
esac
case "$target" in
    ue1|ue2|both) ;;
    *) usage >&2; exit 2 ;;
esac
if [[ ! $duration =~ ^[1-9][0-9]*$ || ! $omit =~ ^[0-9]+$ ]]; then
    echo "DURATION and OMIT must be non-negative integers; DURATION must be positive" >&2
    exit 2
fi
if [[ ! -d $result_dir ]]; then
    echo "RESULT_DIR must already exist: $result_dir" >&2
    exit 2
fi

run_client() {
    local label=$1 namespace=$2 bind_ip=$3 port=$4 cpu=$5
    local -a command=(sudo ip netns exec "$namespace" ip vrf exec vrf-pdu-10)

    if [[ -n $cpu ]]; then
        command+=(taskset -c "$cpu")
    fi
    command+=(iperf3 -J -B "$bind_ip" -c "$dn_ip" -p "$port"
        -t "$duration" -O "$omit" -i 1 "${mode_args[@]}")

    printf '%q ' "${command[@]}" >"$result_dir/$label.command"
    printf '\n' >>"$result_dir/$label.command"
    "${command[@]}" >"$result_dir/$label.iperf3.json" \
        2>"$result_dir/$label.iperf3.stderr" &
    last_pid=$!
}

date -u +%Y-%m-%dT%H:%M:%SZ >"$result_dir/client.started.utc"
printf '%s\n' "$mode" >"$result_dir/mode"
printf '%s\n' "$target" >"$result_dir/target"
printf '%s\n' "$duration" >"$result_dir/duration.seconds"
printf '%s\n' "$omit" >"$result_dir/omit.seconds"
iperf3 --version >"$result_dir/iperf3.version" 2>&1

pids=()
labels=()
if [[ $target == ue1 || $target == both ]]; then
    run_client ue1 "$ue1_ns" "$ue1_ip" 5201 "$ue1_cpu"
    pids+=("$last_pid")
    labels+=(ue1)
fi
if [[ $target == ue2 || $target == both ]]; then
    run_client ue2 "$ue2_ns" "$ue2_ip" 5202 "$ue2_cpu"
    pids+=("$last_pid")
    labels+=(ue2)
fi

status=0
set +e
for index in "${!pids[@]}"; do
    wait "${pids[$index]}"
    client_status=$?
    printf '%d\n' "$client_status" >"$result_dir/${labels[$index]}.exit_status"
    if (( client_status != 0 )); then
        status=$client_status
    fi
done
set -e
date -u +%Y-%m-%dT%H:%M:%SZ >"$result_dir/client.finished.utc"

if (( status != 0 )); then
    echo "One or more iperf3 clients failed; inspect $result_dir/*.stderr" >&2
fi
exit "$status"
