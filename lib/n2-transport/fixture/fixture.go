// Isolated test process. Never use an existing ONVM file-prefix.
package fixture

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	n2 "github.com/nycu-ucr/l25gc-n2-transport"
	"io"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// Main runs only isolated fixtures. A nil classifier selects synthetic payloads.
func Main(classify func([]byte) (bool, error)) {
	role := flag.String("role", "primary", "primary/server/client")
	eal := flag.String("eal", "", "EAL args; unique test prefix required")
	trace := flag.String("pcap", "", "optional IPv4 PCAP")
	drop := flag.Uint("drop", 0, "drop first DATA packets")
	multi := flag.Bool("multi", false, "two addresses per endpoint")
	scenario := flag.String("scenario", "exchange", "exchange/crash/duplicate/deadline/backpressure/shortread")
	vectors := flag.String("vectors", "", "JSON NGAP vectors when using a production classifier")
	flag.Parse()
	must(n2.InitializeFixtureEAL(append([]string{"n2fixture"}, strings.Fields(*eal)...)))
	if *role == "primary" {
		fmt.Println("PRIMARY READY")
		stop := make(chan os.Signal, 1)
		signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
		<-stop
		return
	}
	if *trace != "" {
		must(n2.FixtureTrace(*trace))
	}
	n2.FixtureDropData(*drop)
	server := *role == "server"
	a, b := []string{"192.0.2.10"}, []string{"192.0.2.20"}
	if *multi {
		a = append(a, "192.0.2.11")
		b = append(b, "192.0.2.21")
	}
	if !server {
		a, b = b, a
	}
	cfg := n2.Config{Name: "N2_TEST", LocalIPs: a, PeerIPs: b, Port: 38412,
		Classify: func(p []byte) (bool, error) { return p[0] == 1, nil }}
	if classify != nil {
		cfg.Classify = classify
	}
	ep, err := n2.Open(cfg, server)
	if *scenario == "duplicate" {
		if err == nil {
			panic("duplicate endpoint was allowed")
		}
		fmt.Println("PASS: duplicate endpoint rejected:", err)
		return
	}
	must(err)
	defer ep.Close()
	fmt.Println("ENDPOINT READY")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var conn *n2.Conn
	if server {
		conn, err = ep.Accept(ctx)
	} else {
		conn, err = ep.Dial(ctx)
	}
	must(err)
	defer conn.Close()
	must(conn.SetDeadline(time.Now().Add(25 * time.Second)))
	fmt.Println("ASSOCIATION READY")
	if *scenario == "crash" {
		if !server {
			select {}
		}
		_, err := conn.Read(make([]byte, n2.MaxPDU))
		if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			panic(fmt.Sprintf("peer death did not wake reader: %v", err))
		}
		fmt.Println("PASS: peer death wakes blocked Read:", err)
		return
	}
	if *scenario == "deadline" {
		if !server {
			time.Sleep(time.Second)
			return
		}
		conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		_, err := conn.Read(make([]byte, n2.MaxPDU))
		if !errors.Is(err, os.ErrDeadlineExceeded) {
			panic(fmt.Sprintf("deadline: %v", err))
		}
		conn.SetReadDeadline(time.Time{})
		result := make(chan error, 1)
		go func() { _, e := conn.Read(make([]byte, n2.MaxPDU)); result <- e }()
		time.Sleep(20 * time.Millisecond)
		conn.Close()
		if e := <-result; !errors.Is(e, net.ErrClosed) {
			panic(fmt.Sprintf("close: %v", e))
		}
		fmt.Println("PASS: Read deadline and concurrent Close")
		time.Sleep(200 * time.Millisecond)
		return
	}

	if _, err := conn.Write(make([]byte, n2.MaxPDU+1)); !errors.Is(err, syscall.EMSGSIZE) {
		panic("oversize write was accepted")
	}
	if *scenario == "backpressure" {
		if server {
			time.Sleep(2 * time.Second)
			return
		}
		must(conn.SetWriteDeadline(time.Now().Add(200 * time.Millisecond)))
		payload := make([]byte, n2.MaxPDU)
		count := 0
		for ; count < 1000; count++ {
			_, err := conn.Write(payload)
			if errors.Is(err, os.ErrDeadlineExceeded) {
				break
			}
			must(err)
		}
		if count == 1000 {
			panic("no bounded backpressure")
		}
		fmt.Println("PASS: bounded Write deadline under receiver backpressure; accepted messages:", count)
		return
	}
	if *scenario == "shortread" {
		if !server {
			_, err := conn.Write(make([]byte, 4096))
			must(err)
			time.Sleep(time.Second)
			return
		}
		_, err := conn.Read(make([]byte, 256))
		if err == nil || errors.Is(err, os.ErrDeadlineExceeded) {
			panic(fmt.Sprintf("short Read silently succeeded or stalled: %v", err))
		}
		fmt.Println("PASS: short Read fails explicitly:", err)
		return
	}
	var messages []Vector
	if *vectors != "" {
		data, err := os.ReadFile(*vectors)
		must(err)
		must(json.Unmarshal(data, &messages))
		if classify == nil {
			panic("NGAP vectors require a production classifier")
		}
	} else {
		if classify != nil {
			panic("production classifier requires -vectors")
		}
		for _, size := range []int{1, 128, 4096, 65535, 262144} {
			for stream := 0; stream < 2; stream++ {
				payload := bytes.Repeat([]byte{byte(size % 251)}, size)
				payload[0] = byte(stream)
				messages = append(messages, Vector{Name: fmt.Sprintf("size%d-stream%d", size, stream), PDU: payload, UE: stream == 1})
			}
		}
	}
	buffer := make([]byte, n2.MaxPDU)
	for _, vector := range messages {
		ue, err := cfg.Classify(vector.PDU)
		must(err)
		if ue != vector.UE {
			panic("incorrect stream classification: " + vector.Name)
		}
		if !server {
			_, err = conn.Write(vector.PDU)
			must(err)
		}
		n, err := conn.Read(buffer)
		must(err)
		if !bytes.Equal(buffer[:n], vector.PDU) {
			panic("mismatched message: " + vector.Name)
		}
		if server {
			_, err = conn.Write(buffer[:n])
			must(err)
		}
	}

	if server {
		_, err := conn.Read(buffer)
		if !errors.Is(err, io.EOF) {
			panic(fmt.Sprintf("expected SCTP shutdown, got %v", err))
		}
	}
	must(conn.Close())
	time.Sleep(250 * time.Millisecond) // Allow native SHUTDOWN/ACK/COMPLETE to drain.
	fmt.Printf("PASS: %d bidirectional ordered messages, expected NGAP/stream classification\n", len(messages))
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}

// Vector is an encoded PDU with its independently expected stream class.
type Vector struct {
	Name string
	PDU  []byte
	UE   bool
}
