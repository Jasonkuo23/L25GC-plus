package http2

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nycu-ucr/gonet/http"
	"github.com/nycu-ucr/onvmpoller"
)

type serverReadStep struct {
	data []byte
	err  error
}

type serverReadConn struct {
	net.Conn
	steps []serverReadStep
	reads int
}

func (c *serverReadConn) Read(b []byte) (int, error) {
	c.reads++
	if len(c.steps) == 0 {
		return 0, io.EOF
	}
	step := c.steps[0]
	n := copy(b, step.data)
	if n < len(step.data) {
		c.steps[0].data = step.data[n:]
		return n, nil
	}
	c.steps = c.steps[1:]
	return n, step.err
}

func TestONVMServerReadErrorStopsReader(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://nrf.test/nnrf-nfm/v1/nf-instances", nil)
	frame, err := FastEncodeRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	readErr := errors.New("synthetic read failure")
	for _, tc := range []struct {
		name        string
		steps       []serverReadStep
		wantErr     error
		wantLog     string
		wantRequest bool
	}{
		{name: "idle peer close", steps: []serverReadStep{{err: io.EOF}}, wantErr: io.EOF},
		{name: "partial packet with EOF", steps: []serverReadStep{{data: []byte("partial"), err: io.EOF}}, wantErr: io.ErrUnexpectedEOF, wantLog: "unexpected EOF"},
		{name: "partial packet before EOF", steps: []serverReadStep{{data: []byte("partial")}, {err: io.EOF}}, wantErr: io.ErrUnexpectedEOF, wantLog: "unexpected EOF"},
		{name: "read failure", steps: []serverReadStep{{err: readErr}}, wantErr: readErr, wantLog: readErr.Error()},
		{name: "fragmented request then idle close", steps: []serverReadStep{{data: frame[:len(frame)/2]}, {data: frame[len(frame)/2:], err: errors.New("EOP")}, {err: io.EOF}}, wantErr: io.EOF, wantRequest: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var logs bytes.Buffer
			previousOutput := logg.Out
			logg.SetOutput(&logs)
			defer logg.SetOutput(previousOutput)
			c := &serverReadConn{steps: append([]serverReadStep(nil), tc.steps...)}
			oc := &onvmConn{conn: c, readConnErrCh: make(chan error), httpRequestCh: make(chan *http.Request, 1)}
			done := make(chan struct{})
			go func() { defer close(done); oc.readRequest() }()
			select {
			case got := <-oc.readConnErrCh:
				if got != tc.wantErr {
					t.Errorf("terminal error = %v, want %v", got, tc.wantErr)
				}
			case <-time.After(time.Second):
				t.Fatal("reader did not report its terminal error")
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("reader remained blocked after its terminal error was consumed")
			}
			if c.reads != len(tc.steps) {
				t.Errorf("Read called %d times, want %d", c.reads, len(tc.steps))
			}
			if tc.wantLog == "" {
				if logs.Len() != 0 {
					t.Errorf("normal peer close logged an error: %s", logs.String())
				}
			} else if got := logs.String(); !strings.Contains(got, tc.wantLog) || strings.Count(got, "net.Conn.Read error") != 1 {
				t.Errorf("expected one error containing %q, got %q", tc.wantLog, got)
			}
			if tc.wantRequest {
				select {
				case got := <-oc.httpRequestCh:
					if got.Method != req.Method || got.URL.String() != req.URL.String() {
						t.Errorf("decoded request = %v %v", got.Method, got.URL)
					}
				default:
					t.Error("complete request was lost before the peer closed")
				}
			} else if len(oc.httpRequestCh) != 0 {
				t.Error("incomplete packet was dispatched as a request")
			}
		})
	}
}

// Emulate XIO message boundaries and blocking reads; no ONVM manager is needed.
type regressionConn struct {
	mu          sync.Mutex
	frames      chan []byte
	closed      chan struct{}
	once        sync.Once
	readStarted chan struct{}
	readOnce    sync.Once
	response    []byte
	remaining   []byte
	blockWrite  bool
	closeCount  int32
}

func newRegressionConn(response []byte) *regressionConn {
	return &regressionConn{frames: make(chan []byte, 4), closed: make(chan struct{}), readStarted: make(chan struct{}), response: response}
}

func (c *regressionConn) Read(b []byte) (int, error) {
	c.readOnce.Do(func() { close(c.readStarted) })
	if len(c.remaining) == 0 {
		select {
		case <-c.closed:
			return 0, net.ErrClosed
		case c.remaining = <-c.frames:
		}
	}
	n := copy(b, c.remaining)
	c.remaining = c.remaining[n:]
	if len(c.remaining) == 0 {
		return n, errors.New("EOP")
	}
	return n, nil
}

func (c *regressionConn) Write(b []byte) (int, error) {
	if c.blockWrite {
		<-c.closed
		return 0, net.ErrClosed
	}
	if string(b) != string(upgradePreface) && c.response != nil {
		c.frames <- append([]byte(nil), c.response...)
	}
	return len(b), nil
}
func (c *regressionConn) Close() error {
	c.once.Do(func() { atomic.AddInt32(&c.closeCount, 1); close(c.closed) })
	return nil
}
func (c *regressionConn) LocalAddr() net.Addr              { return &net.TCPAddr{Port: 8001} }
func (c *regressionConn) RemoteAddr() net.Addr             { return &net.TCPAddr{Port: 8000} }
func (c *regressionConn) SetDeadline(time.Time) error      { return nil }
func (c *regressionConn) SetReadDeadline(time.Time) error  { return nil }
func (c *regressionConn) SetWriteDeadline(time.Time) error { return nil }

func regressionTransport(t *testing.T, conn net.Conn) *OnvmTransport {
	t.Helper()
	onvmpoller.DialContextFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { return conn, ctx.Err() }
	t.Cleanup(func() { onvmpoller.DialContextFunc = nil })
	return &OnvmTransport{UseXIO: true}
}

func TestONVMDeadlineUnblocksResponseRead(t *testing.T) {
	c := newRegressionConn(nil)
	ot := regressionTransport(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://nrf.test/nnrf-disc/v1/nf-instances?target-nf-type=CHF", nil)
	done := make(chan error, 1)
	go func() { _, err := ot.RoundTrip(req); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("CHF discovery remained blocked after its deadline")
	}
	if atomic.LoadInt32(&c.closeCount) != 1 {
		t.Fatal("cancelled connection was not closed exactly once")
	}
	if len(ot.ConnPool.conns) != 0 {
		t.Fatal("cancelled connection returned to pool")
	}
}

func TestONVMCancellationDuringPreface(t *testing.T) {
	c := newRegressionConn(nil)
	c.blockWrite = true
	ot := regressionTransport(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://nrf.test/", nil)
	done := make(chan error, 1)
	go func() { _, err := ot.RoundTrip(req); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("preface write remained blocked")
	}
}

func TestONVMDialHonorsContext(t *testing.T) {
	onvmpoller.DialContextFunc = func(ctx context.Context, _, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }
	t.Cleanup(func() { onvmpoller.DialContextFunc = nil })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", "http://nrf.test/", nil)
	done := make(chan error, 1)
	go func() { _, err := (&OnvmTransport{UseXIO: true}).RoundTrip(req); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dial remained blocked")
	}
}

func TestONVMSuccessfulResponseReusesConnection(t *testing.T) {
	body := []byte(`{"chargingDataRef":"one"}`)
	frame, err := FastEncodeResponse(201, http.Header{"Content-Type": {"application/json"}}, int64(len(body)), body)
	if err != nil {
		t.Fatal(err)
	}
	c := newRegressionConn(frame)
	ot := regressionTransport(t, c)
	for i := 0; i < 40; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, "POST", "http://chf.test/nchf-convergedcharging/v3/chargingdata", nil)
		rsp, err := ot.RoundTrip(req)
		cancel() // A completed request must never close the pooled connection.
		if err != nil {
			t.Fatal(err)
		}
		got, err := io.ReadAll(rsp.Body)
		rsp.Body.Close()
		if err != nil || string(got) != string(body) || rsp.StatusCode != 201 {
			t.Fatalf("response=%s status=%d err=%v", got, rsp.StatusCode, err)
		}
		if len(ot.ConnPool.conns["chf.test:80"]) != 1 {
			t.Fatal("pool accumulated duplicate connections")
		}
	}
	if atomic.LoadInt32(&c.closeCount) != 0 {
		t.Fatal("completed request cancellation closed reusable socket")
	}
}

func TestONVMConcurrentCheckoutIsExclusive(t *testing.T) {
	ot := &OnvmTransport{UseXIO: true}
	p := &onvmClientConnPool{ot: ot, mu: new(sync.Mutex), conns: make(map[string][]*OnvmClientConn)}
	ot.ConnPool = p
	req, _ := http.NewRequest("GET", "http://nrf.test/", nil)
	first := &OnvmClientConn{conn: newRegressionConn(nil), req: req}
	p.PutClientConn(first)
	onvmpoller.DialContextFunc = func(context.Context, string, string) (net.Conn, error) { return newRegressionConn(nil), nil }
	t.Cleanup(func() { onvmpoller.DialContextFunc = nil })
	var wg sync.WaitGroup
	got := make(chan *OnvmClientConn, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, err := p.GetClientConn(req, "nrf.test:80")
			if err != nil {
				t.Error(err)
				return
			}
			got <- c
		}()
	}
	wg.Wait()
	close(got)
	seen := make(map[*OnvmClientConn]bool)
	for c := range got {
		if seen[c] {
			t.Fatal("one socket was checked out by multiple concurrent requests")
		}
		seen[c] = true
		c.Close()
	}
}
