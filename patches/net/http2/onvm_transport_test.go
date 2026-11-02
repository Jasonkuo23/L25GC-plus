package http2

import (
	"bytes"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/nycu-ucr/gonet/http"
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
