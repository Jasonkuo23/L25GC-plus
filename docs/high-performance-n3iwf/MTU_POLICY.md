# MTU Boundary Policy

This document defines the fixed MTU contract for the current IPv4
software-IPsec N3IWF profile. It is normative for the ONVM dataplane and the
live acceptance procedure. MTU values below are IP MTUs: they exclude the
14-byte Ethernet header and 4-byte FCS.

## Deployment Contract

NWu, logical N3, and N6 each have a 1500-byte IP MTU. Production NWu uses IPv4
ESP tunnel mode, AES-CBC-128 or AES-CBC-256, HMAC-SHA1-96, and keyed GRE. N3
uses IPv4/UDP/GTP-U with a 16-byte GTP-U header including the PDU Session
Container. NAT-T, IPv6 outer transport, and ESN remain unsupported.

The single end-to-end maximum inner IPv4 or IPv6 PDU packet is **1410 bytes**.
Clear-GRE test mode enforces the same limit even though clear GRE alone could
carry a larger packet. This keeps test and production forwarding semantics
identical.

## Derivation

Let `L` be the complete inner PDU IP-packet length.

| Stage | IP-length calculation | At `L = 1410` |
| --- | --- | ---: |
| N6 | `L` | 1410 |
| Logical N3 | IPv4 20 + UDP 8 + GTP-U/PSC 16 + `L` | 1454 |
| Clear packet protected by ESP | inner NWu IPv4 20 + GRE 8 + `L` | 1438 |
| ESP ciphertext | round up `(20 + 8 + L + pad-length 1 + next-header 1)` to AES block 16 | 1440 |
| Physical NWu | outer IPv4 20 + ESP header 8 + IV 16 + ciphertext + ICV 12 | 1496 |

At the boundary, the physical NWu Ethernet frame is 1510 bytes excluding FCS
and 1514 bytes on the wire including FCS. The logical N3 Ethernet frame is
1468/1472 bytes without/with FCS, and the N6 Ethernet frame is 1424/1428 bytes.
The decrypted clear NWu IPv4/GRE packet is 1438 bytes, or 1452 bytes with its
Ethernet header. At `L = 1411`, AES-CBC padding moves to the next block and the
NWu IP packet becomes 1512 bytes, so the packet is unsupported. N3 would
independently permit `L <= 1456` and N6 would permit `L <= 1500`; NWu ESP is
therefore the limiting stage.

The formula and constants live in `n3iwf_dp_mtu.h`. AES key length does not
change the AES block size or this result.

## Required Behavior

- Uplink and downlink accept an inner packet only when `L <= 1410` and all
  configured-link formulas fit. Accepted payload bytes, including the inner
  IPv4 DF bit, are preserved byte-for-byte.
- A valid-session packet with `L > 1410` is dropped before header removal,
  neighbor learning, or other packet mutation and increments `oversize_drops`
  exactly once. It is not truncated or fragmented.
- IPv4 outer packets with MF set or a nonzero fragment offset, and IPv6 outer
  packets whose immediate next header is Fragment, are dropped and increment
  `fragment_drops`. No reassembly is required by this policy.
- A packet whose mbuf cannot support the complete clear conversion plus ESP
  headroom/tailroom budget is rejected before mutation and increments
  `buffer_drops`. Correctness must not depend on stale or unusually large mbuf
  capacity; clear mode reserves the same production budget.
- The dataplane does not originate ICMP Packet Too Big/Fragmentation Needed.
  Operators must expose the 1410 MTU at both endpoints so endpoint PMTU logic
  prevents ordinary oversize traffic. Above-boundary packets that bypass that
  endpoint policy still fail closed at N3IWF-DP.
- N3IWF-created IPv4 outer headers are not fragmented. Inner DF is an endpoint
  contract and is neither copied to an outer header nor cleared in the inner
  packet.

For inner IPv4 TCP without IP or TCP options, the corresponding maximum TCP
payload and explicit MSS is `1410 - 20 - 20 = 1370`. For inner IPv6 it is
`1410 - 40 - 20 = 1350`. TCP options consume payload space but do not change
the advertised MSS definition.

## Verification

The `n3iwf-dp-clear-path` component test exercises 1409-, 1410-, and 1411-byte
inner packets in both directions. It uses ICMP-shaped vectors and an exact
1410-byte IPv4/TCP vector with 1370 bytes after the base IP/TCP headers,
verifies accepted payloads byte-for-byte, checks that rejected packets are not
mutated, tests zero-headroom rejection in both directions, and checks
outer-fragment counters.

The executable live procedure is in `onvm_test/NON3GPP_README.md` under “MTU
boundary acceptance.” The accepted three-host result is retained under
`results/mtu-boundary-20260906T083817Z/` with the large pcaps retained on their
UE and DN capture hosts as recorded in its report.
