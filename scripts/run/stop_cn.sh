#!/usr/bin/env bash
set -euo pipefail

repo_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd)
if (( EUID != 0 )); then
    exec sudo -- bash "$repo_root/scripts/run/stop_cn.sh" "$@"
fi

dry_run=false
case "$*" in
    '') ;;
    --dry-run) dry_run=true ;;
    *) echo "Usage: $0 [--dry-run]" >&2; exit 2 ;;
esac

# NRF's MongoDB settings are single scalar values in the supplied config.
read -r mongo_uri mongo_db < <(awk '
    $1 == "MongoDBUrl:"  { uri = $2 }
    $1 == "MongoDBName:" { name = $2 }
    END {
        gsub(/^["\047]|["\047]$/, "", uri)
        gsub(/^["\047]|["\047]$/, "", name)
        print uri, name
    }
' "$repo_root/config/nrfcfg.yaml")
[[ -n $mongo_uri && -n $mongo_db ]] || { echo 'Missing NRF MongoDB settings' >&2; exit 1; }

pids=()
managers=()
for entry in /proc/[0-9]*/exe; do
    executable=$(readlink -- "$entry" 2>/dev/null) || continue
    executable=${executable%" (deleted)"}
    # Test NFs may execute from /tmp while running in this checkout.
    if [[ $executable != "$repo_root/"* ]]; then
        cwd=$(readlink -- "${entry%/exe}/cwd" 2>/dev/null) || continue
        [[ $cwd == "$repo_root" || $cwd == "$repo_root/"* ]] || continue
    fi
    name=${executable##*/}
    name=${name%%.*}
    pid=${entry#/proc/}
    pid=${pid%/exe}
    case "$name" in
        onvm_mgr) managers+=("$pid") ;;
        amf|ausf|chf|nrf|nssf|pcf|smf|udm|udr|upf|n3iwf|n3iwf-cp|n3iwf-dp|n3iwf-up|l25gc_upf_c|l25gc_upf_u|l25gc_n3iwf_dp|l25gc_n3iwf_up|l25gc-n3iwf-onvm-only|l25gc_dn_app)
            pids+=("$pid") ;;
    esac
done

# Stop NFs first, then ONVM manager. An already-exited PID is harmless.
for pid in "${pids[@]}" "${managers[@]}"; do
    if "$dry_run"; then echo "Would stop PID $pid"; continue; fi
    echo "Stopping PID $pid"
    if ! kill -KILL "$pid" 2>/dev/null; then
        if kill -0 "$pid" 2>/dev/null; then
            echo "Failed to stop PID $pid" >&2
            exit 1
        fi
    fi
done
"$dry_run" && exit 0

export STOP_CN_MONGO_URI="$mongo_uri" STOP_CN_MONGO_DB="$mongo_db"
echo "Clearing runtime collections in $mongo_db..."
mongosh --nodb --quiet --eval '
const database = new Mongo(process.env.STOP_CN_MONGO_URI).getDB(process.env.STOP_CN_MONGO_DB);
for (const name of [
    "NfProfile",
    "applicationData.influenceData.subsToNotify",
    "applicationData.subsToNotify",
    "policyData.subsToNotify",
    "exposureData.subsToNotify"
]) database.getCollection(name).deleteMany({});
'
echo 'Cleanup complete.'
