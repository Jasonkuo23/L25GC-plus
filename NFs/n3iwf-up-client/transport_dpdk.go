package n3iwfup

/*
#cgo pkg-config: libdpdk
#cgo CFLAGS: -pthread
#cgo LDFLAGS: -pthread
#include <stdlib.h>
#include "transport.h"
*/
import "C"

import (
	"context"
	"fmt"
	"reflect"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

const DefaultControlName = "N3DP_CTRL"

// Options identifies a named DPDK memzone in the ONVM primary's memory domain.
// EALArgs must use a distinct lcore and the primary's file-prefix/memory flags.
// The process is always a secondary; initialization runs once per process.
type Options struct {
	ControlName string
	EALArgs     []string
	// Trace receives operation RTTs, including queue wait and ACK, without keys.
	// It runs synchronously after releasing the client mutex.
	Trace func(messageType uint16, generation uint64, elapsedNanos int64, err error)
}

var eal struct {
	sync.Mutex
	initialized bool
	args        []string
	err         error
}

func normalizedEALArgs(args []string) []string {
	if len(args) == 0 {
		args = []string{"-l", "15", "-n", "3"}
	}
	return append([]string{"n3iwf-cp", "--proc-type=secondary", "--no-pci", "--no-telemetry"}, args...)
}

func initEAL(args []string) error {
	args = normalizedEALArgs(args)
	eal.Lock()
	defer eal.Unlock()
	if eal.initialized {
		if !reflect.DeepEqual(args, eal.args) {
			return fmt.Errorf("DPDK EAL already initialized with different arguments")
		}
		return eal.err
	}
	// Do not allow later flags to override the mandatory secondary process type.
	for _, arg := range args[4:] {
		if arg == "--proc-type" || len(arg) >= 12 && arg[:12] == "--proc-type=" ||
			arg == "--no-shconf" || arg == "--in-memory" || arg == "--no-huge" {
			return fmt.Errorf("unsupported secondary EAL argument %q", arg)
		}
	}
	eal.initialized = true
	eal.args = append([]string(nil), args...)
	argv := make([]*C.char, len(args))
	for i, arg := range args {
		argv[i] = C.CString(arg)
		defer C.free(unsafe.Pointer(argv[i]))
	}
	runtime.LockOSThread()
	rc := C.n3dp_eal_init(C.int(len(argv)), &argv[0])
	runtime.UnlockOSThread()
	if rc != 0 {
		eal.err = fmt.Errorf("initialize DPDK secondary (start ONVM manager first; match lcore, file-prefix and memory flags): %w", syscall.Errno(-rc))
	}
	return eal.err
}

type packetTransport interface {
	send([]byte) error
	receive(context.Context) ([]byte, error)
	close()
}

type dpdkTransport struct{ client *C.struct_n3dp_client }

func openDPDK(opts Options) (packetTransport, error) {
	if opts.ControlName == "" || len(opts.ControlName) > 20 {
		return nil, fmt.Errorf("DPDK control name must contain 1..20 characters")
	}
	if err := initEAL(opts.EALArgs); err != nil {
		return nil, err
	}
	name := C.CString(opts.ControlName)
	defer C.free(unsafe.Pointer(name))
	t := &dpdkTransport{}
	if rc := C.n3dp_client_open(name, &t.client); rc != 0 {
		return nil, fmt.Errorf("attach N3DP memzone %q: %w", opts.ControlName, syscall.Errno(-rc))
	}
	return t, nil
}

func (t *dpdkTransport) send(data []byte) error {
	if rc := C.n3dp_client_send(t.client, unsafe.Pointer(&data[0]), C.size_t(len(data))); rc != 0 {
		return syscall.Errno(-rc)
	}
	return nil
}

func (t *dpdkTransport) receive(ctx context.Context) ([]byte, error) {
	response := make([]byte, 2048)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rc := C.n3dp_client_receive(t.client, unsafe.Pointer(&response[0]), C.size_t(len(response)), 25)
		if rc > 0 {
			return response[:int(rc)], nil
		}
		if syscall.Errno(-rc) != syscall.EAGAIN {
			return nil, syscall.Errno(-rc)
		}
		// Only pending requests spin. Yield between bounded C polls so cancellation
		// and other Go work can run; idle CP does not have a polling goroutine.
		runtime.Gosched()
	}
}

func (t *dpdkTransport) close() {
	C.n3dp_client_close(t.client)
	t.client = nil
}
