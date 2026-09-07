# Remaining Work

This list starts from the implementation described in `CURRENT_STATE.md`.
Items already completed—basic CP/DP contracts, clear GRE round trip, initial
software ESP, access MAC learning, and direct active TX-SA selection—are not
repeated as TODOs.

The 2026-09-07 [paper code review](../../research/n3iwf-paper/paper_outline_code_review.md)
adds publication evidence requirements and implementation gaps below. The
[paper outline](dpdk_n3iwf_paper_outline.md) now follows its proposed structure.
Historical acceptance remains recorded; recovering missing raw artifacts or
repeating an experiment on the final paper build is separate work.

## Paper Priorities

- [x] Align the outline with the actual packet paths, supported ESP profile,
  temporary signalling boundary, existing optional rekey, and qualified
  lookup/allocation claims.
- [ ] Before drafting results, assemble the evidence inventory in item 6.
  Separate locally retained, remotely retained, operator-reported, and planned
  evidence; do not turn reported checkpoints into validated comparisons.
- [ ] Before submission, complete the matched performance/loss/latency/CPU
  comparison in item 5 and retain the functional and final-build bypass
  evidence in items 3 and 6.
- [ ] Resolve the modification/teardown gaps in item 2 before claiming full
  backend parity or lifecycle support. The focused paper can instead retain
  its narrower initial-setup and forwarding claims without implementing these
  extensions now.
- [ ] Complete a sourced Related Work review before making literature-wide
  novelty claims. The local code review establishes no external precedence.

Live RQI remains deliberately deferred. Larger-scale validation, hardware
crypto, NAT-T, IPv6 transport, multicore sharding, and extended lifecycle/rekey
hardening remain outside the core paper scope. Optional ablation and extra QoS
experiments must not displace the central comparison.

## Completed Acceptance Gates

### Multiple-UE validation — completed 2026-09-04

The same-UE/two-session/same-QFI identity case and a true two-UE physical test
have now validated the indexed data model end to end.

Test:

- two different SUPIs;
- different inner NWu addresses;
- the same QFI where assigned by the core;
- simultaneous bidirectional traffic;
- independent TEIDs, SPIs, and learned access MAC state;
- no cross-UE packet delivery.

Acceptance required both UEs to register, establish a PDU session, and forward
concurrently with zero identity leakage and no fast-path O(N) scan. Selective
UE or PDU-session release was outside this acceptance test.

The executable live procedure and evidence checklist are in
`onvm_test/NON3GPP_README.md` under “Two-UE software-IPsec acceptance.”

The first live attempt exposed and corrected per-UE NAS reordering in the
N3IWF NWu TCP forwarding path. With the rebuilt `bin/n3iwf.next`, both UEs
reached `active_sessions=2` and `active_child_sas=2`.

A subsequent simultaneous bidirectional run kept both sessions and Child SAs
active and forwarded both UEs without identity lookup errors, but it exposed a
64-packet anti-replay window that was too narrow for burst reordering. DPDK
reported inbound preparation `EINVAL` 955 times. The contract now programs a
4096-packet window and classifies this condition as `replay_drops` rather than
`crypto_failures`. Ambient IPv6 was removed from the IPv4-only NWu segment so
unrelated traffic did not contaminate the acceptance snapshot.

The rebuilt replay-window rerun now passes the concurrent traffic and counter
gates: both UEs completed bidirectional traffic with zero retransmissions,
approximately 1.44 Gbit/s aggregate sender throughput, balanced uplink/downlink
packet growth, two stable sessions/SAs/MAC learns, and zero error/drop deltas.
The overlapping isolation capture produced two header-only 24-byte pcap files;
both decoded to zero cross-UE packets. Distinct PDU addresses and ports were
visible at the DN, and the prior static reference check confirmed that the
packet path contains no O(N) latest-SA scan. The immediate multiple-UE gate is
therefore accepted. Selective release and performance fairness remain separate
follow-up work.

Publication artifact follow-up remains open: the 2026-09-07 review found the
two-UE result in project reports, but not the corresponding raw load logs and
isolation pcaps in the local retained result directories. Recover and archive
them, or repeat the run, as specified in item 6. This does not undo the
operator-reported 2026-09-04 acceptance.

## Immediate TODO

The following six items track correctness, evaluation, and publication
readiness. Items 3 and 4 preserve completed engineering acceptance with separate
artifact/final-build follow-ups. Full parity in items 1 and 2 is broader than
the current paper's initial-setup and forwarding claims; extended lifecycle
hardening and the other items below remain future work.

### 1. RQI semantic preservation — partially done

Status:

- [x] UPF-U maps the QER Reflective QoS value to downlink PSC RQI.
- [x] N3IWF-DP preserves downlink PSC RQI as the independent GRE RQI bit.
- [x] Direction-aware golden and mbuf tests cover RQI clear/set, chained GTP-U
  extension headers, IPv4/IPv6 inner payloads, and counted rejection of uplink
  RQI/direction misuse.
- [ ] Live RQI capture and end-to-end validation are deliberately skipped for
  now; do not treat the component results as live acceptance evidence.
- [ ] Linux/free5GC versus ONVM parity remains part of the backend-conformance
  work in item 2.

Preserve the Reflective QoS Indicator together with QFI wherever it is defined
by the directional GRE Key and GTP-U PDU Session Container semantics. In
particular, preserve downlink PSC RQI in the GRE Key delivered to the UE. Do not
reconstruct RQI from QFI, invent it in an uplink field where it is not defined,
or silently clear it.

The direction-aware codec and packet-path cases are complete. A future live
capture must demonstrate that the bit survives the applicable end-to-end
direction when live acceptance work resumes.

Acceptance requires exact QFI/RQI semantic parity with the Linux/free5GC path,
with malformed or unsupported encodings rejected and counted.

### 2. Backend-conformance suite — partially done

Status:

- [x] A versioned v1 scenario manifest defines common external requirements
  for both backends.
- [x] The acceptance harness records exact commands, configuration, root and
  submodule revisions/diffs, raw evidence indexes, snapshots, and a Linux/ONVM
  results matrix without overwriting an existing run.
- [x] Harness regression tests and the supporting Go/client/ONVM component
  suites pass.
- [ ] The live Linux backend run has not been performed.
- [ ] The live ONVM backend run has not been performed.
- [ ] Live RQI remains deliberately skipped as recorded in item 1; a `SKIP`
  remains gate-incomplete and cannot be reported as parity acceptance.
- [ ] Supply semantic checks for each live case: successful command exit and
  existing evidence files alone do not establish protocol behavior, integrity,
  rejection, or isolation. Record explicit expected/observed outcomes.
- [ ] Resolve the NGAP modification publication gap before accepting the
  `pdu-session-modification` case: the current
  `handlePDUSessionResourceModifyRequestTransfer` and
  `HandlePDUSessionResourceModifyConfirm` in
  `NFs/n3iwf/internal/ngap/handler.go` modify Go state without publishing the
  corresponding ONVM session update. The tunnel-modification branch also does
  not update the GTP tunnel. Trace QFI-list/TEID changes through acknowledged DP
  programming and test subsequent traffic; a socket upsert test is insufficient.
- [ ] Audit whole-UE and failure teardown paths, especially
  `deleteRanUeUserPlaneSessions`, for associated Child-SA cleanup. The selected
  PDU-release helper explicitly deletes associated SAs; other session-delete
  callers do not supply that same list. Retain basic-release state/counter
  evidence before claiming complete teardown.
- [ ] Exercise the real software-IPsec path with bad ICV/padding, unknown SPI,
  duplicate and out-of-window ESP, and valid-flow survival when those rejection
  claims are included. The seven documented component targets do not link
  `n3iwf_dp_ipsec.c`; SA-table and clear-codec tests are not crypto-path tests.

The suite, normative case list, and executable procedure are under
`onvm_test/backend_conformance/`. The evidence-recording harness is implemented:
`run_command` derives its verdict from the supplied command's exit code,
`record_case` accepts supplied verdicts, and `gate_run` checks completeness.
It is not an independent protocol-conformance oracle. Backend conformance is
not accepted until retained live evidence and semantic checks make every case
pass; regression tests of this bookkeeping do not perform live registration.

Run the same control-plane setup and externally observable behavior against
`userPlaneBackend: linux` and `userPlaneBackend: onvm`. Cover at least:

- registration and PDU-session setup;
- uplink and downlink ping, TCP, and UDP;
- PDU-session modification and basic release;
- QFI and RQI preservation;
- unknown QFI and malformed or invalid ESP handling;
- anti-replay behavior;
- multiple sessions and the completed two-UE isolation case.

Use backend-appropriate counters where Linux and ONVM expose different
internals, but require the same externally visible success, rejection, and
isolation semantics. Basic release belongs in this parity suite; selective
release under traffic, repeated churn, and failure recovery remain lifecycle
hardening.

Acceptance requires a versioned results matrix with exact commands, software
revisions, expected results, and retained raw logs/captures for both backends.

### 3. No-kernel-user-plane proof — completed 2026-09-06

Status:

- [x] A versioned proof plan, evidence collector, analyzer, and strict gate are
  implemented under `onvm_test/no_kernel_user_plane/`.
- [x] The procedure distinguishes the authoritative UE-side NWu capture from
  the potentially empty host-side capture on the bifurcated mlx5 PMD and
  accounts for allowed IKE/signalling ESP separately.
- [x] The live three-host result under
  `results/no-kernel-user-plane-20260906T074303Z/` passes every gate with
  lossless UE, DN, TAP, XFRM, kernel-N3, and host-access captures.
- [x] The retained analyzer passes on a temporary copy of that result during
  the 2026-09-07 paper review.
- [ ] Repeat on the final measured build with clean endpoint completion and
  retained commands/configuration/capture-drop summaries. The existing run's
  interrupted server and final near-zero-duration bitrate interval are not a
  sustained-throughput measurement.
- [ ] State the analyzer's exact scope in the paper: declared SPI and IP/port
  flow observations, payload-byte totals, and positive counter progress. It
  does not validate an application payload hash, reassemble unique TCP bytes,
  or require exact equality between ESP observations and DP counters.
- [ ] Include `oversize_drops`, `buffer_drops`, and `stale_updates` in the final
  run's counter checks, either with retained explicit assertions or an updated
  analyzer. The existing analyzer's selected zero-error checks omit them.

Produce repeatable evidence that ONVM carries UE user packets without the Linux
user-plane path. Separate this claim from signalling: IKE and signalling ESP
still intentionally cross the temporary TAP/Linux boundary.

During identifiable bidirectional UE traffic, record physical-access, TAP,
XFRM, kernel N3, and DN captures plus interface counters. Require ESP on the
access link, no clear GRE there, no UE payload on the TAP/XFRM or kernel N3
path, direct ONVM N3 delivery, and continued end-to-end traffic. Account for
control traffic explicitly so it is not mistaken for a user-plane crossing.

Document the topology and capture limitations of the bifurcated mlx5 PMD. The
paper claim is “no kernel user-plane crossing,” not “the entire N3IWF avoids the
kernel.”

Keep the bypass claim bounded to the captured active-session traffic. With
`-k`, `n3iwf_dp.c` permits TAP classification after unsupported/unknown-or-unbound
ESP results, and `n3iwf_dp_punt.c` does not verify a signalling-SPI allowlist.
Unknown-SPI, stale-binding, and teardown behavior are not established by the
accepted active-flow capture; boundary hardening is tracked under Future Work.

### 4. MTU boundary policy and tests — completed 2026-09-06

Status:

- [x] The 1500-byte NWu/N3/N6 deployment contract and the AES-CBC ESP padding
  boundary derive a 1410-byte maximum inner PDU packet in both directions.
- [x] Uplink and downlink enforce the boundary before GRE/GTP conversion and
  neighbor learning, and export
  distinct `oversize_drops`, `buffer_drops`, and `fragment_drops` counters.
- [x] Component tests cover 1409/1410/1411-byte ICMP/TCP-shaped packets in
  both directions, byte preservation, explicit 1370-byte IPv4 TCP MSS,
  insufficient headroom, and fragmented outer packets.
- [x] `MTU_POLICY.md` defines DF, PMTU, oversize, and no-reassembly behavior;
  `onvm_test/NON3GPP_README.md` contains the live procedure.
- [x] The physical three-host run retained under
  `results/mtu-boundary-20260906T083817Z/` passes below/exact ICMP, endpoint
  PMTU, deliberate downlink oversize, physical ESP/no-GRE, capture-loss, and
  MSS-1370 bidirectional TCP gates.
- [ ] Retrieve or durably index/hash the remotely retained UE/DN pcaps and
  endpoint logs for the publication artifact. The local result contains the
  report and counters, not those large captures; coordinate this with item 6.

Secure uplink decrypts ESP before inspecting the inner packet size. Do not
describe the inner-MTU check as preceding all packet mutation or crypto work.

Derive and document the maximum supported inner packet size in both directions
from the physical MTUs and Ethernet, outer IP, ESP, padding/ICV, GRE, UDP,
GTP-U, and PDU Session Container overheads. Define the behavior for DF, PMTU,
oversize input, and outer fragments; do not leave silent truncation or an
accidental dependency on mbuf headroom.

Test below-boundary, exact-boundary, and above-boundary packets in uplink and
downlink, including ICMP and TCP with an explicit MSS. Verify successful packets
byte-for-byte and require deterministic reject/drop counters for unsupported
fragmentation. Add reassembly only if the chosen policy requires it.

### 5. Performance and scalability evaluation

Status:

- [x] A two-UE ONVM checkpoint procedure and concurrent UE-host TCP
  JSON runner are available under `onvm_test/multi_ue_performance/`.
- [ ] Run and retain the three-repetition one-UE versus two-UE uplink,
  downlink, and bidirectional matrix.
- [ ] Add controlled UDP-loss and loaded p50/p99 latency sweeps.
- [ ] Repeat the identical matrix with the Linux/free5GC backend and the same
  CPU-core budget for the final comparison.
- [ ] Record exact CPU/SMT/NUMA, NIC model/link/queues, IRQ/lcore affinity,
  software revisions, negotiated cipher/key size, MTU/MSS, stream/session/QFI
  counts, duration and rekey-disabled configuration for each comparable run.
- [ ] Account for manager RX/TX/dispatch, N3IWF-DP, UPF and Linux workers/softirq
  in the CPU budget. Capture endpoint CPU limits as well. DPDK polling
  utilization alone is not a CPU-efficiency metric.
- [ ] Document and control baseline host/topology differences. A separate-host
  Linux N3 link versus an in-host ONVM ring can confound an N3IWF-only speedup
  claim; report an integrated-system comparison if those differences remain.
- [ ] Retain secure UDP downlink as well as uplink receiver loss/rate and
  before/after DP counters. The reported 1.2 Gbit/s UL offered-load point is
  provisional and has no retained matched DL/counter dataset.
- [ ] Use fresh trial directories, collect actual client timing and verify
  concurrent intervals. The current runner has no common start barrier or
  start-skew check and can overwrite files in a reused directory.
- [ ] Collect CPU samples, UDP/latency measurements and semantic/counter checks
  explicitly; the TCP runner does not implement these collectors or verdicts.
- [ ] Report every repetition and variation, receiver-duration tails,
  retransmissions, per-UE rates and fairness. Define packet/core denominators
  and matched intervals before calculating packets/s/core or cycles/packet.
- [ ] Profile saturation before attributing the ~1.18–1.19 Gbit/s downlink
  plateau to crypto, UPF shaping, the DN, or UE-XFRM processing.

Run the pinned free5GC/Linux-XFRM and ONVM/DPDK backends under identical
hardware, CPU-core budget, cipher, packet sizes, session/QFI count, TCP/UDP
stream count, duration, and offered load.

Measure:

- uplink, downlink, and aggregate concurrent throughput;
- TCP retransmissions and UDP loss at controlled offered rates;
- p50 and p99 latency;
- per-core utilization and, where practical, cycles per packet;
- CPU/NUMA pinning, NIC/crypto placement, and queue configuration;
- controlled one-versus-two UE behavior, with larger UE/session/QFI/stream
  sweeps only if supported by the testbed and retained paper claims;
- per-UE fairness and the saturation knee.

Acceptance remains at least 2x kernel-N3IWF throughput at no more than 0.1%
loss, without a material p99 latency regression. Current measurements do not
yet prove this target. Store exact commands, topology, revisions, configuration,
and raw results so every figure is reproducible. Measure loaded p50/p99 at
matched loads, including the Linux baseline throughput. Report the actual
result and any missed objective; do not select trials to manufacture 2x.

Use the 1.19 Gbit/s non-rekey reverse result as the primary current checkpoint.
The optional-extension 2026-09-01 rekey-enabled result—8.06 GBytes at 1.15
Gbit/s over 60 seconds with four retransmissions—is supporting regression
evidence. These historical values are reported checkpoints: recover raw trial
artifacts before plotting them. Neither replaces the controlled comparison or
scalability study.

### 6. Publication evidence and reproducibility

- [ ] Build a claim-to-artifact inventory with commands, expected/observed
  results, run intervals, root/submodule revisions, dirty diffs and required
  untracked source contents. Preserve non-secret configuration and source/test
  hashes; hashes without source contents do not make an untracked build
  reproducible.
- [ ] Recover the operator-reported two-UE load logs, identity table, isolated
  counter interval and cross-UE pcaps, or repeat the test. Archive same-UE,
  two-session, same-QFI live evidence separately if claiming it end to end.
  The retained local bypass/MTU runs each demonstrate one session.
- [ ] Retain clean-completion IPv4 ICMP, TCP and secure UDP in both directions
  for the paper's functional table. Keep clear-GRE echo evidence separate from
  production ESP evidence, and test basic release if lifecycle acceptance is
  claimed.
- [ ] Recover the remote MTU artifacts in item 4 and prepare the final-build
  bypass repeat in item 3. Preserve the original accepted result directories.
- [ ] Rebuild and run relevant Go, client, C/DPDK and harness checks on the
  final artifact revision, recording which tests actually exercise crypto and
  which only test contracts/codecs. The paper review reran only three C tests
  and the retained bypass analyzer, not the full suite.
- [ ] Freeze an attributable implementation for publication: preserve essential
  untracked IPsec/MTU/rekey sources/tests, review project diffs, commit in
  dependency order and update parent pointers as described below.
- [ ] Optional: reproduce the old-scan versus direct-SA ablation with matched
  workloads/builds if using quantitative causal claims. `perf.data*` was
  unreadable during review; the reported 58.2% profile is not independently
  checked evidence. Cycles/packet remains optional.

No fair paired Linux/DPDK benchmark was found during review. No amount of
source coverage or table capacity substitutes for those missing experiments.

## Future Work

### Selective release and lifecycle hardening

- Live-test release of one PDU session or UE while the others continue
  forwarding. The Go path has a working-tree correction for session and Child
  SA deletion, but it is not accepted until the active counters each fall by
  exactly one without affecting another session.
- Add repeated setup/release churn, capacity/index collision, deletion during
  traffic, stale-state, and key-erasure soak tests.
- Audit stale-generation protection after complete deletion/restart and
  reconcile CP/DP state after reconnect. Current in-memory generation checks
  and ACKs do not establish durable tombstones or exactly-once recovery.
- Add N3IWUE T3580 transaction/retransmission handling and bounded failure.
- Resolve the observed AMF NRF/NSSF SMF-selection stall; this is outside the
  N3IWF dataplane and must not be hidden by UE retransmission.
- Keep clear GRE as an explicit test mode and document its NWu+QFI ambiguity.

### Reproducibility and commits

Review root and both submodule diffs, separate unrelated local configuration or
certificate changes, commit the N3IWF/ONVM-UPF changes in dependency order, and
update parent submodule pointers. Do not lose the untracked IPsec source files,
optional rekey sources/tests, clear traffic tools, or direct-SA redesign.

### Correctness

#### Packet validation and lifecycle

- Add/extend negative tests for bad outer lengths/checksums, malformed extension
  chains, unsupported GRE flags/protocols, QFI/TEID mismatch, invalid MACs,
  unknown SPI, bad ICV/padding, replay edges, and session deletion during traffic.
- Decide how to enforce UE inner-source anti-spoofing when the N3IWF does not
  own the SMF-assigned PDU address. Do not invent authority the N3IWF lacks;
  obtain state through an appropriate control-plane contract if enforcement is
  required.
- Keep reassembly outside the fixed MTU policy. Any future implementation must
  be separately bounded; extend fragment/extension-chain coverage before
  claiming general IPv6 fragment handling. Today IPv4 outer fragments and the
  immediate IPv6 Fragment next-header case are rejected and counted.
- Audit and test outbound ESP sequence exhaustion and hard/soft lifetimes.
  Vendored DPDK `esn_outb_update_sqn` limits usable packets on overflow, but
  integration edge behavior is unverified and `n3iwf_dp_ipsec.c` does not apply
  the wire soft/hard lifetime fields as a full lifetime policy. Do not claim
  that sequence protection is wholly absent or that exhaustion is validated.

#### Temporary signalling boundary

Audit unknown-SPI, known-SA/unbound-session, stale-binding and deletion traffic
through the `-k` punt fallback before claiming bypass across those conditions.
If a stronger boundary is required, design explicit signalling ownership and
test that failed user-plane state cannot be reclassified as signalling. The
current classifier admits eligible ESP without a signalling-SPI allowlist.

Remove the `-k`/`N3IWF_DP_KERNEL_SIGNALLING_ESP=1` exception only after IKE and
the signalling Child SA have a complete, tested DPDK ownership model. Preserve
control-plane reachability and NAS TCP behavior during the transition.

### Implementation optimization after measurement

#### Batch the cryptodev path

The software backend submits one packet at a time. Introduce bounded bursts,
preallocated crypto operations, and queue-pair ownership without adding packet
copies or shared fast-path locks. Benchmark uplink and downlink separately.

#### Scale state access deliberately

The current NF executes control and packets on one lcore, so direct session
publication requires no cross-core synchronization. Before adding lcores,
design per-lcore/sharded tables and RCU-style or generation-safe updates.

Keep these invariants:

- Inbound: indexed SPI lookup.
- Downlink: TEID/QFI session lookup followed by direct active-SA slot/generation.
- No scan of 4,096 sessions or SAs per packet.
- Full scans, if retained, are explicitly slow-path control/reconciliation work.

#### Hardware and NUMA

Add capability-driven hardware/inline cryptodev support after the software
path is stable. Fail startup when configured requirements are unavailable.
Measure queue placement, NIC/crypto NUMA locality, mbuf headroom, burst size,
cache misses, and lcore saturation before attributing bottlenecks.

### Protocol completeness

#### NAT-T

Extend the existing UDP/4500 control classification and non-ESP marker handling
with user-plane ESP-in-UDP encapsulation/decapsulation, port state, checksums,
replay, and negative tests.
The wire/Go model can describe NAT-T, but the current DPDK SA validation rejects
it. NAT-T interaction with optional rekey belongs to extension testing.

#### IPv6 and dual stack

Extend session contracts and lookups to IPv6 NWu/N3 transport and exercise IPv4,
IPv6, and IPv4v6 PDU traffic end to end. The current Go session builder and C
session upsert deliberately require IPv4 despite address-family fields in wire v1.

#### Release 18 security/interoperability coverage

Produce the missing gap matrix for the applicable behavior in TS 23.501,
24.502, 29.281, 29.244, 33.501, and 38.413. Select and implement required
algorithm profiles beyond AES-CBC/HMAC-SHA1-96, including ESN if required.
Treat rekey collision and replacement-delete validation as optional IKEv2
extension coverage rather than a prerequisite for upstream parity.

### Test automation and extended coverage

- Extend the existing clear-path QFI/RQI, extension-chain and inner-IP golden
  vectors with real ESP crypto-path vectors in both directions, including
  integrity, padding and replay failures. Keep existing component coverage
  distinct from unimplemented crypto-path tests.
- Run live multiple-QFI setup/modification/release and retain QFI/RQI
  preservation across GRE and the PDU Session Container as a regression test.
- Retain the completed multi-UE identity and isolation cases as automated
  regression tests. Add churn and selective release when lifecycle hardening is
  resumed, and keep overlapping rekey in the optional test profile.
- Automate the three-machine physical acceptance sequence: topology validation,
  manager/NF startup, registration, PDU setup, ping/UDP echo, TCP/UDP load,
  release, counter assertions, and cleanup. Keep rekey in a separate optional
  acceptance profile.
- Add core soak tests for repeated register/release with leak, stale-state,
  replay, and key-erasure checks.
- Add CI/static assertions that packet-processing translation units do not call
  the control-only latest-SA scan.

### Optional extensions

#### Child-SA rekey extension

Current upstream free5GC N3IWF does not schedule automatic Child-SA rekey.
Keep L25GC+'s tested implementation disabled by default and outside the core
integration gate. Its optional regression profile should cover repeated
overlap, retransmission and retry exhaustion, lost delete responses,
duplicate/delayed updates, simultaneous rekey, restart/reconciliation, key
erasure, and multi-UE overlapping replacements.

#### Other optional extensions

- Per-QFI packet/byte/drop/latency counters and export to the project metrics
  system without imposing a global per-packet lock.
- Dynamic UPF peers and richer interface-state commands if deployments need
  them; keep the N3 packet itself standards-compatible.
- Hardware inline IPsec and additional cryptodev PMDs.
- Multi-process or multi-instance scaling once correctness and single-instance
  baselines are stable.
- Controlled clear-mode fuzzing as a diagnostic tool. Clear mode must remain
  explicit, test-only, and impossible to enable accidentally in production.
