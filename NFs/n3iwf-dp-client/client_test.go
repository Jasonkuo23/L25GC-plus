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

func TestMarshalSessionRejectsMultipleQFIs(t *testing.T) {
	_, err := marshalSession(Session{
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
	if err == nil {
		t.Fatal("multiple QFIs accepted by wire version 1")
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
