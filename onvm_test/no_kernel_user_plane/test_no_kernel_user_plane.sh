#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
runner="$script_dir/no_kernel_user_plane.sh"
test_root=$(mktemp -d /tmp/n3iwf-no-kernel-proof-test.XXXXXX)
trap 'rm -rf "$test_root"' EXIT

awk -F '\t' -v OFS='\t' '
    $1 == "topology_id" { $2="test-topology" }
    $1 == "xfrm_interface" { $2="xfrmi-default" }
    $1 == "ue_access_interface" { $2="ue0" }
    $1 == "dn_interface" { $2="dn0" }
    $1 == "ue_pdu_ipv4" { $2="10.60.0.1" }
    $1 == "ue_to_n3iwf_spi" { $2="0x1001" }
    $1 == "n3iwf_to_ue_spi" { $2="0x1002" }
    { print }
' "$script_dir/proof-plan.example.tsv" >"$test_root/plan.tsv"
printf 'configuration:\n  userPlaneBackend: onvm\n' >"$test_root/n3iwf.yaml"
printf 'N3IWF_N3_IPV4=192.168.4.1\nUPF_N3_IPV4=192.168.4.2\nONVM_ACCESS_INTERFACE=enp9s0\nN3IWF_CP_TAP_INTERFACE=n3iwf-cp\n' >"$test_root/topology.env"
printf 'configuration:\n  dataplane:\n    n3iwf:\n      enabled: true\n      peer_n3_ip: "192.168.4.1"\n      service_id: 14\n' >"$test_root/upf.yaml"

"$runner" init "$test_root/result" "$test_root/plan.tsv" "$test_root/n3iwf.yaml" \
    "$test_root/topology.env" "$test_root/upf.yaml"
grep -q $'^suite_version\tno-kernel-user-plane-v1$' "$test_root/result/metadata.tsv"
grep -q '^## ' "$test_root/result/revisions/root.status"

if "$runner" init "$test_root/result" "$test_root/plan.tsv" "$test_root/n3iwf.yaml" \
    "$test_root/topology.env" "$test_root/upf.yaml"; then
    echo 'existing result directory was overwritten' >&2
    exit 1
fi

sed 's/userPlaneBackend: onvm/userPlaneBackend: linux/' "$test_root/n3iwf.yaml" \
    >"$test_root/linux.yaml"
if "$runner" init "$test_root/linux-result" "$test_root/plan.tsv" "$test_root/linux.yaml" \
    "$test_root/topology.env" "$test_root/upf.yaml"; then
    echo 'Linux backend was accepted' >&2
    exit 1
fi

if "$runner" gate "$test_root/result"; then
    echo 'gate accepted a run without live evidence' >&2
    exit 1
fi

python3 "$script_dir/make_test_evidence.py" "$test_root/result"
"$runner" analyze "$test_root/result"
"$runner" gate "$test_root/result"
grep -q '^PASS$' "$test_root/result/gate.status"
grep -q '| `tap-user-spi-absent` | PASS | 0 |' "$test_root/result/report.md"
grep -q 'contained 1 IKE/NAT-T UDP packets and 1 ESP packets' "$test_root/result/report.md"

# Replacing the TAP fixture with a declared user-plane SPI must fail closed.
cp "$test_root/result/captures/ue-access.pcap" "$test_root/result/captures/tap.pcap"
if "$runner" analyze "$test_root/result"; then
    echo 'analyzer accepted a user-plane SPI on TAP' >&2
    exit 1
fi
grep -q '^FAIL$' "$test_root/result/gate.status"

python3 "$script_dir/proof.py" --help >/dev/null
grep -q 'collect-remote <result-dir>' <("$runner" 2>&1 || true)
echo 'no-kernel-user-plane harness tests passed'
