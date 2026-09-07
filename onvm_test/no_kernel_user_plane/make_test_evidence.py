#!/usr/bin/env python3
"""Create small synthetic pcaps for the proof harness regression test only."""

import pathlib
import socket
import struct
import sys


def ipv4(source, destination, protocol, payload):
    header = bytearray(20)
    header[0] = 0x45
    struct.pack_into("!H", header, 2, len(header) + len(payload))
    header[8] = 64
    header[9] = protocol
    header[12:16] = socket.inet_aton(source)
    header[16:20] = socket.inet_aton(destination)
    return bytes(header) + payload


def ethernet(packet):
    return b"\x02\x00\x00\x00\x00\x02\x02\x00\x00\x00\x00\x01\x08\x00" + packet


def esp(source, destination, spi):
    return ethernet(ipv4(source, destination, 50, struct.pack("!II", spi, 1)))


def udp(source, destination, source_port, destination_port):
    payload = struct.pack("!HHHH", source_port, destination_port, 8, 0)
    return ethernet(ipv4(source, destination, 17, payload))


def tcp(source, destination, source_port, destination_port, payload_size=0):
    header = bytearray(20)
    struct.pack_into("!HH", header, 0, source_port, destination_port)
    header[12] = 0x50
    payload = bytes(header) + bytes(payload_size)
    return ethernet(ipv4(source, destination, 6, payload))


def write_pcap(path, packets, linktype=1):
    with path.open("wb") as output:
        output.write(struct.pack("<IHHIIII", 0xA1B2C3D4, 2, 4, 0, 0, 65535, linktype))
        for index, packet in enumerate(packets, 1):
            output.write(struct.pack("<IIII", index, 0, len(packet), len(packet)))
            output.write(packet)


def main():
    result = pathlib.Path(sys.argv[1])
    captures = result / "captures"
    write_pcap(captures / "ue-access.pcap", [
        esp("192.168.2.1", "192.168.2.2", 0x1001),
        esp("192.168.2.2", "192.168.2.1", 0x1002),
    ])
    write_pcap(captures / "host-access.pcap", [])
    write_pcap(captures / "tap.pcap", [
        udp("192.168.2.1", "192.168.2.2", 500, 500),
        esp("192.168.2.1", "192.168.2.2", 0x2001),
    ])
    write_pcap(captures / "xfrm.pcap", [
        ipv4("10.0.0.2", "10.0.0.1", 6, struct.pack("!HH", 20000, 20000) + bytes(16)),
    ], 101)
    write_pcap(captures / "kernel-n3.pcap", [], 276)
    write_pcap(captures / "dn.pcap",
               [tcp("10.60.0.1", "192.168.3.2", 40000, 5503, 60000) for _ in range(18)] +
               [tcp("192.168.3.2", "10.60.0.1", 5503, 40000, 60000) for _ in range(18)])
    counters = (
        "unknown_teid", "unknown_qfi", "unknown_spi", "malformed_packets", "replay_drops",
        "crypto_failures", "fragment_drops", "control_punt_drops", "access_mac_changes",
        "access_neighbor_drops",
    )
    for name, uplink, downlink in (("before", 10, 20), ("after", 11, 21)):
        directory = result / "snapshots" / name
        directory.mkdir(parents=True, exist_ok=True)
        lines = [f"uplink_packets={uplink}", f"downlink_packets={downlink}",
                 "active_sessions=1", "active_child_sas=1"]
        lines.extend(f"{counter}=0" for counter in counters)
        (directory / "n3iwf-dp-stats.txt").write_text("\n".join(lines) + "\n")
        (directory / "ip-address.txt").write_text("1: lo: <LOOPBACK> mtu 65536\n")
        (directory / "status.tsv").write_text("synthetic-observer\t0\tsynthetic\n")
    (result / "raw" / "traffic-ue.log").write_text("synthetic bidirectional client success\n")
    (result / "raw" / "traffic-dn.log").write_text("synthetic bidirectional server success\n")
    for name in ("ue-access", "dn", "host-access", "tap", "xfrm", "kernel-n3"):
        (result / "raw" / f"{name}-tcpdump.log").write_text(
            "2 packets captured\n2 packets received by filter\n0 packets dropped by kernel\n"
        )


if __name__ == "__main__":
    main()
