# N3IWF dataplane client

This package is the control-plane side of the versioned `N3DP` Unix
`SOCK_SEQPACKET` contract implemented by `l25gc_n3iwf_dp`.

The pinned free5GC N3IWF v1.3.5 under `NFs/n3iwf` should call
`UpsertSession` only after a successful PDU Session Resource Setup and call
`DeleteSession` before releasing the local TEID. Each UE/PDU-session pair owns
a monotonically increasing generation. Retries reuse the same generation;
modifications and deletion increment it.

The first usable v1 slice accepts exactly one active QFI and IPv4 addresses.
The fixed wire fields retain room for multiple QFIs and IPv6, but those values
are rejected until all packet paths implement them. A later compatible update
or wire-version increment will enable them without silently accepting an
incomplete dataplane behavior.

Child-SA programming is reserved in the protocol but the dataplane currently
returns `StatusUnsupported`; this keeps production traffic fail-closed until a
DPDK cryptodev backend is available.

For a clear-mode integration test, inject a deterministic session after the NF
has created its control socket:

```bash
go build -o /tmp/n3iwf-dpctl ./cmd/n3iwf-dpctl
sudo /tmp/n3iwf-dpctl -operation hello

sudo /tmp/n3iwf-dpctl \
  -operation upsert -generation 1 -ue-id 1 -pdu-session-id 10 \
  -ul-teid 100 -dl-teid 200 -ue-pdu 10.60.0.1 -qfi 9

sudo /tmp/n3iwf-dpctl \
  -operation delete -generation 2 -ue-id 1 -pdu-session-id 10
```

The defaults match the local non-3GPP test: GRE/IPsec-inner NWu
`10.0.0.1/10.0.0.2` and logical N3 `192.168.2.1/192.168.2.2`. The IKE/ESP
outer addresses remain `192.168.127.1/192.168.127.2` and are not session lookup
keys. This tool only exercises the control boundary; the
free5GC N3IWF must ultimately send the same operations from its PDU-session
lifecycle handlers.
