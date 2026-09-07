# Current State

This document reflects the working tree inspected through 2026-09-04 and live
evidence reported through 2026-09-04. The N3IWF, ONVM-UPF, configuration, scripts,
and existing non-3GPP test documentation all contain uncommitted changes. Run
`git status` in the root and in both submodules before assuming this state is
committed or reproducible from the parent commit.

## Relevant Revisions and Working Tree

- Root branch: `feat/n3iwf`, inspected at `9794fda`; the initial integration
  commit is `06757f4` (`feat: n3iwf init`).
- `NFs/n3iwf`: branch `feat/L25GC-plus`, inspected at `3ade29c`, with additional
  uncommitted rekey and user-plane changes.
- `NFs/onvm-upf`: branch `feat/n3iwf`, inspected at `d2951f1`, with additional
  uncommitted software-IPsec and constant-time SA-lookup changes.

The exact current diff, rather than these hashes alone, is the source of truth.

## Implemented Components

### Go control plane

The pinned free5GC-derived N3IWF remains responsible for IKEv2, EAP-5G, NAS,
NGAP/SCTP, certificates, UE contexts, PDU-session procedures, and IKE Child-SA
lifecycle. A `userplane.Backend` abstraction selects either the legacy Linux
path or the ONVM backend.

With `userPlaneBackend: onvm`:

- Startup performs a fail-fast control-socket hello.
- The Go raw GRE/GTP-U user-plane server is not started.
- PDU-session setup programs a Child SA first and then the session contract.
- Session/SA updates and deletes carry monotonically increasing per-session
  generations and require acknowledgements.
- PDU-session release removes dataplane state.
- RAN UE NGAP ID zero is accepted, as required by the local allocator and NGAP.

The signalling Child SA still uses Linux XFRM. In the tested transition mode,
`n3iwf-dp` punts IKE and signalling ESP through a TAP interface while DPDK owns
the PDU-session Child SAs and user traffic.

### CP/DP contract

`NFs/n3iwf-dp-client` and `n3iwf_dp_wire.h` implement protocol version 1 over
Unix `SOCK_SEQPACKET`. It has writer and observer roles, root/effective-UID peer
checks, transaction IDs, acknowledgements, and explicit status codes.

Implemented messages are hello, session upsert/delete, Child-SA upsert/delete,
and stats get. Contracts contain UE/session identity, UL/DL TEIDs, QFIs, NWu/N3
addresses, SPIs, directional keys, IKE transform IDs, selectors, replay window,
flags, sequence/lifetime fields, and generation. Key-bearing temporary copies
are explicitly erased after use where implemented.

The socket carries no user packets.

### ONVM/DPDK dataplane

`l25gc_n3iwf_dp` has three explicit modes:

- Fail-closed: no user traffic accepted.
- `-t`: clear-GRE integration/test mode.
- `-e`: DPDK software-IPsec mode.

Clear and IPsec modes are mutually exclusive. The NF resolves the actual DPDK
access-port MAC at startup. A valid uplink learns the UE source MAC per session;
downlink is rejected until that neighbor is known. Learn, change, and neighbor
drop counters are exported.

The logical N3 path hands full Ethernet/IPv4/UDP/GTP-U frames directly between
N3IWF-DP service 14 and UPF-U service 1 through ONVM rings. UPF-U recognizes the
configured N3IWF peer and routes downlink N3 packets to service 14 rather than a
physical N3 port. A fixed UPF-U bug now zeroes the newly prepended internal
Ethernet/IPv4 header instead of exposing stale mbuf headroom bits.

### Packet conversion

Uplink clear processing parses Ethernet, outer IP, GRE key/QFI, and the inner
PDU packet; validates QFI against the session; learns the access MAC; removes
NWu/GRE headers; and prepends IPv4/UDP/GTP-U with a standards-shaped uplink PDU
Session Container.

Downlink processing parses GTP-U and its extension chain, selects the session
by downlink TEID plus QFI, removes N3 headers, and creates GRE/QFI plus the NWu
outer header and learned Ethernet addresses. QFI occupies bits 24..29 of the
GRE Key field.

IPv4 outer fragments and IPv6 fragment-next-header packets are rejected and
counted. Bounded reassembly is not implemented.

The fixed 1500-byte NWu/N3/N6 profile now enforces a 1410-byte maximum inner
PDU packet before packet mutation in both directions. This is the AES-CBC ESP
padding boundary: 1410 produces a 1496-byte outer NWu IPv4 packet, while 1411
produces 1512 bytes. Oversize, insufficient-buffer, and outer-fragment drops
have distinct counters. Component tests cover below/exact/above ICMP/TCP
vectors and byte preservation. The physical three-host boundary run now passes
as recorded below.

### MTU component verification on 2026-09-06

- `l25gc_n3iwf_dp` rebuilt successfully with the shared MTU contract.
- All seven documented N3IWF-DP/UPF Meson component tests pass.
- `go test ./...` passes in `NFs/n3iwf-dp-client` with the extended, backward-
  compatible stats decoder.
- The clear-path mbuf test passes 1409- and 1410-byte packets in uplink and
  downlink, rejects 1411 without mutation, preserves accepted payload bytes,
  and observes exact oversize, bidirectional buffer, and outer-fragment
  counter deltas.

These component results are supplemented by the physical acceptance below.

### MTU physical acceptance on 2026-09-06

The distributed result recorded under
`results/mtu-boundary-20260906T083817Z/` passes. UE effective MTUs were 1410
on the PDU GRE interfaces, 1438 on the PDU Child-SA XFRM interface, and 1500 on
physical NWu. The lossless UE capture contained 55,203 ESP packets, including
37,159 at the exact expected 1496-byte outer IPv4 length, and zero clear GRE.
The lossless DN capture contained 12 packets each at inner lengths 1409 and
1410 plus the single deliberate 1411-byte downlink input.

Accepted ICMP advanced uplink/downlink by exactly 12/12. Endpoint PMTU rejects
did not reach the dataplane. The deliberate 1411-byte downlink input advanced
only `oversize_drops` by one. Retained MSS-1370 TCP sustained approximately 20
Mbit/s in each direction with zero retransmissions and advanced dataplane
uplink/downlink by 27,368/27,359 in the isolated interval. Oversize remained at
the intentional value one, every user-plane error/drop counter remained
unchanged, and the session and Child SA stayed active. One earlier ambient
control-punt drop was isolated from user traffic and remained stable during
the retained TCP window.

### Software IPsec

The first DPDK backend is implemented for IPv4 ESP tunnel mode, AES-CBC-128 or
AES-CBC-256, HMAC-SHA1-96, and non-ESN anti-replay. It was exercised using the
DPDK AESNI-MB PMD backed by Intel IPSec-MB 2.0. Unsupported contracts, including
NAT-T, ESN, and IPv6 outer transport, are rejected instead of downgraded.

Inbound processing parses ESP, performs indexed SPI lookup, authenticates and
decrypts, enforces the replay window, then enters the GRE/QFI uplink pipeline.
Downlink processing performs the GTP-U conversion, selects the active outbound
SA directly, and encrypts before physical output. Crypto submission currently
handles one packet at a time rather than a burst.

### Optional extension: Child-SA rekey overlap

Current upstream free5GC N3IWF creates PDU-session Child SAs but does not
schedule their automatic replacement. L25GC+ therefore treats the following
rekey implementation as an optional extension. It is disabled by default and
does not gate the core DPDK CP/DP integration.

The Go N3IWF can initiate an RFC 7296 `CREATE_CHILD_SA` rekey with `REKEY_SA`,
matching transforms/selectors, fresh nonces and derived keys. It retransmits the
exact encoded request with backoff, rejects competing peer-initiated rekeys with
an IKE error, installs the replacement, retains both inbound SPIs for a bounded
overlap, sends an authenticated delete for the old SA, and retires it only after
the delete response. Retry exhaustion requests UE context release.

Live short-lifetime testing on 2026-08-25 exposed a peer-side N3IWUE defect:
the UE interpreted the authenticated ESP Delete that retires the old Child SA
as whole-UE deregistration and shut down after the overlap. N3IWUE was corrected
to distinguish ESP and IKE deletes, match the peer-owned SPI, retire only the
selected Child SA, and return the paired local SPI. Live reverse TCP traffic on
2026-09-01 remained continuous through the rekey interval and old-SA retirement,
confirming that the UE no longer deregisters and forwarding transitions to the
replacement SA.

The N3IWF also drops a scheduled rekey event when its SPI-to-NGAP mapping, RAN
UE, or PDU-session context has already been removed. NGAP user-plane teardown
now accepts allocator-produced RAN UE NGAP ID zero while continuing to reject
negative IDs. These guards remove the stale second rekey and invalid-ID cleanup
symptoms seen after the peer shut down.

The dataplane stores up to 4,096 SAs in stable slots and indexes inbound SPI in
an 8,192-entry open-addressed table. A session stores the active outbound slot
and its installation generation. Control updates switch that pointer when the
replacement is installed; old and new inbound SPIs remain accepted during the
overlap. Deleting the selected SA clears the pointer and fails closed.

The outbound fast path is now:

```text
downlink TEID/QFI -> session -> active_outbound_sa_index + generation -> crypto
```

It does not scan the Child-SA table. The O(N) helper is named
`n3iwf_dp_child_sa_find_latest_for_control` and is referenced only from control
update code and tests. Crypto runtime reconciliation scans only when the Child-SA
table revision changes, not once per packet.

## Verification Evidence

### No-kernel-user-plane proof preparation on 2026-09-05

- `onvm_test/no_kernel_user_plane/` now contains a versioned proof plan,
  immutable run initialization, local TAP/XFRM/kernel-N3/physical capture,
  before/after snapshots, a dependency-free pcap analyzer, and a strict gate.
- The analyzer correlates the exact PDU-session ESP SPIs and an identifiable
  bidirectional UE-to-DN flow with N3IWF-DP counter deltas. It requires no
  user-plane observation on TAP, signalling XFRM, or kernel N3, while reporting
  allowed IKE and non-user ESP separately.
- The procedure treats the UE-peer physical NWu capture as authoritative and
  retains the L25GC+ mlx5 capture only as informational because bifurcated PMD
  traffic may bypass host packet sockets.

This is test infrastructure, not live proof. A complete three-host result
directory was subsequently captured and passed as recorded below.

### No-kernel-user-plane live acceptance on 2026-09-06

The retained result under
`results/no-kernel-user-plane-20260906T074303Z/` passes the complete proof
gate. The UE-side physical NWu capture contained 142 packets using the declared
uplink user-plane SPI and 302 using the declared downlink SPI, with no clear
GRE. The DN capture retained 110,971 bytes of identified uplink TCP payload and
328,379 bytes of downlink payload. All six tcpdump observers had zero reported
kernel or interface drops.

N3IWF-DP counters advanced by 142 uplink and 302 downlink packets. Unknown
TEID/QFI/SPI, malformed, replay, crypto, fragment, punt-drop, MAC-change, and
neighbor-drop counters all had zero delta; the session and Child SA remained
active. The TAP contained none of the user-plane SPIs or identifiable UE-to-DN
flow, the signalling XFRM interface contained no UE-to-DN flow, Linux `any`
observed no logical-N3 UDP/2152 packet, and neither logical N3 address was
assigned in Linux.

The TAP did contain four IKE UDP packets and twelve ESP packets using other
SPIs, while XFRM carried the expected NAS TCP/20000 signalling acknowledgments.
This explicitly preserves the temporary signalling exception. The host-side
physical mlx5 capture was empty, as permitted for the bifurcated PMD; the
lossless UE-peer physical capture is the authoritative NWu observation. The
accepted claim is therefore “no kernel user-plane crossing,” not “the entire
N3IWF avoids the kernel.”

The first analyzer used an overly strict 1 MiB per-direction payload floor.
The corrected 64 KiB floor still distinguishes deliberate payload from the
508-byte TCP ACK/control-only counterexample found in an earlier run. The exact
corrected analyzer is retained in the result by SHA-256, together with the
initial and reanalysis hashes.

### RQI component verification on 2026-09-04

- UPF-U now encodes the QER Reflective QoS value as downlink PSC RQI instead
  of emitting QFI alone.
- Direction-aware codec vectors cover exact RQI-clear/set PSC and GRE bytes,
  IPv4 and IPv6 inner payload indicators, and a PSC preceded by another GTP-U
  extension header.
- The mbuf downlink path preserves RQI through TEID/QFI selection into the GRE
  key for IPv4 and IPv6 payloads, including the chained-extension case.
- Uplink construction cannot originate RQI. Uplink GRE carrying the RQI bit,
  an uplink PSC presented to downlink, reserved GRE key bits, duplicate PSCs,
  and unsupported PSC presence bits fail closed; packet-path failures increment
  `malformed_packets`.
- `l25gc_n3iwf_dp` and `l25gc_upf_u` rebuild, and all seven documented
  N3IWF-DP/UPF component tests pass. Live Linux/ONVM parity and an RQI-set
  capture remain required before marking the TODO acceptance complete.

### Tests rerun on 2026-08-24

- `go test ./...` in `NFs/n3iwf-dp-client`: pass.
- `go test ./internal/userplane ./internal/ike ./internal/ngap ./pkg/factory`
  in `NFs/n3iwf`: pass. These tests require permission to open local UDP sockets.
- Current built C tests: codec, session, Child SA, control, punt, clear path, and
  UPF-U N3IWF routing all pass. Socket/DPDK-EAL tests require normal host access.
- Static reference search found no `n3iwf_dp_child_sa_find_session` symbol or
  hot-path call. `n3iwf_dp_child_sa_get_active_outbound` is the downlink caller.

These are unit/component results. They do not replace a fresh build or live
three-host acceptance test after checkout.

### Multi-session identity tests added on 2026-09-01

- The N3IWF-DP binary and session, Child-SA, control, and clear-path component
  targets rebuild successfully with the direct Child-SA/session association.
- The six current N3IWF-DP Meson tests—codec, session, Child SA, control, punt,
  and clear path—pass. New cases cover one UE with two PDU sessions reusing the
  same NWu address and QFI, direct SPI-to-session selection, TEID-selected
  downlink preservation, independent access-MAC learning, session deletion,
  and rejection of a stale association after slot recycling.
- A static fast-path search confirms the IPsec translation unit no longer
  calls the NWu/QFI, identity-scan, or control-only latest-SA lookup helpers.

This was component evidence only at that stage. The true two-UE live gate was
subsequently completed as recorded below. The controlled kernel-versus-DPDK
comparison and broader scalability evaluation remain open in `TODO.md`.

### Two-UE acceptance preparation on 2026-09-04

- All seven documented N3IWF-DP/UPF component acceptance tests pass after
  rebuilding the current working tree. The focused N3IWF Go packages and the
  complete `NFs/n3iwf-dp-client` suite also pass.
- A fresh static reference check finds the control-only latest-SA lookup called
  only from `n3iwf_dp_control.c`; it is not called from the IPsec, downlink, or
  clear packet-processing translation units.
- `onvm_test/NON3GPP_README.md` now contains a step-by-step two-UE
  software-IPsec acceptance procedure, including identity recording,
  simultaneous bidirectional load, packet-capture isolation checks, and exact
  counter gates.

This section records the preparation stage. The later physical run used two
independently provisioned N3IWUE peers with distinct SUPIs, NWu addresses, and
access MACs and completed the gate as recorded below.

### Per-UE NAS ordering correction on 2026-09-04

- The first same-host two-N3IWUE attempt registered both UEs, but only UE2
  established PDU session 10. UE1 received Registration Accept twice, six
  seconds apart, and retained only its signalling Child SA; UE2 installed its
  second Child SA, GRE interfaces, VRF, and PDU address.
- N3IWF's NWu TCP reader correctly uses a two-byte length envelope and
  `io.ReadFull`, so TCP segmentation/coalescing was not the fault. However, it
  launched a new goroutine for every decoded NAS frame. Registration Complete
  and the immediately following PDU Session Establishment Request could
  therefore reach the AMF out of order, including their NAS uplink sequence
  counts.
- NWu forwarding is now serialized within each UE TCP connection while
  remaining concurrent across different UE connections. A regression test
  blocks the first forward and proves the second frame cannot overtake it.
- The focused regression passes with the Go race detector, the complete N3IWF
  Go suite passes, and `bin/n3iwf.next` has been rebuilt.

The rebuilt N3IWF was subsequently restarted and both UEs established PDU
sessions concurrently. The replay-window issue found in that run and the
accepted rerun are recorded below. N3IWUE T3580 retry/recovery remains desirable
defense in depth, but it does not replace ordered N3IWF NAS forwarding.

### Two-UE live replay-window finding on 2026-09-04

- With the NAS ordering correction, both UEs remained installed concurrently
  (`active_sessions=2`, `active_child_sas=2`, two stable access MAC learns),
  and completed simultaneous bidirectional TCP traffic. Uplink and downlink
  advanced by 10,305,782 and 3,987,792 packets respectively, with zero unknown
  TEID, QFI, or SPI events and no access-MAC changes.
- The run was not accepted because `crypto_failures` increased by 955 and
  `control_punt_drops` by two. The bounded NF diagnostics identified the data
  failures as inbound `rte_ipsec_pkt_cpu_prepare` rejections with `EINVAL`,
  not authentication failures. In this DPDK path, `EINVAL` is the anti-replay
  check rejecting a duplicate or sequence number older than the current
  window.
- The user-plane replay window was only 64 packets, which is too narrow for
  burst reordering at the observed aggregate rate. The DPDK Child-SA contract
  now uses a 4096-packet window, keeping anti-replay enabled. The dataplane now
  classifies inbound prepare `EINVAL` as `replay_drops` instead of
  `crypto_failures`. The N3IWF binary and N3IWF-DP were rebuilt; the focused Go
  test and all seven N3IWF-DP/UPF component tests pass.

That run remained unaccepted pending a repeat with the rebuilt binaries and a
clean counter interval; ambient IPv6 on the IPv4-only NWu port was also removed
before the repeat so it could not contaminate the snapshot.

The replay-window rerun then completed with both `iperf3 --bidir` clients
launched concurrently. UE1 carried 559 Mbit/s uplink and 535 Mbit/s downlink;
UE2 carried 173 Mbit/s uplink and 170 Mbit/s downlink, for approximately 1.44
Gbit/s aggregate sender throughput. All four TCP directions reported zero
retransmissions. Dataplane uplink and downlink counters advanced by 1,449,286
and 1,455,065 packets respectively, while every identity, replay, crypto,
malformed, fragment, stale, punt-drop, and access-neighbor counter had zero
delta. Both sessions, Child SAs, and learned access MACs remained stable at
two. This validates concurrent two-UE forwarding and the 4096-packet replay
window correction. A subsequent pair of captures overlapped concurrent UE1 and
UE2 bidirectional connections. `/tmp/ue1-isolation.pcap` and
`/tmp/ue2-isolation.pcap` were each header-only 24-byte files and decoded to
zero packets, proving that neither UE received traffic addressed to the other
UE's PDU address. Together with the distinct identity evidence and static
fast-path scan check, this completes the immediate multiple-UE acceptance gate.
The isolation load showed substantial throughput imbalance, but the immediate
gate sets no minimum per-UE rate; controlled performance and fairness remain
separate work.

### Selective-release correction added on 2026-09-02

- A live release of PDU session 10 exposed that the deployed `bin/n3iwf` was
  older than the source fix which accepts the valid first RAN UE NGAP ID of
  zero. The UE completed its NAS release and removed local session-10 state,
  but N3IWF-DP correctly showed that neither of its two sessions had been
  deleted.
- The current NGAP release path now deletes the selected dataplane session and
  all Child SAs associated with that PDU-session ID before removing the local
  PDU context. It deliberately excludes Child SAs for other sessions and
  handles the temporary two-SA overlap possible during optional rekey.
- Focused release tests and the complete `NFs/n3iwf` Go test suite pass. A new
  `bin/n3iwf.next` was built from this working tree on 2026-09-02. Live
  acceptance still requires a clean two-session restart followed by release of
  session 10, with `active_sessions=1`, `active_child_sas=1`, and uninterrupted
  session-11 traffic.

### Live functional evidence from this development session

- N3IWUE registration, authentication, NGAP exchange, PDU-session setup, and
  acknowledged session/Child-SA programming succeeded.
- Deterministic physical clear-GRE UDP echo completed 5/5 and later 100/100
  replies through N3IWF-DP, logical N3, ONVM-UPF, physical N6, and the DN.
- Secure ESP traffic completed 100/100 pings to the local DN and 100/100 to a
  routed `1.1.1.1` test address with zero unknown TEID/QFI/SPI, replay, crypto,
  malformed, or fragment errors in the reported runs.
- Packet capture on the access link showed ESP and no clear GRE during the
  secure-path test.
- The direct active-SA rekey-overlap design is now confirmed working in a live
  deployment. The operator completed 60-second TCP uplink runs with rekey
  enabled; the supplied intervals remained approximately 1.17-1.47 Gbit/s with
  zero TCP retransmissions. Unit tests additionally verify replacement
  installation, overlap lifecycle, retransmission, timeout cleanup, and
  simultaneous-rekey rejection.
- A 60-second reverse TCP run with the 30-second rekey lifetime and 10-second
  overlap transferred 8.06 GBytes at 1.15 Gbit/s with four retransmissions.
  Every one-second interval remained between approximately 1.14 and 1.16
  Gbit/s; there was no visible rekey-correlated outage or throughput collapse.

### Performance evidence

#### Provisional two-UE throughput checkpoint on 2026-09-06

Two concurrent UEs were exercised for 120 seconds per direction. TCP sender
rates were 822 and 832 Mbit/s uplink (1.654 Gbit/s aggregate) and 589 and 592
Mbit/s downlink (1.181 Gbit/s aggregate). The four senders reported 61, 7, 96,
and 0 retransmissions respectively. The ten-second tail in the uplink receiver
summaries is not included in the throughput comparison because the senders ran
for the requested 120-second measurement interval.

Concurrent UDP uplink offered 600 Mbit/s per UE. Receivers reported 1,535 of
7,499,909 datagrams lost (0.020%) and 2,858 of 7,499,958 lost (0.038%), or
4,393 of 14,999,867 (about 0.029%) in aggregate. This passes the project's
0.1% loss threshold at 1.2 Gbit/s aggregate offered load.

These results are reasonable as a checkpoint but remain provisional. The
matching UDP downlink result and before/after N3IWF-DP counters were not
provided. The 1.181 Gbit/s TCP downlink aggregate is about 28.6% below uplink
and remains close to the earlier 1.19 Gbit/s single-stream downlink ceiling.
The deployment pins all N3IWF-DP work to core 14 and UPF-U to core 3, so adding
a second UE does not add dataplane cores. The downlink-only path also performs
UPF classifier/session/QER and UE-shaper work before GTP encapsulation, then
N3IWF-DP performs GTP parsing, GRE construction, outbound ESP encryption/auth,
and an IPv4 checksum before physical transmit. CPU measurements on manager
cores 0-2, UPF-U core 3, and N3IWF-DP core 14 are required to identify which
stage is saturated; the current data alone does not justify assigning the gap
to crypto, UPF shaping, the DN transmitter, or UE-host XFRM receive processing.

The pre-rekey software-IPsec checkpoint used one TCP stream for 15 seconds:

| Direction | Result | TCP retransmits |
| --- | ---: | ---: |
| UE to DN | about 1.21 Gbit/s | 0 |
| DN to UE (`iperf3 -R`) | about 1.19 Gbit/s | 1 |

An earlier rekey-overlap implementation selected the newest outbound SA by
scanning all 4,096 Child-SA slots for every downlink packet. Downlink fell to
about 504 Mbit/s, with repeat results around 481/478 Mbit/s; disabling timed
rekey did not help because the scan remained in the packet path. A sampled
profile attributed 58.2% of N3IWF-DP CPU to the old session-SA lookup.

The code has been redesigned to use the direct active-SA mapping described
above. Its unit/static checks pass, and live 60-second uplink testing with rekey
enabled has been reported successful. The post-redesign reverse/downlink run on
2026-09-01 transferred 8.06 GBytes at 1.15 Gbit/s over 60 seconds with four TCP
retransmissions. Its one-second intervals stayed at approximately 1.14-1.16
Gbit/s while the 30-second lifetime and 10-second overlap were active. This is
within about 3.4% of the earlier 1.19 Gbit/s checkpoint and confirms that the
old O(4,096-SA) downlink scan regression is removed.

## Known Limitations and Unverified Areas

- The required kernel/free5GC baseline, 2x comparison, p99 latency, controlled
  CPU budget, and <=0.1% loss acceptance test have not been completed.
- Optional rekey-enabled uplink and reverse/downlink TCP traffic are verified
  by the operator. Longer repeated-rekey soak, forced retransmission,
  simultaneous rekey, and failure-recovery cases remain optional extension
  work rather than core integration acceptance.
- Current session programming is IPv4-only. NAT-T, ESN, and IPv6 ESP transport
  are rejected. The codec can identify IPv6 UE payloads, but dual-stack end to
  end is not demonstrated.
- The cryptodev path is single-packet; burst crypto, hardware/inline crypto,
  and multi-lcore session sharding are absent.
- IKE and signalling ESP still cross the TAP/Linux boundary in the tested
  transition configuration. The user plane is zero-kernel-crossing; the whole
  N3IWF is not.
- Multiple QFIs are represented and unit-tested, but a live multiple-QFI QoS
  flow test is not recorded. Downlink RQI preservation is implemented and
  component-tested: UPF-U encodes the PFCP QER RQI in the downlink PSC,
  N3IWF-DP carries it independently of QFI into GRE, and direction-invalid or
  unsupported encodings are rejected and counted. A Linux/ONVM parity run and
  live RQI-set capture are still required for acceptance. Per-QFI byte/packet
  counters are absent.
- Multi-session data structures and two-UE isolation have been validated live.
  The current working tree binds every Child SA to a stable session slot and
  validates a session-slot generation before secure
  uplink forwarding. This removes the ambiguous second lookup by UE inner-NWu
  address plus QFI when one UE reuses a QFI across PDU sessions. Secure downlink
  likewise retains the session selected by TEID/QFI through outbound-SA
  selection instead of reparsing GRE and looking up NWu/QFI. Component tests
  cover same-UE/same-QFI separation and deleted-slot recycling. Concurrent live
  two-UE bidirectional traffic and capture-based isolation now pass, including
  zero identity/drop/error deltas in the accepted interval. Larger UE-count
  scaling, per-UE fairness, selective release, and lifecycle churn remain open;
  multi-UE overlapping rekey remains an optional extension test.
- Fragments are dropped rather than reassembled. The 1410-byte MTU/PMTU policy,
  component boundary tests, and physical live boundary run are accepted.
  Sequence-exhaustion behavior remains unverified.
- N3IWF does not learn the SMF-assigned UE PDU address from NAS; wire v1 carries
  an unspecified address and uplink selection uses authenticated Child SA/NWu
  state plus QFI. Explicit inner-source anti-spoof enforcement is therefore not
  demonstrated.
- A Release 18 gap matrix against TS 23.501, 24.502, 29.281, 29.244, 33.501,
  and 38.413 has not been produced. Do not claim full Release 18 compliance.
- The sample `config/n3iwfcfg.yaml` contains short rekey-test timing values but
  sets `childSARekey.enable: false`. Normal integration and performance runs
  use that disabled default; enable it only for explicit extension tests.
