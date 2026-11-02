package onvmpoller

import (
	"context"
	"errors"
	"net"
)

var errUnavailable = errors.New("ONVM is unavailable in OAuth cache tests")

// Tests may inject a packet-oriented connection without starting ONVM.
var DialContextFunc func(context.Context, string, string) (net.Conn, error)

func DialXIOContext(ctx context.Context, network, address string) (net.Conn, error) {
	if DialContextFunc != nil {
		return DialContextFunc(ctx, network, address)
	}
	return nil, errUnavailable
}

func DialONVM(string, string) (net.Conn, error) { return nil, errUnavailable }
func DialXIO(network, address string) (net.Conn, error) {
	return DialXIOContext(context.Background(), network, address)
}
func ListenONVM(string, string) (net.Listener, error) { return nil, errUnavailable }
func ListenXIO(string, string) (net.Listener, error)  { return nil, errUnavailable }
