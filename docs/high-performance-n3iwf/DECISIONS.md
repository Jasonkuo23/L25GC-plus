# Engineering Decisions

These decisions capture architecture and performance constraints, not the
chronology of the development session.

## Keep the Control Plane in Go

**Problem / context:** The free5GC N3IWF already implements complex IKEv2,
EAP-5G, NAS, NGAP, certificate, UE, and PDU-session procedures.

**Decision:** Keep those responsibilities in the pinned Go N3IWF. Implement
the high-rate user plane as a separate ONVM/DPDK NF.

**Why:** Reusing mature control behavior limits protocol risk and concentrates
C/DPDK work on packet throughput.

**Rejected alternative:** Porting the complete N3IWF to C/DPDK.

**Consequences:** A precise CP/DP lifecycle contract is required. Control-plane
correctness can evolve independently, but user-plane state must be acknowledged
before procedures report success.

## Preserve Standard N3 Packets Inside ONVM

**Problem / context:** N3IWF-DP and UPF-U run on the same ONVM host, making a
private metadata-only handoff tempting.

**Decision:** Exchange complete GTP-U/PDU Session Container packets over ONVM
rings. TEID and QFI stay in the packet.

**Why:** This preserves 3GPP semantics, keeps UPF behavior conventional, makes
packet tests meaningful, and avoids coupling forwarding correctness to private
metadata.

**Rejected alternative:** Strip GTP-U and represent TEID/QFI only in ONVM
metadata.

**Consequences:** Some header work remains, but the logical N3 boundary is
interoperable and inspectable.

## N3IWF-DP Does Not Own N4

**Problem / context:** The dataplane needs TEIDs and QoS state also known to SMF
and UPF.

**Decision:** Go N3IWF programs only its session/Child-SA view. SMF continues to
program UPF through PFCP/N4.

**Why:** It preserves 5GC ownership boundaries and prevents competing rule
authorities.

**Rejected alternative:** Make N3IWF-DP a second PFCP endpoint or copy UPF rule
management into it.

**Consequences:** End-to-end tests must use live PDU-session setup or otherwise
program both sides consistently.

## Use a Versioned Binary Control Socket

**Problem / context:** Go must publish key-bearing state to a DPDK NF without
putting user packets or text parsing in the datapath.

**Decision:** Use N3DP version 1 over Unix `SOCK_SEQPACKET`, network byte order,
transaction IDs, generations, roles, acknowledgements, and explicit errors.

**Why:** Message boundaries are reliable, local access is restrictable, and the
wire format is deterministic in Go and C tests.

**Rejected alternative:** Send user packets over the socket, use ad-hoc JSON,
or let C scrape Go/kernel state.

**Consequences:** Protocol changes must be versioned. Keys must be erased from
temporary copies, and unsupported versions/profiles fail explicitly.

## Use TAP Only for Low-Rate Control-Packet Handoff

**Problem / context:** A DPDK-owned NWu NIC hides ARP/IKE frames from the Go
process and Linux stack.

**Decision:** Strictly classify and punt ARP/IKE and, during the current
transition, signalling ESP through a TAP. Never send PDU user traffic there.

**Why:** It keeps existing Go IKE/NAS behavior functional while the DPDK user
plane is developed and measured.

**Rejected alternative:** Route all NWu packets through TAP or keep the user
plane in XFRM.

**Consequences:** The demonstrated user plane avoids the kernel, but the whole
N3IWF does not yet. Removing the signalling exception is separate correctness
work, not a cosmetic flag change.

## IPv4 and Software Crypto First, Fail Closed Otherwise

**Problem / context:** Dual-stack, NAT-T, ESN, many algorithms, and hardware
offload multiply the initial security surface.

**Decision:** The first DPDK security slice accepts only IPv4 ESP tunnel mode,
AES-CBC-128/256 plus HMAC-SHA1-96, non-ESN replay, using a capable DPDK software
cryptodev. Reject NAT-T, ESN, IPv6 outer transport, and unsupported transforms.

**Why:** This is the profile negotiated in the tested environment and allows
packet and key-direction correctness to be established before expansion.

**Rejected alternative:** Accept unsupported flags and silently fall back or
downgrade.

**Consequences:** NAT-T and IPv6 fields may exist in the contract but are not a
claim of dataplane support. Hardware/inline acceleration is deferred and must
be capability-driven.

## Clear GRE Is an Explicit Test Mode

**Problem / context:** Debugging GRE/QFI, GTP-U, ONVM routing, and physical MAC
handling simultaneously with IPsec obscures faults.

**Decision:** Provide mutually exclusive `-t` clear mode and `-e` software-IPsec
mode. Default behavior is fail closed.

**Why:** Clear mode enabled deterministic physical round-trip acceptance before
crypto was introduced.

**Rejected alternative:** An implicit fallback from failed ESP to clear GRE.

**Consequences:** Clear mode must remain test-only and cannot be a production
downgrade path.

## Learn the UE Access MAC from Valid Uplink State

**Problem / context:** A DPDK NIC cannot rely on the Linux neighbor table, and a
downlink Ethernet frame with zero MAC addresses is unusable.

**Decision:** Read the local MAC from the configured DPDK port, learn the UE
source MAC per session from a valid uplink, and reject downlink until learned.

**Why:** It binds Ethernet output to live access traffic without a kernel data
path and supports MAC change accounting.

**Rejected alternative:** Zero/hard-coded MACs or consulting Linux ARP for each
packet.

**Consequences:** Learn/change/drop counters are required. In secure mode the
learning packet reaches the clear pipeline only after ESP authentication.

## Optional Rekey Keeps Old and New Inbound SAs but One Active TX SA

**Scope:** This is a default-disabled extension. Current upstream free5GC
N3IWF does not schedule automatic Child-SA rekey, so this decision preserves
the tested extension without making it a core DPDK integration requirement.

**Problem / context:** In-flight UE packets may use the old inbound SPI while a
replacement becomes active. Downlink must select one deterministic outbound SA.

**Decision:** Retain both inbound SPI-indexed SAs for the overlap. Publish the
new SA as the session's active outbound slot immediately after its acknowledged
upsert. Retire the old SA after the authenticated IKE delete exchange.

**Why:** This accepts legitimate in-flight uplink without duplicating downlink
or making packet-time generation decisions.

**Rejected alternative:** Delete the old SA immediately, send on both SAs, or
choose the highest generation by scanning the table for every packet.

**Consequences:** Control ordering and stale-generation rules are important.
Deleting the active slot fails closed instead of silently selecting an old SA.

## Never Scan the Child-SA Table in the Packet Fast Path

**Problem / context:** The first overlap implementation changed downlink SA
selection into a scan of all 4,096 Child-SA entries per packet. Reverse TCP
throughput fell from about 1.19 Gbit/s to about 504 Mbit/s, with repeat results
around 481/478 Mbit/s. A profile attributed 58.2% CPU to the old lookup. Turning
off scheduled rekey did not remove the scan and therefore did not restore speed.

**Decision:** Use:

```text
downlink packet -> TEID/QFI session index
                -> session.active_outbound_sa_index + generation
                -> stable SA crypto runtime
```

Inbound uses a direct SPI hash index. The only latest-SA scan is clearly named
`n3iwf_dp_child_sa_find_latest_for_control` and restricted to update handling.

**Why:** Rekey is rare; packet lookup is continuous. Paying O(number-of-SAs)
per packet violates the performance model even with one live session.

**Rejected alternative:** Keep `n3iwf_dp_child_sa_find_session()` in downlink,
hide the scan behind a runtime rekey flag, or cache only a candidate generation
while still searching the table.

**Consequences:** This invariant must be protected by tests and static searches:
**no full-table or O(N) lookup in a per-packet datapath when a direct, indexed,
or safely cached lookup is possible.** Rekey-enabled uplink traffic has now been
live-validated at the expected throughput class, supporting functional overlap
correctness. Because the old scan affected only downlink TX-SA selection, the
reverse/downlink result is the decisive regression check. A 2026-09-01
rekey-enabled reverse run sustained 1.15 Gbit/s for 60 seconds, with stable
one-second intervals and four TCP retransmissions. The controlled kernel
comparison and formal performance acceptance are still required.

## Stable Slots Require Generation Validation

**Problem / context:** Direct slot references are fast, but a deleted slot can
later be reused for another SA.

**Decision:** Store both the active slot and its installation generation in the
session. Validate UE/session identity and generation at packet use.

**Why:** It prevents stale references from encrypting with a recycled key while
retaining O(1) selection.

**Rejected alternative:** Raw pointers without lifetime validation or table
searches to rediscover identity.

**Consequences:** SA storage remains stable, control updates publish slot and
generation together, and active deletion clears the mapping.

## Bind Authenticated Child SAs Directly to Sessions

**Problem / context:** An ESP SPI authenticates a specific Child SA and its
UE/PDU-session identity. Re-entering the clear GRE path after decryption used
inner NWu address plus QFI for another session lookup. One UE can reuse the
same QFI in multiple PDU sessions, so that key is not unique. Secure downlink
had a related issue: it selected by TEID/QFI, built GRE, then looked up the
session again by NWu/QFI to choose the outbound SA.

**Decision:** Each Child SA stores a direct stable session slot and the
session-slot lifetime generation. Secure uplink validates that slot,
generation, and UE/PDU identity after SPI lookup. Secure downlink carries the
session returned by the original TEID/QFI lookup directly into active-SA
selection.

**Why:** This preserves authenticated/indexed identity in both directions,
supports same-UE QFI reuse, and keeps session selection O(1).

**Rejected alternative:** Reusing NWu address plus QFI in production, adding a
session-table scan, or changing standard GRE/QFI encoding with a private
disambiguator.

**Consequences:** Session modifications preserve their slot-lifetime
generation; deletion and recycling change it so stale Child-SA bindings fail
closed. Clear GRE retains its NWu/QFI ambiguity as an explicit test-mode
limitation.

## Single-Lcore Publication Is a Current Constraint, Not a Universal Model

**Problem / context:** The current NF runs control polling and packet callbacks
on one lcore.

**Decision:** Use that serialization for the first implementation; do not add a
global fast-path lock.

**Why:** It is simple and avoids unnecessary contention today.

**Rejected alternative:** Premature shared global locking.

**Consequences:** A future multi-lcore implementation must introduce sharding
and RCU/generation-safe publication before sharing these structures. It cannot
assume the present callback serialization remains true.

## Use One Fail-Closed Inner MTU for Both Directions

**Problem / context:** A 1500-byte inner packet fits N6 but not the current
1500-byte IPv4/AES-CBC ESP NWu profile. Allowing mbuf capacity, endpoint
fragmentation, or clear-test overhead to determine success would make behavior
direction- and allocation-dependent.

**Decision:** Support inner IP packets through 1410 bytes in both directions.
Reject larger packets before mutation, preserve inner DF, reject outer
fragments, and do not reassemble or originate PMTU ICMP in this dataplane.
Expose 1410 to endpoints and distinguish oversize, buffer, and fragment drops.
Clear-GRE test mode uses the production limit.

**Why:** 1410 is the exact AES-CBC padding boundary for the fixed profile:
1410 produces a 1496-byte outer IPv4 ESP packet and 1411 produces 1512 bytes.
A symmetric limit is predictable and remains inside the logical N3 and N6
MTUs.

**Rejected alternative:** Silent truncation, opportunistic forwarding based on
mbuf headroom, inner or outer fragmentation in the NF, unbounded reassembly,
or a larger clear-mode-only MTU.

**Consequences:** Endpoint interfaces/routes must advertise MTU 1410. A future
different physical MTU, NAT-T profile, IPv6 outer profile, or cipher mode must
derive a new boundary and rerun component and live acceptance; it must not
change only a configuration label while retaining these constants.
