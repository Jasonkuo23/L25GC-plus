# Code-grounded review of the DPDK N3IWF paper outline

## A. Executive Summary

Reviewed on 2026-09-07 against the local working tree. The supplied prompt names an outline under `research/n3iwf-paper/`; the actual source is [dpdk_n3iwf_paper_outline.md](../../docs/high-performance-n3iwf/dpdk_n3iwf_paper_outline.md). This report preserves that outline and changes no production code.

**The implementation supports a focused systems paper about moving the N3IWF PDU-session user plane into DPDK while retaining a Go control plane. It does not yet support a measured speedup or CPU-efficiency claim against Linux.** The strongest current combination is the CP/DP boundary, preservation of session identity through ESP/GRE/GTP conversion, and a retained capture-based demonstration of kernel bypass for a particular working deployment.

The principal corrections before drafting are:

1. Describe **PDU-session user-plane bypass on the N3IWF/UPF host**, not an entirely kernel-free N3IWF or UE-to-DN system. Go signalling still uses TAP and Linux XFRM; the UE uses its own Linux networking/XFRM path. The transition classifier is not an explicit signalling-SPI allowlist (C7 below).
2. Replace the outline's generic uplink “QFI/session lookup” with **SPI-indexed Child SA → direct bound session slot and lifetime generation → authenticated NWu/QFI validation**. Downlink retains the TEID-selected session and directly selects its active outbound SA (C4–C6).
3. Qualify the software ESP profile: IPv4 tunnel mode, AES-CBC-128/256, HMAC-SHA1-96, non-ESN, no NAT-T user plane. Do not call this generic IPsec or complete N3IWF interoperability (C3–C4).
4. Distinguish initial setup and control-socket upserts from **live NGAP session modification**. The modification handlers mutate Go state but do not publish corresponding ONVM session updates. That is an implementation gap, not merely an unrun experiment (C9).
5. Keep performance improvement as an evaluation question. No retained fair Linux-versus-DPDK comparison was found. The large throughput numbers are reported checkpoints, not locally reproducible benchmark datasets (E3–E5).
6. Do not list Child-SA rekey itself as unimplemented future work. An optional ONVM rekey implementation and tests exist; exclude it from the main paper evaluation or mention it briefly as an existing extension. Repeated-rekey hardening remains future work (C10).

Add the full-packet logical N3 boundary, acknowledged/versioned state programming, stable session/SA references, per-session MAC learning, and the exact MTU policy to the design. These explain how the integration works and where its correctness boundaries lie. Avoid expanding the paper into NAT-T, IPv6 transport, hardware crypto, or a general lifecycle-hardening project.

### Review basis and limits

Root: `feat/n3iwf` at `9794fda`; N3IWF: `feat/L25GC-plus` at `3ade29c`; ONVM-UPF: `feat/n3iwf` at `d2951f1`. Root and both project submodules contain unstaged changes, and essential IPsec, MTU, rekey, and test sources are untracked. Commit hashes alone therefore do not identify this implementation. Recursive status also shows generated artifacts in `dpdk-kmods`; unrelated root deletions/configuration changes were not treated as paper contributions.

The review uses source inspection, test bodies, and retained evidence, with these additional checks performed during this review:

- Compiled current codec, session, and Child-SA component tests directly with `cc -std=gnu11 -O2` into a temporary directory and ran them: all three passed. Source lists match their entries in [meson.build](../../NFs/onvm-upf/5gc/n3iwf_dp/meson.build).
- Ran the retained SHA-256-named proof analyzer against a **temporary copy** of the no-kernel result: PASS. The original report, captures, and gate were not overwritten.
- Inspected the remaining Go/C tests and harnesses; did not rerun the complete Go/DPDK suites, rebuild deployment binaries, contact the UE/DN hosts, or perform live traffic tests.
- `perf.data` and `perf.data.old` exist, but `perf report --header-only -i perf.data --stdio` failed with permission denied. Their contents are **UNKNOWN**, not evidence of a validated CPU-efficiency result. No permission change was attempted.

This is a paper-claim review, not a comprehensive security or protocol-conformance audit. No external novelty or standards-compliance determination is made.

## B. Claim-to-Code Matrix

### Evidence references

Paths below are relative to the repository root via clickable links. Matrix entries cite these reference IDs and specific functions so that repeated long paths do not obscure the claims.

| ID | Concrete source and relevant symbols |
| --- | --- |
| C1 | [backend.go](../../NFs/n3iwf/internal/userplane/backend.go): `Backend`, `onvmBackend.Start`, `UsesKernelDataPlane`; [init.go](../../NFs/n3iwf/pkg/service/init.go): `N3iwfApp.Run`, conditional `nwuupServer.Run`, default XFRM interface setup. |
| C2 | [client.go](../../NFs/n3iwf-dp-client/client.go): `Session`, `ChildSA`, `exchange`, `marshalSession`, `marshalChildSA`, `parseACK`, `parseStats`; [wire header](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_wire.h); [control.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_control.c): `handle_message`, `send_stats`, `n3iwf_dp_control_poll`, `SO_PEERCRED` check and writer-role arbitration. |
| C3 | [session.go](../../NFs/n3iwf/internal/userplane/session.go): `BuildSession`; [child_sa.go](../../NFs/n3iwf/internal/userplane/child_sa.go): `BuildChildSA`, `ClearChildSAKeys`; [IKE handler](../../NFs/n3iwf/internal/ike/handler.go): `upsertPDUSessionUserPlane`, `upsertChildSAUserPlane`, `continueCreateChildSA`, signalling `ApplyXFRMRule` call. |
| C4 | [ipsec.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_ipsec.c): `n3iwf_dp_handle_ipsec_packet`, `ipv4_esp_spi`, `cpu_crypto`, `runtime_sa`, `init_direction`, `device_supports_profile`, `n3iwf_dp_ipsec_reconcile`; [ipsec.h](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_ipsec.h): runtime structures. |
| C5 | [child_sa.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_child_sa.c): `find_spi`, `n3iwf_dp_child_sa_profile_supported`, `n3iwf_dp_child_sa_get_bound_session`, `n3iwf_dp_child_sa_get_active_outbound`, `n3iwf_dp_child_sa_find_latest_for_control`, upsert/delete/bind functions; [session.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_session.c): `n3iwf_dp_session_upsert_wire`, `find_downlink_teid`, `n3iwf_dp_session_find_downlink`, MAC learning and active-SA functions; corresponding [SA](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_child_sa.h) and [session](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_session.h) structures. |
| C6 | [clear.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_clear.c): `handle_uplink`, `n3iwf_dp_handle_authenticated_uplink`, `prepend_gtpu`, `parse_l3`; [downlink.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_downlink.c): `n3iwf_dp_handle_clear_downlink_selected`, `prepend_clear_gre`; [codec.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_codec.c): `n3iwf_dp_gre_parse/build`, `n3iwf_dp_gtpu_parse/build`. |
| C7 | [n3iwf_dp.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp.c): `main`, `parse_args`, `packet_handler`, `punt_packet_to_cp`, `return_control_packets`, `periodic_action`; [punt.c](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_punt.c): `classify_ipv4`, `classify_udp`, directional classifiers and TAP I/O. |
| C8 | [upf_u_n3iwf.c](../../NFs/onvm-upf/5gc/upf_u/upf_u_n3iwf.c): `upf_u_n3iwf_peer_matches`, `upf_u_n3iwf_prepend_internal_ethernet`, `upf_u_n3iwf_set_route`, `upf_u_n3iwf_dl_pdu_session_information`; [upf_u.c](../../NFs/onvm-upf/5gc/upf_u/upf_u.c): `Encap`, QER QFI/RQI extraction and logical-N3 route callers. |
| C9 | [NGAP handler](../../NFs/n3iwf/internal/ngap/handler.go): `HandlePDUSessionResourceReleaseCommand`, `deletePDUSessionUserPlane`, `childSAsForPDUSession`, `deleteRanUeUserPlaneSessions`, `HandlePDUSessionResourceModifyRequest`, `handlePDUSessionResourceModifyRequestTransfer`, `HandlePDUSessionResourceModifyConfirm`; [NWu control server](../../NFs/n3iwf/internal/nwucp/server.go): `serveConnWithForwarder`. |
| C10 | [rekey.go](../../NFs/n3iwf/internal/ike/rekey.go): `scheduleChildSARekey`, `HandleRekeyChildSA`, `handleChildSARekeyResponse`, `HandleRetransmitRekeyRequest`, `handleRekeyDeleteResponse`, `completeRekeyRetirement`; [ikeue.go](../../NFs/n3iwf/internal/context/ikeue.go): `RekeyRequestState` and request synchronization; [config.go](../../NFs/n3iwf/pkg/factory/config.go): `GetChildSARekey`; [deployment config](../../config/n3iwfcfg.yaml): `enable: false`. |
| C11 | [mtu.h](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_mtu.h): `n3iwf_dp_esp_outer_ipv4_len`, `n3iwf_dp_inner_packet_supported`, buffer budgets; enforcement in C6. |
| C12 | [Linux NWu user-plane server](../../NFs/n3iwf/internal/nwuup/server.go): `newGreConn`, `newGtpuConn`, `forwardUL`, `forwardDL`; [XFRM code](../../NFs/n3iwf/internal/ike/xfrm/xfrm.go): `ApplyXFRMRule`, `addOrReplaceXFRMState`, `addOrReplaceXFRMPolicy`. |
| C13 | [topology](../../config/n3iwf_dp_topology.env), [NF launcher](../../scripts/run/run_n3iwf_dp.sh), [manager launcher](../../scripts/run/run_onvm_mgr.sh), [manager start script](../../NFs/onvm-upf/scripts/start.sh): physical service map, lcores, TAP setup, explicit crypto vdev creation; [UPF config](../../NFs/onvm-upf/5gc/upf_u/config/upf_u.yaml): logical peer/service. |
| T1 | [backend tests](../../NFs/n3iwf/internal/userplane/backend_test.go): lifecycle/startup failure; [session tests](../../NFs/n3iwf/internal/userplane/session_test.go): session construction, ID zero and multiple QFIs; [Child-SA tests](../../NFs/n3iwf/internal/userplane/child_sa_test.go): initiator-dependent directional keys; [client tests](../../NFs/n3iwf-dp-client/client_test.go): wire and stats cases. |
| T2 | [codec tests](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_codec_test.c): `test_directional_golden_headers`, `test_gtpu_extension_chain_preserves_rqi`, malformed cases; [clear-path tests](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_clear_test.c): `test_authenticated_uplink_disambiguates_session`, `test_downlink_preserves_teid_selected_session`, `test_mtu_boundaries`, `test_buffer_and_fragment_drops`, RQI cases. |
| T3 | [session tests](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_session_test.c), [SA tests](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_child_sa_test.c), [control tests](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_control_test.c): identity conflicts, generations, replacement/retirement and recycled bindings; [punt tests](../../NFs/onvm-upf/5gc/n3iwf_dp/n3iwf_dp_punt_test.c); [UPF tests](../../NFs/onvm-upf/5gc/upf_u/upf_u_n3iwf_test.c): service routing and RQI encoding. |
| T4 | [rekey tests](../../NFs/n3iwf/internal/ike/rekey_test.go), [IKE lifecycle tests](../../NFs/n3iwf/internal/ike/userplane_test.go), [NGAP release tests](../../NFs/n3iwf/internal/ngap/userplane_test.go), [NAS ordering test](../../NFs/n3iwf/internal/nwucp/server_test.go), [XFRM replacement tests](../../NFs/n3iwf/internal/ike/xfrm/xfrm_test.go). These are component tests, not deployment evidence. |
| E1 | [No-kernel collector](../../onvm_test/no_kernel_user_plane/no_kernel_user_plane.sh), [analyzer](../../onvm_test/no_kernel_user_plane/proof.py): `analyze`, `read_pcap`, `flow_direction`; [harness tests](../../onvm_test/no_kernel_user_plane/test_no_kernel_user_plane.sh); [retained result](../../results/no-kernel-user-plane-20260906T074303Z/report.md), [gate](../../results/no-kernel-user-plane-20260906T074303Z/gate.status), [captures](../../results/no-kernel-user-plane-20260906T074303Z/captures), [traffic logs](../../results/no-kernel-user-plane-20260906T074303Z/raw). |
| E2 | [MTU result](../../results/mtu-boundary-20260906T083817Z/report.md) and adjacent counter snapshots. The report identifies remote UE/DN artifacts; those pcaps/logs were not available for local reanalysis. |
| E3 | [Backend harness](../../onvm_test/backend_conformance/backend_conformance.sh): `run_command`, `record_case`, `validate_evidence_index`, `gate_run`, `report_runs`; [scenario manifest](../../onvm_test/backend_conformance/scenarios.tsv); [harness tests](../../onvm_test/backend_conformance/test_backend_conformance.sh). No paired live result directories found. |
| E4 | [Performance runner](../../onvm_test/multi_ue_performance/run_two_ue_iperf.sh): `run_client`; [performance procedure](../../onvm_test/multi_ue_performance/README.md). Runner records TCP commands, JSON, exit status and coarse trial timestamps; CPU/UDP/latency procedures are largely manual. |
| E5 | [CURRENT_STATE.md](../../docs/high-performance-n3iwf/CURRENT_STATE.md) and [NON3GPP_README.md](../../onvm_test/NON3GPP_README.md) contain historical performance and two-UE reports. These are **secondary reports**, not substitutes for missing raw logs, measurements, or captures. |

### Classification rules

`IMPLEMENTED` means executable support with directly inspectable test or live evidence for the stated scope; it does not mean general certification. `IMPLEMENTED_BUT_NOT_VALIDATED` means executable support exists but the claimed end-to-end experiment is missing or only reported. `PARTIALLY_IMPLEMENTED` identifies an actual capability gap. `NOT_IMPLEMENTED` denotes an absent requested capability in the scoped implementation. `DOCUMENTATION_ONLY` covers a planned evaluation/result or assertion with no retained supporting experiment. `UNKNOWN` marks evidence this review cannot determine. Repeated outline statements are consolidated below; section numbers refer to the original outline.

| Outline item / claim | Status | Code evidence | Test/evidence | Paper action |
| --- | --- | --- | --- | --- |
| Positioning, §§1/3.1/10: CP/DP-separated N3IWF | IMPLEMENTED | C1 `Backend`, `Run`; C3 `continueCreateChildSA`; C7 `packet_handler` | T1, T4; E1 active traffic/state | KEEP |
| §§1–3: reuse free5GC-derived IKE/EAP-5G/NAS/NGAP control behavior | IMPLEMENTED | C1 startup; C3 IKE handler; C9 NAS and NGAP handlers | T1/T4 and active configured deployment in E1 | KEEP BUT NARROW CLAIM to reused control protocols with modifications |
| Research question: preserve full interoperability/control behavior | PARTIALLY_IMPLEMENTED | C3 IPv4-only contract; C5 restricted profile; C9 modification gap | E3 is not an accepted parity result | KEEP BUT NARROW CLAIM to tested N3IWUE/5GC procedures/profile |
| §§2.2/3.1/5.3: Linux reference backend exists | IMPLEMENTED | C1 backend switch; C12 raw GRE/GTP sockets and XFRM rules | T1 backend selection; no retained live Linux benchmark | KEEP; NEEDS EXPERIMENT for baseline |
| §2.2: baseline is simply kernel-only forwarding | PARTIALLY_IMPLEMENTED | C12 `forwardUL/forwardDL` also do packet work in Go userspace | Direct source trace, not a measured breakdown | NEEDS WORDING CORRECTION: Linux XFRM/networking plus Go GRE/GTP processing |
| Contributions 1/3, §§3.5/5.2/7.2: PDU user-plane kernel bypass | IMPLEMENTED | C4/C6/C8 secure conversion and TONF ring handoff | E1 retained captures, counters and successful reanalysis | KEEP BUT NARROW CLAIM to observed deployment/flows and N3IWF host |
| §§3.1/3.5: signalling remains on Linux/TAP | IMPLEMENTED | C3 signalling XFRM call; C7 TAP punt/return | E1 four IKE UDP and twelve non-user-SPI ESP packets on TAP | KEEP; explicitly include NAS signalling |
| Any reading of “kernel bypass” as all N3IWF traffic or all failure cases | NOT_IMPLEMENTED | C7 permits ESP through transition path; C1 creates default XFRM | E1 permits signalling and tests a finite interval | REMOVE that interpretation |
| §§3.1/3.3/3.4: N3 and DPDK UPF integration | IMPLEMENTED | C6 `prepend_gtpu`; C8 `Encap`, peer matching, TONF route | T3 UPF routing; E1 traffic and recorded service map | KEEP; explain same-host logical N3 |
| §3.2: identity, TEIDs, QFIs, addresses, SPIs, keys and algorithms shared with DP | IMPLEMENTED | C2 wire structures/marshal functions; C3 builders | T1/T3 | KEEP |
| §3.2: “UE inner IP” means SMF-assigned PDU address known to N3IWF | NOT_IMPLEMENTED | C3 `BuildSession` explicitly sets `UEPDUAddress: net.IPv4zero` | T1 session construction | NEEDS WORDING CORRECTION: distinguish allocated inner NWu address from PDU address |
| §3.3: uplink ESP → GRE/QFI → session lookup → GTP-U | PARTIALLY_IMPLEMENTED | C4 resolves SA and its bound session before decrypt; C6 validates authenticated NWu/QFI against that session | T2/T3 same-UE/same-QFI cases | NEEDS WORDING CORRECTION to actual path below |
| §3.4: downlink GTP-U → session → GRE → SA → ESP | IMPLEMENTED | C6 returns TEID-selected session; C4 uses C5 direct active SA | T2/T3 and E1 | KEEP; clarify direct selection rather than a fresh session search |
| §§3.3/3.4/4.4: O(1)-style lookup; no full SA scan per packet | IMPLEMENTED | C5 open-addressed SPI/TEID indexes and direct slot references; latest-SA scan only called by C2 control handling | T3; inspected references | KEEP BUT NARROW CLAIM: expected indexed lookup, constant direct slot access; not worst-case O(1) hashing |
| §§4.1/4.3: C/DPDK/OpenNetVM user-space ESP | IMPLEMENTED | C4 `cpu_crypto` calls `rte_ipsec_pkt_cpu_prepare/process`; CPU-crypto action | E1 ESP/data correlation; no project cryptographic vector suite | KEEP BUT NARROW CLAIM to current profile |
| §4.1: software crypto backend | IMPLEMENTED | C4 profile capability checks/session initialization; C13 AESNI-MB vdev launch | E1 retained manager arguments; T3 rejects unsupported SA profiles | KEEP; specify synchronous software cryptodev |
| Broad/unqualified ESP algorithm support | PARTIALLY_IMPLEMENTED | C5 only AES-CBC-128/256 + HMAC-SHA1-96, IPv4 GRE tunnel, no ESN/NAT-T | T3 profile cases; exact negotiated key size not retained in benchmark metadata | KEEP BUT NARROW CLAIM |
| §§2.1/4.3: GRE and QFI conversion | IMPLEMENTED | C6 keyed GRE and GTP-U PSC builders/parsers; QFI bits 24–29 | T2 exact bytes, IPv4/IPv6 inner codec cases | KEEP |
| §§3.2/4.2: multiple configured QFIs | IMPLEMENTED_BUT_NOT_VALIDATED | C3 validates 1–63 distinct QFIs; C5 64-bit bitmap membership | T1/T2; no retained live multiple-QFI run | KEEP BUT NARROW CLAIM to component support |
| §§4.2/5.5: session and Child-SA state | IMPLEMENTED | C5 stable entries, UE/PDU identities, TEID/SPI indexes, slot generations | T3 passed current session/SA tests | KEEP |
| §4.4: mbuf reuse and reduced payload copying | IMPLEMENTED | C6 `rte_pktmbuf_adj/prepend`; C4 crypto uses same mbuf and copies/restores Ethernet header | T2 payload preservation | KEEP BUT NARROW CLAIM to in-place processing; avoid blanket zero-copy |
| Implied allocation-free packet handling | PARTIALLY_IMPLEMENTED | C4 `runtime_sa` lazily calls `init_direction`, including `rte_zmalloc` and crypto-session creation | Direct call trace; steady-state runtime cache exists | NEEDS WORDING CORRECTION: cached steady state, initialization on first SA use |
| §4.4: historical 4,096-SA scan caused throughput regression | IMPLEMENTED_BUT_NOT_VALIDATED | C5 current code removes old lookup; old behavior is described in E5 | Historical profile/throughput are secondary reports; raw perf unreadable | NEEDS EXPERIMENT for quantitative ablation; qualitative motivation can remain |
| §§5.1/7.1: bidirectional ICMP connectivity | IMPLEMENTED | C4/C6 transparent inner payload forwarding | T2 ICMP-shaped boundaries; E2 report/counters, remote ping logs not locally inspected | KEEP BUT NARROW CLAIM and retrieve raw artifacts for publication |
| §§5.1/7.1: bidirectional TCP connectivity | IMPLEMENTED | C4/C6 packet path | E1 actual endpoint logs/pcaps; E2 retained report | KEEP; E1 is a path demonstration, not sustained-throughput acceptance |
| §§5.1/7.1: bidirectional production UDP connectivity | IMPLEMENTED_BUT_NOT_VALIDATED | C4/C6 carry UDP PDU traffic; clear generators under `onvm_test/n3iwf_clear_*.py` | E5 reports clear UDP echo and secure UDP UL; no retained secure UDP DL dataset | NEEDS EXPERIMENT; distinguish clear UDP test from production ESP |
| §§5.1/5.5/7.5: two simultaneous UEs and isolation | IMPLEMENTED_BUT_NOT_VALIDATED | C5 identity/TEID/SPI binding; C6 preserves selected session | T2/T3; E5 reports successful live two-UE test, but corresponding raw pair of isolation pcaps absent locally | KEEP BUT NARROW CLAIM: component-tested and operator-reported live result; archive evidence |
| §§4.2/5.5: multiple PDU sessions including same QFI | IMPLEMENTED_BUT_NOT_VALIDATED | C5 SA→session lifetime binding, unique DL TEID; C6 authenticated-session entry | T2 same-UE/same-QFI tests; live two-session history only E5 | NEEDS EXPERIMENT for a publication-grade live multi-session claim |
| §§5.5/7.5: scalability beyond two UEs/sessions | DOCUMENTATION_ONLY | C5 fixed-capacity structures provide capacity, not throughput/fairness proof | E4 planned matrix; no larger live scale results found | MOVE TO FUTURE WORK; call present test two-UE concurrency |
| §§5.3/6/7.3: separate-host Linux baseline deployment/comparison | DOCUMENTATION_ONLY | C1/C12 provide baseline software, not a performed experiment | No retained Linux run or hardware-matched pair found | NEEDS EXPERIMENT; host placement must not confound comparison |
| Contributions 4, §§5.3/7.3/10: significant throughput improvement | DOCUMENTATION_ONLY | Forwarding implementation is not a speedup measurement | E5 DPDK-only checkpoints; no Linux denominator | NEEDS EXPERIMENT; omit achieved-improvement language |
| §5.3: TCP/UDP and packet-size throughput evaluation | DOCUMENTATION_ONLY | E4 TCP runner exists; UDP sizes/rates are manual procedure | No retained repeated matrix, size sweep, or DL UDP point found | NEEDS EXPERIMENT |
| §§5.4/7.4: CPU efficiency and utilization results | DOCUMENTATION_ONLY | E4 runner does not measure CPU; README supplies manual `mpstat` command | No paired per-core measurements found | NEEDS EXPERIMENT, not a missing forwarding feature |
| §§5.4/7.4: cycles/packet, packets/s/core, Gbit/s/core | DOCUMENTATION_ONLY | C2 exposes aggregate counters; calculations/profiling require matched intervals and core accounting | No retained derived metrics with valid denominators | NEEDS EXPERIMENT; keep cycles/packet optional |
| Existing `perf.data*` CPU-profile evidence | UNKNOWN | Binary recording files present; contents unreadable under current permissions | Header inspection failed; cannot authenticate E5's 58.2% figure | NEEDS EXPERIMENT or recover attributable recording; do not use as CPU-efficiency result |
| §6: reproducible topology/core/NIC/crypto setup | PARTIALLY_IMPLEMENTED | C13 fixed config; E1 process snapshots show actual core/service settings | Missing complete per-benchmark CPU/NIC/NUMA/queue/cipher inventory; dirty/untracked code | KEEP; NEEDS EXPERIMENT metadata capture |
| §§5.2/7.2: capture/counter kernel-bypass methodology | IMPLEMENTED | E1 analyzer classifies declared SPIs and IP/port flow, checks captures/counters | Retained run reanalysis PASS | KEEP BUT NARROW CLAIM to observation-based proof, not exhaustive formal proof |
| §8: “first” or literature-wide novelty/gap assertions | UNKNOWN | A local implementation cannot establish external precedence | No literature review performed by design | REMOVE priority claims; retain Related Work as work to substantiate separately |
| §9: IPv4-only current scope | PARTIALLY_IMPLEMENTED | C3 live contracts require IPv4; C6 codec supports IPv6 inner protocol/payload; C4 outer tunnel is IPv4 | T2 IPv6 inner component vectors; no live dual stack | NEEDS WORDING CORRECTION: IPv4 transport/session integration; limited inner IPv6 codec support |
| §9: NAT-T user-plane ESP as future work | NOT_IMPLEMENTED | C5 rejects NAT-T flag; C7 UDP/4500 control classification is separate | T3 unsupported profile cases | KEEP as FUTURE WORK |
| §9: Child-SA rekey / overlap as future implementation | IMPLEMENTED_BUT_NOT_VALIDATED | C10 optional ONVM initiator, retransmission, install/retire; C5 overlapping inbound and direct outbound | T4; E5 reports live rekey but raw sequence/soak evidence not retained locally | NEEDS WORDING CORRECTION: existing optional extension; extended validation is future work |
| §9: hardware/inline crypto | NOT_IMPLEMENTED | C4 chooses `RTE_SECURITY_ACTION_TYPE_CPU_CRYPTO` | No hardware/inline path tested or found in N3IWF-DP | KEEP as FUTURE WORK |
| §9: complete removal of TAP/kernel signalling dependencies | NOT_IMPLEMENTED | C1/C3/C7 preserve signalling interfaces and punts | E1 explicitly observes this boundary | KEEP as FUTURE WORK |
| §9: broader Release 18 coverage | DOCUMENTATION_ONLY | C3/C5/C6 support a constrained profile | No conformance/gap matrix or certification evidence | KEEP as FUTURE WORK; do not infer compliance from packet shapes |
| §10: experiments verify bypass | IMPLEMENTED | C4/C7/C8 plus E1 methodology | Retained finite one-session experiment | KEEP with setup and observation limits |
| §10: performance “is compared” and improvement demonstrated | DOCUMENTATION_ONLY | No implementation function can establish this result | No fair paired result | NEEDS WORDING CORRECTION until measurement |

### Actual packet paths and remaining Linux boundary

**Uplink:** physical service mapping sends NWu frames to service 14. In C4, `meta->src == 0` selects physical ingress; `ipv4_esp_spi` extracts the SPI, C5 looks up the Child SA and validates its bound session slot/lifetime/identity, and `cpu_crypto` performs ESP processing. C6 then parses the decrypted IPv4/GRE packet, validates inner NWu source and QFI against that already-selected session, enforces the inner MTU/buffer checks, validates/learns the access MAC, removes NWu/GRE headers, constructs Ethernet/IPv4/UDP/GTP-U/UL PSC, and chooses TONF/service 1. C8 receives conventional N3 packet contents and applies the existing UPF path to N6. Session-slot validation precedes crypto in the current implementation; it is not itself authentication.

**Downlink:** UPF `Encap` emits GTP-U/DL PSC including QER QFI/RQI; configured peer matching selects TONF/service 14. C6 parses the extension chain, requires DL PSC semantics, selects by TEID and validates QFI/neighbor/size, then creates keyed GRE while returning the same session to C4. C5 directly validates that session's active outbound SA slot/generation. C4 encrypts/authenticates, restores Ethernet, computes outer IPv4 checksum, and chooses OUT/access port. `mbuf.port` is not the secure path's direction discriminator after an ONVM handoff.

**Boundary caveat:** C7 attempts the software-IPsec path first, then allows the TAP classifier on `-EPROTONOSUPPORT` or `-ENOENT`. The classifier admits ESP addressed to the local NWu address when `-k` is enabled; it does not carry a list of signalling SPIs. C4 also returns `-ENOENT` when a known SA has no valid bound session. Thus, “only verified signalling SAs can ever reach Linux” is too strong. E1 shows that the tested active PDU SPIs did not reach TAP during its captured interval; it does not establish that property during unknown-SPI injection, deletion, or stale-state conditions. The narrow bypass claim remains supported without representing this classifier as complete isolation under every lifecycle event.

### Statements to narrow or correct

| Original wording or likely overinterpretation | Safer wording and reason |
| --- | --- |
| “Preserve the existing control-plane behavior” | “Reuse the free5GC-derived control protocols and synchronize initial PDU-session/Child-SA state with a new dataplane.” C9 modification is incomplete and NAS/rekey/XFRM handling changed. |
| “UE inner IP” in the state contract | “IKE-assigned inner NWu address; the SMF-assigned PDU address is unspecified in wire v1.” C3 does not provide PDU-source authorization. |
| “O(1) lookup” | “Direct generation-validated SA/session references and open-addressed SPI/TEID indexes, avoiding a full latest-SA scan per packet.” C5 hash probing can examine up to the index size, especially with collisions/tombstones. |
| “Zero-copy/allocation-free fast path” | “In-place mbuf header transformations and cached crypto state.” C4 copies headers and lazily allocates runtime SAs. The source does not justify a whole-pipeline zero-copy or zero-allocation guarantee. |
| “User-space ESP” | “DPDK CPU-crypto processing of the supported IPv4 AES-CBC/HMAC-SHA1 profile.” C4 processes one packet per call, not a crypto burst. |
| “Two-UE scalability” | “Two-UE concurrent forwarding/isolation, with a separately reported checkpoint.” E5 is not a retained scaling curve or validated capacity limit. |
| “Kernel-based N3IWF overhead causes the measured improvement” | “Compare the integrated DPDK path with the Linux-XFRM/Go reference under matched conditions.” C12 includes Go processing; C8 also changes N3 placement. No causal breakdown is measured. |
| “Production-ready lifecycle/security” | “Initial setup, indexed state, profile rejection, and tested component lifecycle operations.” C9 modification, C10 long-soak/collision validation, and sequence/lifetime handling do not support comprehensive hardening claims. |
| “All malformed packets/fragments are handled” | “Implemented parsers reject tested malformed GRE/PSC and outer-fragment cases.” C6 checks only the immediate IPv6 Fragment next-header case; no general IPv6 extension traversal/reassembly or complete hostile-packet audit is demonstrated. |

### Evaluation readiness by research question

#### RQ1 — Functional correctness

The defensible core is initial session programming plus IPv4 bidirectional forwarding. T1/T3/T4 cover contract construction, setup ordering, identity and release helpers. T2 covers actual mbuf translation, QFI/RQI, same-UE session separation and MTU boundaries. E1 contains real bidirectional TCP traffic; E2 retains the physical ICMP/MTU report and local counters. Secure UDP in both directions, live multiple-QFI/RQI parity, and publication-ready two-UE/multi-session artifacts still need collection.

The seven documented N3IWF-DP/UPF component tests are **not seven software-IPsec tests**: [the Meson source lists](../../NFs/onvm-upf/5gc/n3iwf_dp/meson.build) do not link `n3iwf_dp_ipsec.c` into those test executables. SA-table validation and clear-packet conversion tests do not test AES/HMAC output, bad ICV/padding, or replay-window edges through the real CPU-crypto path. E1 supplies positive live ESP evidence; E3's negative ESP/replay scenarios remain a manifest, not completed tests.

E3 is an evidence-recording and comparison harness. `run_command` marks any successful supplied command PASS from its exit code; `record_case` accepts a supplied verdict and checks referenced files; `gate_run` checks completeness/status. It does not independently establish packet semantics or traffic integrity. Its regression test even uses `printf registration-ok` to test bookkeeping. This is useful infrastructure, but neither an automatic conformance oracle nor evidence that registration/parity was exercised.

C9 introduces two important scope limits:

- `handlePDUSessionResourceModifyRequestTransfer` updates QoS/AMBR state and removes Go QoS flows, but does not rebuild/publish `QFIList` or an ONVM `UpsertSession`; the tunnel-modification branch does not actually update the GTP tunnel. `HandlePDUSessionResourceModifyConfirm` likewise has no DP publication. The socket's upsert capability therefore cannot stand in for live PDU modification support.
- Selected PDU release invokes session deletion and associated SA deletions, including overlap SAs. T4 exercises the helper sequence and ID-zero case. Other teardown paths use `deleteRanUeUserPlaneSessions`, which issues session deletions without the same explicit SA list. Do not generalize the helper test to complete UE-release, failure-recovery or leak-free churn behavior. `[CODE REVIEW NEEDED]` before claiming comprehensive teardown; `[EXPERIMENT NEEDED]` for selective release under continuing traffic.

A focused paper can exclude those broader lifecycle claims. It should not describe the full E3 suite as accepted while leaving its modification/release requirements unresolved.

#### RQ2 — Kernel bypass

E1 is the strongest retained experimental artifact:

| Observation | Retained result |
| --- | --- |
| Physical UE-side NWu ESP matching declared user SPIs | 142 uplink; 302 downlink |
| N3IWF-DP packet counter changes | +142 uplink; +302 downlink |
| Identified DN TCP payload counted by analyzer | 110,971 bytes UL; 328,379 bytes DL |
| User SPIs / identified user flow on TAP | 0 / 0 |
| Identified user flow on signalling XFRM | 0 |
| Logical N3 UDP/2152 in kernel capture | 0 |
| Logical N3 addresses assigned to Linux | Neither address |
| All six capture drop summaries | Zero reported kernel/interface drops |
| Session and Child-SA counts | 1 → 1 each |
| Allowed TAP control observations | Four IKE UDP packets and twelve ESP packets with other SPIs |
| Host mlx5 access capture | Empty; UE-side physical capture is authoritative |

This establishes observation-based kernel bypass for that declared flow and active SPI pair in that interval. Code/configuration and matching traffic/counters support the direct logical N3 handoff; there is no packet capture of the ONVM ring itself. The result cannot prove all protocols, UEs, rekeys, lifecycle transitions, security failures, or future deployments avoid the kernel.

Further precision for writing the methodology:

- `flow_direction` recognizes IP/protocol/port tuples; it does not validate an application payload marker or end-to-end payload hash. The 64 KiB floor rejects ACK-only evidence, but payload-byte totals are not a unique-byte/TCP-reassembly or application-integrity proof.
- The retained counts happen to match ESP and DP progress exactly. The generic analyzer gate requires positive progress on each side, not exact equality. Describe the equality as an observation of this run, not a property enforced by every gate invocation.
- `raw/traffic-dn.log` records server interruption, asymmetric sender/receiver totals, and a final 6.55 Gbit/s interval rounded to `10.05–10.05` seconds. That line is **not** a sustained-throughput result. The artifact remains useful for showing actual bidirectional payload/path observations; obtain clean endpoint completion for a polished functional-results table.
- The analyzer checks a selected set of error counters; its zero-error gate does not include the later `oversize_drops`, `buffer_drops`, or `stale_updates` fields. E2 covers MTU separately. Do not say this analyzer enforces every current DP counter.

#### RQ3 — Throughput

The following inventory covers project-relevant measurements found locally or reported in the project records. “Not recorded” means no attributable value was found for that measurement; current configuration is not retroactively substituted for a missing run manifest. No Linux throughput measurement was found in the project result directories.

Topology shorthand: **P** = UE/N3IWUE Linux XFRM → physical NWu → same-host N3IWF-DP/ONVM-UPF logical N3 → physical N6 → DN. Current/retained deployment settings use mlx5 PCI `09:00.0` for NWu, `08:00.0` for N6, manager lcores 0–2, UPF-U lcore 3, N3IWF-DP lcore 14. E1 process snapshots verify those settings **for E1**. Exact NIC model/link speed, CPU model/SMT/NUMA and negotiated AES key size are not a complete retained per-benchmark inventory.

| Measurement and evidence quality | Topology; packet size/protocol | Cipher; cores/NIC attribution | Duration/direction | Reported result | Publication use |
| --- | --- | --- | --- | --- | --- |
| E5 initial paced checkpoint, reported 2026-08-17 | P; DN `1.1.1.1`; TCP MSS 1100, `--fq-rate 100M` | Reported software ESP profile; exact negotiated key size and trial core/NIC manifest not retained | 15 s UL, one stream | 100 Mbit/s sender, 99.7 receiver, 0 retransmissions | Historical correctness checkpoint only |
| E5 initial unpaced checkpoint | P; DN `1.1.1.1`; TCP, exact MSS/packet distribution not recorded for these commands | Same reported profile; no raw trial metadata | 15 s each, UL and reverse DL separately | ~1.21 Gbit/s UL (0 retransmissions); ~1.19 DL (1) | Provisional DPDK-only figures; retrieve originals before plotting |
| E5 old scan regression | P reported; TCP reverse; packet size and trial duration not recorded | Old 4,096-SA lookup; profile reportedly 58.2% CPU; raw `perf.data*` not inspectable | DL; repeat timing not retained | ~504, 481, 478 Mbit/s | Design motivation; quantitative causal ablation needs reproducible old/new builds |
| E5 optional rekey UL | P reported; TCP, packet size not retained | Software ESP; rekey enabled; exact trial CPU/NIC manifest missing | 60 s UL | Reported intervals ~1.17–1.47 Gbit/s, 0 retransmissions | Optional regression report, not primary baseline |
| E5 optional rekey DL, 2026-09-01 | P; DN `192.168.3.2`; TCP, size not retained | Software ESP; 30 s lifetime/10 s overlap; exact key size/core manifest missing | 60 s reverse | 8.06 GBytes, 1.15 Gbit/s, 4 retransmissions; intervals ~1.14–1.16 | Reported continuity checkpoint; no retained complete rekey experiment found |
| E5 accepted two-UE bidirectional report, 2026-09-04 | P; two UE PDU addresses; TCP `--bidir`; size not retained | Software ESP, replay window 4096; core/NIC settings described but no trial manifest | Concurrent four directions; procedure specifies ≥60 s, actual raw interval absent | UE1 559/535 Mbit/s UL/DL; UE2 173/170; ~1.437 Gbit/s aggregate, zero retransmissions/errors reported | Concurrent support is plausible and component-backed; raw isolation/load artifacts needed |
| E5 provisional two-UE TCP, 2026-09-06 | P reported; TCP; packet size not retained | Reported DP 14/UPF-U 3; mlx5 setup; complete trial identity/cipher/CPU record absent | 120 s per direction, concurrent two UEs | UL 822+832 = 1.654 Gbit/s; DL 589+592 = 1.181; retransmissions 61/7/96/0 | Checkpoint only; no retained JSON/counter pair |
| E5 provisional two-UE UDP UL, 2026-09-06 | P reported; UDP; exact datagram length/duration not established by retained raw output | Software profile reported; no complete trial manifest | Concurrent UL, 600 Mbit/s offered per UE | 4,393/14,999,867 lost ≈0.029%; 1.2 Gbit/s aggregate offered | Single reported loss point; no DL counterpart or counters; not accepted comparative performance |
| E1 retained bypass traffic | P; TCP port 5503; varied packet sizes, no fixed-size benchmark | AESNI-MB vdev and DP 14/UPF-U 3/manager 0–2 retained; exact cipher key size not recorded | ~10 s bidirectional traffic within capture window | UL sender 88.2 Kbit/s; DL sender summary 298 Kbit/s, receiver ~88.2 Kbit/s; interrupted server | Path proof only; do not promote tail rate to throughput |
| E2 MTU/TCP physical report | P; inner ICMP 1409/1410/1411; TCP MSS 1370, paced 20 Mbit/s | Reported current software profile; physical ESP lengths retained in report, large pcaps remote | Separate UL/DL; procedure says 15 s, raw timing not local | TCP UL 20.2 sender/19.9 receiver; DL 20.1/20.1 Mbit/s; 0 retransmissions | MTU acceptance evidence, not throughput ceiling |
| Linux or upstream free5GC comparison | No retained deployment run | No matched inventory | No measured interval | No result found | `[EXPERIMENT NEEDED]` |

**No existing pair is publication-quality evidence of a throughput improvement.** Different checkpoints also cannot be summed or compared as if they share packet sizes, direction concurrency, rekey settings or core budgets. The ≥2× throughput objective at ≤0.1% loss and no p99 regression remains an objective, not an achieved result. It should not force selective reporting if the measured improvement is smaller.

For the central comparison, use matched CPU/NIC/link/NUMA resources, the same crypto suite/key size, UE/DN setup, MTU/MSS, offered load, session/QFI count, stream count and duration. Retain repetitions, receiver loss, retransmissions, counter intervals and loaded latency. A separate-host baseline is acceptable only with a documented comparable platform and topology; changing physical N3 transport to an in-host ring changes more than the N3IWF implementation. Either control those differences or explicitly report an **integrated-system comparison** rather than attributing the entire speedup to ESP or kernel bypass alone.

E4 provides concurrent TCP clients with `-J`, direction flags and warm-up configuration. It is not a full experiment controller: no common timed-start barrier, no measured UE start-skew check, no integrated UDP/latency/CPU collector, and no automatic packet/counter verdict. Its files can be overwritten when reusing a result directory. Plan immutable trial directories and verify overlapping intervals; avoid claiming synchronized/reproducible benchmarking solely because the script exists.

#### RQ4 — CPU efficiency

Current instrumentation is enough to begin measurement, not to claim a result. C2 exposes cumulative packet/error/state counters; E4 documents manual `mpstat -P ALL` alongside traffic. The executable runner records no per-core CPU sample, cycles, packet rate or efficiency metric itself. No retained matched CPU dataset was found; inaccessible `perf.data*` does not fill this gap.

Count manager RX/TX/dispatch cores as well as N3IWF-DP, UPF-U, Linux softirq and relevant worker time. DPDK polling utilization is not directly comparable to kernel idle percentage. Report useful throughput at an explicitly equal CPU allocation, with per-core measurements to explain saturation. Packets/s/core and cycles/packet require a declared packet-counting boundary and matched timing; UL/DL aggregate NF counters may otherwise double-count different stages. A throughput plateau near 1.18–1.19 Gbit/s downlink is not evidence identifying crypto, UPF shaping, the DN or UE-XFRM as the bottleneck.

#### RQ5 — Multi-UE/session scalability

- **Largest reported live scope:** two concurrent UEs with one PDU session each; E5 also reports a same-UE/two-PDU-session development case. Neither establishes more than two simultaneous live PDU sessions. Optional rekey can increase SA count without increasing session count.
- **Locally reanalyzable live scope:** E1 demonstrates one active session/Child SA. E2 also reports one. The two-UE isolation pcaps named in E5 are not in the retained local result directories reviewed here.
- **Structural scope:** C5 has 4,096 session slots, 4,096 bidirectional Child-SA-pair slots and 8,192-entry indexes. These are capacity constants, not an experimentally established UE maximum. Overlap consumes additional SA slots; collision/tombstone behavior, churn, UE-side limits and CPU saturation constrain usable scale.

Keep a small two-UE concurrency/isolation experiment. If the paper uses “scalability,” add at least a controlled one-versus-two matrix with repeated trials, per-UE results and fairness; larger curves are optional and should not be promised without a feasible testbed. State-table size alone is not a scaling result.

## C. Missing Paper-Worthy Implementation Details

These additions explain the existing architecture. They are not recommendations to implement every absent feature.

| Candidate and status | Executable evidence and technical significance | Recommended paper coverage |
| --- | --- | --- |
| Full-packet logical N3 — IMPLEMENTED | C6/C8 exchange Ethernet/IPv4/UDP/GTP-U/PSC and choose TONF routes. TEID/QFI remain in packet bytes; ONVM metadata selects service/direction. Internal Ethernet framing is synthetic, not a physical-N3 neighbor-resolution claim. | **Design**, main subsection/figure; distinguish physical NWu/N6 from logical N3. Explain preserved packet semantics without claiming standards certification. |
| State ownership and local control protocol — IMPLEMENTED | C1–C3 separate control decisions from forwarding; C2 uses network-order SEQPACKET messages, transactions, single writer/observers, peer UID checks and ACK/status. C8 still receives existing UPF rules; no N4 endpoint is added to N3IWF-DP. | **Design**, main subsection. Explain ownership and updates, not every wire offset. |
| Setup ordering and generation lifetimes — IMPLEMENTED | C3 installs SA before session; C2 binds existing SA when session arrives. C5 separates installation generation, command watermark and session-slot lifetime. Deleting active SA clears selection; recycling invalidates old bindings. | **Design**, short state-transition diagram; **Evaluation**, T3 stale-reference/overlap cases. Do not imply durable tombstones, transactional restart recovery or exactly-once retries: deletion removes state, and C2 ACK does not prove lazy crypto initialization succeeded. |
| Preserving authenticated session identity — IMPLEMENTED | C4/C5 use SPI→SA→bound session; C6 carries DL TEID-selected session into encryption. This avoids ambiguity when one UE reuses NWu address/QFI across PDU sessions. | **Design**, main packet-path explanation; **Evaluation**, same-QFI/multiple-session component and retained live isolation tests. Stronger than a generic “hash lookup” paragraph. |
| Access-MAC learning — IMPLEMENTED | C5 `n3iwf_dp_session_learn_access_mac`, C6 valid-uplink learning, C7 actual local DPDK port MAC. DL fails until a session neighbor is known. The source MAC is observed on a frame whose ESP payload passed crypto; Ethernet itself is not cryptographically authenticated. | **Implementation**, short paragraph; **Evaluation**, independent neighbor state for two UEs. |
| Explicit modes/profile rejection — IMPLEMENTED | C7 mutually exclusive clear `-t` and secure `-e`, default DROP; C5 unsupported-profile rejection; C4 crypto capability checks. | **Implementation**, one profile table; **Discussion**, supported subset and temporary ESP punt caveat. Clear-mode results must be labelled. |
| MTU and buffer discipline — IMPLEMENTED | C11 derives inner limit 1410: NWu outer IPv4 1496 at 1410 and 1512 at 1411; C6 enforces before GRE/GTP conversion/MAC learning, with distinct oversize/buffer/fragment counters. ESP decrypt necessarily occurs before the UL inner-size check. | **Design**, compact policy paragraph; **Evaluation**, T2/E2 boundaries. Preserve inner DF; endpoint PMTU configuration required, no NF-originated PTB/fragmentation/reassembly. |
| RQI independent of QFI — IMPLEMENTED_BUT_NOT_VALIDATED | C8 encodes QER RQI; C6 preserves DL PSC RQI as GRE mask `0x80`, QFI as bits 24–29; direction-invalid/reserved encodings rejected. | **Implementation**, short semantics paragraph; **Evaluation**, T2 exact-byte tests. Live RQI/parity remains excluded or `[EXPERIMENT NEEDED]`, not a reason to expand the core paper. |
| In-place packet handling and software crypto integration — IMPLEMENTED | C4 CPU-crypto on one mbuf, Ethernet restoration and IPv4 checksum; C6 header adjust/prepend. C13 primary process creates AESNI-MB vdev; C4 sets up a queue pair and caches runtime SAs. | **Implementation**, concise paragraph. No blanket zero-copy/allocation-free claim; measure warm versus first-SA-use effects only if material. |
| Replay policy — IMPLEMENTED_BUT_NOT_VALIDATED | C3 passes a 4096-packet replay window; C4 uses DPDK replay processing and classifies inbound prepare `EINVAL` as replay drops. E5's reordering fix motivates the setting; no retained actual replay-injection suite. | **Implementation**, profile detail; **Evaluation**, bounded negative replay test if making explicit anti-replay correctness claims. Do not frame a wider window as disabling replay protection. |
| ESP sequence/lifetime policy — PARTIALLY_IMPLEMENTED | C4 passes initial outbound sequence into DPDK. Vendored [ipsec_sqn.h](../../NFs/onvm-upf/subprojects/dpdk/lib/ipsec/ipsec_sqn.h) `esn_outb_update_sqn` limits usable packets on overflow, so sequence protection is not wholly absent. C4 does not consume wire soft/hard lifetime seconds to enforce a full lifetime policy. | **Discussion**, scope limit. `[CODE REVIEW NEEDED]` and edge tests before promising exhaustive exhaustion/hard-lifetime behavior; do not claim observed sequence wrapping. |
| Observability — IMPLEMENTED | C2 `send_stats`/`parseStats`, [dpctl](../../NFs/n3iwf-dp-client/cmd/n3iwf-dpctl/main.go) `run`, C4 capped non-key diagnostics. Aggregate packet/error/state and neighbor counters support E1/E2 correlation. | **Implementation**, short paragraph; **Evaluation**, show counter deltas. No per-QFI telemetry or latency histogram claim. |
| NAS ordering/XFRM restart fixes — IMPLEMENTED | C9 `serveConnWithForwarder` serializes each UE's NAS frames; C12 replacement helpers delete/add stale XFRM state/policy on collision; T4 covers both. | **Implementation**, at most a brief integration lesson. Detailed NAS/XFRM debugging is not a main contribution. |
| Selective cleanup — IMPLEMENTED_BUT_NOT_VALIDATED | C9 selected release deletes session and associated SA pairs; T4 checks overlap and exclusion of other sessions. End-to-end release during traffic remains unvalidated locally and other teardown paths differ. | **Evaluation** only if retained basic-release evidence is collected; **Discussion** for extensive churn/recovery. Do not advertise production-ready lifecycle. |
| Optional rekey — IMPLEMENTED_BUT_NOT_VALIDATED | C10 initiates replacement, retransmits stored packets, retires old SA after response; C5 keeps overlapping inbound SAs and one direct outbound selection. T4 tests selected scenarios; E5 reports live continuity. | **Discussion**, short existing-extension note; detailed collision/delete validation and multi-UE soak remain outside the main paper. |
| Single-lcore state publication — IMPLEMENTED | C7 runs control polling and packet callback in the NF loop; C13 pins one N3IWF-DP lcore. Direct state references rely on this serialization. C4 requests DPDK atomic sequence updates, so “no atomics anywhere” is false. | **Design/Discussion**, explicit concurrency assumption. State “no added global packet-path mutex,” not universally lock-free software. |
| Crypto batching — NOT_IMPLEMENTED | C4 `cpu_crypto` passes an array of length one to prepare/process. An IPSec-MB backend name does not establish batching across N3IWF packets. | **Discussion**, `[FUTURE WORK]` after measurement; no current Design contribution. |
| N3IWF multicore sharding, per-core SA tables, RSS-aware ownership — NOT_IMPLEMENTED | C7 has one NF state instance; C13 one configured DP lcore. No N3IWF sharding/publication scheme found in C4/C5/C7. Generic ONVM/DPDK capabilities do not supply this design. | **Discussion**, `[FUTURE WORK]`; no current scaling claim. |
| Per-QFI counters — NOT_IMPLEMENTED | C2 stats structure and C5 session state provide aggregate/bitmap state, not per-QFI packet/byte histograms. | No extra paper subsection. Optional **Discussion** item if QoS evaluation needs it. |
| Bounded parsing/security hardening — PARTIALLY_IMPLEMENTED | C6 checks GRE flags/QFI/RQI and GTP extension lengths/duplicates; C7 validates punt lengths/fragments. C6 `parse_l3` does not validate every IP total-length/checksum invariant, and the secure path lacks a project-level hostile ESP vector suite. | **Implementation**, name actual checks; **Discussion**, avoid a comprehensive parser/security correctness claim. |
| UE PDU-source anti-spoof authorization — NOT_IMPLEMENTED | C3 provides unspecified PDU IP; C6 validates NWu address/QFI, not an SMF-authorized inner source. Session isolation and source authorization are separate. | **Discussion**, one clear limitation. Do not add a new control authority solely to enlarge paper scope. |

### Implementation-grounded contribution story

The primary positioning is justified as an **implemented kernel-bypass N3IWF user-plane architecture**, supported by C1–C8 and E1. The DPDK NWu-to-N3 dataplane is its mechanism. The particularly useful design explanation is preserving UE/PDU identity across security and tunnel conversion while maintaining complete logical N3 packet semantics; the particularly useful experimental contribution is the explicit accounting for permitted signalling versus forbidden PDU traffic in a bifurcated-PMD deployment.

Performance improvement is a plausible **secondary research question**, currently unsupported as a result. Neither DPDK use, in-place mbufs, hash tables nor same-host rings establish external novelty or speedup by themselves. Present them as choices in this system. Keep the main story focused; broader standards coverage, optional rekey, and unmeasured scaling should not displace the architecture and its evidence.

## D. Proposed Revised Paper Outline

The structure below combines the original Evaluation/Experimental Setup/Results material to reduce repetition for a workshop paper. Each subsection states what to write, not invented final results.

### 1. Introduction

- Motivate the cost of the Linux-XFRM/Go user-plane path and the goal of retaining the existing control protocols while replacing packet forwarding (C1/C12); treat performance causation as a hypothesis.
- State contributions: CP/DP architecture, identity-preserving ESP/GRE/GTP dataplane, and capture/counter verification of bypass (C1–C8/E1).
- `[EXPERIMENT NEEDED]` Add a measured performance contribution only after a fair paired comparison; no achieved speedup in the abstract yet.

### 2. Background and Design Goals

#### 2.1. Non-3GPP user-plane packet path and reference implementation

- Explain NWu ESP protecting inner NWu IP/GRE/QFI/PDU, N3 GTP-U/PSC, and the UPF boundary (C3/C6/C8).
- Show the Linux reference as XFRM plus Go raw GRE/GTP socket processing (C12), including where kernel/userspace transitions occur.

#### 2.2. Scope and invariants

- State the supported IPv4 software-ESP profile, complete logical N3 packets, and Go/DP/UPF ownership split (C1–C5/C8).
- Limit bypass to N3IWF-host PDU-session traffic; identify TAP/Linux signalling and the single-lcore state model (C7/C13).

### 3. System Design

#### 3.1. Physical and logical architecture

- Draw UE → physical NWu → N3IWF-DP/service 14 → logical N3 ring → UPF-U/service 1 → physical N6 → DN, with a separate Go/TAP/N2 control branch (C7/C8/C13).
- Explain why TEID/QFI/RQI remain in N3 packet bytes and why N3IWF-DP does not become an N4 endpoint (C2/C6/C8).

#### 3.2. State ownership and CP/DP synchronization

- Explain SEQPACKET, writer/observer roles, UID checks, transactions, ACKs and generation-tagged session/SA commands (C2/C3).
- Show initial SA-before-session binding and stable references; distinguish command/install generations from session-slot lifetimes (C2/C5).
- Limit lifecycle claims to demonstrated setup/updates and helper-level deletion; `[CODE REVIEW NEEDED]` for broad NGAP modification/teardown claims (C9).

#### 3.3. Uplink identity-preserving fast path

- Describe physical ingress classification, SPI-indexed SA, validated bound session, ESP crypto and authenticated GRE/QFI validation (C4–C7).
- Explain per-session neighbor learning, in-place GTP-U/UL PSC construction and service handoff (C5/C6).
- Contrast secure session identity with clear-test NWu/QFI ambiguity only as needed to explain the production binding.

#### 3.4. Downlink fast path and state lookup

- Describe TEID/QFI session lookup, DL RQI preservation, learned neighbor use and GRE construction (C5/C6/C8).
- Carry the selected session directly into outbound SA slot/generation validation and ESP output (C4/C5).
- Explain why a full latest-SA scan is confined to control updates; qualify hash lookup complexity and lazy runtime initialization.

#### 3.5. Kernel and packet-size boundaries

- Explain the temporary TAP/XFRM signalling path and the limits of the `-k` classifier; state exactly what bypass observation establishes (C7/E1).
- Present the 1410-byte inner boundary for the 1500-byte IPv4 AES-CBC profile, endpoint PMTU policy and explicit oversize/buffer/fragment drops (C6/C11).

### 4. Implementation

#### 4.1. Software and crypto integration

- Identify Go N3IWF, C ONVM NF, DPDK CPU-crypto/AESNI-MB and same-host UPF integration; publish exact implementation revisions/artifact hashes (C1–C4/C13).
- Summarize in-place mbuf changes, Ethernet restoration/checksum calculation, one-packet crypto calls and cached runtime SAs (C4/C6).

#### 4.2. Correctness and observability mechanisms

- Summarize profile rejection, directional QFI/RQI checks, replay configuration, identity generations and aggregate counters with references to representative tests (C2/C5/C6; T1–T3).
- Briefly mention NAS ordering and signalling-XFRM replacement only as integration changes necessary for the tested deployment (C9/C12; T4).

### 5. Evaluation

#### 5.1. Experimental setup and comparison contract

- Record CPU/SMT/NUMA, exact NIC/link/queues, all relevant lcores/IRQ affinity, software and cipher/key-size versions, MTU/MSS, topology, session/QFI counts and rekey-disabled configuration (C13/E4).
- `[EXPERIMENT NEEDED]` Establish a fair Linux-XFRM/Go comparison, retaining trial commands/configuration/raw outputs and explaining any physical-versus-logical N3 topology difference.
- Define measured intervals, repeated trials, receiver loss and latency methodology; count manager and UPF resource budgets, not just N3IWF-DP's one lcore.

#### 5.2. RQ1: Functional correctness within the supported profile

- Present separate component and live tables: T1–T4 for contracts/identity/codec/MTU; E1/E2 for observed traffic and MTU acceptance. Clearly label remote or operator-reported evidence.
- `[EXPERIMENT NEEDED]` Retain clean bidirectional ICMP/TCP/secure-UDP and two-UE isolation artifacts; add basic release if claiming lifecycle acceptance.
- Keep live RQI/multiple-QFI and full backend parity explicitly unaccepted unless measured; do not silently convert E3 manifest entries to PASS.

#### 5.3. RQ2: Does the PDU-session user plane bypass Linux?

- Describe E1 collectors, declared SPI/flow correlation, capture-drop checks and counter snapshots, including why the peer NWu capture is authoritative for mlx5.
- Present the retained proof table and separately account for allowed IKE/signalling ESP; distinguish aggregate payload observations from application-integrity testing.
- `[EXPERIMENT NEEDED]` For final publication, retain a clean-completion repeat on the final measured build; do not broaden to untested lifecycle or malicious-traffic cases.

#### 5.4. RQ3: Throughput, loss and latency versus Linux

- `[EXPERIMENT NEEDED]` Run repeated UL, DL and simultaneous bidirectional TCP/UDP trials at controlled packet sizes/loads; report receiver throughput/loss and TCP retransmissions.
- `[EXPERIMENT NEEDED]` Compare maximum throughput meeting ≤0.1% loss and report loaded p50/p99 at matched loads, including the Linux baseline rate.
- Report all repetitions/variation and any failure to meet the ≥2× objective; historical E5 checkpoints can guide test settings but cannot replace this result.

#### 5.5. RQ4: CPU budget and efficiency

- `[EXPERIMENT NEEDED]` Record per-core activity across manager, N3IWF-DP, UPF, Linux workers/softirq and endpoint limits; compare throughput under equal allocations.
- Report throughput/core with a stated denominator; add packets/s/core or cycles/packet only when counters and measured intervals support them.

#### 5.6. RQ5: Two-UE concurrency and fairness

- `[EXPERIMENT NEEDED]` Archive the reported two-UE identities/isolation evidence or repeat it; keep one-UE/two-session/same-QFI component results distinct.
- `[EXPERIMENT NEEDED]` Measure repeated one-versus-two traffic, aggregate/per-UE rates and fairness under fixed CPU allocation (E4).
- `[FUTURE WORK]` Larger UE/session scaling and lifecycle churn; do not infer scale from 4,096-entry capacity.

### 6. Related Work

- Compare N3IWF implementations, relevant DPDK UPFs and kernel-bypass/NFV frameworks in a later sourced literature review; repository evidence alone cannot establish precedence.
- Position the contribution around this implementation's CP/DP boundary, identity-preserving conversion and validation methodology, avoiding “first” claims.

### 7. Discussion and Limitations

- State transport/security subset, single-lcore/one-packet crypto, incomplete modification/extended teardown, unspecified PDU-source authorization, endpoint MTU dependency and temporary signalling boundary (C3–C11).
- Existing optional rekey (C10) is outside the main evaluation; `[FUTURE WORK]` repeated/multi-UE rekey, collision/delete validation and failure recovery. `[CODE REVIEW NEEDED]` for exhaustive lifetime/sequence-security claims.
- `[FUTURE WORK]` NAT-T user plane, IPv6 transport/dual stack, hardware/inline crypto, sharding and broader conformance. Add only limitations material to the paper's claims.

### 8. Conclusion

- Summarize the implemented CP/DP split and evidence for PDU-session bypass within the tested profile (C1–C8/E1).
- `[EXPERIMENT NEEDED]` State the actual measured comparison once available; do not conclude speedup or efficiency from architecture alone.

### Priorities

#### Must fix before writing

1. Correct kernel-boundary, actual lookup order, inner-NWu versus PDU address, IPv4/profile, and existing-rekey wording. Keep quantitative improvement conditional.
2. Remove broad control-plane parity, complete lifecycle, strict O(1), universally zero-copy/allocation-free and large-scale scalability claims. C9 modification is a concrete capability gap; narrowing the paper is sufficient without implementing it now.
3. Build an evidence inventory that distinguishes retained artifacts, remote artifacts, reported history and planned experiments. Identify the exact dirty/untracked implementation rather than citing parent revisions alone.

#### Must measure before submission

1. A repeated fair Linux-versus-DPDK throughput/loss comparison under the declared CPU budget, plus loaded p50/p99 latency and CPU accounting. This is required for the proposed performance contribution; report the result even if it misses 2×.
2. Clean supported-profile bidirectional ICMP/TCP/secure-UDP and retained two-UE isolation if those remain paper claims. Retrieve E2's remote MTU evidence and replace missing two-UE artifacts with attributable raw data.
3. Repeat the bypass observation on the final measured build with clean endpoint completion and preserved commands/configuration/capture-drop summaries; E1 already supplies a working method and positive retained example.

#### Nice to have

1. A reproducible old-scan versus direct-SA ablation with identical builds/workloads, if the scan-regression example remains quantitative.
2. One-versus-two fairness and saturation analysis, basic release evidence, and targeted negative ESP/replay cases proportional to the security/correctness claims actually retained.
3. Live multiple-QFI/RQI parity or broader backend-conformance results, if feasible without displacing the central architecture/performance evaluation. Until then, label component-only support.

#### Keep as future work

- NAT-T data processing, IPv6 transport/dual stack, hardware/inline crypto, multicore sharding and larger-scale validation.
- Extended session churn/recovery, comprehensive lifetime and optional-rekey hardening, and complete removal of the temporary signalling boundary.
- Full Release 18 coverage and per-QFI telemetry; neither is necessary to justify the current focused architecture contribution.
