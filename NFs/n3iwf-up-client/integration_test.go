package n3iwfup

import (
	"bytes"
	"context"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The runner starts an isolated C primary with a unique EAL file-prefix.
// Ordinary unit tests never attach to a live ONVM deployment.
func TestDPDKIntegration(t *testing.T) {
	args := os.Getenv("N3DP_INTEGRATION_EAL_ARGS")
	if args == "" {
		t.Skip("run tests/run_integration.py with hugepages for multiprocess verification")
	}
	opts := Options{ControlName: "N3DP_TEST", EALArgs: strings.Fields(args)}
	writer, observer, competitor := NewWithOptions(opts), NewWithOptions(opts), NewWithOptions(opts)
	defer writer.Close()
	defer observer.Close()
	defer competitor.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := writer.Hello(ctx); err != nil {
		t.Fatal(err)
	}
	if err := observer.HelloObserver(ctx); err != nil {
		t.Fatal(err)
	}
	if err := competitor.Hello(ctx); err == nil {
		t.Fatal("two writers accepted")
	}
	sa := ChildSA{
		UEID: 77, PDUSessionID: 11, InboundSPI: 0x1111, OutboundSPI: 0x2222,
		EncryptionID: 12, IntegrityID: 2, ReplayWindow: 4096, IPProtocol: 47,
		LocalAddress: net.ParseIP("192.168.2.2"), PeerAddress: net.ParseIP("192.168.2.1"),
		LocalSelector: net.ParseIP("10.0.0.1"), PeerSelector: net.ParseIP("10.0.0.2"),
		InboundEncryptionKey: bytes.Repeat([]byte{1}, 16), OutboundEncryptionKey: bytes.Repeat([]byte{2}, 16),
		InboundIntegrityKey: bytes.Repeat([]byte{3}, 20), OutboundIntegrityKey: bytes.Repeat([]byte{4}, 20),
	}
	if err := observer.UpsertChildSA(ctx, 1, sa); err == nil {
		t.Fatal("observer mutated state")
	}
	if err := writer.UpsertChildSA(ctx, 1, sa); err != nil {
		t.Fatal(err)
	}
	session := Session{
		UEID: 77, PDUSessionID: 11, UplinkTEID: 12345, DownlinkTEID: 54321, QFIs: []uint8{9},
		UEPDUAddress: net.IPv4zero, N3IWFNWuAddress: net.ParseIP("10.0.0.1"), UENWuAddress: net.ParseIP("10.0.0.2"),
		N3IWFN3Address: net.ParseIP("192.168.4.1"), UPFN3Address: net.ParseIP("192.168.4.2"),
	}
	if err := writer.UpsertSession(ctx, 2, session); err != nil {
		t.Fatal(err)
	}
	if err := writer.UpsertSession(ctx, 2, session); err == nil {
		t.Fatal("stale generation accepted")
	}
	stats, err := observer.GetStats(ctx)
	if err != nil || stats.ActiveSessions != 1 || stats.ActiveChildSAs != 1 || stats.StaleUpdates != 1 {
		t.Fatalf("stats=%+v error=%v", stats, err)
	}
	// Stop only the isolated fixture. Time out an update, retire its lease,
	// resume the DP, and prove the abandoned queued mutation is discarded.
	if rawPID := os.Getenv("N3DP_INTEGRATION_PID"); rawPID != "" {
		pid, err := strconv.Atoi(rawPID)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Kill(pid, syscall.SIGSTOP); err != nil {
			t.Fatal(err)
		}
		defer syscall.Kill(pid, syscall.SIGCONT)
		short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		err = writer.DeleteSession(short, 3, 77, 11)
		cancel()
		if err != context.DeadlineExceeded {
			t.Fatalf("timeout: %v", err)
		}
		if err := syscall.Kill(pid, syscall.SIGCONT); err != nil {
			t.Fatal(err)
		}
		stats, err = observer.GetStats(ctx)
		if err != nil || stats.ActiveSessions != 1 {
			t.Fatalf("timed-out queued delete applied: %+v %v", stats, err)
		}
	}
	if err := writer.DeleteSession(ctx, 4, 77, 11); err != nil {
		t.Fatal(err)
	}
	if err := writer.DeleteChildSA(ctx, 5, 77, 11, sa.InboundSPI); err != nil {
		t.Fatal(err)
	}
	stats, err = observer.GetStats(ctx)
	if err != nil || stats.ActiveSessions != 0 || stats.ActiveChildSAs != 0 {
		t.Fatalf("cleanup: %+v %v", stats, err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := competitor.Hello(ctx); err != nil {
		t.Fatal("writer lease not released:", err)
	}
	competitor.Close()
	if crashArgs := os.Getenv("N3DP_INTEGRATION_CRASH_EAL_ARGS"); crashArgs != "" {
		child := exec.Command(os.Args[0], "-test.run=^TestDPDKCrashChild$")
		child.Env = append(os.Environ(), "N3DP_CRASH_CHILD=1", "N3DP_CRASH_ARGS="+crashArgs)
		if output, err := child.CombinedOutput(); err != nil {
			t.Fatalf("crash child: %v %s", err, output)
		}
		deadline := time.Now().Add(2500 * time.Millisecond)
		for {
			if err := competitor.Hello(ctx); err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("dead writer lease not reclaimed")
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestDPDKCrashChild(t *testing.T) {
	if os.Getenv("N3DP_CRASH_CHILD") != "1" {
		t.Skip("integration subprocess only")
	}
	c := NewWithOptions(Options{ControlName: "N3DP_TEST", EALArgs: strings.Fields(os.Getenv("N3DP_CRASH_ARGS"))})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := c.Hello(ctx); err != nil {
		t.Fatal(err)
	}
	// Deliberately skip Client.Close: parent must recover the dead writer lease.
	os.Exit(0)
}
