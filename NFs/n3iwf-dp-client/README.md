# N3IWF dataplane client

This package is the control-plane side of the versioned `N3DP` Unix
`SOCK_SEQPACKET` contract implemented by `l25gc_n3iwf_dp`.

The pinned free5GC N3IWF under `NFs/n3iwf` programs the negotiated Child-SA,
then the matching PDU session, before returning PDU Session Resource Setup
success. It deletes the session before the Child-SA and before releasing local
tunnel state. Each UE/PDU-session pair owns a monotonically increasing
generation.

The v1 slice accepts one through 63 QFIs and IPv4 addresses. IPv6 fields are
reserved but rejected until all packet paths implement them.

Child-SA upsert/delete is implemented with directional keys, SPI and transform
validation, generation ordering, rekey overlap, and secure key erasure. A PDU
session is rejected until its matching Child-SA exists. Packet ESP processing
still remains fail-closed until the DPDK cryptodev backend is available.

For a running NF, use the CLI as a read-only observer:

```bash
go build -o /tmp/n3iwf-dpctl ./cmd/n3iwf-dpctl
sudo /tmp/n3iwf-dpctl -operation hello
sudo /tmp/n3iwf-dpctl -operation stats
```

Use the pinned N3IWF for live lifecycle programming. The deterministic Meson
control test covers writer authorization, Child-SA-before-session ordering,
stale updates and read-only observers without putting keys on a command line.

`stats` prints stable `name=value` output for uplink/downlink packets, unknown
TEID/QFI, malformed packets, replay drops, crypto failures, fragment drops,
stale control updates, and TAP control-punt traffic/drops. The client accepts
the original nine-counter, twelve-counter, and current fifteen-counter v1
replies while validating the wire version, response type, message length and
transaction ID. The final three counters report access-MAC learns, changes,
and downlink/invalid-neighbor drops.

The defaults match the local non-3GPP test: GRE/IPsec-inner NWu
`10.0.0.1/10.0.0.2` and logical N3 `192.168.2.1/192.168.2.2`. The IKE/ESP
outer addresses remain `192.168.127.1/192.168.127.2` and are not session lookup
keys.

The DPDK-side `n3iwf-dp-clear-path` Meson test installs this same deterministic
session and sends both keyed GRE and GTP-U/PDU Session Container packets through
the packet transformation function used by the live NF. Run the complete
component boundary with:

```bash
cd ../onvm-upf
./env/bin/meson setup --reconfigure build
./env/bin/meson compile -C build l25gc_n3iwf_dp n3iwf_dp_clear_test
./env/bin/meson test -C build \
  n3iwf-dp-codec n3iwf-dp-session n3iwf-dp-child-sa n3iwf-dp-control \
  n3iwf-dp-clear-path upf-u-n3iwf-route --print-errorlogs
```

This deterministic EAL test does not require physical NIC binding or an ONVM
manager. Physical-port and live-ring acceptance remains a separate hardware
test; do not interpret the EAL result as proof of NIC or ring configuration.
