# Architecture

## Component Boundary

```text
                         N2 / SCTP / NGAP
                    +----------------------> AMF
                    |
UE -- physical NWu --+--> n3iwf-dp -- TAP --> Go N3IWF
   IKE/signalling         ONVM NF             IKEv2, EAP-5G,
                                               NAS, NGAP, UE/PDU state
                              ^                    |
                              |  N3DP v1 control   |
                              +-- Unix SEQPACKET <-+

UE == ESP{GRE/QFI{PDU}} == n3iwf-dp == GTP-U/PSC == UPF-U -- N6 -- DN
       physical NWu           ONVM service 14       service 1
                               logical N3 rings
```

The Go process decides what state should exist. The C/DPDK NF performs packet
work using already-programmed state. The socket does not transport packets.

## Responsibilities

### Go N3IWF control plane (`NFs/n3iwf`)

- IKEv2 and EAP-5G authentication.
- NAS control transport over the signalling Child SA.
- NGAP/SCTP toward AMF on N2.
- UE, IKE SA, Child-SA, and PDU-session procedure state.
- Extraction of UL/DL TEIDs, QFI lists, UPF peer, NWu/N3 addresses, negotiated
  transforms, SPIs, directional keys, selectors, and replay policy.
- Generation-numbered session/Child-SA programming and deletion.
- Optional, default-disabled N3IWF-initiated Child-SA rekey, retransmission,
  overlap timing, and deletion.

The Linux backend remains available as a reference path. With the ONVM backend,
the Go raw GRE/GTP-U user-plane server is not started.

### Control client (`NFs/n3iwf-dp-client`)

- Marshals N3DP version 1 messages in network byte order.
- Uses Unix `SOCK_SEQPACKET` so one datagram equals one command.
- Performs writer/observer hello and correlates acknowledgements by transaction.
- Exposes session, Child-SA, and stats operations to Go and `n3iwf-dpctl`.

### N3IWF-DP (`NFs/onvm-upf/5gc/n3iwf_dp`)

- Owns the physical NWu/access user-plane packets in production mode.
- Punts only narrowly classified ARP, IKE UDP/500 or UDP/4500, and—when the
  temporary transition flag is set—unowned signalling ESP to a TAP.
- Converts GRE/QFI and GTP-U/PDU Session Container in place using mbuf
  prepend/adjust operations.
- Authenticates/decrypts uplink ESP and encrypts downlink ESP with DPDK
  cryptodev.
- Maintains indexed session/SA state, replay windows, sequence state, access
  neighbor MACs, and counters.

### ONVM-UPF (`NFs/onvm-upf/5gc/upf_u`)

- Remains the N3 termination and N6 forwarding function.
- Receives/sends ordinary N3 packets, not private session metadata.
- Routes configured N3IWF-peer downlink packets to ONVM service 14.
- Continues to receive forwarding rules through its existing SMF/PFCP path;
  N3IWF-DP does not take over N4.

## Code Map

| Area | Primary files |
| --- | --- |
| Go backend boundary | `NFs/n3iwf/internal/userplane/backend.go` |
| Session/SA contract construction | `NFs/n3iwf/internal/userplane/session.go`, `child_sa.go` |
| PDU setup/delete integration | `NFs/n3iwf/internal/ike/handler.go`, `NFs/n3iwf/internal/ngap/handler.go` |
| Optional rekey extension | `NFs/n3iwf/internal/ike/rekey.go`, `internal/context/ike.go`, `ikeue.go` |
| Go configuration/startup split | `NFs/n3iwf/pkg/factory/config.go`, `pkg/service/init.go` |
| Go wire client and inspector | `NFs/n3iwf-dp-client/client.go`, `cmd/n3iwf-dpctl/main.go` |
| C wire/control boundary | `n3iwf_dp_wire.h`, `n3iwf_dp_control.c` |
| Session/index/MAC state | `n3iwf_dp_session.c`, `n3iwf_dp_session.h` |
| Child-SA/index state | `n3iwf_dp_child_sa.c`, `n3iwf_dp_child_sa.h` |
| GRE/GTP conversion | `n3iwf_dp_codec.c`, `n3iwf_dp_clear.c`, `n3iwf_dp_downlink.c` |
| ESP/cryptodev | `n3iwf_dp_ipsec.c`, `n3iwf_dp_ipsec.h` |
| MTU contract | `n3iwf_dp_mtu.h`, `docs/high-performance-n3iwf/MTU_POLICY.md` |
| TAP boundary and NF loop | `n3iwf_dp_punt.c`, `n3iwf_dp.c` |
| UPF logical-N3 routing | `NFs/onvm-upf/5gc/upf_u/upf_u_n3iwf.c`, `upf_u.c` |
| Deployment topology | `config/n3iwf_dp_topology.env`, `scripts/run/run_n3iwf_dp.sh`, `run_onvm_mgr.sh` |
| Live diagnostic tools | `onvm_test/n3iwf_clear_gre.py`, `n3iwf_clear_dn_echo.py`, `NON3GPP_README.md` |

Paths in the middle column without a prefix are relative to
`NFs/onvm-upf/5gc/n3iwf_dp`. Unit tests sit beside the corresponding Go or C
source and use `_test.go` or `_test.c` suffixes.

## Physical and Logical Topology

The checked-in topology file currently describes:

| Role | Current value |
| --- | --- |
| N3IWF-DP ONVM service | 14 |
| UPF-U service | 1 |
| NWu/access DPDK port | port 1, PCI `0000:09:00.0`, kernel name `enp9s0` |
| N6 DPDK port | port 0, PCI `0000:08:00.0`, kernel name `enp8s0` |
| Physical NWu outer | UE `192.168.2.1`, N3IWF `192.168.2.2` |
| Inner NWu/GRE | N3IWF `10.0.0.1`, UE allocated from the configured subnet |
| Logical N3 in mbufs | N3IWF `192.168.4.1`, UPF `192.168.4.2` |
| Physical N6 | UPF `192.168.3.1`, DN `192.168.3.2` |

The mlx5 deployment uses a bifurcated PMD, so kernel interfaces may remain
visible while DPDK receives the relevant traffic. Port numbers must be verified
from the manager at each deployment; PCI enumeration assumptions caused earlier
traffic to be delivered to UPF-U instead of N3IWF-DP.

## Uplink Packet Path

```text
1. NWu NIC receives Ethernet / IPv4 / ESP.
2. N3IWF-DP looks up the Child SA by inbound SPI through an open-addressed index.
3. Verify ICV and padding, enforce anti-replay, decrypt ESP.
4. Resolve the Child SA's direct session slot and validate its slot generation
   and UE/PDU identity. A deleted or recycled slot fails closed.
5. Parse authenticated outer NWu IP and GRE Key; validate NWu address and QFI
   against that session and validate the access destination MAC.
6. Reject an inner packet above the 1410-byte production boundary before
   learning or packet mutation.
7. Learn/update the UE source MAC, then remove Ethernet/NWu/GRE headers while
   retaining the inner PDU packet.
8. Prepend Ethernet / IPv4 / UDP / GTP-U T-PDU / PDU Session Container using
   the session's uplink TEID and QFI.
9. Set ONVM action TONF, destination UPF-U service 1.
10. UPF-U applies its normal N3/PFCP state and sends the payload on N6.
```

The clear `-t` path parses GRE and uses NWu address plus QFI because it has no
authenticated SPI identity; that key is ambiguous when one UE reuses a QFI
across PDU sessions and remains a test-mode limitation. Production `-e`
requires successful cryptodev initialization.

## Downlink Packet Path

```text
1. UPF-U creates a complete N3 GTP-U packet and sends it to service 14.
2. N3IWF-DP parses outer IP/UDP, GTP-U, extension headers, TEID, and QFI.
3. Find session through the downlink TEID index and validate QFI.
4. Reject an inner packet above the 1410-byte production boundary before
   packet mutation.
5. Remove N3/GTP-U and prepend GRE Key/QFI plus inner NWu IP.
6. Read session.active_outbound_sa_index and generation; validate that exact
   stable Child-SA slot. No SA-table scan is allowed here.
7. Encrypt/authenticate ESP and prepend the physical NWu Ethernet/IP headers.
8. Send ONVM action OUT to the NWu/access port using the local port MAC and the
   session's learned UE MAC.
```

Downlink fails closed if there is no session, QFI, learned UE MAC, active SA,
matching generation, crypto runtime, or valid packet.

## GRE/QFI and GTP-U/PSC Encoding

- GRE Key Present is required for QoS traffic.
- QFI is the six-bit value in bits 24..29 of the 32-bit GRE Key field.
- RQI is an independent downlink indication: PSC octet 2 bit 6 maps to GRE Key
  octet 8 bit 7 (numeric mask `0x00000080`). It is never derived from QFI and
  is not originated or accepted as an uplink indication.
- GRE protocol identifies the inner IPv4 or IPv6 PDU packet.
- N3 uses GTPv1-U T-PDU (`UDP/2152`) with the PDU Session Container extension.
- Uplink construction sets the uplink PDU-session information direction and
  carries the same QFI without RQI. Downlink parsing requires the downlink
  direction and carries the received RQI unchanged into GRE.
- TEID and QFI remain in the packet across the ONVM logical N3 hop.

## Session and Child-SA State

### Sessions

The C table has 4,096 stable entries and 8,192-entry open-addressed indexes:

- Clear/test uplink key: UE inner-NWu address; QFI is validated with a 64-bit
  bitmap.
- Downlink key: downlink TEID; QFI is validated with the same bitmap.

Each session also holds UL/DL TEIDs, logical NWu/N3 peers, UE/PDU identity,
generation, learned access MAC, and the active outbound SA slot/generation.
Identity scans used to apply control updates are slow-path operations.

### Child SAs

The table has 4,096 stable SA slots and an 8,192-entry inbound-SPI index. A
slot contains negotiated wire parameters, directional key material, install
generation, a session command watermark, and the bound session slot plus its
lifetime generation. Crypto runtime state uses the same stable SA slot and is
reconciled only when the table revision changes.

When the optional rekey extension is enabled:

```text
old inbound SPI ----\
                     +--> both accepted during overlap
new inbound SPI ----/

session.active_outbound_sa_index --> new SA immediately after acknowledged upsert
```

The slot generation prevents a recycled slot from being used accidentally.
Deleting the inactive old SA leaves the active mapping intact. Deleting the
selected SA clears the mapping and drops downlink until control supplies a
valid replacement.

## Control-Plane/Data-Plane Update Ordering

Initial setup programs the Child SA before the PDU session. A missing session
during Child-SA activation is expected; the later session upsert binds the
installed SA to the stable session slot. This initial add/update/delete
lifecycle is required by the core integration and does not depend on automatic
rekey.

If the optional rekey extension is enabled, it programs the new Child SA,
binds both overlap SAs to that session, atomically changes the session's active
outbound selection in the single-lcore execution model, keeps the old inbound
SA for overlap, then deletes the old SA after IKE acknowledgement.

Every accepted update advances a per-session generation/watermark. Duplicate or
delayed generations are rejected and counted. The current single-lcore callback
model serializes control polling with packet handling; multi-lcore support will
require an explicit publication scheme rather than assuming this property.

## Fast-Path Complexity Invariants

| Operation | Required normal path |
| --- | --- |
| Secure uplink session | SPI-indexed Child SA, then direct session slot plus slot generation |
| Clear/test uplink session | Hash/index by NWu address, then QFI bitmap |
| Downlink session | Hash/index by TEID, then QFI bitmap |
| Inbound Child SA | Hash/index by SPI |
| Outbound Child SA | Direct session slot plus generation |
| Crypto runtime | Direct stable-slot cache |

No per-packet path may call the control-only latest-SA scan. Table-wide scans
are acceptable only for infrequent control updates, explicit diagnostics,
initialization/close, or revision-triggered reconciliation.
