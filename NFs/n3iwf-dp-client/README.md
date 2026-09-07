# N3IWF dataplane client

This package is the control-plane side of the versioned `N3DP` Unix
`SOCK_SEQPACKET` contract implemented by `l25gc_n3iwf_dp`.

The pinned free5GC N3IWF v1.3.5 under `NFs/n3iwf` should call
`UpsertSession` only after a successful PDU Session Resource Setup and call
`DeleteSession` before releasing the local TEID. Each UE/PDU-session pair owns
a monotonically increasing generation. Retries reuse the same generation;
modifications and deletion increment it.

Protocol version 1 supports IPv4 session addresses and up to 64 QFIs per PDU
session. Session updates carry a monotonically increasing generation and stale
updates are rejected by the dataplane.

Child-SA add/update/delete is implemented end to end. The contract carries the
negotiated SPIs, algorithms, traffic selectors, replay window and key material.
The clear-mode dataplane consumes the SA-to-session relationship but
deliberately bypasses ESP. Clear mode is test-only and must be requested
explicitly when starting the NF. The first DPDK software-IPsec slice accepts
only IPv4 ESP tunnel mode,
AES-CBC-128 or AES-CBC-256 with HMAC-SHA1-96, without NAT-T or ESN;
unsupported contracts receive
`StatusUnsupported` instead of being silently downgraded.

For a clear-mode integration test, inject a deterministic session after the NF
has created its control socket:

```bash
go build -o ../../bin/n3iwf-dpctl ./cmd/n3iwf-dpctl
sudo ../../bin/n3iwf-dpctl -operation stats

sudo ../../bin/n3iwf-dpctl \
  -operation upsert -generation 1 -ue-id 1 -pdu-session-id 10 \
  -ul-teid 100 -dl-teid 200 -ue-pdu 10.60.0.1 -qfi 9

sudo ../../bin/n3iwf-dpctl \
  -operation delete -generation 2 -ue-id 1 -pdu-session-id 10
```

The stats response includes `oversize_drops`, `buffer_drops`, and
`fragment_drops` for the fail-closed MTU policy. The current 1500-byte
IPv4/AES-CBC NWu profile accepts inner IP packets through 1410 bytes; see
`../../docs/high-performance-n3iwf/MTU_POLICY.md`.

The example three-NIC physical test uses N3IWF GRE/IPsec-inner NWu
`10.0.0.1`; the UE-side address is dynamically allocated (often
`10.0.0.2`, but the live programmed contract is authoritative). Logical N3
uses `192.168.4.1/192.168.4.2`. The physical
IKE/ESP outer endpoints are `192.168.2.2/192.168.2.1` and are not session
lookup keys. The pinned free5GC N3IWF sends these operations from its live
Child-SA and PDU-session lifecycle handlers. `n3iwf-dpctl` is intended for
inspection and deterministic contract tests; use the live N3IWUE registration
for an end-to-end test so the SMF also installs the matching UPF rules.
