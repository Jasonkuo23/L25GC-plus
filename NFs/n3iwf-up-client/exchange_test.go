package n3iwfup

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"
)

type fakeTransport struct {
	requests        [][]byte
	last            []byte
	closed          bool
	waitForCancel   bool
	wrongGeneration bool
}

func (f *fakeTransport) send(request []byte) error {
	f.last = append([]byte(nil), request...)
	f.requests = append(f.requests, f.last)
	return nil
}
func (f *fakeTransport) receive(ctx context.Context) ([]byte, error) {
	if f.waitForCancel {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	generation := binary.BigEndian.Uint64(f.last[16:24])
	if f.wrongGeneration {
		generation++
	}
	payload := make([]byte, 16)
	binary.BigEndian.PutUint64(payload[8:16], generation)
	return marshalMessage(MessageACK, binary.BigEndian.Uint32(f.last[12:16]), generation, payload), nil
}
func (f *fakeTransport) close() { f.closed = true }

func TestCanceledContextDoesNotAttach(t *testing.T) {
	c := New("unused")
	c.open = func(Options) (packetTransport, error) { t.Fatal("attached canceled client"); return nil, nil }
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Hello(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestContextCanceledDuringAttachDoesNotSend(t *testing.T) {
	c := New("unused")
	fake := &fakeTransport{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c.open = func(Options) (packetTransport, error) { cancel(); return fake, nil }
	if err := c.DeleteSession(ctx, 4, 1, 10); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(fake.requests) != 0 || !fake.closed {
		t.Fatal("sent after attach exhausted context")
	}
}

func TestTimeoutRetiresLeaseAndReconnectsRoleWithoutReplayingUpdate(t *testing.T) {
	first, second := &fakeTransport{}, &fakeTransport{}
	opened := 0
	c := New("unused")
	c.open = func(Options) (packetTransport, error) {
		opened++
		if opened == 1 {
			return first, nil
		}
		return second, nil
	}
	if err := c.Hello(context.Background()); err != nil {
		t.Fatal(err)
	}
	first.waitForCancel = true
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	if err := c.DeleteSession(ctx, 2, 1, 10); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if !first.closed {
		t.Fatal("timed-out lease retained")
	}
	if err := c.DeleteSession(context.Background(), 3, 1, 10); err != nil {
		t.Fatal(err)
	}
	if len(first.requests) != 2 || len(second.requests) != 2 {
		t.Fatal("unexpected replay")
	}
	if binary.BigEndian.Uint16(second.requests[0][6:8]) != MessageHello || second.requests[0][24] != RoleWriter {
		t.Fatal("writer role was not reacquired")
	}
	if binary.BigEndian.Uint64(second.requests[1][16:24]) != 3 {
		t.Fatal("old update replayed")
	}
}

func TestACKMustMatchRequestedGeneration(t *testing.T) {
	c := New("unused")
	c.open = func(Options) (packetTransport, error) { return &fakeTransport{wrongGeneration: true}, nil }
	if err := c.DeleteSession(context.Background(), 4, 1, 10); err == nil {
		t.Fatal("wrong-generation ACK accepted")
	}
}

func TestConcurrentCommandsSerializeAndTraceOutsideMutex(t *testing.T) {
	fake := &fakeTransport{}
	c := New("unused")
	c.open = func(Options) (packetTransport, error) { return fake, nil }
	c.options.Trace = func(_ uint16, _ uint64, _ int64, _ error) {
		if !c.mu.TryLock() {
			t.Error("trace called with mutex held")
		} else {
			c.mu.Unlock()
		}
	}
	// Check callback reentry separately; another concurrent caller can hold mu.
	if err := c.Hello(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.options.Trace = nil
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(generation uint64) {
			defer wg.Done()
			if err := c.DeleteSession(context.Background(), generation, 1, 10); err != nil {
				t.Error(err)
			}
		}(uint64(i + 1))
	}
	wg.Wait()
	for i, request := range fake.requests {
		if binary.BigEndian.Uint32(request[12:16]) != uint32(i+1) {
			t.Fatal("transaction sequence")
		}
	}
}
