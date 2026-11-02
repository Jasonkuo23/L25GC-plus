/* SPDX-License-Identifier: Apache-2.0 */
#include "../transport.h"
#include <assert.h>
#include <errno.h>
#include <stdio.h>
#include <string.h>
#include <rte_eal.h>

int main(void)
{
    char *args[] = {"n3dp-transport-test", "-l", "0", "--no-huge",
                   "--no-pci", "--no-shconf", "--no-telemetry"};
    struct n3dp_server server, duplicate;
    struct n3dp_client *clients[N3DP_MAX_CLIENTS], *extra = NULL;
    struct n3dp_message *request;
    unsigned char secret[412], response[N3DP_MAX_MESSAGE];
    memset(secret, 0x91, sizeof(secret));
    assert(rte_eal_init(sizeof(args) / sizeof(args[0]), args) >= 0);
    assert(n3dp_client_open("missing", &extra) == -ENOENT);
    assert(n3dp_server_open(&server, "too_long_for_control_name") == -EINVAL);
    assert(n3dp_server_open(&server, "N3DP_TRANSPORT_TEST") == 0);
    assert(n3dp_server_open(&duplicate, "N3DP_TRANSPORT_TEST") == -EBUSY);
    for (unsigned i = 0; i < N3DP_MAX_CLIENTS; ++i)
        assert(n3dp_client_open("N3DP_TRANSPORT_TEST", &clients[i]) == 0);
    assert(n3dp_client_open("N3DP_TRANSPORT_TEST", &extra) == -ENOSPC);
    assert(n3dp_client_send(clients[0], secret, N3DP_MAX_MESSAGE + 1) == -EMSGSIZE);
    assert(n3dp_client_send(clients[0], secret, sizeof(secret)) == 0);
    assert(n3dp_client_send(clients[0], secret, sizeof(secret)) == -EBUSY);
    assert(n3dp_client_receive(clients[0], response, sizeof(response), 10) == -EAGAIN);
    assert(n3dp_server_receive(&server, 0, &request) == 0);
    assert(request->length == sizeof(secret));
    assert(memcmp(request->data, secret, sizeof(secret)) == 0);
    uint64_t owner = request->owner;
    n3dp_server_release(&server, request);
    /* A persistent memzone buffer must hold no keys after consumption. */
    for (unsigned i = 0; i < sizeof(*request); ++i)
        assert(((unsigned char *)request)[i] == 0);
    assert(n3dp_server_reply(&server, 0, owner, "ACK", 3) == 0);
    assert(n3dp_server_reply(&server, 0, owner, "BAD", 3) == -ENOBUFS);
    assert(n3dp_client_receive(clients[0], response, sizeof(response), 0) == 3);
    assert(memcmp(response, "ACK", 3) == 0);

    /* A timed-out connection may have a queued command. Closing retires the
     * entire lease; maintenance drops it rather than applying it to a new CP. */
    assert(n3dp_client_send(clients[0], secret, sizeof(secret)) == 0);
    n3dp_client_close(clients[0]);
    assert(n3dp_server_maintain(&server) == 1);
    assert(n3dp_client_open("N3DP_TRANSPORT_TEST", &clients[0]) == 0);
    assert(n3dp_server_receive(&server, 0, &request) == -EAGAIN);
    assert(n3dp_server_reply(&server, 0, owner, "OLD", 3) == -ESTALE);

    /* Both queued commands and queued replies from an old DP epoch are invalid. */
    assert(n3dp_client_send(clients[0], secret, sizeof(secret)) == 0);
    assert(n3dp_client_send(clients[1], secret, sizeof(secret)) == 0);
    assert(n3dp_server_receive(&server, 1, &request) == 0);
    owner = request->owner;
    n3dp_server_release(&server, request);
    assert(n3dp_server_reply(&server, 1, owner, "OLD", 3) == 0);
    n3dp_server_close(&server);
    assert(n3dp_client_receive(clients[0], response, sizeof(response), 0) == -ENOTCONN);
    assert(n3dp_client_open("N3DP_TRANSPORT_TEST", &extra) == -ENOTCONN);
    assert(n3dp_server_open(&server, "N3DP_TRANSPORT_TEST") == 0);
    assert(n3dp_server_receive(&server, 0, &request) == -ESTALE);
    assert(n3dp_client_receive(clients[1], response, sizeof(response), 0) == -ESTALE);
    for (unsigned i = 0; i < N3DP_MAX_CLIENTS; ++i) {
        assert(n3dp_client_send(clients[i], secret, sizeof(secret)) == -ESTALE);
        n3dp_client_close(clients[i]);
    }
    assert(n3dp_server_maintain(&server) == 1);
    for (unsigned i = 0; i < N3DP_MAX_CLIENTS; ++i) {
        assert(n3dp_client_open("N3DP_TRANSPORT_TEST", &clients[i]) == 0);
        n3dp_client_close(clients[i]);
    }
    assert(n3dp_server_maintain(&server) == 1);
    n3dp_server_close(&server);
    assert(rte_eal_cleanup() == 0);
    puts("n3dp transport lifecycle tests passed");
    return 0;
}
