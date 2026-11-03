
/* L25GC virtual IPv4 interface. Included in usrsctp user_socket.c by the
 * pinned build script, to use its private mbuf and native SCTP input API.
 * Caller validates IPv4 length/checksum/addresses before handing us a packet.
 * SCTP CRC32C is always verified by the original stack. */
int l25_sctp_register_ipv4(uint32_t address)
{
    struct sockaddr_in sin;
    memset(&sin, 0, sizeof(sin));
    sin.sin_family = AF_INET;
    sin.sin_addr.s_addr = address;
    return sctp_add_addr_to_vrf(SCTP_DEFAULT_VRFID, NULL, 0xfffffffe,
        0, "n2shm", NULL, (struct sockaddr *)&sin, 0, 0) == NULL ? -1 : 0;
}

void l25_sctp_input(const void *packet, size_t length)
{
    struct mbuf *m, *mm;
    struct sockaddr_in src, dst;
    struct ip *ip;
    struct sctphdr *sh;
    struct sctp_chunkhdr *ch;
    int remaining;
    if (length < 36 || length > 65535) return;
    m = sctp_get_mbuf_for_msg((unsigned int)length, 1, M_NOWAIT, 0, MT_DATA);
    if (m == NULL) return;
    remaining = (int)length;
    for (mm = m; mm != NULL; mm = mm->m_next) {
        mm->m_len = min((int)M_SIZE(mm), remaining);
        m->m_pkthdr.len += mm->m_len;
        remaining -= mm->m_len;
    }
    m_copyback(m, 0, (int)length, (caddr_t)packet);
    if (SCTP_BUF_LEN(m) < 36 && (m = m_pullup(m, 36)) == NULL) return;
    ip = mtod(m, struct ip *);
    sh = (struct sctphdr *)((char *)ip + 20);
    ch = (struct sctp_chunkhdr *)((char *)sh + sizeof(*sh));
    memset(&src, 0, sizeof(src));
    memset(&dst, 0, sizeof(dst));
    src.sin_family = dst.sin_family = AF_INET;
    src.sin_addr = ip->ip_src;
    dst.sin_addr = ip->ip_dst;
    src.sin_port = sh->src_port;
    dst.sin_port = sh->dest_port;
    SCTP_STAT_INCR(sctps_recvpackets);
    SCTP_STAT_INCR_COUNTER64(sctps_inpackets);
    sctp_common_input_processing(&m, 20, 32, (int)length,
        (struct sockaddr *)&src, (struct sockaddr *)&dst, sh, ch,
        1, ip->ip_tos & 3, SCTP_DEFAULT_VRFID, 0);
    if (m != NULL) sctp_m_freem(m);
}

#include <netinet/sctp_bsd_addr.h>
/* Drain iterator work under the same caller lock as timers and I/O. */
void l25_sctp_iterator_poll(void)
{
    SCTP_IPI_ITERATOR_WQ_LOCK();
    sctp_iterator_worker();
    SCTP_IPI_ITERATOR_WQ_UNLOCK();
}
