#!/usr/bin/env bash
set -euo pipefail

workspace_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
topology="$workspace_dir/config/n3iwf_dp_topology.env"
upf_config="$workspace_dir/NFs/onvm-upf/5gc/upf_u/config/upf_u.yaml"
n3iwf_onvm_config="$workspace_dir/onvm_nf_configs/n3iwf_dp.json"

fail() {
    echo "[N3IWF topology] ERROR: $*" >&2
    exit 1
}

require_uint() {
    local name=$1
    local value=${!name:-}
    [[ $value =~ ^[0-9]+$ ]] || fail "$name must be an unsigned integer"
}

yaml_scalar() {
    local key=$1
    awk -v wanted="$key" '
        $1 == wanted ":" {
            value = $2
            gsub(/["[:space:]]/, "", value)
            print value
            exit
        }
    ' "$upf_config"
}

json_number() {
    local key=$1
    local file=${2:-$n3iwf_onvm_config}
    awk -v wanted="\"$key\"" '
        $1 == wanted ":" {
            value = $2
            gsub(/[,[:space:]]/, "", value)
            print value
            exit
        }
    ' "$file"
}

[[ -r $topology ]] || fail "missing $topology"
[[ -r $upf_config ]] || fail "missing $upf_config"
[[ -r $n3iwf_onvm_config ]] || fail "missing $n3iwf_onvm_config"

# shellcheck source=../config/n3iwf_dp_topology.env
source "$topology"

for name in N3IWF_DP_SERVICE_ID N3IWF_DP_INSTANCE_ID UPF_U_SERVICE_ID \
    UPF_C_SERVICE_ID UPF_U_CORE_ID UPF_C_CORE_ID N3IWF_DP_CORE_ID \
    ONVM_PORTMASK ONVM_ACCESS_PORT ONVM_N6_PORT; do
    require_uint "$name"
done

for name in N3IWF_DP_SERVICE_ID UPF_U_SERVICE_ID UPF_C_SERVICE_ID; do
    value=${!name}
    ((value > 0 && value < 32)) || fail "$name=$value is outside ONVM range 1..31"
done

[[ $N3IWF_DP_SERVICE_ID != "$UPF_U_SERVICE_ID" ]] ||
    fail "N3IWF-DP and UPF-U service IDs collide"
[[ $N3IWF_DP_SERVICE_ID != "$UPF_C_SERVICE_ID" ]] ||
    fail "N3IWF-DP and UPF-C service IDs collide"
[[ $UPF_U_SERVICE_ID != "$UPF_C_SERVICE_ID" ]] ||
    fail "UPF-U and UPF-C service IDs collide"

[[ $UPF_U_CORE_ID != "$UPF_C_CORE_ID" &&
   $UPF_U_CORE_ID != "$N3IWF_DP_CORE_ID" &&
   $UPF_C_CORE_ID != "$N3IWF_DP_CORE_ID" ]] ||
    fail "default UPF-U, UPF-C and N3IWF-DP lcores must be distinct"

[[ $ONVM_ACCESS_PORT != "$ONVM_N6_PORT" ]] ||
    fail "access and N6 must use different physical DPDK ports"
((ONVM_PORTMASK & (1 << ONVM_ACCESS_PORT))) ||
    fail "ONVM_PORTMASK does not include access port $ONVM_ACCESS_PORT"
((ONVM_PORTMASK & (1 << ONVM_N6_PORT))) ||
    fail "ONVM_PORTMASK does not include N6 port $ONVM_N6_PORT"
[[ $ONVM_ACCESS_PCI != "$ONVM_N6_PCI" ]] ||
    fail "access and N6 PCI addresses must be distinct"
[[ -n ${N3IWF_TEST_NWU_IPV4:-} && -n ${N3IWF_IPSEC_INNER_IPV4:-} &&
   -n ${N3IWF_TEST_UE_IPSEC_INNER_IPV4:-} ]] ||
    fail "NWu outer and IPsec-inner test addresses must be configured"
[[ $N3IWF_TEST_NWU_IPV4 != "$N3IWF_IPSEC_INNER_IPV4" ]] ||
    fail "IKE/ESP outer and GRE/IPsec-inner N3IWF addresses must be distinct"
[[ $N3IWF_IPSEC_INNER_IPV4 != "$N3IWF_TEST_UE_IPSEC_INNER_IPV4" ]] ||
    fail "N3IWF and UE GRE/IPsec-inner addresses must be distinct"

[[ $(yaml_scalar n3_port) == "$ONVM_ACCESS_PORT" ]] ||
    fail "UPF transitional n3_port must equal ONVM access port until TONF routing lands"
[[ $(yaml_scalar n6_port) == "$ONVM_N6_PORT" ]] ||
    fail "UPF n6_port does not match the topology contract"
[[ $(yaml_scalar upf_n3_ip) == "$UPF_N3_IPV4" ]] ||
    fail "UPF logical N3 address does not match the topology contract"
[[ $(yaml_scalar upf_n6_ip) == "$UPF_N6_IPV4" ]] ||
    fail "UPF N6 address does not match the topology contract"
[[ $(yaml_scalar enabled) == "true" ]] ||
    fail "UPF same-server N3IWF routing is not enabled"
[[ $(yaml_scalar peer_n3_ip) == "$N3IWF_N3_IPV4" ]] ||
    fail "UPF N3IWF peer address does not match the topology contract"
[[ $(yaml_scalar service_id) == "$N3IWF_DP_SERVICE_ID" ]] ||
    fail "UPF N3IWF service does not match the topology contract"

[[ $(json_number serviceid) == "$N3IWF_DP_SERVICE_ID" ]] ||
    fail "n3iwf_dp.json service ID does not match the topology contract"
[[ $(json_number instanceid) == "$N3IWF_DP_INSTANCE_ID" ]] ||
    fail "n3iwf_dp.json instance ID does not match the topology contract"
[[ $(json_number portmask) == "$ONVM_PORTMASK" ]] ||
    fail "n3iwf_dp.json portmask does not match the topology contract"

for config in "$workspace_dir"/onvm_nf_configs/*.json; do
    [[ $config == "$n3iwf_onvm_config" ]] && continue
    [[ $(json_number serviceid "$config") != "$N3IWF_DP_SERVICE_ID" ]] ||
        fail "service $N3IWF_DP_SERVICE_ID also appears in $config"
    [[ $(json_number instanceid "$config") != "$N3IWF_DP_INSTANCE_ID" ]] ||
        fail "instance $N3IWF_DP_INSTANCE_ID also appears in $config"
done

if command -v lspci >/dev/null 2>&1; then
    lspci -D -s "$ONVM_ACCESS_PCI" >/dev/null 2>&1 ||
        fail "access PCI device $ONVM_ACCESS_PCI is not present"
    lspci -D -s "$ONVM_N6_PCI" >/dev/null 2>&1 ||
        fail "N6 PCI device $ONVM_N6_PCI is not present"
fi

echo "[N3IWF topology] OK"
echo "  services: n3iwf-dp=$N3IWF_DP_SERVICE_ID upf-u=$UPF_U_SERVICE_ID upf-c=$UPF_C_SERVICE_ID"
echo "  physical: port $ONVM_ACCESS_PORT ($ONVM_ACCESS_PCI)=NWu/access, port $ONVM_N6_PORT ($ONVM_N6_PCI)=N6"
echo "  logical N3: n3iwf=$N3IWF_N3_IPV4 upf=$UPF_N3_IPV4 (direct ONVM mbuf)"
echo "  local NWu test: ESP outer n3iwf=$N3IWF_TEST_NWU_IPV4, GRE inner n3iwf=$N3IWF_IPSEC_INNER_IPV4 ue=$N3IWF_TEST_UE_IPSEC_INNER_IPV4"
