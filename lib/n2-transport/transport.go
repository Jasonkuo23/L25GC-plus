// Package n2transport carries native SCTP/IPv4 over a same-host DPDK link.
// SCTP processing is supplied by pinned usrsctp, not emulated by ring metadata.
package n2transport

/*
#cgo pkg-config: libdpdk
#cgo CFLAGS: -I${SRCDIR}/../../build/n2-usrsctp/install/include
#cgo LDFLAGS: ${SRCDIR}/../../build/n2-usrsctp/install/lib/libusrsctp.a -lpthread
#include <stdlib.h>
#include "bridge.h"
*/
import "C"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

const MaxPDU = 256 * 1024

var openMu sync.Mutex

// Config has no EAL arguments: the caller must initialize its existing EAL
// once, before Open. One endpoint/association per NF process is supported.
type Config struct {
	Name         string        `yaml:"name" valid:"-"`
	LocalIPs     []string      `yaml:"localIPs" valid:"-"`
	PeerIPs      []string      `yaml:"peerIPs" valid:"-"`
	Port         uint16        `yaml:"port" valid:"-"`
	DSCP         uint8         `yaml:"dscp" valid:"-"`
	WriteTimeout time.Duration `yaml:"writeTimeout,omitempty" valid:"-"`
	// Classify decodes NGAP with the caller's pinned library and returns true
	// for UE-associated signalling. Stream 0 is non-UE, stream 1 is UE.
	Classify func([]byte) (bool, error) `yaml:"-" valid:"-"`
}

func (cfg Config) Validate() error {
	if cfg.Name == "" || len(cfg.Name) > 16 || strings.ContainsAny(cfg.Name, "/\\ \t\r\n\x00") {
		return fmt.Errorf("N2 name must contain 1..16 non-space characters without slashes")
	}
	if cfg.WriteTimeout < 0 {
		return fmt.Errorf("N2 writeTimeout must not be negative")
	}
	if cfg.Port == 0 || cfg.DSCP > 63 {
		return fmt.Errorf("N2 port must be nonzero and DSCP <= 63")
	}
	seen := map[string]bool{}
	for _, ips := range [][]string{cfg.LocalIPs, cfg.PeerIPs} {
		if len(ips) < 1 || len(ips) > 4 {
			return fmt.Errorf("N2 requires 1..4 local and peer IPv4 addresses")
		}
		for _, address := range ips {
			ip := net.ParseIP(address)
			if ip == nil || ip.To4() == nil || ip.IsUnspecified() || ip.IsMulticast() || address == "255.255.255.255" {
				return fmt.Errorf("invalid N2 unicast IPv4 address %q", address)
			}
			key := ip.String()
			if seen[key] {
				return fmt.Errorf("duplicate/overlapping N2 address %s", key)
			}
			seen[key] = true
		}
	}
	return nil
}

// UEAssociated traverses decoded NGAP IE fields. It works with both pinned
// free5gc and nycu-ucr types without linking two incompatible NGAP modules.
// UE-associated messages carry an AMF/RAN UE NGAP identity, including unions.
func UEAssociated(pdu interface{}) bool { return hasUE(reflect.ValueOf(pdu), 0) }
func hasUE(v reflect.Value, depth int) bool {
	if depth > 32 || !v.IsValid() {
		return false
	}
	if v.Kind() == reflect.Interface || v.Kind() == reflect.Ptr {
		if v.IsNil() {
			return false
		}
		return hasUE(v.Elem(), depth+1)
	}
	switch v.Kind() {
	case reflect.Struct:
		if v.Type().Name() == "NGReset" || v.Type().Name() == "NGResetAcknowledge" {
			return false
		}
		for i := 0; i < v.NumField(); i++ {
			field := v.Field(i)
			name := v.Type().Field(i).Name
			if (name == "AMFUENGAPID" || name == "RANUENGAPID") && (field.Kind() != reflect.Ptr || !field.IsNil()) {
				return true
			}
			if hasUE(field, depth+1) {
				return true
			}
		}
	case reflect.Slice, reflect.Array:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return false
		}
		for i := 0; i < v.Len(); i++ {
			if hasUE(v.Index(i), depth+1) {
				return true
			}
		}
	}
	return false
}

type Endpoint struct {
	mu         sync.Mutex
	native     *C.struct_n2_endpoint
	cfg        Config
	server     bool
	changed    chan struct{}
	stop, done chan struct{}
	closeOnce  sync.Once
	err        error
	conns      map[*Conn]bool
	claimed    bool
}

func Open(cfg Config, server bool) (*Endpoint, error) {
	openMu.Lock()
	defer openMu.Unlock()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if cfg.Classify == nil {
		return nil, fmt.Errorf("N2 NGAP classifier is required")
	}
	if cfg.WriteTimeout == 0 {
		cfg.WriteTimeout = 5 * time.Second
	}
	cfg.LocalIPs = append([]string(nil), cfg.LocalIPs...)
	cfg.PeerIPs = append([]string(nil), cfg.PeerIPs...)
	var local, peer [4]C.uint32_t
	for i, ip := range cfg.LocalIPs {
		b := net.ParseIP(ip).To4()
		Cmem := unsafe.Slice((*byte)(unsafe.Pointer(&local[i])), 4)
		copy(Cmem, b)
	}
	for i, ip := range cfg.PeerIPs {
		b := net.ParseIP(ip).To4()
		Cmem := unsafe.Slice((*byte)(unsafe.Pointer(&peer[i])), 4)
		copy(Cmem, b)
	}
	name := C.CString(cfg.Name)
	defer C.free(unsafe.Pointer(name))
	ep := &Endpoint{cfg: cfg, server: server, changed: make(chan struct{}), stop: make(chan struct{}), done: make(chan struct{}), conns: make(map[*Conn]bool)}
	role := 0
	if server {
		role = 1
	}
	rc := C.n2_open(name, C.int(role), &local[0], C.uint(len(cfg.LocalIPs)), &peer[0], C.uint(len(cfg.PeerIPs)), C.uint16_t(cfg.Port), C.uint8_t(cfg.DSCP), &ep.native)
	if rc != 0 {
		return nil, fmt.Errorf("open N2 SCTP/IPv4 shared-memory endpoint: %w", syscall.Errno(-rc))
	}
	go ep.pump()
	return ep, nil
}
func (ep *Endpoint) signalLocked() { close(ep.changed); ep.changed = make(chan struct{}) }
func (ep *Endpoint) pump() {
	defer close(ep.done)
	for {
		select {
		case <-ep.stop:
			return
		default:
		}
		ep.mu.Lock()
		rc := C.n2_poll(ep.native)
		if rc < 0 {
			ep.err = fmt.Errorf("N2 memory link lost: %w", syscall.Errno(-rc))
		}
		ep.signalLocked()
		failed := ep.err != nil
		ep.mu.Unlock()
		if failed {
			return
		}
		if rc == 0 {
			C.n2_wait(ep.native, 1000)
		} else {
			runtime.Gosched()
		}
	}
}
func wait(ctx context.Context, changed <-chan struct{}, deadline time.Time) error {
	var timeout <-chan time.Time
	var timer *time.Timer
	if !deadline.IsZero() {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return os.ErrDeadlineExceeded
		}
		timer = time.NewTimer(remaining)
		defer timer.Stop()
		timeout = timer.C
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timeout:
		return os.ErrDeadlineExceeded
	case <-changed:
		return nil
	}
}
func (ep *Endpoint) newConn(s *C.struct_socket) *Conn {
	c := &Conn{ep: ep, socket: s}
	ep.conns[c] = true
	return c
}
func (ep *Endpoint) Accept(ctx context.Context) (*Conn, error) {
	if !ep.server {
		return nil, fmt.Errorf("Accept requires server endpoint")
	}
	for {
		ep.mu.Lock()
		if ep.err != nil {
			err := ep.err
			ep.mu.Unlock()
			return nil, err
		}
		if ep.claimed {
			ep.mu.Unlock()
			return nil, fmt.Errorf("one N2 association per endpoint; restart peers to reconnect")
		}
		var code C.int
		s := C.n2_accept(ep.native, &code)
		if s != nil {
			ep.claimed = true
			c := ep.newConn(s)
			status := C.n2_established(s)
			ep.mu.Unlock()
			if status != 1 {
				c.Close()
				return nil, fmt.Errorf("N2 requires two negotiated SCTP streams")
			}
			return c, nil
		}
		changed := ep.changed
		ep.mu.Unlock()
		if syscall.Errno(code) != syscall.EAGAIN && syscall.Errno(code) != syscall.EWOULDBLOCK {
			return nil, syscall.Errno(code)
		}
		if err := wait(ctx, changed, time.Time{}); err != nil {
			return nil, err
		}
	}
}
func (ep *Endpoint) Dial(ctx context.Context) (*Conn, error) {
	if ep.server {
		return nil, fmt.Errorf("Dial requires client endpoint")
	}
	ep.mu.Lock()
	if ep.err != nil {
		err := ep.err
		ep.mu.Unlock()
		return nil, err
	}
	if ep.claimed {
		ep.mu.Unlock()
		return nil, fmt.Errorf("N2 association already claimed")
	}
	ep.claimed = true
	var code C.int
	s := C.n2_connect(ep.native, &code)
	if s == nil {
		ep.mu.Unlock()
		return nil, syscall.Errno(code)
	}
	c := ep.newConn(s)
	ep.mu.Unlock()
	for {
		ep.mu.Lock()
		err := ep.err
		var status C.int
		if err == nil && !c.closed {
			status = C.n2_established(s)
		} else if err == nil {
			err = net.ErrClosed
		}
		changed := ep.changed
		ep.mu.Unlock()
		if err != nil {
			c.Close()
			return nil, err
		}
		if status == 1 {
			return c, nil
		}
		// Before association creation, ENOENT/EINVAL can be returned by status.
		if status < 0 && syscall.Errno(-status) != syscall.ENOENT && syscall.Errno(-status) != syscall.EINVAL {
			c.Close()
			return nil, syscall.Errno(-status)
		}
		if err := wait(ctx, changed, time.Time{}); err != nil {
			c.Close()
			return nil, err
		}
	}
}
func (ep *Endpoint) Close() error {
	ep.closeOnce.Do(func() {
		close(ep.stop)
		C.n2_wake(ep.native)
		<-ep.done
		ep.mu.Lock()
		defer ep.mu.Unlock()
		for c := range ep.conns {
			if !c.closed {
				C.n2_socket_abort(c.socket)
				c.closed = true
			}
		}
		ep.err = net.ErrClosed
		C.n2_close(ep.native)
		ep.signalLocked()
	})
	return nil
}

// Conn preserves one complete NGAP PDU per Read/Write. A short Read buffer
// fails the association rather than silently truncating an NGAP message.
type Conn struct {
	ep                          *Endpoint
	socket                      *C.struct_socket
	readMu, writeMu             sync.Mutex
	closed                      bool // protected by ep.mu
	readDeadline, writeDeadline time.Time
}

var _ net.Conn = (*Conn)(nil)

func (c *Conn) Read(b []byte) (int, error) {
	c.readMu.Lock()
	defer c.readMu.Unlock()
	if len(b) == 0 {
		return 0, nil
	}
	ep := c.ep
	for {
		ep.mu.Lock()
		if c.closed || ep.err != nil {
			err := ep.err
			if err == nil {
				err = net.ErrClosed
			}
			ep.mu.Unlock()
			return 0, err
		}
		if !c.readDeadline.IsZero() && !time.Now().Before(c.readDeadline) {
			ep.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		var stream C.uint
		rc := C.n2_recv(c.socket, unsafe.Pointer(&b[0]), C.size_t(len(b)), &stream)
		changed, deadline := ep.changed, c.readDeadline
		ep.mu.Unlock()
		if rc > 0 {
			ue, err := ep.cfg.Classify(b[:int(rc)])
			expected := C.uint(0)
			if ue {
				expected = 1
			}
			if err != nil || stream != expected {
				c.Close()
				return 0, fmt.Errorf("invalid NGAP PDU or SCTP stream: %v", err)
			}
			return int(rc), nil
		}
		if rc == 0 {
			return 0, io.EOF
		}
		err := syscall.Errno(-rc)
		if err != syscall.EAGAIN && err != syscall.EWOULDBLOCK {
			c.Close()
			return 0, err
		}
		if err := wait(context.Background(), changed, deadline); err != nil {
			return 0, err
		}
	}
}
func (c *Conn) Write(b []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if len(b) == 0 || len(b) > MaxPDU {
		return 0, syscall.EMSGSIZE
	}
	ue, err := c.ep.cfg.Classify(b)
	if err != nil {
		return 0, err
	}
	stream := C.uint(0)
	if ue {
		stream = 1
	}
	ep := c.ep
	limit := time.Now().Add(ep.cfg.WriteTimeout)
	for {
		ep.mu.Lock()
		if c.closed || ep.err != nil {
			err := ep.err
			if err == nil {
				err = net.ErrClosed
			}
			ep.mu.Unlock()
			return 0, err
		}
		deadline := c.writeDeadline
		if deadline.IsZero() || limit.Before(deadline) {
			deadline = limit
		}
		if !time.Now().Before(deadline) {
			ep.mu.Unlock()
			return 0, os.ErrDeadlineExceeded
		}
		rc := C.n2_send(c.socket, unsafe.Pointer(&b[0]), C.size_t(len(b)), stream)
		changed := ep.changed
		ep.mu.Unlock()
		if rc >= 0 {
			if int(rc) != len(b) {
				c.Close()
				return int(rc), io.ErrShortWrite
			}
			return int(rc), nil
		}
		err := syscall.Errno(-rc)
		if err != syscall.EAGAIN && err != syscall.EWOULDBLOCK && err != syscall.ENOBUFS {
			return 0, err
		}
		if err := wait(context.Background(), changed, deadline); err != nil {
			return 0, err
		}
	}
}
func (c *Conn) Close() error {
	c.ep.mu.Lock()
	defer c.ep.mu.Unlock()
	if !c.closed {
		C.n2_socket_close(c.socket)
		c.closed = true
		delete(c.ep.conns, c)
		c.ep.signalLocked()
	}
	return nil
}

type sctpAddr struct{ net.TCPAddr }

func (sctpAddr) Network() string { return "sctp" }

func (c *Conn) LocalAddr() net.Addr {
	return &sctpAddr{net.TCPAddr{IP: net.ParseIP(c.ep.cfg.LocalIPs[0]), Port: int(c.ep.cfg.Port)}}
}
func (c *Conn) RemoteAddr() net.Addr {
	return &sctpAddr{net.TCPAddr{IP: net.ParseIP(c.ep.cfg.PeerIPs[0]), Port: int(c.ep.cfg.Port)}}
}
func (c *Conn) SetDeadline(t time.Time) error {
	c.ep.mu.Lock()
	defer c.ep.mu.Unlock()
	c.readDeadline = t
	c.writeDeadline = t
	c.ep.signalLocked()
	return nil
}
func (c *Conn) SetReadDeadline(t time.Time) error {
	c.ep.mu.Lock()
	defer c.ep.mu.Unlock()
	c.readDeadline = t
	c.ep.signalLocked()
	return nil
}
func (c *Conn) SetWriteDeadline(t time.Time) error {
	c.ep.mu.Lock()
	defer c.ep.mu.Unlock()
	c.writeDeadline = t
	c.ep.signalLocked()
	return nil
}

// InitializeFixtureEAL is ONLY for isolated tools. Production uses the EAL
// already initialized by ONVM/XIO or N3DP. It never changes an existing EAL.
func InitializeFixtureEAL(args []string) error {
	argv := make([]*C.char, len(args))
	for i, arg := range args {
		argv[i] = C.CString(arg)
		defer C.free(unsafe.Pointer(argv[i]))
	}
	if len(argv) == 0 {
		return errors.New("empty fixture EAL args")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	rc := C.n2_fixture_eal(C.int(len(argv)), &argv[0])
	if rc != 0 {
		return syscall.Errno(-rc)
	}
	return nil
}

// FixtureTrace writes raw IPv4 PCAP. Do not enable for production NAS traffic.
func FixtureTrace(path string) error {
	p := C.CString(path)
	defer C.free(unsafe.Pointer(p))
	rc := C.n2_trace(p)
	if rc < 0 {
		return syscall.Errno(-rc)
	}
	return nil
}
func FixtureDropData(count uint) { C.n2_test_drop_data(C.uint(count)) }
