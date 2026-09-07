// Command n3iwf-dpctl injects control messages into l25gc_n3iwf_dp.
// It is an integration-test tool, not a replacement for the N3IWF control
// plane lifecycle hooks.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"time"

	n3iwfdp "github.com/nycu-ucr/l25gc-n3iwf-dp-client"
)

type options struct {
	operation    string
	socket       string
	timeout      time.Duration
	generation   uint64
	ueID         uint64
	pduSessionID uint64
	uplinkTEID   uint64
	downlinkTEID uint64
	uePDU        string
	n3iwfNWu     string
	ueNWu        string
	n3iwfN3      string
	upfN3        string
	qfi          uint
}

func parseOptions() options {
	var opts options

	flag.StringVar(&opts.operation, "operation", "hello", "hello, stats, install-clear, upsert, or delete")
	flag.StringVar(&opts.socket, "socket", "/run/l25gc/n3iwf-dp.sock", "N3DP Unix socket path")
	flag.DurationVar(&opts.timeout, "timeout", 2*time.Second, "control request timeout")
	flag.Uint64Var(&opts.generation, "generation", 0, "monotonic UE/session generation")
	flag.Uint64Var(&opts.ueID, "ue-id", 0, "stable non-zero UE identifier")
	flag.Uint64Var(&opts.pduSessionID, "pdu-session-id", 0, "non-zero PDU session identifier")
	flag.Uint64Var(&opts.uplinkTEID, "ul-teid", 0, "N3 uplink TEID")
	flag.Uint64Var(&opts.downlinkTEID, "dl-teid", 0, "N3 downlink TEID")
	flag.StringVar(&opts.uePDU, "ue-pdu", "", "UE IPv4 PDU address")
	flag.StringVar(&opts.n3iwfNWu, "n3iwf-nwu", "10.0.0.1", "N3IWF IPv4 GRE/IPsec-inner NWu address")
	flag.StringVar(&opts.ueNWu, "ue-nwu", "10.0.0.2", "UE IPv4 GRE/IPsec-inner NWu address")
	flag.StringVar(&opts.n3iwfN3, "n3iwf-n3", "192.168.4.1", "N3IWF logical IPv4 N3 address")
	flag.StringVar(&opts.upfN3, "upf-n3", "192.168.4.2", "UPF logical IPv4 N3 address")
	flag.UintVar(&opts.qfi, "qfi", 0, "single active QFI (1..63)")
	flag.Parse()
	return opts
}

func validateUint32(name string, value uint64) (uint32, error) {
	if value == 0 || value > math.MaxUint32 {
		return 0, fmt.Errorf("-%s must be between 1 and %d", name, uint64(math.MaxUint32))
	}
	return uint32(value), nil
}

func run(opts options) error {
	if opts.socket == "" {
		return errors.New("-socket must not be empty")
	}
	if opts.timeout <= 0 {
		return errors.New("-timeout must be positive")
	}

	client := n3iwfdp.New(opts.socket)
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), opts.timeout)
	defer cancel()

	switch opts.operation {
	case "hello":
		return client.HelloObserver(ctx)
	case "stats":
		stats, err := client.GetStats(ctx)
		if err != nil {
			return err
		}
		fmt.Printf("uplink_packets=%d\n", stats.UplinkPackets)
		fmt.Printf("downlink_packets=%d\n", stats.DownlinkPackets)
		fmt.Printf("unknown_teid=%d\n", stats.UnknownTEID)
		fmt.Printf("unknown_qfi=%d\n", stats.UnknownQFI)
		fmt.Printf("unknown_spi=%d\n", stats.UnknownSPI)
		fmt.Printf("oversize_drops=%d\n", stats.OversizeDrops)
		fmt.Printf("buffer_drops=%d\n", stats.BufferDrops)
		fmt.Printf("malformed_packets=%d\n", stats.MalformedPackets)
		fmt.Printf("replay_drops=%d\n", stats.ReplayDrops)
		fmt.Printf("crypto_failures=%d\n", stats.CryptoFailures)
		fmt.Printf("fragment_drops=%d\n", stats.FragmentDrops)
		fmt.Printf("stale_updates=%d\n", stats.StaleUpdates)
		fmt.Printf("control_to_cp=%d\n", stats.ControlToCP)
		fmt.Printf("control_from_cp=%d\n", stats.ControlFromCP)
		fmt.Printf("control_punt_drops=%d\n", stats.ControlPuntDrops)
		fmt.Printf("access_mac_learns=%d\n", stats.AccessMACLearns)
		fmt.Printf("access_mac_changes=%d\n", stats.AccessMACChanges)
		fmt.Printf("access_neighbor_drops=%d\n", stats.AccessNeighborDrops)
		fmt.Printf("active_sessions=%d\n", stats.ActiveSessions)
		fmt.Printf("active_child_sas=%d\n", stats.ActiveChildSAs)
		return nil
	case "upsert", "install-clear":
		if err := client.Hello(ctx); err != nil {
			return fmt.Errorf("acquire dataplane writer role: %w", err)
		}
		pduSessionID, err := validateUint32("pdu-session-id", opts.pduSessionID)
		if err != nil {
			return err
		}
		uplinkTEID, err := validateUint32("ul-teid", opts.uplinkTEID)
		if err != nil {
			return err
		}
		downlinkTEID, err := validateUint32("dl-teid", opts.downlinkTEID)
		if err != nil {
			return err
		}
		if opts.generation == 0 {
			return errors.New("-generation must be non-zero")
		}
		if opts.qfi == 0 || opts.qfi > 63 {
			return errors.New("-qfi must be between 1 and 63")
		}
		if opts.operation == "install-clear" {
			sa := n3iwfdp.ChildSA{
				UEID: opts.ueID, PDUSessionID: pduSessionID,
				InboundSPI: 0x1001, OutboundSPI: 0x1002,
				EncryptionID: 12, IntegrityID: 2, ReplayWindow: 64,
				LocalAddress: net.ParseIP("192.168.2.2"), PeerAddress: net.ParseIP("192.168.2.1"),
				LocalSelector: net.ParseIP(opts.n3iwfNWu), PeerSelector: net.ParseIP(opts.ueNWu),
				IPProtocol:            47,
				InboundEncryptionKey:  bytes.Repeat([]byte{0x11}, 16),
				InboundIntegrityKey:   bytes.Repeat([]byte{0x22}, 20),
				OutboundEncryptionKey: bytes.Repeat([]byte{0x33}, 16),
				OutboundIntegrityKey:  bytes.Repeat([]byte{0x44}, 20),
			}
			if err := client.UpsertChildSA(ctx, opts.generation, sa); err != nil {
				return fmt.Errorf("install deterministic clear-test Child SA: %w", err)
			}
		}
		return client.UpsertSession(ctx, opts.generation, n3iwfdp.Session{
			UEID:            opts.ueID,
			PDUSessionID:    pduSessionID,
			UplinkTEID:      uplinkTEID,
			DownlinkTEID:    downlinkTEID,
			UEPDUAddress:    net.ParseIP(opts.uePDU),
			N3IWFNWuAddress: net.ParseIP(opts.n3iwfNWu),
			UENWuAddress:    net.ParseIP(opts.ueNWu),
			N3IWFN3Address:  net.ParseIP(opts.n3iwfN3),
			UPFN3Address:    net.ParseIP(opts.upfN3),
			QFIs:            []uint8{uint8(opts.qfi)},
		})
	case "delete":
		if err := client.Hello(ctx); err != nil {
			return fmt.Errorf("acquire dataplane writer role: %w", err)
		}
		pduSessionID, err := validateUint32("pdu-session-id", opts.pduSessionID)
		if err != nil {
			return err
		}
		if opts.generation == 0 {
			return errors.New("-generation must be non-zero")
		}
		return client.DeleteSession(ctx, opts.generation, opts.ueID, pduSessionID)
	default:
		return fmt.Errorf("unsupported -operation %q", opts.operation)
	}
}

func main() {
	if err := run(parseOptions()); err != nil {
		fmt.Fprintf(os.Stderr, "n3iwf-dpctl: %v\n", err)
		os.Exit(1)
	}
}
