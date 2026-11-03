#define _GNU_SOURCE
#include "bridge.h"
#include <arpa/inet.h>
#include <errno.h>
#include <limits.h>
#include <linux/futex.h>
#include <pthread.h>
#include <sched.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/syscall.h>
#include <sys/time.h>
#include <time.h>
#include <unistd.h>
#include <usrsctp.h>
#include <rte_eal.h>
#include <rte_errno.h>
#include <rte_memzone.h>
#include <rte_ring_elem.h>

#define N2_MAGIC 0x4e325331U
#define N2_VERSION 2U
#define N2_CLOSED (UINT64_C(1) << 63)
#define N2_MTU 1500U
#define N2_CAPACITY 256U
#define N2_MAX_PDU (256U * 1024U)

/* Copy packets into the ring itself: no outstanding pool objects on crash. */
struct n2_packet { uint32_t length; uint8_t data[N2_MTU]; };
struct n2_shared {
    uint32_t magic, version;
    uint64_t epoch, owner[2], startup_lock;
    uint32_t ready;
    uint32_t wake[2], sleeping[2];
    struct rte_ring *ring[2]; /* ring[i] is consumed by endpoint i */
};
struct n2_endpoint {
    struct n2_shared *shared;
    uint64_t epoch, owner, peer_owner, last_tick, last_liveness;
    uint32_t local[4], peer[4];
    unsigned nlocal, npeer, side;
    uint16_t port;
    uint8_t dscp;
    struct socket *listener, *dial;
    int closed;
};
/* One N2 endpoint per NF process; all stack calls serialized by Go's mutex.
 * EAL belongs to ONVM/N3DP and is never initialized or destroyed here. */
static struct n2_endpoint *endpoint;
static unsigned drop_data;
static FILE *capture;
extern int l25_sctp_register_ipv4(uint32_t);
extern void l25_sctp_iterator_poll(void);
extern void l25_sctp_input(const void *, size_t);

static uint64_t now_ms(void) {
    struct timespec t;
    clock_gettime(CLOCK_MONOTONIC, &t);
    return (uint64_t)t.tv_sec * 1000 + t.tv_nsec / 1000000;
}
static int alive(uint64_t owner) {
    if (!owner || (owner & N2_CLOSED)) return 0;
    return kill((pid_t)(uint32_t)owner, 0) == 0 || errno != ESRCH;
}
static int contains(const uint32_t *list, unsigned count, uint32_t ip) {
    for (unsigned i = 0; i < count; i++) if (list[i] == ip) return 1;
    return 0;
}
static uint16_t checksum(const uint8_t *p) {
    uint32_t sum = 0;
    for (unsigned i = 0; i < 20; i += 2) sum += (p[i] << 8) | p[i+1];
    while (sum >> 16) sum = (sum & 0xffff) + (sum >> 16);
    return (uint16_t)~sum;
}
static void signal_side(struct n2_shared *s, unsigned side) {
    __atomic_add_fetch(&s->wake[side], 1, __ATOMIC_RELEASE);
    if (__atomic_load_n(&s->sleeping[side], __ATOMIC_SEQ_CST))
        syscall(SYS_futex, &s->wake[side], FUTEX_WAKE, 1, NULL, NULL, 0);
}
void n2_wake(struct n2_endpoint *ep) { signal_side(ep->shared, ep->side); }
void n2_wait(struct n2_endpoint *ep, unsigned us) {
    struct n2_shared *s = ep->shared;
    unsigned i = ep->side;
    uint32_t seq = __atomic_load_n(&s->wake[i], __ATOMIC_ACQUIRE);
    __atomic_store_n(&s->sleeping[i], 1, __ATOMIC_SEQ_CST);
    if (rte_ring_empty(s->ring[i])) {
        struct timespec t = { .tv_sec = 0, .tv_nsec = (long)us * 1000 };
        syscall(SYS_futex, &s->wake[i], FUTEX_WAIT, seq, &t, NULL, 0);
    }
    __atomic_store_n(&s->sleeping[i], 0, __ATOMIC_SEQ_CST);
}
int n2_trace(const char *path) {
    uint32_t header[] = {0xa1b2c3d4, 0x00040002, 0, 0, 65535, 101}; /* DLT_RAW */
    capture = fopen(path, "wbx");
    if (!capture) return -errno;
    if (fwrite(header, sizeof(header), 1, capture) != 1) return -EIO;
    return 0;
}
void n2_test_drop_data(unsigned count) { drop_data = count; }
int l25_n2_ip_output(const void *data, size_t length) {
    struct n2_endpoint *ep = endpoint;
    struct n2_packet packet = {0};
    uint32_t src, dst;
    if (!ep || ep->closed) return ENETDOWN;
    if (length < 36 || length > N2_MTU) return EMSGSIZE;
    memcpy(packet.data, data, length);
    uint8_t *p = packet.data;
    memcpy(&src, p+12, 4); memcpy(&dst, p+16, 4);
    if (p[0] != 0x45 || p[9] != 132 ||
        !contains(ep->local, ep->nlocal, src) || !contains(ep->peer, ep->npeer, dst)) return EHOSTUNREACH;
    p[1] = (ep->dscp << 2) | (p[1] & 3);
    p[10] = p[11] = 0;
    uint16_t sum = checksum(p);
    p[10] = sum >> 8; p[11] = sum & 255;
    packet.length = (uint32_t)length;
    if (capture) {
        struct timeval t; gettimeofday(&t, NULL);
        uint32_t hdr[] = {(uint32_t)t.tv_sec, (uint32_t)t.tv_usec, (uint32_t)length, (uint32_t)length};
        fwrite(hdr, sizeof(hdr), 1, capture); fwrite(p, length, 1, capture); fflush(capture);
    }
    /* Fixture-only loss injection; SCTP, not the memory link, retransmits. */
    if (drop_data && p[32] == 0) { drop_data--; return 0; }
    if (rte_ring_enqueue_elem(ep->shared->ring[1-ep->side], &packet, sizeof(packet)) != 0) return ENOBUFS;
    signal_side(ep->shared, 1-ep->side);
    return 0;
}

static int set_option(struct socket *s, int level, int option, const void *value, socklen_t length) {
    int rc = usrsctp_setsockopt(s, level, option, value, length);
    if (rc) fprintf(stderr, "N2 SCTP option %d/%d failed: %s\n", level, option, strerror(errno));
    return rc;
}
static struct socket *new_socket(struct n2_endpoint *ep) {
    struct socket *s = usrsctp_socket(AF_INET, SOCK_STREAM, IPPROTO_SCTP, NULL, NULL, 0, NULL);
    if (!s) return NULL;
    int one = 1, buffer = 4 * 1024 * 1024;
    uint32_t partial = N2_MAX_PDU + 1;
    struct sctp_initmsg init = {.sinit_num_ostreams=2, .sinit_max_instreams=16, .sinit_max_attempts=4};
    struct sctp_event ev = {.se_assoc_id=SCTP_ALL_ASSOC, .se_on=1};
    struct sctp_paddrparams path = {0};
    path.spp_address.ss_family = AF_INET;
    path.spp_flags = SPP_PMTUD_DISABLE;
    /* usrsctp socket API counts SCTP payload, excluding IPv4/common header. */
    path.spp_pathmtu = N2_MTU - 20 - 12;
    if (usrsctp_set_non_blocking(s, 1) ||
        set_option(s, IPPROTO_SCTP, SCTP_INITMSG, &init, sizeof(init)) ||
        set_option(s, IPPROTO_SCTP, SCTP_NODELAY, &one, sizeof(one)) ||
        set_option(s, IPPROTO_SCTP, SCTP_RECVRCVINFO, &one, sizeof(one)) ||
        set_option(s, SOL_SOCKET, SO_RCVBUF, &buffer, sizeof(buffer)) ||
        set_option(s, SOL_SOCKET, SO_SNDBUF, &buffer, sizeof(buffer)) ||
        set_option(s, IPPROTO_SCTP, SCTP_PARTIAL_DELIVERY_POINT, &partial, sizeof(partial)) ||
        set_option(s, IPPROTO_SCTP, SCTP_PEER_ADDR_PARAMS, &path, sizeof(path))) goto fail;
    ev.se_type = SCTP_ASSOC_CHANGE;
    if (set_option(s, IPPROTO_SCTP, SCTP_EVENT, &ev, sizeof(ev))) goto fail;
    ev.se_type = SCTP_SHUTDOWN_EVENT;
    if (set_option(s, IPPROTO_SCTP, SCTP_EVENT, &ev, sizeof(ev))) goto fail;
    struct sockaddr_in addresses[4] = {0};
    for (unsigned i=0; i<ep->nlocal; i++) {
        addresses[i].sin_family = AF_INET;
        addresses[i].sin_addr.s_addr = ep->local[i];
        addresses[i].sin_port = htons(ep->port);
    }
    if (usrsctp_bindx(s, (struct sockaddr *)addresses, ep->nlocal, SCTP_BINDX_ADD_ADDR)) { fprintf(stderr,"N2 bindx: %s\n",strerror(errno)); goto fail; }
    return s;
fail: {
        int saved = errno;
        usrsctp_close(s); errno = saved; return NULL;
    }
}
int n2_open(const char *name, int server, const uint32_t *local, unsigned nlocal,
            const uint32_t *peer, unsigned npeer, uint16_t port, uint8_t dscp,
            struct n2_endpoint **out) {
    if (endpoint || !name || strlen(name)>16 || !*name || !nlocal || nlocal>4 || !npeer || npeer>4 || !port || dscp>63) return -EINVAL;
    if (rte_eal_process_type() != RTE_PROC_SECONDARY && rte_eal_process_type() != RTE_PROC_PRIMARY) return -ENODEV;
    const struct rte_memzone *mz = rte_memzone_lookup(name);
    if (!mz && server) {
        mz = rte_memzone_reserve_aligned(name, sizeof(struct n2_shared), SOCKET_ID_ANY, 0, 64);
        if (!mz) return -rte_errno;
        struct n2_shared *s = mz->addr;
        memset(s, 0, sizeof(*s)); s->version=N2_VERSION;
        for (unsigned i=0; i<2; i++) {
            char ring_name[32]; snprintf(ring_name,sizeof(ring_name),"%s_%u",name,i);
            s->ring[i]=rte_ring_create_elem(ring_name,sizeof(struct n2_packet),N2_CAPACITY,SOCKET_ID_ANY,RING_F_SP_ENQ|RING_F_SC_DEQ);
            if (!s->ring[i]) return -rte_errno;
        }
        __atomic_store_n(&s->magic,N2_MAGIC,__ATOMIC_RELEASE);
    }
    if (!mz) return -ENOENT;
    struct n2_shared *s = mz->addr;
    if (mz->len < sizeof(*s) || __atomic_load_n(&s->magic,__ATOMIC_ACQUIRE)!=N2_MAGIC || s->version!=N2_VERSION) return -EPROTO;
    unsigned side = server ? 0 : 1;
    uint64_t owner = ((now_ms() & 0x7fffffff) << 32) | (uint32_t)getpid();
    /* Both roles serialize claims/reset; a dead initializer can be replaced.
     * ready is published only after the listening socket exists. */
    uint64_t lock = __atomic_load_n(&s->startup_lock, __ATOMIC_ACQUIRE);
    if (alive(lock) || !__atomic_compare_exchange_n(&s->startup_lock,&lock,owner,0,__ATOMIC_ACQ_REL,__ATOMIC_ACQUIRE)) return -EBUSY;
    int claim_error = 0;
    if (server) {
        if (alive(__atomic_load_n(&s->owner[0],__ATOMIC_ACQUIRE)) || alive(__atomic_load_n(&s->owner[1],__ATOMIC_ACQUIRE))) claim_error = EBUSY;
        else {
            __atomic_store_n(&s->ready,0,__ATOMIC_RELEASE);
            __atomic_store_n(&s->owner[0],owner,__ATOMIC_RELEASE);
            rte_ring_reset(s->ring[0]); rte_ring_reset(s->ring[1]);
            __atomic_store_n(&s->owner[1],0,__ATOMIC_RELEASE);
            __atomic_add_fetch(&s->epoch,1,__ATOMIC_RELEASE);
        }
    } else {
        if (!__atomic_load_n(&s->ready,__ATOMIC_ACQUIRE) || !alive(__atomic_load_n(&s->owner[0],__ATOMIC_ACQUIRE))) claim_error = ECONNREFUSED;
        else if (__atomic_load_n(&s->owner[1],__ATOMIC_ACQUIRE)) claim_error = EBUSY;
        else __atomic_store_n(&s->owner[1],owner,__ATOMIC_RELEASE);
    }
    __atomic_store_n(&s->startup_lock,0,__ATOMIC_RELEASE);
    if (claim_error) return -claim_error;
    struct n2_endpoint *ep = calloc(1,sizeof(*ep));
    if (!ep) { __atomic_store_n(&s->owner[side],owner|N2_CLOSED,__ATOMIC_RELEASE); return -ENOMEM; }
    ep->shared=s; ep->side=side; ep->owner=owner; ep->epoch=__atomic_load_n(&s->epoch,__ATOMIC_ACQUIRE);
    ep->port=port; ep->dscp=dscp; ep->nlocal=nlocal; ep->npeer=npeer;
    memcpy(ep->local,local,nlocal*4); memcpy(ep->peer,peer,npeer*4);
    ep->last_tick=now_ms(); endpoint=ep;
    usrsctp_init_nothreads(0,NULL,NULL);
    usrsctp_sysctl_set_sctp_ecn_enable(0);
    usrsctp_sysctl_set_sctp_no_csum_on_loopback(0);
    for (unsigned i=0; i<nlocal; i++) if (l25_sctp_register_ipv4(local[i])) { n2_close(ep); return -EADDRNOTAVAIL; }
    if (server) {
        ep->listener=new_socket(ep);
        if (!ep->listener || usrsctp_listen(ep->listener,16)) { int e=errno; n2_close(ep); return -e; }
    }
    if (server) __atomic_store_n(&s->ready,1,__ATOMIC_RELEASE);
    *out=ep; signal_side(s,1-side); return 0;
}
int n2_poll(struct n2_endpoint *ep) {
    if (ep->closed) return -EBADF;
    if (__atomic_load_n(&ep->shared->epoch,__ATOMIC_ACQUIRE)!=ep->epoch) return -ESTALE;
    uint64_t peer=__atomic_load_n(&ep->shared->owner[1-ep->side],__ATOMIC_ACQUIRE);
    if ((peer & N2_CLOSED) || (ep->peer_owner && peer!=ep->peer_owner)) return -ECONNRESET;
    if (peer) ep->peer_owner=peer;
    uint64_t now=now_ms();
    if (now-ep->last_liveness>=500) {
        ep->last_liveness=now;
        if (peer && !alive(peer)) return -ECONNRESET;
    }
    unsigned count=0;
    struct n2_packet packet;
    while (count<64 && !rte_ring_dequeue_elem(ep->shared->ring[ep->side],&packet,sizeof(packet))) {
        count++;
        uint8_t *p=packet.data; uint32_t src,dst;
        memcpy(&src,p+12,4); memcpy(&dst,p+16,4);
        if (packet.length<36 || packet.length>N2_MTU || p[0]!=0x45 || p[9]!=132 ||
            ((unsigned)p[2]<<8 | p[3])!=packet.length || checksum(p)!=0 ||
            (p[6]&0x3f) || p[7] || !contains(ep->local,ep->nlocal,dst) || !contains(ep->peer,ep->npeer,src)) continue;
        l25_sctp_input(p,packet.length);
    }
    if (now>ep->last_tick) {
        uint64_t elapsed=now-ep->last_tick;
        usrsctp_handle_timers(elapsed>UINT32_MAX?UINT32_MAX:(uint32_t)elapsed);
        ep->last_tick=now;
    }
    l25_sctp_iterator_poll();
    return (int)count;
}
struct socket *n2_accept(struct n2_endpoint *ep, int *error) {
    struct socket *s=usrsctp_accept(ep->listener,NULL,NULL);
    *error=s?0:errno; return s;
}
struct socket *n2_connect(struct n2_endpoint *ep, int *error) {
    struct socket *s=new_socket(ep);
    if (!s) { *error=errno; return NULL; }
    struct sockaddr_in addresses[4]={0};
    for (unsigned i=0; i<ep->npeer; i++) {
        addresses[i].sin_family=AF_INET; addresses[i].sin_addr.s_addr=ep->peer[i]; addresses[i].sin_port=htons(ep->port);
    }
    if (usrsctp_connectx(s,(struct sockaddr *)addresses,ep->npeer,NULL) && errno!=EINPROGRESS) {
        *error=errno; usrsctp_close(s); return NULL;
    }
    ep->dial=s; *error=0; return s;
}
int n2_established(struct socket *s) {
    struct sctp_status status={0}; socklen_t len=sizeof(status);
    if (usrsctp_getsockopt(s,IPPROTO_SCTP,SCTP_STATUS,&status,&len)) return -errno;
    if (status.sstat_state!=SCTP_ESTABLISHED) return 0;
    if (status.sstat_outstrms<2 || status.sstat_instrms<2) return -EPROTO;
    return 1;
}
int n2_send(struct socket *s,const void *data,size_t size,unsigned stream) {
    if (!size || size>N2_MAX_PDU || stream>1) return -EMSGSIZE;
    struct sctp_sndinfo info={0}; info.snd_sid=stream; info.snd_ppid=htonl(60);
    ssize_t n=usrsctp_sendv(s,data,size,NULL,0,&info,sizeof(info),SCTP_SENDV_SNDINFO,0);
    return n<0?-errno:(int)n;
}
int n2_recv(struct socket *s,void *data,size_t size,unsigned *stream) {
    for (unsigned i=0;i<16;i++) {
        struct sctp_rcvinfo info={0}; socklen_t len=sizeof(info); unsigned type=0; int flags=0;
        ssize_t n=usrsctp_recvv(s,data,size,NULL,NULL,&info,&len,&type,&flags);
        if (n<=0) return n<0?-errno:0;
        if (flags & MSG_NOTIFICATION) {
            union sctp_notification *event=data;
            if ((size_t)n<sizeof(event->sn_header)) return -EPROTO;
            if (event->sn_header.sn_type==SCTP_SHUTDOWN_EVENT) return 0;
            if (event->sn_header.sn_type==SCTP_ASSOC_CHANGE) {
                if ((size_t)n<sizeof(event->sn_assoc_change)) return -EPROTO;
                unsigned state=event->sn_assoc_change.sac_state;
                if (state==SCTP_COMM_LOST || state==SCTP_CANT_STR_ASSOC || state==SCTP_RESTART) return -ECONNRESET;
                if (state==SCTP_SHUTDOWN_COMP) return 0;
            }
            continue;
        }
        if (!(flags&MSG_EOR)) return -EMSGSIZE;
        if (type!=SCTP_RECVV_RCVINFO || ntohl(info.rcv_ppid)!=60 || info.rcv_sid>1 || (info.rcv_flags&SCTP_UNORDERED)) return -EPROTO;
        *stream=info.rcv_sid; return (int)n;
    }
    return -EAGAIN;
}
void n2_socket_close(struct socket *s) { usrsctp_close(s); }
void n2_socket_abort(struct socket *s) {
    /* Endpoint loss cannot silently retain an association. Abort pending data
     * explicitly; no transparent replay into a replacement association. */
    struct linger linger={.l_onoff=1,.l_linger=0};
    usrsctp_setsockopt(s,SOL_SOCKET,SO_LINGER,&linger,sizeof(linger));
    usrsctp_close(s);
}
void n2_close(struct n2_endpoint *ep) {
    if (!ep || ep->closed) return;
    if (ep->listener) { usrsctp_close(ep->listener); ep->listener=NULL; }
    ep->closed=1;
    __atomic_store_n(&ep->shared->owner[ep->side],ep->owner|N2_CLOSED,__ATOMIC_RELEASE);
    signal_side(ep->shared,1-ep->side); signal_side(ep->shared,ep->side);
    /* Keep tiny local handle and primary-owned shared objects alive. This
     * process-lifetime stack cannot reopen after Close; restart both peers. */
    if (capture) { fclose(capture); capture=NULL; }
}
int n2_fixture_eal(int argc,char **argv) {
    cpu_set_t mask; pthread_getaffinity_np(pthread_self(),sizeof(mask),&mask);
    int rc=rte_eal_init(argc,argv);
    pthread_setaffinity_np(pthread_self(),sizeof(mask),&mask);
    return rc<0?-rte_errno:0;
}
