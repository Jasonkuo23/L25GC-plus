#ifndef L25_N2_BRIDGE_H
#define L25_N2_BRIDGE_H
#include <stddef.h>
#include <stdint.h>
struct n2_endpoint;
struct socket;
int n2_open(const char *name, int server, const uint32_t *local, unsigned nlocal,
            const uint32_t *peer, unsigned npeer, uint16_t port, uint8_t dscp,
            struct n2_endpoint **out);
int n2_poll(struct n2_endpoint *ep);
void n2_wait(struct n2_endpoint *ep, unsigned microseconds);
void n2_wake(struct n2_endpoint *ep);
void n2_close(struct n2_endpoint *ep);
struct socket *n2_accept(struct n2_endpoint *ep, int *error);
struct socket *n2_connect(struct n2_endpoint *ep, int *error);
int n2_established(struct socket *s);
int n2_send(struct socket *s, const void *data, size_t size, unsigned stream);
int n2_recv(struct socket *s, void *data, size_t size, unsigned *stream);
void n2_socket_close(struct socket *s);
void n2_socket_abort(struct socket *s);
int n2_fixture_eal(int argc, char **argv);
void n2_test_drop_data(unsigned count);
int n2_trace(const char *path);
#endif
