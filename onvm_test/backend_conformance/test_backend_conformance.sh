#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
runner="$script_dir/backend_conformance.sh"
test_root=$(mktemp -d /tmp/n3iwf-backend-conformance-test.XXXXXX)
trap 'rm -rf "$test_root"' EXIT

printf 'configuration:\n  userPlaneBackend: linux\n' >"$test_root/linux.yaml"
printf 'configuration:\n  userPlaneBackend: onvm\n' >"$test_root/onvm.yaml"
sed 's/CHANGE_ME/test-value/g' "$script_dir/test-plan.example.tsv" >"$test_root/plan.tsv"

"$runner" init linux "$test_root/linux" "$test_root/linux.yaml" "$test_root/plan.tsv"
"$runner" init onvm "$test_root/onvm" "$test_root/onvm.yaml" "$test_root/plan.tsv"
"$runner" run "$test_root/linux" registration -- /bin/sh -c 'printf registration-ok'
"$runner" run "$test_root/onvm" registration -- /bin/sh -c 'printf registration-ok'
grep -Fq $'registration\tPASS\t/bin/sh -c printf\\ registration-ok\t' \
    "$test_root/linux/results.tsv"
"$runner" record "$test_root/linux" rqi-preservation SKIP - - 'live RQI capture deferred'
"$runner" record "$test_root/onvm" rqi-preservation SKIP - - 'live RQI capture deferred'
"$runner" snapshot "$test_root/linux" before
"$runner" check "$test_root/linux"
"$runner" check "$test_root/onvm"
"$runner" report "$test_root/linux" "$test_root/onvm" "$test_root/report.md"
grep -q '^| `registration` | control | PASS | PASS |' "$test_root/report.md"
grep -q '^| `rqi-preservation` | qos | SKIP | SKIP |' "$test_root/report.md"

if "$runner" gate "$test_root/linux"; then
    echo 'gate unexpectedly accepted incomplete results' >&2
    exit 1
fi

if "$runner" init linux "$test_root/wrong" "$test_root/onvm.yaml" "$test_root/plan.tsv"; then
    echo 'backend/config mismatch unexpectedly accepted' >&2
    exit 1
fi

if "$runner" record "$test_root/linux" ping-ul PASS 'ping command' \
    evidence/missing.txt 'should fail'; then
    echo 'missing evidence unexpectedly accepted' >&2
    exit 1
fi

sed 's/test-value/other-value/g' "$test_root/plan.tsv" >"$test_root/other-plan.tsv"
"$runner" init onvm "$test_root/other-onvm" "$test_root/onvm.yaml" "$test_root/other-plan.tsv"
if "$runner" report "$test_root/linux" "$test_root/other-onvm" "$test_root/wrong-plan.md"; then
    echo 'different test plans unexpectedly compared' >&2
    exit 1
fi

echo 'backend-conformance harness tests passed'
