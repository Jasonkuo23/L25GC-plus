# Non-3GPP Access Testing (N3IWF)

Tests UE connection via untrusted Wi-Fi networks through N3IWF.

## Quick Start

```bash
# Start N3IWF
sudo ./start_n3iwf.sh

# Run test
sudo ./RUN_NON3GPP_TEST.sh

# Stop N3IWF
sudo ./stop_n3iwf.sh
```

## Test Flow

1. **IKE_SA_INIT** - Establish security association
2. **IKE_AUTH** - Certificate + EAP-5G authentication
3. **NAS Registration** - Over TCP with 2-byte length envelope
4. **PDU Session** - Establish data bearer
5. **Control Plane Validation** - All procedures complete

## Expected Result

```
✅ IKE signaling working
✅ EAP-5G authentication successful
✅ Registration complete
✅ PDU Session established
✅ PASSED
```

## Key Files

- `non3gpp_test.go` - Test implementation
- `start_n3iwf.sh` - Start N3IWF service
- `RUN_NON3GPP_TEST.sh` - Run test
- `config/n3iwfcfg_test.yaml` - N3IWF configuration

## Architecture

```
UE (Test) ←─NWu/IKE/IPsec─→ N3IWF ←─N2/SCTP─→ AMF ←→ SMF ←─N4/PFCP─→ UPF ←─N6─→ DN
                         │
                         └─N3/GTP-U─→ UPF
```

## Interface Roles

The same-server ONVM deployment uses the following fixed split:

| Plane | Endpoint/address | Ownership |
|---|---|---|
| Management | `eno1`, `130.127.133.102/22` | Linux kernel; never passed to DPDK |
| N2 | AMF `127.0.0.18:38412` | Same-host loopback/SCTP |
| N4 | SMF `127.0.0.1`, UPF-C `127.0.0.8` | Same-host loopback/PFCP |
| NWu | N3IWF `192.168.127.1/24`, UE `192.168.127.2/24` | DPDK port 1, PCI `0000:82:00.0` |
| N3 | N3IWF `192.168.2.1`, UPF-U `192.168.2.2` | Logical IP/GTP-U inside ONVM mbufs; no Linux interface |
| N6 | UPF-U `192.168.3.1/24`, DN `192.168.3.2/24` | DPDK port 0, PCI `0000:02:00.3` |
| UE PDU pool | `10.60.0.0/16` | Routed through UPF-U |
| GRE/IPsec inner | N3IWF `10.0.0.1`, UE pool `10.0.0.0/24` | Inside the authenticated NWu tunnel |

### NWu control-packet TAP

`n3iwf-dp` owns physical DPDK access port 1. The launcher creates the
`n3iwf-cp` TAP with `192.168.127.1/24`, matching `ikeBindAddress` in
`config/n3iwfcfg.yaml`. The NF passes complete Ethernet frames only for ARP,
IKE on UDP/500, and IKE/NAT-T keepalives on UDP/4500. Frames returned by Linux
are filtered by source address and protocol before transmission on port 1.

`N3IWF_DP_KERNEL_SIGNALLING_ESP=1` additionally permits native ESP and
ESP-in-UDP/4500 for free5GC's signalling Child SA. This is an explicit
transitional path, not the target user plane: encrypted PDU traffic remains
fail-closed until DPDK cryptodev ESP processing consumes the programmed SAs.
IPv4 fragments, VLAN frames, IPv6 NWu, and unrelated TAP traffic are currently
rejected. Inspect `control_to_cp`, `control_from_cp`, and
`control_punt_drops` with `n3iwf-dpctl -operation stats`.

For a live smoke test, start the ONVM manager and UPFs first, then start
`scripts/run/run_n3iwf_dp.sh`. The latter creates/configures the TAP before the
Go N3IWF binds its IKE sockets. Build and start the pinned control plane from
the repository root with:

```bash
cd NFs/n3iwf
go build -o /tmp/l25gc-n3iwf ./cmd
cd ../..
sudo /tmp/l25gc-n3iwf -c config/n3iwfcfg.yaml
```

While the UE initiates IKE, verify the boundary and counters:

```bash
ip -d link show n3iwf-cp
ip address show dev n3iwf-cp
sudo tcpdump -ni n3iwf-cp 'arp or udp port 500 or udp port 4500 or esp'
sudo /tmp/n3iwf-dpctl -operation stats
```

On this host DPDK enumerates PCI `02:00.3` as port 0 and PCI `82:00.0` as port
1, regardless of allow-list order. Confirm the manager MAC mapping before
sending traffic: port 0 is `34:17:eb:e4:7b:2d` (N6) and port 1 is
`00:8c:fa:5b:11:ae` (NWu). The ingress service map must be `1:14,0:1`.

DPDK owns server PCI `0000:02:00.3`, so do not assign `192.168.3.1` to a
Linux interface on the L25GC+ server. Physically cable that port to a dedicated
DN interface. On the DN host, identify the interface by link state/MAC (do not
reuse the NWu interface), replace `<DN_INTERFACE>`, and run:

```bash
ip -br link
sudo ethtool <DN_INTERFACE>
sudo ip link set <DN_INTERFACE> up
sudo ip addr replace 192.168.3.2/24 dev <DN_INTERFACE>
sudo ip route replace 10.60.0.0/16 via 192.168.3.1 dev <DN_INTERFACE>
ip route get 10.60.0.1
```

Restart the ONVM manager after cabling. Both ports must report `Link Up`; port
0 is N6 and port 1 is NWu. From the DN, validate neighbor resolution first:

```bash
sudo tcpdump -eni <DN_INTERFACE> 'arp or net 10.60.0.0/16'
sudo arping -I <DN_INTERFACE> -c 3 192.168.3.1
ip neigh show 192.168.3.1
```

The manager's port-0 RX must increase for each ARP request and TX must increase
for each UPF reply. A successful ARP exchange is the primary N6 link check;
`ping 192.168.3.1` is only meaningful if UPF-U implements a local ICMP reply.

Do not configure `192.168.2.1` or `192.168.2.2` on a Linux interface. They are
standard N3 IP endpoints in the packet, but transport between them is direct
ONVM mbuf handoff.

The `start_n3iwf.sh` script creates `n3iwf-ue` as a local dummy NWu test
link:

- `192.168.127.2`: simulated UE outer/IKE address
- `192.168.127.1`: N3IWF IKE bind address
- `10.0.0.1`: N3IWF IPsec inner address on `xfrmi-default`

Do not use `n3iwf-ue` to ping AMF or DN addresses. It is not the N2, N3, or
N6 interface, and forcing traffic through it with `ping -I n3iwf-ue` will send
packets out the wrong side of the topology.

For the kernel baseline, use N2 and N6 checks appropriate to that deployment.
For the ONVM topology above, N2/N4 are same-host loopback and N3 has no
pingable interface. Validate N3 using N3IWF-DP/UPF counters and packet tests.
After ARP works, validate routed UE traffic from the DN host:

```bash
ping -c 3 <UE_PDU_IP>
```

The ONVM N3IWF configuration is `config/n3iwfcfg.yaml`, with
`n3iwfGtpBindAddress: 192.168.2.1` and `userPlaneBackend: onvm`. The separate
`onvm_test/config/n3iwfcfg_test.yaml` file retains the kernel/free5GC baseline
and must not be used to start the DPDK user plane.

For iperf3, run the server on the DN address and bind the client to the UE PDU
address after the PDU session is established:

```bash
# DN node
iperf3 -s -B <DN_N6_IP>

# UE/N3IWF side, after PDU session setup
iperf3 -c <DN_N6_IP> -B <UE_PDU_IP>
```

## Technical Details

### ONVM clear-path component acceptance

The kernel `start_n3iwf.sh` flow above remains the functional IKE/EAP/NAS
baseline. It must not run alongside the DPDK N3IWF user plane on the same NWu
port.

Before attempting a live ONVM run, validate the exact clear-mode packet
transformation used by `l25gc_n3iwf_dp`:

```bash
cd ../NFs/onvm-upf
./env/bin/meson setup --reconfigure build
./env/bin/meson test -C build --print-errorlogs \
  n3iwf-dp-codec n3iwf-dp-session n3iwf-dp-child-sa \
  n3iwf-dp-control n3iwf-dp-punt \
  n3iwf-dp-clear-path
```

The version-1 control contract now programs a bidirectional ESP Child-SA pair
before activating its PDU session. It carries the UE/PDU identity, inbound and
outbound SPI, IKEv2 transform IDs, directional keys, outer endpoints, traffic
selectors, NAT-T/ESN flags, replay window, sequence and lifetime values. All
updates are acknowledged and generation ordered; replacement/deletion erases
the dataplane key copy. The Unix seqpacket socket is mode `0600`, accepts only
root or the N3IWF-DP process UID, assigns exactly one writer after HELLO, and
keeps stats clients read-only. RAN UE NGAP ID zero and multiple QFIs are valid.

The test verifies keyed GRE/QFI to standard GTP-U/PDU Session Container and
the reverse downlink conversion, TEIDs 100/200, QFI 9, logical N3 addresses,
ONVM `TONF 1`/`OUT 0` decisions, payload preservation and the unknown-QFI drop
counter. Clear mode reads the local Ethernet address from the configured DPDK
access port at startup. The first accepted GRE uplink for a session learns the
UE source MAC; a later valid uplink can update it. Downlink remains fail-closed
until that session has a learned UE MAC, then emits Ethernet source=access-port
MAC and destination=learned-UE MAC. The component test covers first learn, MAC
change, downlink-before-learning rejection, and both Ethernet addresses. It
uses a DPDK EAL mempool but does not bind a NIC or traverse live ONVM rings.

During a live clear-mode run, query service 14 counters with:

```bash
cd ../NFs/n3iwf-dp-client
go build -o /tmp/n3iwf-dpctl ./cmd/n3iwf-dpctl
sudo /tmp/n3iwf-dpctl -operation stats
```

For one successful uplink followed by downlink, expect
`access_mac_learns=1`, `access_mac_changes=0`, and both packet counters to
increase. `access_neighbor_drops` increases when a downlink arrives before a
UE MAC is learned, an uplink carries an invalid source MAC, or its Ethernet
destination is not the configured access-port MAC. A non-zero
`access_mac_changes` is useful during UE/NIC movement but should be investigated
if the test topology is static.

Clear mode is unencrypted and is only an integration aid. Production mode
continues to drop user traffic until cryptodev ESP processing is implemented.

### NAS Message Format
Messages use 3GPP TS 24.502 envelope:
- 2-byte length (big-endian)
- NAS message payload

### Authentication
- EAP-5G over IKE
- Certificate-based N3IWF authentication
- 5G-AKA for UE authentication

### Troubleshooting

**`Add XFRM policy: file exists` during IKE_AUTH:** this indicates signalling
Child-SA state left by an unclean prior N3IWF exit. The pinned N3IWF now uses
exact XFRM state/policy update semantics for a matching SPI or selector and
rolls back a partially installed pair. Rebuild and restart the N3IWF; do not
use global `ip xfrm state/policy deleteall` on a host that may run other IPsec
users.

**N3IWF not responding:**
```bash
ps aux | grep n3iwf
sudo ./start_n3iwf.sh
```

**Port conflicts:**
```bash
sudo netstat -tulpn | grep 500
# Kill conflicting process if needed
```

**Test timeout:**
- Check N3IWF logs: `journalctl -u n3iwf -f`
- Verify all NFs running (AMF, SMF, UPF, etc.)

## Duration
~21-25 seconds
