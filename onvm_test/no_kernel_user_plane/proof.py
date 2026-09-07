#!/usr/bin/env python3
"""Analyze retained pcaps for the no-kernel-user-plane acceptance gate."""

import argparse
import hashlib
import ipaddress
import pathlib
import re
import struct
import sys


PCAP_MAGIC = {
    b"\xd4\xc3\xb2\xa1": "<",
    b"\xa1\xb2\xc3\xd4": ">",
    b"\x4d\x3c\xb2\xa1": "<",
    b"\xa1\xb2\x3c\x4d": ">",
}
# This is an identity/path proof, not a throughput test.  The floor only needs
# to distinguish an intentional payload transfer from TCP setup/control/ACKs.
MINIMUM_DIRECTION_PAYLOAD_BYTES = 64 * 1024


def die(message):
    raise ValueError(message)


def read_plan(path):
    values = {}
    for number, raw in enumerate(path.read_text().splitlines(), 1):
        if not raw or raw.startswith("#"):
            continue
        fields = raw.split("\t")
        if len(fields) != 2 or not all(fields):
            die(f"invalid plan line {number}")
        values[fields[0]] = fields[1]
    return values


def ipv4(raw):
    return str(ipaddress.IPv4Address(raw))


def decode_frame(frame, linktype):
    if linktype in (12, 101):  # DLT_RAW / LINKTYPE_RAW
        protocol = 0x0800 if frame and frame[0] >> 4 == 4 else 0
        offset = 0
    elif linktype == 1:  # Ethernet
        if len(frame) < 14:
            return None
        protocol = struct.unpack("!H", frame[12:14])[0]
        offset = 14
        while protocol in (0x8100, 0x88A8):
            if len(frame) < offset + 4:
                return None
            protocol = struct.unpack("!H", frame[offset + 2:offset + 4])[0]
            offset += 4
    elif linktype == 113:  # Linux cooked v1
        if len(frame) < 16:
            return None
        protocol = struct.unpack("!H", frame[14:16])[0]
        offset = 16
    elif linktype == 276:  # Linux cooked v2
        if len(frame) < 20:
            return None
        protocol = struct.unpack("!H", frame[0:2])[0]
        offset = 20
    else:
        die(f"unsupported pcap link type {linktype}; use tcpdump on Ethernet, TAP, XFRM, or any")
    if protocol != 0x0800 or len(frame) < offset + 20:
        return {"kind": "other"}
    packet = frame[offset:]
    header_len = (packet[0] & 0x0F) * 4
    if packet[0] >> 4 != 4 or header_len < 20 or len(packet) < header_len:
        return {"kind": "malformed-ipv4"}
    protocol = packet[9]
    result = {
        "kind": "ipv4", "protocol": protocol,
        "source": ipv4(packet[12:16]), "destination": ipv4(packet[16:20]),
    }
    total_length = struct.unpack("!H", packet[2:4])[0]
    payload = packet[header_len:]
    if protocol == 50 and len(payload) >= 4:
        result["spi"] = struct.unpack("!I", payload[:4])[0]
    elif protocol in (6, 17) and len(payload) >= 4:
        result["source_port"], result["destination_port"] = struct.unpack("!HH", payload[:4])
        if protocol == 6 and len(payload) >= 13:
            transport_header_len = (payload[12] >> 4) * 4
            if transport_header_len >= 20:
                result["application_payload_bytes"] = max(
                    0, total_length - header_len - transport_header_len)
        elif protocol == 17 and len(payload) >= 8:
            udp_length = struct.unpack("!H", payload[4:6])[0]
            result["application_payload_bytes"] = max(0, udp_length - 8)
    return result


def read_pcap(path):
    data = path.read_bytes()
    if len(data) < 24 or data[:4] not in PCAP_MAGIC:
        die(f"{path}: not a classic pcap produced by tcpdump")
    endian = PCAP_MAGIC[data[:4]]
    linktype = struct.unpack(endian + "I", data[20:24])[0]
    offset = 24
    packets = []
    while offset < len(data):
        if len(data) < offset + 16:
            die(f"{path}: truncated packet header")
        captured = struct.unpack(endian + "I", data[offset + 8:offset + 12])[0]
        offset += 16
        if len(data) < offset + captured:
            die(f"{path}: truncated packet data")
        packets.append(decode_frame(data[offset:offset + captured], linktype))
        offset += captured
    return [packet for packet in packets if packet is not None]


def read_stats(path):
    values = {}
    for raw in path.read_text().splitlines():
        if "=" not in raw:
            continue
        key, value = raw.split("=", 1)
        if value.isdigit():
            values[key] = int(value)
    return values


def read_capture_drops(path):
    text = path.read_text()
    match = re.search(r"(\d+) packets dropped by kernel", text)
    if match is None:
        die(f"{path}: tcpdump kernel-drop summary is missing")
    kernel = int(match.group(1))
    match = re.search(r"(\d+) packets dropped by interface", text)
    interface = int(match.group(1)) if match else 0
    return kernel, interface


def parse_spi(value):
    try:
        parsed = int(value, 0)
    except ValueError as error:
        raise ValueError(f"invalid SPI {value!r}; use decimal or 0x-prefixed hexadecimal") from error
    if not 0 < parsed <= 0xFFFFFFFF:
        die(f"SPI is outside 1..0xffffffff: {value}")
    return parsed


def flow_direction(packet, plan):
    if not packet or packet.get("kind") != "ipv4":
        return None
    endpoints = (packet.get("source"), packet.get("destination"))
    uplink = (plan["ue_pdu_ipv4"], plan["dn_ipv4"])
    downlink = tuple(reversed(uplink))
    if endpoints not in (uplink, downlink):
        return None
    protocol = plan["traffic_protocol"]
    expected = {"tcp": 6, "udp": 17, "icmp": 1}[protocol]
    if packet.get("protocol") != expected:
        return None
    if protocol != "icmp":
        port = int(plan["traffic_port"])
        if port not in (packet.get("source_port"), packet.get("destination_port")):
            return None
    return "uplink" if endpoints == uplink else "downlink"


def analyze(result_dir):
    plan = read_plan(result_dir / "config" / "proof-plan.tsv")
    required = [
        "captures/ue-access.pcap", "captures/host-access.pcap", "captures/tap.pcap",
        "captures/xfrm.pcap", "captures/kernel-n3.pcap", "captures/dn.pcap",
        "snapshots/before/n3iwf-dp-stats.txt", "snapshots/after/n3iwf-dp-stats.txt",
        "snapshots/before/ip-address.txt", "snapshots/after/ip-address.txt",
        "snapshots/before/status.tsv", "snapshots/after/status.tsv",
        "raw/traffic-ue.log", "raw/traffic-dn.log",
        "raw/ue-access-tcpdump.log", "raw/dn-tcpdump.log",
        "raw/host-access-tcpdump.log", "raw/tap-tcpdump.log",
        "raw/xfrm-tcpdump.log", "raw/kernel-n3-tcpdump.log",
    ]
    for relative in required:
        if not (result_dir / relative).is_file():
            die(f"missing required evidence: {relative}")

    captures = {name: read_pcap(result_dir / "captures" / f"{name}.pcap") for name in (
        "ue-access", "host-access", "tap", "xfrm", "kernel-n3", "dn")}
    inbound_spi = parse_spi(plan["ue_to_n3iwf_spi"])
    outbound_spi = parse_spi(plan["n3iwf_to_ue_spi"])

    def user_spi_count(name, spi):
        return sum(packet.get("protocol") == 50 and packet.get("spi") == spi
                   for packet in captures[name])

    access_ul = user_spi_count("ue-access", inbound_spi)
    access_dl = user_spi_count("ue-access", outbound_spi)
    access_gre = sum(packet.get("protocol") == 47 for packet in captures["ue-access"])
    tap_user_esp = user_spi_count("tap", inbound_spi) + user_spi_count("tap", outbound_spi)
    tap_marker = sum(flow_direction(packet, plan) is not None for packet in captures["tap"])
    xfrm_marker = sum(flow_direction(packet, plan) is not None for packet in captures["xfrm"])
    kernel_n3 = sum(
        packet.get("protocol") == 17 and
        {packet.get("source"), packet.get("destination")} ==
        {plan["logical_n3iwf_ipv4"], plan["logical_upf_ipv4"]} and
        2152 in (packet.get("source_port"), packet.get("destination_port"))
        for packet in captures["kernel-n3"]
    )
    dn_ul = sum(flow_direction(packet, plan) == "uplink" for packet in captures["dn"])
    dn_dl = sum(flow_direction(packet, plan) == "downlink" for packet in captures["dn"])
    dn_ul_bytes = sum(packet.get("application_payload_bytes", 0) for packet in captures["dn"]
                      if flow_direction(packet, plan) == "uplink")
    dn_dl_bytes = sum(packet.get("application_payload_bytes", 0) for packet in captures["dn"]
                      if flow_direction(packet, plan) == "downlink")
    tap_ike = sum(packet.get("protocol") == 17 and
                  bool({packet.get("source_port"), packet.get("destination_port")} & {500, 4500})
                  for packet in captures["tap"])
    tap_other_esp = sum(packet.get("protocol") == 50 and
                        packet.get("spi") not in (inbound_spi, outbound_spi)
                        for packet in captures["tap"])

    before = read_stats(result_dir / "snapshots" / "before" / "n3iwf-dp-stats.txt")
    after = read_stats(result_dir / "snapshots" / "after" / "n3iwf-dp-stats.txt")
    unchanged = ("unknown_teid", "unknown_qfi", "unknown_spi", "malformed_packets",
                 "replay_drops", "crypto_failures", "fragment_drops", "control_punt_drops",
                 "access_mac_changes", "access_neighbor_drops")
    required_stats = ("uplink_packets", "downlink_packets", "active_sessions", "active_child_sas") + unchanged
    for key in required_stats:
        if key not in before or key not in after:
            die(f"missing dataplane statistic {key}")
    deltas = {key: after.get(key, 0) - before.get(key, 0) for key in set(before) | set(after)}

    address_text = "\n".join(
        (result_dir / "snapshots" / point / "ip-address.txt").read_text()
        for point in ("before", "after")
    )
    snapshot_failures = []
    for point in ("before", "after"):
        for raw in (result_dir / "snapshots" / point / "status.tsv").read_text().splitlines():
            fields = raw.split("\t")
            if len(fields) < 2 or not fields[1].isdigit() or int(fields[1]) != 0:
                snapshot_failures.append(f"{point}:{fields[0] if fields else 'invalid'}")
    capture_drops = {}
    for name in ("ue-access", "dn", "host-access", "tap", "xfrm", "kernel-n3"):
        capture_drops[name] = read_capture_drops(result_dir / "raw" / f"{name}-tcpdump.log")
    checks = [
        ("snapshot-observers-succeeded", not snapshot_failures,
         0 if not snapshot_failures else ",".join(snapshot_failures)),
        ("access-user-esp-uplink", access_ul > 0, access_ul),
        ("access-user-esp-downlink", access_dl > 0, access_dl),
        ("access-clear-gre-absent", access_gre == 0, access_gre),
        ("tap-user-spi-absent", tap_user_esp == 0, tap_user_esp),
        ("tap-clear-user-flow-absent", tap_marker == 0, tap_marker),
        ("xfrm-user-flow-absent", xfrm_marker == 0, xfrm_marker),
        ("kernel-n3-gtpu-absent", kernel_n3 == 0, kernel_n3),
        ("dn-marker-uplink", dn_ul > 0, dn_ul),
        ("dn-marker-downlink", dn_dl > 0, dn_dl),
        ("dn-payload-uplink", dn_ul_bytes >= MINIMUM_DIRECTION_PAYLOAD_BYTES, dn_ul_bytes),
        ("dn-payload-downlink", dn_dl_bytes >= MINIMUM_DIRECTION_PAYLOAD_BYTES, dn_dl_bytes),
        ("dataplane-uplink-progress", deltas["uplink_packets"] > 0, deltas["uplink_packets"]),
        ("dataplane-downlink-progress", deltas["downlink_packets"] > 0, deltas["downlink_packets"]),
        ("active-session-retained", before["active_sessions"] > 0 and
         after["active_sessions"] == before["active_sessions"],
         f'{before["active_sessions"]}->{after["active_sessions"]}'),
        ("active-child-sa-retained", before["active_child_sas"] > 0 and
         after["active_child_sas"] == before["active_child_sas"],
         f'{before["active_child_sas"]}->{after["active_child_sas"]}'),
        ("logical-n3-address-not-in-kernel", plan["logical_n3iwf_ipv4"] not in address_text and
         plan["logical_upf_ipv4"] not in address_text, 0),
    ]
    for name, (kernel_drops, interface_drops) in capture_drops.items():
        checks.append((f"lossless-capture-{name}", kernel_drops == 0 and interface_drops == 0,
                       f"kernel={kernel_drops},interface={interface_drops}"))
    for key in unchanged:
        checks.append((f"zero-delta-{key}", deltas.get(key, 0) == 0, deltas.get(key, 0)))

    report = result_dir / "report.md"
    analyzer_hash = hashlib.sha256(pathlib.Path(__file__).read_bytes()).hexdigest()
    with report.open("w") as output:
        output.write("# No-kernel-user-plane proof report\n\n")
        output.write(f"Analyzer SHA-256: `{analyzer_hash}`.\n\n")
        output.write("Claim under test: **UE user packets cross neither Linux nor kernel XFRM/N3; "
                     "IKE and signalling ESP may cross the temporary TAP boundary.**\n\n")
        output.write("| Check | Result | Observed |\n|---|---:|---:|\n")
        for name, passed, observed in checks:
            output.write(f"| `{name}` | {'PASS' if passed else 'FAIL'} | {observed} |\n")
        output.write("\n## Explicit control-traffic accounting\n\n")
        output.write(f"The TAP capture contained {tap_ike} IKE/NAT-T UDP packets and "
                     f"{tap_other_esp} ESP packets with SPIs other than the two user-plane SPIs. "
                     "These are allowed signalling observations, not evidence of a user-plane crossing.\n\n")
        output.write("## mlx5 bifurcation limitation\n\n")
        output.write(f"The L25GC+ host-side physical-access capture contained "
                     f"{len(captures['host-access'])} packets. This count is informational: the bifurcated "
                     "mlx5 PMD can deliver packets to DPDK without exposing them to host tcpdump. The UE-peer "
                     "physical capture is authoritative for NWu wire format.\n\n")
        output.write("Direct logical-N3 delivery is established by the paired N3IWF-DP uplink/downlink "
                     "counter progress, successful bidirectional marker traffic at the DN, the retained ONVM "
                     "service configuration, zero kernel UDP/2152 observations, and absence of logical N3 "
                     "addresses from Linux. No single host pcap can observe packets while they are on ONVM rings.\n")
    passed = all(item[1] for item in checks)
    (result_dir / "gate.status").write_text("PASS\n" if passed else "FAIL\n")
    print(f"proof gate: {'PASS' if passed else 'FAIL'}; report: {report}")
    return 0 if passed else 1


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("result_dir", type=pathlib.Path)
    args = parser.parse_args()
    result_dir = args.result_dir.resolve()
    try:
        (result_dir / "gate.status").write_text("ERROR\n")
        return analyze(result_dir)
    except (OSError, ValueError, KeyError) as error:
        print(f"no-kernel-user-plane: {error}", file=sys.stderr)
        return 2


if __name__ == "__main__":
    sys.exit(main())
