# High-Performance DPDK-based N3IWF Paper Outline

Target: a workshop systems paper focused on the implemented N3IWF user-plane
architecture and its evaluation. This outline incorporates the professor's
motivation and alternative-architecture comments and the
[code-grounded review](../../research/n3iwf-paper/paper_outline_code_review.md).
Implementation status and remaining work are tracked in [CURRENT_STATE.md](CURRENT_STATE.md)
and [TODO.md](TODO.md).

The central question is whether retaining the free5GC-derived control protocols
while moving PDU-session forwarding into DPDK preserves the tested access
behavior and improves performance over a separate-node Linux-XFRM/Go N3IWF.
The comparison evaluates both forwarding implementation and deployment
architecture; any topology differences must be explicit.
Architecture and retained kernel-bypass observations support the current story;
performance improvement remains to be measured. The bypass claim applies to
PDU-session traffic on the N3IWF/UPF host; signalling still uses TAP/Linux.

Markers distinguish unfinished work:

- `[EXPERIMENT NEEDED]`: evidence must be collected before claiming the result.
- `[CODE REVIEW NEEDED]`: implementation behavior requires further inspection.
- `[FUTURE WORK]`: deliberately outside the current paper scope.

Experimental setup and results are grouped by research question in Section 5.
The bullets below guide drafting; they are not assertions of completed experiments.

## 1. Introduction

- Introduce the motivation for bringing Wi-Fi and other non-3GPP access into a common 5GC framework, with mobility and communication continuity as broader service goals; develop this motivation in Section 2.1.
- State contributions: CP/DP architecture, identity-preserving ESP/GRE/GTP dataplane, and capture/counter verification of bypass.
- Position the integrated DPDK design against the separate-node Linux-XFRM/Go alternative. `[EXPERIMENT NEEDED]` Add a measured performance contribution only after a fair comparison; no achieved speedup in the abstract yet.

## 2. Background, Motivation, and Design Goals

### 2.1. Why N3IWF?

- Explain why integrating Wi-Fi and other non-3GPP IP access into 5G is useful: frame N3IWF's role in untrusted non-3GPP access and motivate a common core for subscriber authentication, session management, policy and service delivery. Support this background with appropriate architecture references when drafting.
- Motivate seamless mobility and uninterrupted communication as service goals when users move between cellular and non-3GPP coverage. Distinguish these goals from demonstrated capabilities: N3IWF alone does not guarantee continuity, and this project has not evaluated inter-access handover or interruption-free mobility.
- Connect the common 5GC access framework to the paper's problem: useful non-3GPP connectivity also needs an efficient secure user plane. This paper evaluates forwarding architecture and performance, rather than mobility procedures.

### 2.2. Non-3GPP User-Plane Path

- Explain NWu ESP protecting inner NWu IP/GRE/QFI/PDU, N3 GTP-U/PSC, and the UPF boundary.
- Show the uplink and downlink packet nesting, distinguishing physical NWu addresses, inner NWu/GRE addresses and UE PDU addresses; introduce only the protocol details needed to understand the design.

### 2.3. Alternative N3IWF Architectures and Reference Backend

- Describe the previously used separate-node deployment: UE → NWu → Linux-XFRM/Go N3IWF host → physical N3 network → UPF host → N6 → DN. Trace Linux XFRM/IPsec and Go raw GRE/GTP socket processing, including kernel/userspace transitions; avoid describing the forwarding engine as entirely kernel-resident.
- Compare it with the proposed same-host N3IWF-DP/ONVM-UPF architecture using a paired diagram and a compact table covering component placement, ESP/GRE/GTP processing, N3 transport, kernel involvement and resource allocation. Discuss independent placement versus co-location as architectural tradeoffs.
- Explain the potential packet-processing and physical-N3 costs motivating kernel bypass. Treat the earlier deployment's limited throughput as a motivation requiring attributable measurements; `[EXPERIMENT NEEDED]` quantify the baseline in Section 5 before claiming a ceiling, speedup or specific bottleneck.

### 2.4. Design Goals and Scope

- Define the goal of kernel-bypass PDU-session forwarding while reusing the existing Go control protocols, preserving full logical N3 packet semantics and existing UPF/N4 ownership.
- State the IPv4 ESP tunnel profile: AES-CBC-128/256, HMAC-SHA1-96, non-ESN, software CPU crypto and one N3IWF-DP lcore. Limit the bypass claim to PDU-session traffic on the N3IWF/UPF host; signalling still uses TAP/Linux.
- State explicit non-goals: full control-plane parity, demonstrated inter-access mobility, and an entirely kernel-free N3IWF. `[FUTURE WORK]` NAT-T user plane, IPv6 transport, hardware/inline crypto, multicore sharding and broader lifecycle/conformance coverage.

## 3. System Design

### 3.1. Physical and Logical Architecture

- Draw UE → physical NWu → N3IWF-DP/service 14 → logical N3 ring → UPF-U/service 1 → physical N6 → DN, with a separate Go/TAP/N2 control branch.
- Explain why TEID/QFI/RQI remain in N3 packet bytes and why N3IWF-DP does not become an N4 endpoint.

### 3.2. CP/DP State Ownership and Synchronization

- Explain SEQPACKET, writer/observer roles, UID checks, transactions, ACKs and generation-tagged session/SA commands.
- Show initial SA-before-session binding and stable references; distinguish command/install generations from session-slot lifetimes and the IKE-assigned inner NWu address from the unspecified SMF-assigned PDU address.
- Limit lifecycle claims to initial setup, control-socket upserts and helper-level deletion. Live NGAP modification does not publish updated DPDK state; `[CODE REVIEW NEEDED]` for broader teardown claims.

### 3.3. Uplink Identity-Preserving Fast Path

- Describe physical ingress classification, SPI-indexed SA, validated bound session, ESP crypto and authenticated GRE/QFI validation.
- Explain per-session neighbor learning, in-place GTP-U/UL PSC construction and service handoff.
- Contrast secure session identity with clear-test NWu/QFI ambiguity only as needed to explain the production binding.

### 3.4. Downlink Fast Path and Indexed State Lookup

- Describe TEID/QFI session lookup, DL RQI preservation, learned neighbor use and GRE construction.
- Carry the selected session directly into outbound SA slot/generation validation and ESP output.
- Explain why a full latest-SA scan is confined to control updates; describe expected indexed lookup rather than worst-case O(1), and disclose lazy runtime allocation on first SA use.

### 3.5. Kernel Boundary and MTU Policy

- Explain the temporary TAP/XFRM signalling path and the limits of the `-k` classifier; state exactly what bypass observation establishes.
- Present the 1410-byte inner boundary for the 1500-byte IPv4 AES-CBC profile, endpoint PMTU policy and explicit oversize/buffer/fragment drops.

## 4. Implementation

### 4.1. ONVM/DPDK and Software Crypto

- Identify Go N3IWF, C ONVM NF, DPDK CPU-crypto/AESNI-MB and same-host UPF integration; publish exact implementation revisions/artifact hashes.
- Explain primary-process crypto-device creation and NF crypto initialization, the supported cipher profile and fail-closed startup/mode selection. State that crypto currently processes one packet per call.

### 4.2. In-Place mbuf Packet Processing

- Summarize header removal/prepend operations on the existing mbuf, Ethernet preservation/restoration around ESP, and software IPv4 checksum calculation.
- Explain the headroom/tailroom checks and payload-preservation tests. Use “in-place” or “reduced copying”; do not claim a universally zero-copy or allocation-free pipeline.

### 4.3. State Tables and Generation Validation

- Describe stable session and Child-SA slots, SPI/TEID indexes, QFI bitmaps, direct session/active-SA references, and validation against recycled slots; connect these structures to Section 3's identity model.
- Distinguish installation generations, command watermarks and session-slot lifetimes. Explain cached crypto runtime state and its lazy allocation on first SA use; table capacity does not establish validated UE scale.

### 4.4. Observability and Integration Mechanisms

- Summarize aggregate packet/error/state counters, per-session access-MAC learning, directional QFI/RQI validation and replay configuration with representative tests. Distinguish these counters from absent per-QFI telemetry.
- Briefly mention NAS ordering and signalling-XFRM replacement only as integration changes necessary for the tested deployment.

## 5. Evaluation

### 5.1. Experimental Setup and Compared Architectures

- Define the two compared architectures with topology figures: integrated DPDK N3IWF-DP/ONVM-UPF with logical N3, and separate-node Linux-XFRM/Go N3IWF with a physical N3 link to the UPF. Identify the exact free5GC-derived reference revision and any differences from the earlier deployment.
- `[EXPERIMENT NEEDED]` Match or document CPU/SMT/NUMA, NIC/link/queues, IRQ/lcore affinity, software, cipher/key size, MTU/MSS, session/QFI/stream counts and rekey-disabled configuration. Record host count and total resource budgets, including manager and UPF work, plus UE/DN limits.
- Define measurement intervals, repetitions, receiver loss and latency methodology. Retain commands/configuration/raw outputs and report the comparison as an integrated-system result when placement and N3 transport differ; do not attribute all gains to the N3IWF forwarding engine without a controlled additional experiment.

### 5.2. RQ1: Functional Correctness

- Present separate component and live tables: Go/C tests for contracts, identity, codecs and MTU; retained kernel-bypass and MTU results for live observations. Clearly label remote or operator-reported evidence.
- `[EXPERIMENT NEEDED]` Retain clean bidirectional ICMP/TCP/secure-UDP and two-UE isolation artifacts; add basic release if claiming lifecycle acceptance.
- Keep live RQI/multiple-QFI and full backend parity explicitly unaccepted unless measured; do not silently convert backend-conformance manifest entries to PASS.

### 5.3. RQ2: Kernel-Bypass Verification

- Describe the no-kernel-user-plane collectors, declared SPI/flow correlation, capture-drop checks and counter snapshots, including why the peer NWu capture is authoritative for mlx5.
- Present the retained proof table and separately account for allowed IKE/signalling ESP; distinguish aggregate payload observations from application-integrity testing.
- `[EXPERIMENT NEEDED]` For final publication, retain a clean-completion repeat on the final measured build; do not broaden to untested lifecycle or malicious-traffic cases.

### 5.4. RQ3: Throughput, Loss, and Latency vs. Linux N3IWF

- `[EXPERIMENT NEEDED]` Run repeated UL, DL and simultaneous bidirectional TCP/UDP trials at controlled packet sizes/loads; report receiver throughput/loss and TCP retransmissions.
- `[EXPERIMENT NEEDED]` Compare maximum throughput meeting ≤0.1% loss and report loaded p50/p99 at matched loads, including the Linux baseline rate.
- Report all repetitions/variation and any failure to meet the ≥2× objective; historical reported checkpoints can guide test settings but cannot replace this result.

### 5.5. RQ4: CPU Efficiency

- `[EXPERIMENT NEEDED]` Record per-core activity across manager, N3IWF-DP, UPF, Linux workers/softirq and endpoint limits; compare throughput under equal allocations.
- Report throughput/core with a stated denominator; add packets/s/core or cycles/packet only when counters and measured intervals support them.

### 5.6. RQ5: Two-UE Concurrency

- `[EXPERIMENT NEEDED]` Archive the reported two-UE identities/isolation evidence or repeat it; keep one-UE/two-session/same-QFI component results distinct.
- `[EXPERIMENT NEEDED]` Measure repeated one-versus-two traffic, aggregate/per-UE rates and fairness under fixed CPU allocation.
- `[FUTURE WORK]` Larger UE/session scaling and lifecycle churn; do not infer scale from 4,096-entry capacity.

## 6. Related Work

- Review N3IWF implementations and work on non-3GPP/Wi-Fi integration with 5G, including the access-integration and mobility motivations introduced in Section 2.1.
- Compare DPDK-based 5G user-plane systems and kernel-bypass NFV systems. Support comparisons with external sources when drafting; repository evidence alone cannot establish precedence.
- Position the contribution around this implementation's CP/DP boundary, identity-preserving conversion and validation methodology, avoiding “first” claims.

## 7. Discussion and Limitations

- State transport/security subset, single-lcore/one-packet crypto, incomplete modification/extended teardown, unspecified PDU-source authorization, endpoint MTU dependency and temporary signalling boundary. Explicitly exclude demonstrated cellular/Wi-Fi handover or uninterrupted mobility from the results.
- Existing default-disabled optional rekey is outside the main evaluation; `[FUTURE WORK]` repeated/multi-UE rekey, collision/delete validation and failure recovery. `[CODE REVIEW NEEDED]` for exhaustive lifetime/sequence-security claims.
- Discuss separate-node versus integrated deployment tradeoffs and the limits of attributing system-level performance differences to kernel bypass alone. `[FUTURE WORK]` NAT-T user plane, IPv6 transport/dual stack, hardware/inline crypto, sharding and broader conformance.

## 8. Conclusion

- Summarize the implemented CP/DP split and evidence for PDU-session bypass within the tested profile.
- `[EXPERIMENT NEEDED]` State the actual measured comparison once available; do not conclude speedup or efficiency from architecture alone.
