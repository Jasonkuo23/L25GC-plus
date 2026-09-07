# N3IWF backend-conformance suite

This is the versioned acceptance ledger for comparing the Linux/free5GC and
ONVM/DPDK N3IWF user-plane backends. `scenarios.tsv` is the normative v1 case
list. A case has identical external pass criteria on both backends; only setup,
captures, and internal counters may differ.

The harness deliberately does not start the CN, DN, or UE. That keeps process
ownership explicit and lets the same ledger cover a remote N3IWUE and DN. It
does execute individual verification commands, save their output, take local
snapshots, validate evidence, and produce the parity matrix.

## Create one run per backend

Use a fresh result directory. `init` refuses to overwrite evidence and rejects
a config whose `userPlaneBackend` does not match the requested run.

```bash
result_root=$PWD/results/backend-conformance-$(date -u +%Y%m%dT%H%M%SZ)
cp onvm_test/backend_conformance/test-plan.example.tsv /tmp/backend-test-plan.tsv
# Edit /tmp/backend-test-plan.tsv and replace every CHANGE_ME value.

./onvm_test/backend_conformance/backend_conformance.sh init \
  linux "$result_root/linux" /absolute/path/to/n3iwfcfg-linux.yaml \
  /tmp/backend-test-plan.tsv

./onvm_test/backend_conformance/backend_conformance.sh init \
  onvm "$result_root/onvm" "$PWD/config/n3iwfcfg.yaml" \
  /tmp/backend-test-plan.tsv
```

Initialization retains the root and relevant submodule HEADs, status, staged
and unstaged binary diffs, hashes of untracked submodule files and suite inputs,
the selected config, hostname, and UTC time. Run the
two backends on the same hardware/topology with the same UE subscriptions,
cipher, QFIs, traffic duration, payload sizes, and loss threshold. Do not run
both backends at once. `report` refuses to compare different test-plan hashes.

## Capture a case

Take snapshots before and after each live operation:

```bash
suite=./onvm_test/backend_conformance/backend_conformance.sh
$suite snapshot "$result_root/onvm" registration.before
# Start or operate N3IWUE yourself, then retain UE/core logs and pcaps.
$suite snapshot "$result_root/onvm" registration.after
```

For a command whose exit status is the pass condition, let the harness run and
record it. Arguments are executed directly, not through `eval`:

```bash
$suite run "$result_root/onvm" ping-ul -- \
  ping -I 60.60.0.1 -c 5 -W 1 60.60.0.101
```

For multi-host or manually inspected cases, copy logs and pcaps under the run
directory, create an evidence index containing one relative artifact path per
line, then record the exact commands. Example:

```bash
printf '%s\n' \
  raw/ue-registration.log raw/n3iwf.log raw/amf.log \
  snapshots/registration.before snapshots/registration.after \
  > "$result_root/onvm/evidence/registration.txt"

$suite record "$result_root/onvm" registration PASS \
  'n3iwue -c /absolute/path/to/config.yaml 2>&1 | tee raw/ue-registration.log' \
  evidence/registration.txt \
  'Registration Accept observed; UE remained registered'
```

Record `FAIL` with the same evidence discipline. Use `SKIP` only with a reason;
it remains an unaccepted result. Per the current project decision, live
`rqi-preservation` should be recorded `SKIP` on both backends until that capture
is resumed—it must not inherit a PASS from component tests.

## Required live sequence

Perform the cases in manifest order. Registration and setup establish the
baseline. Run bidirectional ICMP, TCP, and UDP with simultaneous NWu, N3, and N6
captures. Exercise basic modification before basic release. Re-establish clean
state before negative injection. During unknown-QFI, invalid-ESP, and replay
cases, prove both the expected rejection/counter delta and continued delivery
of a valid flow. Finally run two sessions and the documented two-UE isolation
procedure in `../NON3GPP_README.md`.

Linux evidence should include `ip -s xfrm state`, `ip -s xfrm policy`, interface
counters, and kernel N3/NWu captures. ONVM evidence should include
`bin/n3iwf-dpctl -operation stats`, physical NWu/N6 captures, and the logical N3
observation available from the NFs. Counter names need not match; the packet
outcome must. Every snapshot includes `status.tsv`; a nonzero command status is
retained evidence of an unavailable observer and must be resolved before using
that snapshot for a `PASS`.

`invalid-esp-rejection` is one acceptance case but its evidence must separately
show unknown SPI, malformed/truncated ESP, bad ICV, and bad padding. Likewise,
`anti-replay` must show both a duplicate and a packet older than the negotiated
window. Never inject negative traffic into a shared or production network.

## Validate and compare

```bash
$suite check "$result_root/linux"
$suite check "$result_root/onvm"
$suite report "$result_root/linux" "$result_root/onvm" "$result_root/matrix.md"
$suite gate "$result_root/linux"
$suite gate "$result_root/onvm"
```

`check` validates the ledger and evidence indexes. `report` creates the common
matrix. `gate` succeeds only when every v1 case is `PASS`; `NOT_RUN`, `SKIP`, or
`FAIL` make it fail. Keep the complete result root because `matrix.md` alone is
not acceptance evidence.

Run the harness regression test with:

```bash
bash onvm_test/backend_conformance/test_backend_conformance.sh
```
