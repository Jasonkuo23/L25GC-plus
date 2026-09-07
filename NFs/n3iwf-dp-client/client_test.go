package n3iwfdp

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestMarshalSessionIPv4SingleQFI(t *testing.T) {
	payload, err := marshalSession(Session{
		UEID:            11,
		PDUSessionID:    10,
		UplinkTEID:      0x01020304,
		DownlinkTEID:    0x05060708,
		UEPDUAddress:    net.ParseIP("10.60.0.1"),
		N3IWFNWuAddress: net.ParseIP("10.0.0.1"),
		UENWuAddress:    net.ParseIP("10.0.0.2"),
		N3IWFN3Address:  net.ParseIP("192.168.2.1"),
		UPFN3Address:    net.ParseIP("192.168.2.2"),
		QFIs:            []uint8{9},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(payload), 167; got != want {
		t.Fatalf("payload length=%d want=%d", got, want)
	}
	if got := binary.BigEndian.Uint32(payload[12:16]); got != 0x01020304 {
		t.Fatalf("uplink TEID=%#x", got)
	}
	if payload[20] != 4 || payload[21] != 1 {
		t.Fatalf("family/count=%d/%d", payload[20], payload[21])
	}
	if payload[104] != 9 {
		t.Fatalf("unexpected QFI: %d", payload[104])
	}
}

func TestMarshalSessionIPv6(t *testing.T) {
	_, err := marshalSession(Session{
		UEID:            12,
		PDUSessionID:    11,
		UplinkTEID:      100,
		DownlinkTEID:    200,
		UEPDUAddress:    net.ParseIP("2001:db8:60::1"),
		N3IWFNWuAddress: net.ParseIP("2001:db8:10::1"),
		UENWuAddress:    net.ParseIP("2001:db8:10::2"),
		N3IWFN3Address:  net.ParseIP("2001:db8:20::1"),
		UPFN3Address:    net.ParseIP("2001:db8:20::2"),
		QFIs:            []uint8{7},
	})
	if err == nil {
		t.Fatal("IPv6 session accepted by wire version 1")
	}
}

func TestMarshalSessionSupportsMultipleQFIs(t *testing.T) {
	payload, err := marshalSession(Session{
		UEID:            1,
		PDUSessionID:    1,
		UplinkTEID:      1,
		DownlinkTEID:    2,
		UEPDUAddress:    net.ParseIP("10.0.0.1"),
		N3IWFNWuAddress: net.ParseIP("10.0.0.2"),
		UENWuAddress:    net.ParseIP("10.0.0.3"),
		N3IWFN3Address:  net.ParseIP("10.0.0.4"),
		UPFN3Address:    net.ParseIP("10.0.0.5"),
		QFIs:            []uint8{5, 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	if payload[21] != 2 || payload[104] != 5 || payload[105] != 9 {
		t.Fatalf("unexpected QFI encoding: count=%d values=%d,%d",
			payload[21], payload[104], payload[105])
	}
}

func TestMarshalChildSA(t *testing.T) {
	sa := ChildSA{
		UEID: 42, PDUSessionID: 10, InboundSPI: 0x1001,
		OutboundSPI: 0x1002, EncryptionID: 12, IntegrityID: 2,
		ReplayWindow: 64, LocalAddress: net.ParseIP("192.168.2.2"),
		PeerAddress:   net.ParseIP("192.168.2.1"),
		LocalSelector: net.ParseIP("10.0.0.1"),
		PeerSelector:  net.ParseIP("10.0.0.2"), IPProtocol: 47,
		InboundEncryptionKey: make([]byte, 32), InboundIntegrityKey: make([]byte, 20),
		OutboundEncryptionKey: make([]byte, 32), OutboundIntegrityKey: make([]byte, 20),
	}
	payload, err := marshalChildSA(sa)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 388 || binary.BigEndian.Uint32(payload[12:16]) != 0x1001 ||
		payload[60] != 4 || payload[61] != 47 ||
		payload[62] != 32 || payload[64] != 32 {
		t.Fatalf("unexpected Child SA wire encoding")
	}
}

func TestParseACK(t *testing.T) {
	message := make([]byte, 40)
	binary.BigEndian.PutUint32(message[0:4], wireMagic)
	binary.BigEndian.PutUint16(message[4:6], wireVersion)
	binary.BigEndian.PutUint16(message[6:8], MessageACK)
	binary.BigEndian.PutUint32(message[8:12], uint32(len(message)))
	binary.BigEndian.PutUint32(message[12:16], 77)
	binary.BigEndian.PutUint64(message[16:24], 8)
	binary.BigEndian.PutUint32(message[24:28], uint32(StatusOK))
	binary.BigEndian.PutUint64(message[32:40], 8)

	ack, err := parseACK(message, 77)
	if err != nil {
		t.Fatal(err)
	}
	if ack.AppliedGeneration != 8 {
		t.Fatalf("generation=%d", ack.AppliedGeneration)
	}
}

func TestParseStats(t *testing.T) {
	message := make([]byte, headerLen+(20*8))
	binary.BigEndian.PutUint32(message[0:4], wireMagic)
	binary.BigEndian.PutUint16(message[4:6], wireVersion)
	binary.BigEndian.PutUint16(message[6:8], MessageStats)
	binary.BigEndian.PutUint32(message[8:12], uint32(len(message)))
	binary.BigEndian.PutUint32(message[12:16], 91)
	for index := uint64(0); index < 20; index++ {
		binary.BigEndian.PutUint64(message[headerLen+(index*8):], index+11)
	}
	stats, err := parseStats(message, 91)
	if err != nil {
		t.Fatal(err)
	}
	if stats.UplinkPackets != 11 || stats.ControlPuntDrops != 22 ||
		stats.AccessMACLearns != 23 || stats.AccessMACChanges != 24 ||
		stats.AccessNeighborDrops != 25 || stats.ActiveSessions != 26 ||
		stats.ActiveChildSAs != 27 || stats.UnknownSPI != 28 ||
		stats.OversizeDrops != 29 || stats.BufferDrops != 30 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}
