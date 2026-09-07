# High-Performance DPDK N3IWF

This folder is the persistent engineering context for the L25GC+ high-performance
N3IWF project. It deliberately does not describe the rest of L25GC+.

Use the documents as follows:

- `OVERVIEW.md`: stable purpose, boundaries, and target architecture.
- `CURRENT_STATE.md`: evidence-backed implementation and verification status.
- `TODO.md`: prioritized remaining work.
- `ARCHITECTURE.md`: packet paths, component responsibilities, and state model.
- `DECISIONS.md`: choices that should not be reversed accidentally.
- `MTU_POLICY.md`: normative packet-size, PMTU, and fragmentation behavior.

## Motivation

The upstream free5GC N3IWF implements IKEv2, EAP-5G, NAS relay, NGAP, IPsec,
GRE/QFI, and GTP-U using Go services and Linux networking/XFRM. That is useful
as a functional reference, but its high-throughput user plane crosses the
kernel and cannot hand packets directly to L25GC+'s ONVM/DPDK UPF.

L25GC+ needs an N3IWF that preserves standards-compatible non-3GPP access while
keeping the high-rate UE user plane in DPDK from the NWu NIC through ONVM-UPF.

## Goal

Retain the existing N3IWF control-plane behavior in Go and implement the N3IWF
user-plane datapath as an ONVM/DPDK NF. The resulting system should remove the
kernel from the high-throughput user-plane path while preserving standard 3GPP
packet formats on NWu and N3.

The design is governed by these constraints:

- Correctness and interoperability precede optimization.
- N3 carries ordinary GTP-U packets with a PDU Session Container; ONVM-private
  metadata must not replace TEID or QFI on the logical N3 link.
- The production NWu user plane is ESP-protected. Clear GRE exists only as an
  explicit test mode.
- No per-packet heap allocation, global lock, full-table scan, or other O(N)
  lookup belongs in a fast path when direct or indexed state is possible.
- Unsupported security profiles fail closed; there is no silent downgrade.
- N3IWF-DP is not an N4 endpoint. SMF continues to control UPF through N4.

## Scope

This project includes:

- The pinned free5GC-derived N3IWF control plane under `NFs/n3iwf`.
- The Go-to-dataplane contract under `NFs/n3iwf-dp-client`.
- The ONVM/DPDK NF under `NFs/onvm-upf/5gc/n3iwf_dp`.
- The small ONVM-UPF changes needed for a logical N3 service hop.
- GRE/QFI to GTP-U/PDU Session Container conversion in both directions.
- IPv4 ESP tunnel processing, initial Child-SA synchronization, and anti-replay
  for the initial software-crypto profile.
- Access-side control-packet punt/return, session-aware MAC learning, tests,
  live acceptance checks, and performance measurement.

## Non-goals

- Reimplementing IKEv2, EAP-5G, NAS, NGAP, certificate handling, or UE/PDU
  procedure state machines in C.
- Making N3IWF-DP an AMF, SMF, PFCP/N4, or general routing component.
- Documenting or redesigning unrelated L25GC+ NFs.
- Replacing 3GPP N3 packets with an ONVM-only private tunnel format.
- Treating the current implementation as complete Release 18 compliance.
- Requiring automatic Child-SA rekey for upstream free5GC behavioral parity or
  for completion of the core DPDK CP/DP integration.

## Intended Architecture

The Go N3IWF owns control-plane protocols and lifecycle decisions. It sends
versioned session and Child-SA updates over a Unix `SOCK_SEQPACKET` control
socket. User packets never traverse that socket.

The `n3iwf_dp` ONVM NF owns the production user plane:

```text
Control: UE <-> NWu NIC <-> n3iwf-dp TAP boundary <-> Go N3IWF <-> N2/AMF

User UL: UE -- ESP/GRE+QFI --> n3iwf-dp -- GTP-U+PSC --> ONVM-UPF -- N6 --> DN
User DL: UE <-- ESP/GRE+QFI -- n3iwf-dp <-- GTP-U+PSC -- ONVM-UPF <-- N6 -- DN
```

N3IWF-DP and UPF-U exchange complete, standards-shaped N3 frames through ONVM
rings. The physical ports are NWu/access and N6; N3 is logical on the same
server. See `ARCHITECTURE.md` for state ownership and packet-stage details.

## Performance Objective

The original acceptance objective is at least 2x the aggregate throughput of
the kernel/free5GC N3IWF at no more than 0.1% loss, using identical hardware,
cipher suite, packet mix, session/QFI count, and CPU-core budget. At the
kernel baseline throughput, p99 latency must not regress.

This target has not yet been demonstrated. A useful software-IPsec checkpoint
reached about 1.21 Gbit/s uplink and 1.19 Gbit/s downlink for a single TCP
stream. A later rekey-enabled 60-second reverse run sustained 1.15 Gbit/s
without a rekey-correlated collapse. The equivalent kernel baseline, p99
latency, and controlled CPU comparison are still missing. Performance results
and their qualifications belong in `CURRENT_STATE.md`, not here.

## First-Implementation Support

The first implementation intentionally targets:

- IPv4 NWu and N3 transport.
- IPv4 or IPv6 UE payload indicated by the GRE protocol field where the clear
  codec permits it; the live session/control path is currently IPv4-only.
- QFI values 1 through 63 and multiple QFIs per PDU session.
- Standard GTP-U T-PDU with the PDU Session Container extension header.
- IPv4 ESP tunnel mode without NAT-T or ESN.
- IKE transform 12 (AES-CBC, 128- or 256-bit keys) with transform 2
  (HMAC-SHA1-96) and a non-ESN replay window.
- DPDK software cryptodev, currently exercised with AESNI-MB/IPSec-MB.
- One ONVM packet-processing lcore for the current NF instance.

## Optional Extensions

N3IWF-initiated RFC 7296 Child-SA rekey is implemented and verified with
temporary old/new inbound overlap and direct active outbound-SA selection.
Current upstream free5GC N3IWF does not schedule Child-SA rekey, so L25GC+
disables this extension by default. It is maintained as production-hardening
and regression-test functionality, but it does not gate the core integration.

## Deferred Functionality

Deferred work includes full NAT-T data processing, IPv6 transport/session
contracts, broader Release 18 algorithm/profile coverage, ESN, bounded fragment
reassembly, hardware/inline crypto, multi-lcore sharding with safe publication,
per-QFI telemetry, multi-UE scale validation, and removal of the temporary
kernel/TAP signalling-ESP exception.

## Starting a New Codex Session

A new session should:

1. Read `docs/high-performance-n3iwf/OVERVIEW.md`.
2. Read `docs/high-performance-n3iwf/CURRENT_STATE.md`.
3. Read `docs/high-performance-n3iwf/TODO.md`.
4. Read `docs/high-performance-n3iwf/ARCHITECTURE.md` when architecture is relevant.
5. Read `docs/high-performance-n3iwf/DECISIONS.md` before changing datapath architecture or performance-sensitive code.
6. Read `docs/high-performance-n3iwf/MTU_POLICY.md` for packet-size or fragmentation work.
7. Inspect `git status`.
8. Inspect `git diff`.
9. Review recent commits relevant to the requested task.
10. Read only the relevant source files and tests.

Do not attempt to understand the entire 5GC repository before starting work.

Treat the repository and current code as the source of truth.
