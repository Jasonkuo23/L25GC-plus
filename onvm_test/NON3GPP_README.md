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

The `start_n3iwf.sh` script creates `n3iwf-ue` as a local dummy NWu test
link:

- `192.168.127.2`: simulated UE outer/IKE address
- `192.168.127.1`: N3IWF IKE bind address
- `10.0.0.1`: N3IWF IPsec inner address on `xfrmi-default`

Do not use `n3iwf-ue` to ping AMF or DN addresses. It is not the N2, N3, or
N6 interface, and forcing traffic through it with `ping -I n3iwf-ue` will send
packets out the wrong side of the topology.

For the default local L25GC+ layout, use these checks instead:

```bash
# N2 / AMF reachability
ping -c 3 -I enp7s0 192.168.1.2

# N3 / UPF-U reachability from the access side, if N3IWF runs on a UE/RAN node
ping -c 3 -I <N3IWF_N3_INTERFACE> 192.168.2.2

# N6 / DN reachability after PDU session setup, from the UE PDU address/interface
ping -I <UE_PDU_INTERFACE_OR_ADDRESS> <DN_N6_IP>
```

When using ONVM UPF-U for user-plane metrics, `n3iwfGtpBindAddress` in
`config/n3iwfcfg_test.yaml` must be an N3 address reachable by the UPF's N3
side. Use `127.0.0.33` only for loopback/free5GC-style tests. For a split
UE/RAN + core topology, set it to the N3IWF/UE-RAN node's N3 IP, for example
`192.168.2.1` when the UPF-U N3 IP is `192.168.2.2`.

For iperf3, run the server on the DN address and bind the client to the UE PDU
address after the PDU session is established:

```bash
# DN node
iperf3 -s -B <DN_N6_IP>

# UE/N3IWF side, after PDU session setup
iperf3 -c <DN_N6_IP> -B <UE_PDU_IP>
```

## Technical Details

### NAS Message Format
Messages use 3GPP TS 24.502 envelope:
- 2-byte length (big-endian)
- NAS message payload

### Authentication
- EAP-5G over IKE
- Certificate-based N3IWF authentication
- 5G-AKA for UE authentication

### Troubleshooting

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
