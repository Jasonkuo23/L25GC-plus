# No-kernel-user-plane proof

This suite produces the retained evidence for TODO item 3. It proves the
narrow claim that UE PDU traffic in the ONVM backend does not cross the Linux
user-plane path. It does **not** claim that the whole N3IWF avoids Linux: ARP,
IKE, and the signalling Child SA intentionally use `n3iwf-cp` and kernel XFRM
while `N3IWF_DP_KERNEL_SIGNALLING_ESP=1` is enabled.

The acceptance interval correlates four independent observations:

1. the two PDU-session ESP SPIs appear in both directions on the UE's physical
   NWu capture, and clear GRE does not;
2. the same SPIs and the identifiable UE-to-DN flow do not appear on the
   N3IWF host TAP or signalling XFRM interface;
3. no GTP-U between the logical N3 addresses appears on any Linux interface;
4. the DN sees the unique flow in both directions while N3IWF-DP uplink and
   downlink counters increase without error-counter growth.

Together with the retained UPF-U configuration, this triangulates the logical
N3 handoff over ONVM service rings. There is deliberately no claim that a Linux
pcap can observe a packet while it is on an ONVM ring.

## 1. Prepare a plan

Use a fresh PDU session with the ONVM software-IPsec backend and automatic
rekey disabled. Copy the template outside the repository and fill every
`CHANGE_ME` value:

```bash
cp onvm_test/no_kernel_user_plane/proof-plan.example.tsv /tmp/nkup-plan.tsv
ip -d link show type xfrm
```

Record the exact PDU-session inbound and outbound SPIs, not the signalling-SA
SPIs. The N3IWF log prints the inbound SPI when it programs the ONVM Child SA;
the paired values are also visible on the UE with `ip -s xfrm state`. Write
SPIs as decimal or `0x`-prefixed hexadecimal. Do not retain key material.

Use a unique TCP or UDP port that carries no other traffic during the capture.
The examples below use TCP port 5503. For ICMP, set `traffic_protocol` to
`icmp` and `traffic_port` to `0`.

## 2. Initialize a non-overwriting result directory

Run this on the L25GC+ host after the session exists. The harness refuses to
overwrite a result directory and requires an ONVM N3IWF configuration.

```bash
suite=./onvm_test/no_kernel_user_plane/no_kernel_user_plane.sh
result=$PWD/results/no-kernel-user-plane-$(date -u +%Y%m%dT%H%M%SZ)

$suite init "$result" /tmp/nkup-plan.tsv \
  "$PWD/config/n3iwfcfg.yaml" \
  "$PWD/config/n3iwf_dp_topology.env" \
  "$PWD/NFs/onvm-upf/5gc/upf_u/config/upf_u.yaml"

sudo -E "$suite" snapshot "$result" before
```

Initialization retains the root and relevant submodule revisions, status,
tracked binary diffs, untracked submodule file hashes, the proof plan, and all
three deployed configuration inputs. Check the `before` snapshot's
`status.tsv`; every observer used by the proof must have exit status zero.

## 3. Start simultaneous captures

Start the authoritative physical-access capture on the UE host. Capturing on
the L25GC+ `enp9s0` alone is insufficient: mlx5 is a bifurcated PMD, so the
interface remains kernel-visible while traffic delivered to DPDK can bypass
host packet sockets and `tcpdump`.

```bash
# UE host; use the plan's ue_access_interface and outer addresses.
sudo timeout --signal=INT 45 tcpdump -U -nn -s 128 -B 131072 -i <UE_ACCESS_IF> \
  -w /tmp/ue-access.pcap \
  'host 192.168.2.1 and host 192.168.2.2 and (esp or udp port 500 or udp port 4500 or ip proto 47)' \
  2>/tmp/ue-access-tcpdump.log
```

At the same time, start the DN capture. Its filter must use the assigned PDU
address, DN address, protocol, and unique marker port from the plan:

```bash
# DN host
sudo timeout --signal=INT 45 tcpdump -U -nn -s 128 -B 131072 -i <DN_IF> \
  -w /tmp/dn.pcap \
  'host <UE_PDU_IP> and host 192.168.3.2 and tcp port 5503' \
  2>/tmp/dn-tcpdump.log
```

Then start all four local observers in a third terminal:

```bash
sudo -E "$suite" capture-local "$result"
```

`capture-local` records the L25GC+ physical mlx5 interface, `n3iwf-cp`, the
named signalling XFRM interface, and UDP/2152 on Linux `any` for exactly the
plan duration. The physical-host capture is retained but its packet count is
informational because of mlx5 bifurcation.

## 4. Run identifiable bidirectional traffic

Start traffic only after all captures report that they are active. A TCP
example is:

```bash
# DN host
timeout --signal=INT 40 iperf3 -s -B 192.168.3.2 -p 5503 \
  > /tmp/traffic-dn.log 2>&1

# UE host, inside the PDU-session VRF when N3IWUE uses one
sudo ip vrf exec <UE_PDU_VRF> iperf3 -c 192.168.3.2 -p 5503 \
  -B <UE_PDU_IP> --bidir -b 20M -t 8 \
  2>&1 | tee /tmp/traffic-ue.log
```

This is a path proof, not a throughput benchmark. Keep the offered rate low
enough that every tcpdump reports zero kernel/interface drops. The 128-byte
snapshot length retains Ethernet, IP, ESP SPI, and transport headers without
forcing the capture host to write full TCP payloads.

Wait for every UE, DN, and local tcpdump process to exit before collection;
copying a pcap while tcpdump is writing can leave a truncated packet record.
The easiest collection method is one command
from the L25GC+ host (the targets may be aliases from `~/.ssh/config`):

```bash
$suite collect-remote "$result" ubuntu@uerannode ubuntu@dnnode
```

The files must be readable by the SSH user. If tcpdump created a root-only
pcap, run `sudo chmod 0644 /tmp/ue-access.pcap` or
`sudo chmod 0644 /tmp/dn.pcap` on that peer, then repeat collection. A retry
keeps files already collected and fetches the missing ones. The collector fully
reads each staged pcap and rejects it before retention if it is truncated.

Alternatively, copy the artifacts manually without changing their required
names:

```bash
scp <UE_HOST>:/tmp/ue-access.pcap "$result/captures/ue-access.pcap"
scp <UE_HOST>:/tmp/traffic-ue.log "$result/raw/traffic-ue.log"
scp <UE_HOST>:/tmp/ue-access-tcpdump.log "$result/raw/ue-access-tcpdump.log"
scp <DN_HOST>:/tmp/dn.pcap "$result/captures/dn.pcap"
scp <DN_HOST>:/tmp/traffic-dn.log "$result/raw/traffic-dn.log"
scp <DN_HOST>:/tmp/dn-tcpdump.log "$result/raw/dn-tcpdump.log"

sudo -E "$suite" snapshot "$result" after
```

Do not initiate registration, PDU-session modification, release, DPD, or rekey
inside the marker interval. Ambient IKE or signalling ESP is allowed, but the
report counts it separately from the two declared user-plane SPIs.

## 5. Analyze and gate

```bash
$suite analyze "$result"
$suite gate "$result"
```

`gate` reruns the analyzer so a stale `gate.status` cannot accept changed or
missing evidence. Every analyzer revision used for a run is copied under
`analysis/` by its SHA-256 hash, and `analysis/history.tsv` retains both the
initial and reanalysis hashes. This permits an acceptance-policy correction
without modifying or concealing the originally captured evidence.

The analyzer accepts classic pcap output from `tcpdump` on Ethernet, TAP,
XFRM, and Linux `any` (Ethernet, raw IP, SLL, or SLL2 link types). It does not accept
pcapng; this avoids silently depending on optional Wireshark tooling.

Acceptance requires all of the following:

- both declared user-plane SPIs are present on the UE physical capture;
- every local and remote tcpdump reports zero packets dropped by the kernel or
  interface; absence claims are invalid when the observer lost packets;
- zero clear GRE on physical NWu;
- zero declared user-plane SPIs and zero identifiable clear flow on TAP;
- zero identifiable flow on the signalling XFRM interface;
- zero logical-N3 UDP/2152 packets on Linux and no logical N3 address assigned
  to a Linux interface;
- the unique flow appears in both directions on N6/DN;
- the DN capture contains at least 64 KiB of TCP/UDP payload in each direction,
  so TCP ACKs or iperf control messages cannot substitute for an uplink and a
  downlink payload test;
- N3IWF-DP uplink/downlink counters both increase, sessions/SAs remain active,
  and identity, replay, crypto, malformed, fragment, punt-drop, MAC-change,
  and neighbor-drop counters do not increase.

Retain the complete result directory. `report.md` or a screenshot alone is not
the evidence. In the paper, describe this as “no kernel user-plane crossing”
and state the signalling exception and mlx5 capture limitation alongside it.

## Harness regression check

```bash
bash onvm_test/no_kernel_user_plane/test_no_kernel_user_plane.sh
```
