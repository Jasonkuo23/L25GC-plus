/* SPDX-License-Identifier: Apache-2.0 */
#define _GNU_SOURCE
#include "transport.h"

#include <errno.h>
#include <pthread.h>
#include <sched.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>

#include <rte_cycles.h>
#include <rte_eal.h>
#include <rte_errno.h>
#include <rte_memzone.h>
#include <rte_pause.h>
#include <rte_random.h>
#include <rte_ring.h>

#define N3DP_SHM_MAGIC 0x4e334453U
#define N3DP_SHM_VERSION 1U
#define N3DP_CLOSED (UINT64_C(1) << 63)
#define N3DP_RING_SIZE 2U

struct n3dp_channel {
    uint64_t owner __rte_cache_aligned;
    struct rte_ring *requests;
    struct rte_ring *responses;
    struct n3dp_message request;
    struct n3dp_message response;
};

struct n3dp_shared {
    uint32_t magic;
    uint32_t version;
    uint32_t size;
    uint32_t ready;
    uint64_t server_owner;
    uint64_t epoch;
    struct n3dp_channel channels[N3DP_MAX_CLIENTS];
};

struct n3dp_client {
    struct n3dp_shared *shared;
    unsigned index;
    uint64_t owner;
    uint64_t epoch;
    int outstanding;
};

/* Volatile stores keep traffic-key erasure from being optimized away. */
static void erase(void *data, size_t length)
{
    volatile unsigned char *p = data;
    while (length-- != 0) *p++ = 0;
}

static uint64_t new_owner(void)
{
    return ((rte_rand() & UINT64_C(0x7fffffff)) << 32) |
           (uint32_t)getpid();
}

static int owner_alive(uint64_t owner)
{
    if (owner == 0) return 0;
    if (kill((pid_t)(uint32_t)owner, 0) == 0) return 1;
    return errno != ESRCH;
}

int n3dp_eal_init(int argc, char **argv)
{
    cpu_set_t affinity;
    int saved = pthread_getaffinity_np(pthread_self(), sizeof(affinity), &affinity);
    int rc;
    if (saved != 0) return -saved;
    rc = rte_eal_init(argc, argv);
    int error = rc < 0 ? (rte_errno != 0 ? rte_errno : EIO) : 0;
    /* EAL pins its main thread. Restore the Go runtime thread's original mask;
     * these rings have no lcore-local cache and support non-EAL threads. */
    saved = pthread_setaffinity_np(pthread_self(), sizeof(affinity), &affinity);
    if (error != 0) return -error;
    return saved == 0 ? 0 : -saved;
}

static int valid_shared(const struct rte_memzone *mz)
{
    if (mz == NULL || mz->len < sizeof(struct n3dp_shared)) return 0;
    struct n3dp_shared *s = mz->addr;
    return __atomic_load_n(&s->magic, __ATOMIC_ACQUIRE) == N3DP_SHM_MAGIC &&
           s->version == N3DP_SHM_VERSION && s->size == sizeof(*s);
}

static int create_resources(struct n3dp_shared *s, const char *name)
{
    char resource[RTE_RING_NAMESIZE];
    for (unsigned i = 0; i < N3DP_MAX_CLIENTS; ++i) {
        if (s->channels[i].requests == NULL) {
            snprintf(resource, sizeof(resource), "%s_q%u", name, i);
            s->channels[i].requests = rte_ring_lookup(resource);
            if (s->channels[i].requests == NULL)
                s->channels[i].requests = rte_ring_create(resource, N3DP_RING_SIZE,
                    SOCKET_ID_ANY, RING_F_SP_ENQ | RING_F_SC_DEQ);
        }
        if (s->channels[i].responses == NULL) {
            snprintf(resource, sizeof(resource), "%s_r%u", name, i);
            s->channels[i].responses = rte_ring_lookup(resource);
            if (s->channels[i].responses == NULL)
                s->channels[i].responses = rte_ring_create(resource, N3DP_RING_SIZE,
                    SOCKET_ID_ANY, RING_F_SP_ENQ | RING_F_SC_DEQ);
        }
        if (s->channels[i].requests == NULL || s->channels[i].responses == NULL)
            return -rte_errno;
    }
    return 0;
}

int n3dp_server_open(struct n3dp_server *server, const char *name)
{
    const struct rte_memzone *mz;
    struct n3dp_shared *s;
    uint64_t expected, owner;
    int rc;
    if (server == NULL || name == NULL || name[0] == '\0' || strlen(name) > 20)
        return -EINVAL;
    memset(server, 0, sizeof(*server));
    mz = rte_memzone_lookup(name);
    if (mz == NULL) {
        mz = rte_memzone_reserve_aligned(name, sizeof(*s), SOCKET_ID_ANY, 0,
                                        RTE_CACHE_LINE_SIZE);
        if (mz == NULL) return -rte_errno;
        s = mz->addr;
        memset(s, 0, sizeof(*s));
        s->version = N3DP_SHM_VERSION;
        s->size = sizeof(*s);
        __atomic_store_n(&s->magic, N3DP_SHM_MAGIC, __ATOMIC_RELEASE);
    } else if (!valid_shared(mz)) {
        return -EPROTONOSUPPORT;
    }
    s = mz->addr;
    expected = __atomic_load_n(&s->server_owner, __ATOMIC_ACQUIRE);
    if (owner_alive(expected)) return -EBUSY;
    owner = new_owner();
    if (!__atomic_compare_exchange_n(&s->server_owner, &expected, owner, 0,
                                     __ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE))
        return -EBUSY;
    __atomic_store_n(&s->ready, 0, __ATOMIC_RELEASE);
    rc = create_resources(s, name);
    if (rc != 0) {
        __atomic_store_n(&s->server_owner, 0, __ATOMIC_RELEASE);
        return rc;
    }
    server->shared = s;
    server->owner = owner;
    server->epoch = __atomic_add_fetch(&s->epoch, 1, __ATOMIC_ACQ_REL);
    (void)n3dp_server_maintain(server);
    __atomic_store_n(&s->ready, 1, __ATOMIC_RELEASE);
    return 0;
}

void n3dp_server_release(struct n3dp_server *server, struct n3dp_message *message)
{
    erase(message, sizeof(*message));
    (void)server;
}

static void drain_channel(struct n3dp_server *server, unsigned index)
{
    struct n3dp_channel *channel = &server->shared->channels[index];
    /* The client has closed or died and this DP is the sole consumer. Reset
     * also recovers a ring if a killed SP producer stopped before tail commit.
     * Buffers are embedded in the memzone, so crashes cannot leak pool objects. */
    rte_ring_reset(channel->requests);
    rte_ring_reset(channel->responses);
    erase(&channel->request, sizeof(channel->request));
    erase(&channel->response, sizeof(channel->response));
    /* Only the DP recycles a lease, after the client has stopped accessing it. */
    __atomic_store_n(&channel->owner, 0, __ATOMIC_RELEASE);
}

int n3dp_server_maintain(struct n3dp_server *server)
{
    uint64_t now = rte_get_timer_cycles();
    int check_dead = now >= server->next_reap;
    int changed = 0;
    if (check_dead) server->next_reap = now + rte_get_timer_hz();
    for (unsigned i = 0; i < N3DP_MAX_CLIENTS; ++i) {
        uint64_t owner = __atomic_load_n(&server->shared->channels[i].owner,
                                        __ATOMIC_ACQUIRE);
        if (owner != 0 && ((owner & N3DP_CLOSED) ||
                          (check_dead && !owner_alive(owner)))) {
            drain_channel(server, i);
            changed = 1;
        }
    }
    return changed;
}

uint64_t n3dp_server_client_owner(struct n3dp_server *server, unsigned index)
{
    if (index >= N3DP_MAX_CLIENTS) return 0;
    uint64_t owner = __atomic_load_n(&server->shared->channels[index].owner,
                                    __ATOMIC_ACQUIRE);
    return (owner & N3DP_CLOSED) ? 0 : owner;
}

int n3dp_server_receive(struct n3dp_server *server, unsigned index,
                        struct n3dp_message **message)
{
    if (index >= N3DP_MAX_CLIENTS || server->shared == NULL) return -EINVAL;
    struct n3dp_channel *channel = &server->shared->channels[index];
    if (n3dp_server_client_owner(server, index) == 0) return -EAGAIN;
    if (rte_ring_sc_dequeue(channel->requests, (void **)message) != 0)
        return -EAGAIN;
    if ((*message)->owner != n3dp_server_client_owner(server, index) ||
        (*message)->epoch != server->epoch || (*message)->length > N3DP_MAX_MESSAGE) {
        n3dp_server_release(server, *message);
        *message = NULL;
        return -ESTALE;
    }
    return 0;
}

int n3dp_server_reply(struct n3dp_server *server, unsigned index,
                      uint64_t owner, const void *data, size_t length)
{
    struct n3dp_message *message;
    if (index >= N3DP_MAX_CLIENTS || length > N3DP_MAX_MESSAGE) return -EINVAL;
    if (owner != n3dp_server_client_owner(server, index)) return -ESTALE;
    struct n3dp_channel *channel = &server->shared->channels[index];
    if (rte_ring_free_count(channel->responses) == 0) return -ENOBUFS;
    message = &channel->response;
    message->owner = owner;
    message->epoch = server->epoch;
    message->length = length;
    memcpy(message->data, data, length);
    if (rte_ring_sp_enqueue(server->shared->channels[index].responses, message) != 0) {
        n3dp_server_release(server, message);
        return -ENOBUFS;
    }
    return 0;
}

void n3dp_server_close(struct n3dp_server *server)
{
    if (server == NULL || server->shared == NULL) return;
    __atomic_store_n(&server->shared->ready, 0, __ATOMIC_RELEASE);
    (void)n3dp_server_maintain(server);
    __atomic_store_n(&server->shared->server_owner, 0, __ATOMIC_RELEASE);
    /* Keep resources until primary teardown. Live clients can still hold
     * pointers; a DP restart advances epoch rather than freeing their memory. */
    server->shared = NULL;
}

static int client_valid(struct n3dp_client *client)
{
    struct n3dp_shared *s = client->shared;
    if (!__atomic_load_n(&s->ready, __ATOMIC_ACQUIRE)) return -ENOTCONN;
    if (__atomic_load_n(&s->epoch, __ATOMIC_ACQUIRE) != client->epoch ||
        __atomic_load_n(&s->channels[client->index].owner, __ATOMIC_ACQUIRE) != client->owner)
        return -ESTALE;
    return 0;
}

int n3dp_client_open(const char *name, struct n3dp_client **out)
{
    const struct rte_memzone *mz;
    struct n3dp_client *client;
    if (name == NULL || name[0] == '\0' || strlen(name) > 20 || out == NULL)
        return -EINVAL;
    *out = NULL;
    mz = rte_memzone_lookup(name);
    if (mz == NULL) return -ENOENT;
    if (!valid_shared(mz)) return -EPROTONOSUPPORT;
    struct n3dp_shared *s = mz->addr;
    if (!__atomic_load_n(&s->ready, __ATOMIC_ACQUIRE)) return -ENOTCONN;
    if (!owner_alive(__atomic_load_n(&s->server_owner, __ATOMIC_ACQUIRE)))
        return -ENOTCONN;
    client = calloc(1, sizeof(*client));
    if (client == NULL) return -ENOMEM;
    client->shared = s;
    client->epoch = __atomic_load_n(&s->epoch, __ATOMIC_ACQUIRE);
    client->owner = new_owner();
    for (unsigned i = 0; i < N3DP_MAX_CLIENTS; ++i) {
        uint64_t expected = 0;
        if (__atomic_compare_exchange_n(&s->channels[i].owner, &expected,
                client->owner, 0, __ATOMIC_ACQ_REL, __ATOMIC_ACQUIRE)) {
            client->index = i;
            int rc = client_valid(client);
            if (rc != 0) { n3dp_client_close(client); return rc; }
            *out = client;
            return 0;
        }
    }
    free(client);
    return -ENOSPC;
}

void n3dp_client_close(struct n3dp_client *client)
{
    if (client == NULL) return;
    uint64_t expected = client->owner;
    __atomic_compare_exchange_n(&client->shared->channels[client->index].owner,
        &expected, client->owner | N3DP_CLOSED, 0,
        __ATOMIC_RELEASE, __ATOMIC_RELAXED);
    free(client);
}

int n3dp_client_send(struct n3dp_client *client, const void *data, size_t length)
{
    struct n3dp_message *message;
    int rc = client_valid(client);
    if (rc != 0) return rc;
    if (length == 0 || length > N3DP_MAX_MESSAGE) return -EMSGSIZE;
    if (client->outstanding) return -EBUSY;
    struct n3dp_channel *channel = &client->shared->channels[client->index];
    if (rte_ring_free_count(channel->requests) == 0) return -ENOBUFS;
    message = &channel->request;
    message->owner = client->owner;
    message->epoch = client->epoch;
    message->length = length;
    memcpy(message->data, data, length);
    if (rte_ring_sp_enqueue(client->shared->channels[client->index].requests, message) != 0) {
        erase(message, sizeof(*message));
        return -ENOBUFS;
    }
    client->outstanding = 1;
    return 0;
}

int n3dp_client_receive(struct n3dp_client *client, void *data, size_t capacity,
                        unsigned spin_us)
{
    struct n3dp_message *message;
    uint64_t until = rte_get_timer_cycles() +
                     rte_get_timer_hz() * spin_us / 1000000U;
    if (!client->outstanding) return -EINVAL;
    do {
        int rc = client_valid(client);
        if (rc != 0) return rc;
        if (rte_ring_sc_dequeue(client->shared->channels[client->index].responses,
                                (void **)&message) == 0) {
            rc = -EPROTO;
            if (message->owner == client->owner && message->epoch == client->epoch &&
                message->length > 0 && message->length <= N3DP_MAX_MESSAGE) {
                rc = message->length <= capacity ? (int)message->length : -EMSGSIZE;
                if (rc > 0) memcpy(data, message->data, message->length);
            }
            erase(message, sizeof(*message));
            client->outstanding = 0;
            return rc;
        }
        rte_pause();
    } while (rte_get_timer_cycles() < until);
    return -EAGAIN;
}
