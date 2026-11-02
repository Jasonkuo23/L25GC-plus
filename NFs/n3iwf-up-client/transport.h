/* SPDX-License-Identifier: Apache-2.0 */
#ifndef N3DP_TRANSPORT_H
#define N3DP_TRANSPORT_H

#include <stddef.h>
#include <stdint.h>

#define N3DP_DEFAULT_CONTROL_NAME "N3DP_CTRL"
#define N3DP_MAX_CLIENTS 8U
#define N3DP_MAX_MESSAGE 2048U

struct n3dp_shared;
struct n3dp_client;
struct n3dp_message {
    uint64_t owner;
    uint64_t epoch;
    uint32_t length;
    uint8_t data[N3DP_MAX_MESSAGE];
};

/* Local handles. No Go pointer is stored in DPDK memory. */
struct n3dp_server {
    struct n3dp_shared *shared;
    uint64_t owner;
    uint64_t epoch;
    uint64_t next_reap;
};

int n3dp_eal_init(int argc, char **argv);
int n3dp_server_open(struct n3dp_server *server, const char *name);
void n3dp_server_close(struct n3dp_server *server);
/* Returns 1 when maintenance may have released client leases. */
int n3dp_server_maintain(struct n3dp_server *server);
uint64_t n3dp_server_client_owner(struct n3dp_server *server, unsigned index);
int n3dp_server_receive(struct n3dp_server *server, unsigned index,
                        struct n3dp_message **message);
int n3dp_server_reply(struct n3dp_server *server, unsigned index,
                      uint64_t owner, const void *data, size_t length);
void n3dp_server_release(struct n3dp_server *server, struct n3dp_message *message);

int n3dp_client_open(const char *name, struct n3dp_client **client);
void n3dp_client_close(struct n3dp_client *client);
/* Exactly one outstanding request per handle; callers serialize operations. */
int n3dp_client_send(struct n3dp_client *client, const void *data, size_t length);
/* Bounded spin using rte_pause, no sleep, socket or syscall on the IPC path. */
int n3dp_client_receive(struct n3dp_client *client, void *data, size_t capacity,
                        unsigned spin_us);

#endif
