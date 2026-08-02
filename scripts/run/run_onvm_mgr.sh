#!/bin/bash
set -e

# -----------------------------------------
# Start script for ONVM Manager
# -----------------------------------------

# Load the shared same-server N3IWF port contract.
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
TOPOLOGY_FILE="$SCRIPT_DIR/../../config/n3iwf_dp_topology.env"
if [ ! -r "$TOPOLOGY_FILE" ]; then
  echo "Error: N3IWF topology contract not found: $TOPOLOGY_FILE" >&2
  exit 1
fi
# shellcheck source=../../config/n3iwf_dp_topology.env
source "$TOPOLOGY_FILE"

# Set working directory (default: user home directory)
WORK_DIR=$HOME
DEFAULT_ONVM_MGR_PATH="$WORK_DIR/L25GC-plus/NFs/onvm-upf"


# Defaults:
# - Manager cores (in start.sh) default to 0,1,2 if -m is not passed
# - Here we only set:
#     -k PORTMASK     (which NIC ports)
#     -n NF_COREMASK  (which cores ONVM can use for NFs)
DEFAULT_PORTMASK="$ONVM_PORTMASK"
DEFAULT_NF_COREMASK="0xFFF8"
DEFAULT_OUTPUT="stdout"
DEFAULT_ALLOW_PCI_LIST="$ONVM_ACCESS_PCI $ONVM_N6_PCI"
DEFAULT_PORT_SERVICE_MAP="$ONVM_ACCESS_PORT:$N3IWF_DP_SERVICE_ID,$ONVM_N6_PORT:$UPF_U_SERVICE_ID"

# Usage function
usage() {
  echo "Usage: $0 [-p ONVM_MGR_PATH] [-k PORTMASK] [-n NF_COREMASK] [-s OUTPUT] [-a ALLOW_PCI_LIST]"
  echo
  echo "  -p ONVM_MGR_PATH   Path to ONVM-UPF (default: $DEFAULT_ONVM_MGR_PATH)"
  echo "  -k PORTMASK        DPDK portmask passed to start.sh -k (default: $DEFAULT_PORTMASK)"
  echo "                     Example: 3 -> use ports 0 and 1"
  echo "  -n NF_COREMASK     NF coremask passed to start.sh -n (default: $DEFAULT_NF_COREMASK)"
  echo "                     Example: 0xF0 -> cores 4-7 for NFs"
  echo "  -s OUTPUT          Stats/output mode (web|stdout) (default: $DEFAULT_OUTPUT)"
  echo "  -a ALLOW_PCI_LIST  List of PCI devices to allow (default: $DEFAULT_ALLOW_PCI_LIST)"
  echo "                     Example: -a \"0000:08:00.0 0000:09:00.0\""
  exit 1
}

# Parse input arguments
while getopts "p:k:n:s:a:h" opt; do
  case $opt in
    p) ONVM_MGR_PATH="$OPTARG" ;;
    k) PORTMASK="$OPTARG" ;;
    n) NF_COREMASK="$OPTARG" ;;
    s) OUTPUT="$OPTARG" ;;
    a) ALLOW_LIST="$OPTARG" ;;
    h) usage ;;
    *) usage ;;
  esac
done

# Apply defaults if not provided
ONVM_MGR_PATH="${ONVM_MGR_PATH:-$DEFAULT_ONVM_MGR_PATH}"
PORTMASK="${PORTMASK:-$DEFAULT_PORTMASK}"
NF_COREMASK="${NF_COREMASK:-$DEFAULT_NF_COREMASK}"
OUTPUT="${OUTPUT:-$DEFAULT_OUTPUT}"
ALLOW_LIST="${ALLOW_LIST:-$DEFAULT_ALLOW_PCI_LIST}"

"$SCRIPT_DIR/../check_n3iwf_topology.sh"
if [ "$PORTMASK" != "$DEFAULT_PORTMASK" ] ||
   [ "$ALLOW_LIST" != "$DEFAULT_ALLOW_PCI_LIST" ]; then
  echo "[WARN] Custom manager ports override the committed N3IWF topology contract" >&2
  echo "[WARN] Do not use this override for N3IWF integration acceptance tests" >&2
fi

# Check that the directory exists
if [ ! -d "$ONVM_MGR_PATH" ]; then
  echo "Error: ONVM MGR path does not exist: $ONVM_MGR_PATH"
  echo "Usage: $0 [ONVM_MGR_PATH]"
  exit 1
fi

echo "[INFO] Changing directory to: $ONVM_MGR_PATH"
cd "$ONVM_MGR_PATH"

# Small delay to ensure environment is ready
sleep 1.0

# Launch ONVM_MGR with specified arguments
echo "[INFO] Starting ONVM_MGR with options: -k $PORTMASK -n $NF_COREMASK -s $OUTPUT"
echo "[INFO] Using PCI allow list: $ALLOW_LIST"
echo "[INFO] Port $ONVM_ACCESS_PORT ($ONVM_ACCESS_PCI): NWu/access"
echo "[INFO] Port $ONVM_N6_PORT ($ONVM_N6_PCI): N6"
echo "[INFO] N3 is logical: N3IWF $N3IWF_N3_IPV4 -> UPF $UPF_N3_IPV4 over ONVM rings"
echo "[INFO] Ingress service map: $DEFAULT_PORT_SERVICE_MAP"

ONVM_ALLOW_LIST="$ALLOW_LIST" ./scripts/start.sh -k "$PORTMASK" -n "$NF_COREMASK" \
  -i "$DEFAULT_PORT_SERVICE_MAP" -s "$OUTPUT"
