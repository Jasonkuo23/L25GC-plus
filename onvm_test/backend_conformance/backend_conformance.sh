#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd "$script_dir/../.." && pwd)
scenario_file="$script_dir/scenarios.tsv"
suite_version=backend-conformance-v1

usage() {
    cat <<'EOF'
Usage:
  backend_conformance.sh init <linux|onvm> <result-dir> <n3iwf-config> <test-plan>
  backend_conformance.sh run <result-dir> <case-id> -- <command> [args...]
  backend_conformance.sh record <result-dir> <case-id> <PASS|FAIL|SKIP> <command> <evidence-index> <note>
  backend_conformance.sh snapshot <result-dir> <name>
  backend_conformance.sh check <result-dir>
  backend_conformance.sh gate <result-dir>
  backend_conformance.sh report <linux-result-dir> <onvm-result-dir> <report.md>

An evidence index is a retained text file, relative to the result directory,
that lists every log/capture used for the case. PASS and FAIL require one.
SKIP is never accepted by gate and requires a reason in <note>.
EOF
}

die() {
    echo "backend-conformance: $*" >&2
    exit 1
}

clean_field() {
    printf '%s' "$1" | tr '\t\r\n' '   '
}

require_result_dir() {
    local result_dir=$1
    [[ -f "$result_dir/metadata.tsv" && -f "$result_dir/results.tsv" ]] ||
        die "not an initialized result directory: $result_dir"
    grep -q $'^suite_version\tbackend-conformance-v1$' "$result_dir/metadata.tsv" ||
        die "unsupported or missing suite version in $result_dir"
}

case_exists() {
    local case_id=$1
    awk -F '\t' -v id="$case_id" '$1 == id { found=1 } END { exit !found }' "$scenario_file"
}

validate_evidence_index() {
    local result_dir=$1 index=$2 entry count=0
    [[ -f "$index" ]] || die "evidence index does not exist: $index"
    while IFS= read -r entry || [[ -n "$entry" ]]; do
        [[ -z "$entry" || "$entry" == \#* ]] && continue
        [[ "$entry" != /* && "$entry" != *../* && "$entry" != .. ]] ||
            die "evidence path must stay inside the result directory: $entry"
        [[ -e "$result_dir/$entry" ]] || die "indexed evidence does not exist: $entry"
        count=$((count + 1))
    done <"$index"
    [[ $count -gt 0 ]] || die "evidence index is empty: $index"
}

config_backend() {
    local config=$1
    local value
    value=$(awk '
        $1 == "userPlaneBackend:" {
            value=$2
            gsub(/["\047]/, "", value)
            sub(/#.*/, "", value)
            print value
            exit
        }
    ' "$config")
    printf '%s\n' "${value:-linux}"
}

validate_test_plan() {
    local plan=$1 key value
    [[ -r "$plan" ]] || die "cannot read test plan: $plan"
    for key in plan_version topology_id ue_profiles dnn snssai ipsec_profile qfis \
        ping_count tcp_duration_seconds udp_duration_seconds udp_loss_threshold \
        traffic_payload_bytes; do
        value=$(awk -F '\t' -v key="$key" '$1 == key { print $2; found=1; exit } END { exit !found }' \
            "$plan") || die "test plan is missing $key"
        [[ -n "$value" && "$value" != *CHANGE_ME* ]] || die "test plan has no value for $key"
    done
    [[ $(awk -F '\t' '$1 == "plan_version" { print $2 }' "$plan") == \
        backend-conformance-plan-v1 ]] || die "unsupported test-plan version"
}

capture_repo_state() {
    local result_dir=$1
    local component path label

    git -C "$repo_root" rev-parse HEAD >"$result_dir/revisions/root.head"
    git -C "$repo_root" status --short --branch >"$result_dir/revisions/root.status"
    git -C "$repo_root" diff --binary >"$result_dir/revisions/root.diff"
    git -C "$repo_root" diff --cached --binary >"$result_dir/revisions/root.cached.diff"
    git -C "$repo_root" submodule status >"$result_dir/revisions/submodules.status"

    for component in NFs/n3iwf NFs/n3iwf-dp-client NFs/onvm-upf; do
        path="$repo_root/$component"
        [[ -d "$path/.git" || -f "$path/.git" ]] || continue
        label=${component//\//_}
        git -C "$path" rev-parse HEAD >"$result_dir/revisions/$label.head"
        git -C "$path" status --short --branch >"$result_dir/revisions/$label.status"
        git -C "$path" diff --binary >"$result_dir/revisions/$label.diff"
        git -C "$path" diff --cached --binary >"$result_dir/revisions/$label.cached.diff"
        git -C "$path" ls-files --others --exclude-standard >"$result_dir/revisions/$label.untracked"
        if [[ -s "$result_dir/revisions/$label.untracked" ]]; then
            while IFS= read -r untracked; do
                sha256sum "$path/$untracked"
            done <"$result_dir/revisions/$label.untracked" \
                >"$result_dir/revisions/$label.untracked.sha256"
        else
            : >"$result_dir/revisions/$label.untracked.sha256"
        fi
    done
}

init_run() {
    [[ $# -eq 4 ]] || die "init requires backend, result directory, N3IWF config, and test plan"
    local backend=$1 result_dir=$2 config=$3 plan=$4 declared timestamp plan_hash scenario_hash script_hash
    [[ "$backend" == linux || "$backend" == onvm ]] || die "backend must be linux or onvm"
    [[ -r "$config" ]] || die "cannot read N3IWF config: $config"
    validate_test_plan "$plan"
    [[ ! -e "$result_dir" ]] || die "result path already exists: $result_dir"
    declared=$(config_backend "$config")
    [[ "$declared" == "$backend" ]] ||
        die "config selects '$declared', not requested backend '$backend'"

    mkdir -p "$result_dir/revisions" "$result_dir/config" "$result_dir/raw" \
        "$result_dir/captures" "$result_dir/snapshots" "$result_dir/evidence"
    timestamp=$(date -u +%Y-%m-%dT%H:%M:%SZ)
    plan_hash=$(sha256sum "$plan" | awk '{print $1}')
    scenario_hash=$(sha256sum "$scenario_file" | awk '{print $1}')
    script_hash=$(sha256sum "${BASH_SOURCE[0]}" | awk '{print $1}')
    {
        printf 'suite_version\t%s\n' "$suite_version"
        printf 'backend\t%s\n' "$backend"
        printf 'created_utc\t%s\n' "$timestamp"
        printf 'host\t%s\n' "$(hostname)"
        printf 'repo_root\t%s\n' "$repo_root"
        printf 'source_config\t%s\n' "$(realpath "$config")"
        printf 'source_test_plan\t%s\n' "$(realpath "$plan")"
        printf 'test_plan_sha256\t%s\n' "$plan_hash"
        printf 'scenario_sha256\t%s\n' "$scenario_hash"
        printf 'harness_sha256\t%s\n' "$script_hash"
    } >"$result_dir/metadata.tsv"
    cp "$config" "$result_dir/config/n3iwfcfg.yaml"
    cp "$plan" "$result_dir/config/test-plan.tsv"
    cp "$scenario_file" "$result_dir/scenarios.tsv"
    {
        printf 'case_id\tstatus\tcommand\tevidence_index\tnote\trecorded_utc\n'
        awk -F '\t' '!/^#/ && NF { print $1 "\tNOT_RUN\t-\t-\t-\t-" }' "$scenario_file"
    } >"$result_dir/results.tsv"
    capture_repo_state "$result_dir"
    echo "initialized $backend run: $result_dir"
}

record_case() {
    [[ $# -eq 7 ]] || die "internal record_case argument error"
    local result_dir=$1 case_id=$2 status=$3 command=$4 evidence=$5 note=$6 timestamp=$7
    local temp evidence_path
    require_result_dir "$result_dir"
    case_exists "$case_id" || die "unknown case id: $case_id"
    [[ "$status" == PASS || "$status" == FAIL || "$status" == SKIP ]] ||
        die "status must be PASS, FAIL, or SKIP"
    if [[ "$status" == SKIP ]]; then
        [[ "$note" != "" && "$note" != "-" ]] || die "SKIP requires a reason"
    else
        [[ "$evidence" != "" && "$evidence" != "-" ]] ||
            die "$status requires an evidence index"
        [[ "$evidence" != /* && "$evidence" != *../* && "$evidence" != .. ]] ||
            die "evidence index must stay inside the result directory: $evidence"
        evidence_path="$result_dir/$evidence"
        validate_evidence_index "$result_dir" "$evidence_path"
    fi
    command=$(clean_field "$command")
    evidence=$(clean_field "$evidence")
    note=$(clean_field "$note")
    temp=$(mktemp "$result_dir/results.tsv.XXXXXX")
    BC_ID="$case_id" BC_STATUS="$status" BC_COMMAND="$command" BC_EVIDENCE="$evidence" \
        BC_NOTE="$note" BC_TIMESTAMP="$timestamp" awk -F '\t' -v OFS='\t' '
        BEGIN {
            id=ENVIRON["BC_ID"]; status=ENVIRON["BC_STATUS"]
            command=ENVIRON["BC_COMMAND"]; evidence=ENVIRON["BC_EVIDENCE"]
            note=ENVIRON["BC_NOTE"]; timestamp=ENVIRON["BC_TIMESTAMP"]
        }
        NR == 1 { print; next }
        $1 == id { $2=status; $3=command; $4=evidence; $5=note; $6=timestamp; found=1 }
        { print }
        END { if (!found) exit 2 }
    ' "$result_dir/results.tsv" >"$temp" || {
        rm -f "$temp"
        die "failed to update case $case_id"
    }
    mv "$temp" "$result_dir/results.tsv"
    echo "$case_id: $status"
}

record_command() {
    [[ $# -eq 6 ]] || die "record requires result directory, case, status, command, evidence, and note"
    record_case "$1" "$2" "$3" "$4" "$5" "$6" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
}

run_command() {
    [[ $# -ge 4 && "$3" == -- ]] || die "run syntax: run <result-dir> <case-id> -- <command> [args...]"
    local result_dir=$1 case_id=$2 command_text log evidence rc status arg
    shift 3
    require_result_dir "$result_dir"
    case_exists "$case_id" || die "unknown case id: $case_id"
    command_text=''
    for arg in "$@"; do
        printf -v arg '%q' "$arg"
        command_text+="${command_text:+ }$arg"
    done
    log="raw/$case_id.log"
    evidence="evidence/$case_id.txt"
    printf '%s\n' "$command_text" >"$result_dir/raw/$case_id.command"
    set +e
    "$@" >"$result_dir/$log" 2>&1
    rc=$?
    set -e
    printf '%s\n' "$log" >"$result_dir/$evidence"
    status=PASS
    [[ $rc -eq 0 ]] || status=FAIL
    record_case "$result_dir" "$case_id" "$status" "$command_text" "$evidence" \
        "exit_code=$rc" "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    return "$rc"
}

snapshot() {
    [[ $# -eq 2 ]] || die "snapshot requires result directory and name"
    local result_dir=$1 name=$2 backend out
    require_result_dir "$result_dir"
    [[ "$name" =~ ^[A-Za-z0-9._-]+$ ]] || die "snapshot name contains unsafe characters"
    backend=$(awk -F '\t' '$1 == "backend" { print $2 }' "$result_dir/metadata.tsv")
    out="$result_dir/snapshots/$name"
    mkdir "$out"
    : >"$out/status.tsv"
    snapshot_one() {
        local label=$1
        shift
        set +e
        "$@" >"$out/$label.txt" 2>&1
        local rc=$?
        set -e
        printf '%s\t%s\t' "$label" "$rc" >>"$out/status.tsv"
        printf '%q ' "$@" >>"$out/status.tsv"
        printf '\n' >>"$out/status.tsv"
    }
    snapshot_one ip-link ip -s link
    snapshot_one xfrm-state ip -s xfrm state
    snapshot_one xfrm-policy ip -s xfrm policy
    snapshot_one socket-summary ss -s
    if [[ "$backend" == onvm ]]; then
        snapshot_one n3iwf-dp-stats "$repo_root/bin/n3iwf-dpctl" -operation stats
    fi
    echo "snapshot saved: $out"
}

check_run() {
    [[ $# -eq 1 ]] || die "check requires one result directory"
    local result_dir=$1 expected actual bad=0 status evidence evidence_path recorded_scenario
    require_result_dir "$result_dir"
    recorded_scenario=$(awk -F '\t' '$1 == "scenario_sha256" {print $2}' "$result_dir/metadata.tsv")
    [[ -n "$recorded_scenario" && "$recorded_scenario" == \
        "$(sha256sum "$result_dir/scenarios.tsv" | awk '{print $1}')" ]] ||
        die "recorded scenario manifest does not match its metadata"
    expected=$(awk -F '\t' '!/^#/ && NF { count++ } END { print count+0 }' "$scenario_file")
    actual=$(awk -F '\t' 'NR > 1 { count++ } END { print count+0 }' "$result_dir/results.tsv")
    [[ "$expected" -eq "$actual" ]] || die "case count mismatch: expected $expected, got $actual"
    while IFS=$'\t' read -r case_id status _ evidence _; do
        [[ "$case_id" == case_id ]] && continue
        case_exists "$case_id" || { echo "unknown result case: $case_id" >&2; bad=1; continue; }
        [[ "$status" == NOT_RUN || "$status" == PASS || "$status" == FAIL || "$status" == SKIP ]] || {
            echo "invalid status for $case_id: $status" >&2; bad=1; continue;
        }
        if [[ "$status" == PASS || "$status" == FAIL ]]; then
            if [[ "$evidence" = /* || "$evidence" = *../* || "$evidence" == .. ]]; then
                echo "evidence index escapes result directory for $case_id" >&2
                bad=1
                continue
            fi
            evidence_path="$result_dir/$evidence"
            if [[ ! -f "$evidence_path" ]]; then
                echo "missing evidence index for $case_id" >&2
                bad=1
            else
                validate_evidence_index "$result_dir" "$evidence_path" || bad=1
            fi
        fi
    done <"$result_dir/results.tsv"
    [[ $bad -eq 0 ]] || return 1
    echo "result structure valid: $result_dir ($actual cases)"
}

gate_run() {
    [[ $# -eq 1 ]] || die "gate requires one result directory"
    local result_dir=$1
    check_run "$result_dir"
    if awk -F '\t' 'NR > 1 && $2 != "PASS" { print $1 ": " $2; bad=1 } END { exit bad }' \
        "$result_dir/results.tsv"; then
        echo "acceptance gate passed: $result_dir"
    else
        echo "acceptance gate incomplete or failed: $result_dir" >&2
        return 1
    fi
}

report_runs() {
    [[ $# -eq 3 ]] || die "report requires Linux directory, ONVM directory, and output path"
    local linux_dir=$1 onvm_dir=$2 output=$3 linux_plan onvm_plan linux_scenarios onvm_scenarios
    require_result_dir "$linux_dir"
    require_result_dir "$onvm_dir"
    [[ $(awk -F '\t' '$1 == "backend" {print $2}' "$linux_dir/metadata.tsv") == linux ]] ||
        die "first result directory is not Linux"
    [[ $(awk -F '\t' '$1 == "backend" {print $2}' "$onvm_dir/metadata.tsv") == onvm ]] ||
        die "second result directory is not ONVM"
    linux_plan=$(awk -F '\t' '$1 == "test_plan_sha256" {print $2}' "$linux_dir/metadata.tsv")
    onvm_plan=$(awk -F '\t' '$1 == "test_plan_sha256" {print $2}' "$onvm_dir/metadata.tsv")
    [[ -n "$linux_plan" && "$linux_plan" == "$onvm_plan" ]] ||
        die "Linux and ONVM runs do not use the same test plan"
    linux_scenarios=$(awk -F '\t' '$1 == "scenario_sha256" {print $2}' "$linux_dir/metadata.tsv")
    onvm_scenarios=$(awk -F '\t' '$1 == "scenario_sha256" {print $2}' "$onvm_dir/metadata.tsv")
    [[ -n "$linux_scenarios" && "$linux_scenarios" == "$onvm_scenarios" ]] ||
        die "Linux and ONVM runs do not use the same scenario manifest"
    check_run "$linux_dir" >/dev/null
    check_run "$onvm_dir" >/dev/null
    awk -F '\t' '
        FILENAME == ARGV[1] && !/^#/ && NF { area[$1]=$2; requirement[$1]=$3; order[++n]=$1; next }
        FILENAME == ARGV[2] && FNR > 1 { linux[$1]=$2; next }
        FILENAME == ARGV[3] && FNR > 1 { onvm[$1]=$2; next }
        END {
            print "# N3IWF backend-conformance report"
            print ""
            print "Suite: `backend-conformance-v1`. Exact commands, evidence indexes, and timestamps are retained in each `results.tsv`; repository state and configuration are retained beside it."
            print ""
            print "| Case | Area | Linux | ONVM | Required external behavior |"
            print "|---|---|---:|---:|---|"
            for (i=1; i<=n; i++) {
                id=order[i]
                gsub(/\|/, "\\|", requirement[id])
                print "| `" id "` | " area[id] " | " linux[id] " | " onvm[id] " | " requirement[id] " |"
            }
        }
    ' "$scenario_file" "$linux_dir/results.tsv" "$onvm_dir/results.tsv" >"$output"
    echo "report written: $output"
}

[[ $# -ge 1 ]] || { usage; exit 2; }
action=$1
shift
case "$action" in
    init) init_run "$@" ;;
    run) run_command "$@" ;;
    record) record_command "$@" ;;
    snapshot) snapshot "$@" ;;
    check) check_run "$@" ;;
    gate) gate_run "$@" ;;
    report) report_runs "$@" ;;
    -h|--help|help) usage ;;
    *) usage >&2; die "unknown action: $action" ;;
esac
