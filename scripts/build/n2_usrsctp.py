#!/usr/bin/env python3
"""Build the pinned SCTP stack with a native IPv4 packet I/O adapter.

No system install. --source accepts an existing checkout for offline builds.
The small adapter replaces IP output, not SCTP protocol processing. All output
goes to the application callback; raw sockets are never used in this build.
"""
import argparse
from pathlib import Path
import shutil
import subprocess

REVISION = 'c625e21950600460cb5c6075616eb1622df2b06a'
ROOT = Path(__file__).resolve().parents[2]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--source', type=Path)
    args = parser.parse_args()
    output = ROOT / 'build/n2-usrsctp'
    output.mkdir(parents=True, exist_ok=True)
    upstream = args.source or output / 'upstream'
    if not upstream.exists():
        subprocess.run(['git', 'clone', 'https://github.com/sctplab/usrsctp.git', str(upstream)], check=True)
        subprocess.run(['git', '-C', str(upstream), 'checkout', '--detach', REVISION], check=True)
    actual = subprocess.check_output(['git', '-C', str(upstream), 'rev-parse', 'HEAD'], text=True).strip()
    if actual != REVISION:
        raise SystemExit(f'Expected usrsctp {REVISION}, got {actual}')
    if subprocess.check_output(['git', '-C', str(upstream), 'status', '--porcelain'], text=True).strip():
        raise SystemExit('usrsctp source must be clean')
    source = output / 'source'
    shutil.copytree(upstream, source, ignore=shutil.ignore_patterns('.git'), dirs_exist_ok=True)
    file = source / 'usrsctplib/user_socket.c'
    data = file.read_text()
    anchor = '\tmemset((void *)&dst, 0, sizeof(struct sockaddr_in));'
    assert data.count(anchor) == 1
    hook = '''
    /* L25GC: hand the complete native IPv4 packet to the memory link. */
    {
        extern int l25_n2_ip_output(const void *, size_t);
        unsigned char packet[65535];
        size_t length = ntohs(ip->ip_len);
        if (use_udp_tunneling || length < sizeof(struct ip) || length > sizeof(packet)) {
            *result = EINVAL;
        } else {
            m_copydata(m, 0, (int)length, (caddr_t)packet);
            *result = l25_n2_ip_output(packet, length);
        }
        sctp_m_freem(m);
        return;
    }
'''
    data = data.replace(anchor, hook + anchor)
    data += (ROOT / 'lib/n2-transport/native/ip_adapter.inc').read_text()
    file.write_text(data)
    file = source / 'usrsctplib/netinet/sctp_pcb.c'
    data = file.read_text()
    # Only explicitly registered virtual IPs belong to this stack. Do not
    # advertise host NICs or start an address iterator for the virtual link.
    data = data.replace('\tsctp_startup_iterator();', '''
    if (start_threads) sctp_startup_iterator();
    else {
        SCTP_ITERATOR_LOCK_INIT();
        SCTP_IPI_ITERATOR_WQ_INIT();
        TAILQ_INIT(&sctp_it_ctl.iteratorhead);
    }''')
    data = data.replace('\tsctp_init_vrf_list(SCTP_DEFAULT_VRF);', '\tif (start_threads) sctp_init_vrf_list(SCTP_DEFAULT_VRF);')
    file.write_text(data)
    subprocess.run(['cmake', '-S', str(source), '-B', str(output / 'cmake'),
                    '-DCMAKE_BUILD_TYPE=Release', '-DCMAKE_POSITION_INDEPENDENT_CODE=ON',
                    '-Dsctp_build_programs=OFF', '-Dsctp_debug=OFF', '-Dsctp_inet6=OFF',
                    '-Dsctp_werror=OFF', '-DCMAKE_INSTALL_PREFIX=' + str(output / 'install')], check=True)
    subprocess.run(['cmake', '--build', str(output / 'cmake'), '-j', '4'], check=True)
    subprocess.run(['cmake', '--install', str(output / 'cmake')], check=True)
    license_dir = output / 'install/share/licenses/usrsctp'
    license_dir.mkdir(parents=True, exist_ok=True)
    shutil.copy2(source / 'LICENSE.md', license_dir / 'LICENSE.md')
    (output / 'REVISION').write_text(REVISION + '\n')
    print('Built pinned SCTP/IP stack:', output)


if __name__ == '__main__':
    main()
