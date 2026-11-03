#!/usr/bin/env python3
"""Run isolated N2 fixture processes; never attach to the deployed ONVM domain."""
import argparse
from collections import Counter
import json
import os
from pathlib import Path
import signal
import struct
import subprocess
import tempfile
import time


def packets(path):
    data = path.read_bytes()
    assert struct.unpack_from('<I', data)[0] == 0xa1b2c3d4
    offset = 24
    while offset < len(data):
        _, _, length, original = struct.unpack_from('<IIII', data, offset)
        assert length == original
        offset += 16
        yield data[offset:offset + length]
        offset += length
    assert offset == len(data)


CRC_TABLE = []
for index in range(256):
    value = index
    for _ in range(8):
        value = (value >> 1) ^ (0x82f63b78 if value & 1 else 0)
    CRC_TABLE.append(value)


def verify_capture(paths, loss, multi, fragmented=True):
    kinds, streams, fragments, repeats, addresses = set(), set(), 0, 0, set()
    for path in paths:
        tsns = Counter()
        for packet in packets(path):
            assert packet[0] == 0x45 and packet[9] == 132
            assert len(packet) == struct.unpack_from('!H', packet, 2)[0] <= 1500
            words = sum(struct.unpack_from('!10H', packet))
            while words >> 16:
                words = (words & 65535) + (words >> 16)
            assert words == 65535, 'IPv4 checksum'
            sctp = bytearray(packet[20:])
            expected = struct.unpack_from('<I', sctp, 8)[0]
            sctp[8:12] = b'\0' * 4
            crc = 0xffffffff
            for byte in sctp:
                crc = CRC_TABLE[(crc ^ byte) & 255] ^ (crc >> 8)
            assert (crc ^ 0xffffffff) == expected, 'SCTP CRC32C'
            assert struct.unpack_from('!HH', sctp) == (38412, 38412)
            offset = 12
            while offset + 4 <= len(sctp):
                kind, flags, length = struct.unpack_from('!BBH', sctp, offset)
                assert length >= 4 and offset + length <= len(sctp)
                kinds.add(kind)
                if kind == 0:
                    tsn, stream, _, ppid = struct.unpack_from('!IHHI', sctp, offset + 4)
                    assert ppid == 60 and stream in (0, 1) and not flags & 4
                    streams.add(stream)
                    tsns[tsn] += 1
                    fragments += (flags & 3) != 3
                if kind in (1, 2):
                    outbound, inbound = struct.unpack_from('!HH', sctp, offset + 12)
                    assert outbound >= 2 and inbound >= 2
                    param = offset + 20
                    while param + 4 <= offset + length:
                        typ, size = struct.unpack_from('!HH', sctp, param)
                        assert size >= 4
                        if typ == 5:
                            addresses.add(bytes(sctp[param+4:param+size]))
                        param += (size + 3) & ~3
                offset += (length + 3) & ~3
        repeats += sum(count - 1 for count in tsns.values())
    assert {0, 1, 2, 3, 7, 8, 10, 11, 14} <= kinds, kinds
    assert streams == {0, 1}
    if fragmented:
        assert fragments > 0
    if loss:
        assert repeats >= 2, 'no evidence of DATA retransmission in both directions'
    if multi:
        assert len(addresses) >= 4, 'multihoming addresses not advertised'
    return dict(chunk_types=sorted(kinds), streams=sorted(streams),
                fragmented_chunks=fragments, retransmitted_tsns=repeats,
                advertised_ipv4=len(addresses), checksums='PASS')


def main():
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument('--fixture', default='/tmp/n2fixture')
    p.add_argument('--server-fixture')
    p.add_argument('--client-fixture')
    p.add_argument('--vectors')
    p.add_argument('--drop', type=int, default=0)
    p.add_argument('--multi', action='store_true')
    p.add_argument('--restart', action='store_true')
    p.add_argument('--scenario', choices=['exchange', 'crash', 'deadline', 'backpressure', 'shortread'], default='exchange')
    p.add_argument('--cores', default='16,17,18,19', help='primary,server,client,duplicate lcores')
    args = p.parse_args()
    cores = [int(x) for x in args.cores.split(',')]
    assert len(cores) == 4 and len(set(cores)) == 4
    out = Path(tempfile.mkdtemp(prefix='n2-sctp-integration-', dir='/tmp'))
    env = dict(os.environ, RUNTIME_DIRECTORY=str(out))
    prefix = 'n2_' + out.name.rsplit('-', 1)[-1]
    processes, logs, evidence = [], [], []

    def start(role, core, label, scenario=args.scenario):
        eal = f'-l {core} --file-prefix {prefix} --no-pci --no-telemetry'
        eal += ' --huge-dir /mnt/huge -m 64' if role == 'primary' else ' --proc-type=secondary'
        log = (out / (label + '.log')).open('w')
        logs.append(log)
        binary = {'server':args.server_fixture,'client':args.client_fixture}.get(role) or args.fixture
        cmd = [binary, '-role', role, '-eal', eal, '-scenario', scenario]
        if args.vectors and role != 'primary':
            cmd += ['-vectors',str(Path(args.vectors).resolve())]
        if role != 'primary':
            cmd += ['-pcap', str(out / (label + '.pcap')), '-drop', str(args.drop)]
            if args.multi:
                cmd += ['-multi']
        proc = subprocess.Popen(cmd, env=env, stdout=log, stderr=subprocess.STDOUT)
        processes.append(proc)
        return proc

    def ready(proc, label, marker):
        until = time.monotonic() + 15
        path = out / (label + '.log')
        while marker not in path.read_text():
            if proc.poll() is not None or time.monotonic() > until:
                raise RuntimeError(path.read_text())
            time.sleep(.02)

    def finish(proc, label):
        if proc.wait(timeout=45) != 0:
            raise RuntimeError((out / (label + '.log')).read_text())

    try:
        primary = start('primary', cores[0], 'primary')
        ready(primary, 'primary', 'PRIMARY READY')
        for iteration in range(2 if args.restart else 1):
            sl, cl = f'server{iteration}', f'client{iteration}'
            server = start('server', cores[1], sl)
            ready(server, sl, 'ENDPOINT READY')
            client = start('client', cores[2], cl)
            if args.scenario == 'crash':
                ready(client, cl, 'ASSOCIATION READY')
                dl = f'duplicate{iteration}'
                duplicate = start('client', cores[3], dl, 'duplicate')
                finish(duplicate, dl)
                before = time.monotonic()
                client.kill()
                client.wait()
                finish(server, sl)
                elapsed = time.monotonic() - before
                assert elapsed < 2, elapsed
                evidence.append(dict(peer_death_seconds=elapsed, duplicate='rejected'))
            else:
                finish(client, cl)
                finish(server, sl)
                if args.scenario == 'exchange':
                    evidence.append(verify_capture([out / (x + '.pcap') for x in (sl, cl)], args.drop, args.multi, not args.vectors))
        (out / 'manifest.json').write_text(json.dumps(dict(prefix=prefix, options=vars(args), evidence=evidence, result='PASS'), indent=2))
        print('PASS:', out)
    finally:
        for proc in reversed(processes):
            if proc.poll() is None:
                proc.send_signal(signal.SIGTERM)
                try:
                    proc.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    proc.kill()
                    proc.wait()
        for log in logs:
            log.close()
        # Let the invoking developer inspect artifacts without more sudo calls.
        if os.environ.get('SUDO_UID'):
            uid, gid = int(os.environ['SUDO_UID']), int(os.environ['SUDO_GID'])
            for path in out.iterdir():
                if path.is_file():
                    os.chown(path, uid, gid)
            os.chown(out, uid, gid)
        print('Artifacts:', out)


if __name__ == '__main__':
    main()
