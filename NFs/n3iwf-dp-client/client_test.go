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

func TestMarshalSessionAcceptsRANUENGAPIDZero(t *testing.T) {
	_, err := marshalSession(Session{PDUSessionID: 1, UplinkTEID: 1, DownlinkTEID: 2,
		UEPDUAddress: net.IPv4zero, N3IWFNWuAddress: net.ParseIP("10.0.0.1"),
		UENWuAddress: net.ParseIP("10.0.0.2"), N3IWFN3Address: net.ParseIP("192.168.2.1"),
		UPFN3Address: net.ParseIP("192.168.2.2"), QFIs: []uint8{9}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMarshalChildSA(t *testing.T) {
	payload, err := marshalChildSA(ChildSA{UEID: 0, PDUSessionID: 10,
		InboundSPI: 0x01020304, OutboundSPI: 0x05060708,
		EncryptionID: 12, IntegrityID: 2, ReplayWindow: 64, IPProtocol: 47,
		LocalAddress: net.ParseIP("192.168.127.1"), PeerAddress: net.ParseIP("192.168.127.2"),
		LocalSelector: net.ParseIP("10.0.0.1"), PeerSelector: net.ParseIP("10.0.0.2"),
		InboundEncryptionKey: make([]byte, 16), InboundIntegrityKey: make([]byte, 20),
		OutboundEncryptionKey: make([]byte, 16), OutboundIntegrityKey: make([]byte, 20)})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 388 {
		t.Fatalf("payload length=%d", len(payload))
	}
	if binary.BigEndian.Uint32(payload[12:16]) != 0x01020304 || payload[60] != 4 || payload[61] != 47 {
		t.Fatalf("invalid Child SA encoding")
	}
}

func TestMarshalChildSARejectsIncompleteKeys(t *testing.T) {
	_, err := marshalChildSA(ChildSA{InboundSPI: 1, OutboundSPI: 2,
		EncryptionID: 12, IntegrityID: 2, ReplayWindow: 64, IPProtocol: 47,
		LocalAddress: net.ParseIP("10.0.0.1"), PeerAddress: net.ParseIP("10.0.0.2"),
		LocalSelector: net.ParseIP("10.0.0.1"), PeerSelector: net.ParseIP("10.0.0.2"),
		InboundEncryptionKey: make([]byte, 16), OutboundEncryptionKey: make([]byte, 16)})
	if err == nil {
		t.Fatal("incomplete integrity keys accepted")
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

func TestMarshalSessionMultipleQFIs(t *testing.T) {
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
		t.Fatal("multi-QFI encoding is incorrect")
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
	message := make([]byte, headerLen+(15*8))
	binary.BigEndian.PutUint32(message[0:4], wireMagic)
	binary.BigEndian.PutUint16(message[4:6], wireVersion)
	binary.BigEndian.PutUint16(message[6:8], MessageStats)
	binary.BigEndian.PutUint32(message[8:12], uint32(len(message)))
	binary.BigEndian.PutUint32(message[12:16], 91)
	for index := uint64(0); index < 15; index++ {
		binary.BigEndian.PutUint64(message[headerLen+(index*8):], index+11)
	}

	stats, err := parseStats(message, 91)
	if err != nil {
		t.Fatal(err)
	}
	if stats.UplinkPackets != 11 || stats.DownlinkPackets != 12 ||
		stats.UnknownTEID != 13 || stats.UnknownQFI != 14 ||
		stats.MalformedPackets != 15 || stats.ReplayDrops != 16 ||
		stats.CryptoFailures != 17 || stats.FragmentDrops != 18 ||
		stats.StaleUpdates != 19 || stats.ControlToCP != 20 ||
		stats.ControlFromCP != 21 || stats.ControlPuntDrops != 22 ||
		stats.AccessMACLearns != 23 || stats.AccessMACChanges != 24 ||
		stats.AccessNeighborDrops != 25 {
		t.Fatalf("unexpected stats: %+v", stats)
	}
}

func TestParseControlStats(t *testing.T) {
	message := make([]byte, headerLen+(12*8))
	binary.BigEndian.PutUint32(message[0:4], wireMagic)
	binary.BigEndian.PutUint16(message[4:6], wireVersion)
	binary.BigEndian.PutUint16(message[6:8], MessageStats)
	binary.BigEndian.PutUint32(message[8:12], uint32(len(message)))
	binary.BigEndian.PutUint32(message[12:16], 93)

	stats, err := parseStats(message, 93)
	if err != nil {
		t.Fatal(err)
	}
	if stats.AccessMACLearns != 0 || stats.AccessMACChanges != 0 ||
		stats.AccessNeighborDrops != 0 {
		t.Fatalf("older response has access counters: %+v", stats)
	}
}

func TestParseLegacyStats(t *testing.T) {
	message := make([]byte, headerLen+(9*8))
	binary.BigEndian.PutUint32(message[0:4], wireMagic)
	binary.BigEndian.PutUint16(message[4:6], wireVersion)
	binary.BigEndian.PutUint16(message[6:8], MessageStats)
	binary.BigEndian.PutUint32(message[8:12], uint32(len(message)))
	binary.BigEndian.PutUint32(message[12:16], 92)

	stats, err := parseStats(message, 92)
	if err != nil {
		t.Fatal(err)
	}
	if stats.ControlToCP != 0 || stats.ControlFromCP != 0 ||
		stats.ControlPuntDrops != 0 {
		t.Fatalf("legacy response has control counters: %+v", stats)
	}
}

func TestParseStatsRejectsWrongResponse(t *testing.T) {
	message := make([]byte, headerLen+(9*8))
	binary.BigEndian.PutUint32(message[0:4], wireMagic)
	binary.BigEndian.PutUint16(message[4:6], wireVersion)
	binary.BigEndian.PutUint16(message[6:8], MessageACK)
	binary.BigEndian.PutUint32(message[8:12], uint32(len(message)))
	binary.BigEndian.PutUint32(message[12:16], 91)

	if _, err := parseStats(message, 91); err == nil {
		t.Fatal("ACK accepted as a stats response")
	}
	if _, err := parseStats(message[:len(message)-1], 91); err == nil {
		t.Fatal("truncated stats response accepted")
	}
}
